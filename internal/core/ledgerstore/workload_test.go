package ledgerstore

// This file is the workload measurement harness of ticket 03 (C1, brief
// section 3.14). It measures the colony ledger and the body store against a
// stated workload model and records what it observes. It sets no budget,
// threshold or pass mark, and no result is a claim about capacity
// (Core:676, 678). It is a measurement harness, not a regression test:
// TestWorkload is skipped unless DWARF_LEDGER_WORKLOAD_OUT names a results
// file, and TestHelperWorkloadChild, the process that each run re-executes the
// test binary as, is skipped unless TestWorkload started it. TestWorkload also
// skips under the race detector, which would distort every figure.
//
// # Model
//
// Every figure is a measurement against one assumed model, labelled as an
// assumption in every result: each context makes changesPerHour changes an
// hour, and each change runs the 41-commit script of the brief scriptRepeats
// times. scriptRepeats is the owner's OG3 answer "Heavier: double the
// traffic", a named constant that every result line carries, so that a
// replaced model rescales from per-unit results. The script's counts are
// derived from its table below, not typed in.
//
// # Scenario groups
//
// The subtests of TestWorkload are the scenario groups, in the order in which
// they are run. Select one with -run '^TestWorkload$/^<group>$'.
//
//	generate  the colonies <contexts>x<days>[b] for 12, 20 and N contexts and 20x30
//	recover   Open of each colony: the first access (cold) or Check and three Opens (warm)
//	append    append/sequential, append/steady, append/burst, append/saturation
//	bodies    bodies/publish
//	crash     recover/crash: a regenerated 20x7, a writer killed under load, then Open
//	dedup     dedup/20x7: Submit of command IDs that are hot, fresh, old, unrecorded and mismatched
//	collect   collect/20x1b: Collect beside an appender, then ReadBody of collected bodies
//
// A colony name is <contexts>x<days>, with a "b" if its bodies were written
// too; a day is 24 modelled hours. The colonies without bodies write the pin
// and unpin records of the script as plain records of the same payload size.
//
// # Environment
//
//	DWARF_LEDGER_WORKLOAD_OUT     the results file, to which one JSON line is appended per run
//	DWARF_LEDGER_WORKLOAD_DIR     the directory that holds the generated colonies, outside the repository,
//	                              which must survive the restart before the cold runs; required except in
//	                              a smoke run, where it defaults to t.TempDir()
//	DWARF_LEDGER_WORKLOAD_LARGER  N, the larger population; the owner's OG3 answer, 60, by default
//	DWARF_LEDGER_WORKLOAD_CACHE   cold or warm: the label that the person who restarted the machine
//	                              gives, because the harness cannot observe the file cache
//	DWARF_LEDGER_WORKLOAD_SMOKE   1: every duration is 10 s and every colony one modelled hour
//
// # Results
//
// Each run is one child process that prints one JSON object. The parent adds
// the child's user and system CPU time and appends one line to the results
// file; fields that were not measured are omitted. A line holds the scenario,
// colony, contexts, modelled hours, cache label, scriptRepeats and the model's
// label; counts and failures by error; latency percentiles in ms (nearest
// rank: pXX is the sample at rank ceil(XX*n/100)); times in ms; rates; per-unit
// figures; CPU seconds; peak working set and peak commit in bytes; the Go heap
// in use after a collection; and, where the run has them, the hot entries,
// segments, active pin pairs, and log and body bytes. Peak commit is
// reported on Windows only. The parent also logs a summary table.
//
// # Operation
//
// Run the groups in the order above. Between generate and the cold recover
// runs, restart the machine and set the cache label for each recover run, once
// as cold and once as warm; the parent reads only the sidecars of the colonies,
// which lie beside them, so that each colony's first access is its cold Open.
// A sidecar, <colony>.workload.json in the colonies' directory, is written
// when a colony is complete and holds what its scenarios need; a colony
// without one is an interrupted generation and is generated again. The
// append and bodies groups run on fresh colonies, which the parent removes.
// dedup records commands in its colony and collect deletes bodies from its,
// so each should run once, and collect refuses a second run; crash generates
// its own colony again for every run. The 20x30 colony is generated only if
// the disk has room for it after the others.
//
// A failed call into the store is counted by its error, never repeated, so
// that a publication that a program holding the temporary file makes fail, a
// sharing violation on rename for example, is measured and not assumed away.
// A failure that leaves the store unusable, a log that does not hold the
// records that a load was acknowledged, an acknowledged commit that Open does
// not keep, a command that Submit treats otherwise than its dedup case
// expects, or a collected body that ReadBody misclassifies fails the test:
// these are invariants of the store, not thresholds.
//
// A smoke run writes smaller segments and opens the dedup colony with a
// smaller hot index, so that the colonies span several segments and evict
// commands, which an hour of history would not; the results note both.

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"text/tabwriter"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

const (
	workloadOutEnv    = "DWARF_LEDGER_WORKLOAD_OUT"
	workloadDirEnv    = "DWARF_LEDGER_WORKLOAD_DIR"
	workloadLargerEnv = "DWARF_LEDGER_WORKLOAD_LARGER"
	workloadCacheEnv  = "DWARF_LEDGER_WORKLOAD_CACHE"
	workloadSmokeEnv  = "DWARF_LEDGER_WORKLOAD_SMOKE"
	// workloadChildEnv carries the JSON childSpec of one run to the child.
	workloadChildEnv = "DWARF_LEDGER_WORKLOAD_CHILD"

	// ownerLarger is N, the owner's OG3 answer for the larger enrolled
	// population.
	ownerLarger = 60
	// workloadSeed seeds every generated colony and load.
	workloadSeed = 20261009
)

// The workload model: an assumption, labelled as one in every result.
const (
	// scriptRepeats is how many times each change runs the 41-commit
	// script. For OG3 the owner chose to double the traffic, which
	// is 2. Every per-hour figure of the model scales with it.
	scriptRepeats = 2

	// changesPerHour is 6 changes an hour for every context, one every ten
	// minutes (D78, decisions:569).
	changesPerHour = 6
	changeDuration = 10 * time.Minute

	// burstFactor is the burst's requests per context: 4 x 12 = 48 and
	// 4 x 20 = 80 (roadmap:338).
	burstFactor = 4

	diffBodyBytes    = 32 << 10
	runLogBodyBytes  = 128 << 10
	messageBodyBytes = 4 << 10
	// requestRecordBytes is the payload of the record that a request, a
	// message and an "other" command each carry beside the command.
	requestRecordBytes = 1 << 10
)

// stepKind is the kind of one commit of the script.
type stepKind uint8

const (
	stepPlain   stepKind = iota // two plain records
	stepRunLog                  // two plain records, the first referencing a run-log body
	stepRequest                 // Submit: a command and a record referencing a diff body
	stepPin                     // body_pin of the script's run logs
	stepMessage                 // Submit: a command and a record referencing a message body
	stepOther                   // Submit: a command and a record, no body
	stepUnpin                   // body_unpin of that pin
)

// records is the number of records in the commit, a command included.
func (k stepKind) records() int {
	if k == stepPin || k == stepUnpin {
		return 1
	}
	return 2
}

func (k stepKind) submits() bool {
	return k == stepRequest || k == stepMessage || k == stepOther
}

// bodyBytes is the size of the body that the commit publishes first, or 0.
func (k stepKind) bodyBytes() int {
	switch k {
	case stepRequest:
		return diffBodyBytes
	case stepRunLog:
		return runLogBodyBytes
	case stepMessage:
		return messageBodyBytes
	}
	return 0
}

// script is the 41 commits that one change runs: c1 a request, c2 to c11
// plain with c5 and c9 each referencing a run log, c12 a pin of the two run
// logs, c13 to c17 messages, c18 and c19 other commands, c20 to c40 plain and
// c41 the unpin.
var script = func() []stepKind {
	var s []stepKind
	add := func(k stepKind, n int) {
		for range n {
			s = append(s, k)
		}
	}
	add(stepRequest, 1)
	add(stepPlain, 3)
	add(stepRunLog, 1)
	add(stepPlain, 3)
	add(stepRunLog, 1)
	add(stepPlain, 2)
	add(stepPin, 1)
	add(stepMessage, 5)
	add(stepOther, 2)
	add(stepPlain, 21)
	add(stepUnpin, 1)
	return s
}()

// modelCounts counts what the model writes.
type modelCounts struct {
	commits, records, commands, bodies int
	bodyBytes                          int64
}

func (m modelCounts) times(n int) modelCounts {
	return modelCounts{m.commits * n, m.records * n, m.commands * n, m.bodies * n, m.bodyBytes * int64(n)}
}

// scriptCounts derives the counts of one run of the script from its table.
func scriptCounts() modelCounts {
	var m modelCounts
	for _, k := range script {
		m.commits++
		m.records += k.records()
		if k.submits() {
			m.commands++
		}
		if n := k.bodyBytes(); n > 0 {
			m.bodies++
			m.bodyBytes += int64(n)
		}
	}
	return m
}

func changeCounts() modelCounts      { return scriptCounts().times(scriptRepeats) }
func contextHourCounts() modelCounts { return changeCounts().times(changesPerHour) }

// modelCommitsPerSecond is the model's commit rate for the given number of
// contexts: a calculation, not a measurement.
func modelCommitsPerSecond(contexts int) float64 {
	return float64(contexts*contextHourCounts().commits) / time.Hour.Seconds()
}

// modelLabel is the label that every result carries.
func modelLabel() string {
	c := contextHourCounts()
	return fmt.Sprintf("assumed workload, not a capacity claim: %d changes per context-hour, each running the %d-commit script %d times; "+
		"per context-hour %d commits, %d records, %d commands and %d bodies of %.2f MiB",
		changesPerHour, len(script), scriptRepeats, c.commits, c.records, c.commands, c.bodies, float64(c.bodyBytes)/(1<<20))
}

// modelSummary describes the model for the parent's log.
func modelSummary(contexts []int) string {
	var b strings.Builder
	sc, ch, hr := scriptCounts(), changeCounts(), contextHourCounts()
	fmt.Fprintf(&b, "workload model (an assumption): script of %d commits, %d records, %d commands and %d bodies of %d KiB; scriptRepeats %d\n",
		sc.commits, sc.records, sc.commands, sc.bodies, sc.bodyBytes>>10, scriptRepeats)
	fmt.Fprintf(&b, "  per change: %d commits, %d records, %d commands, %d bodies of %d KiB\n", ch.commits, ch.records, ch.commands, ch.bodies, ch.bodyBytes>>10)
	fmt.Fprintf(&b, "  per context-hour (%d changes): %d commits, %d records, %d commands, %d bodies of %.2f MiB\n",
		changesPerHour, hr.commits, hr.records, hr.commands, hr.bodies, float64(hr.bodyBytes)/(1<<20))
	for _, n := range contexts {
		fmt.Fprintf(&b, "  %3d contexts: %.2f commits/s, %d records/h, %d commands/h, %d bodies/h (%.1f MiB/h), burst of %d requests\n",
			n, modelCommitsPerSecond(n), n*hr.records, n*hr.commands, n*hr.bodies, float64(n)*float64(hr.bodyBytes)/(1<<20), burstFactor*n)
	}
	return b.String()
}

// runTimes are the durations of the timed scenarios.
type runTimes struct{ steady, burstAt, saturation, crash time.Duration }

const smokeRun = 10 * time.Second

// An hour of history is too little to evict a command or to fill a second
// segment, so a smoke run would never meet the cold paths. Smoke therefore
// writes smaller segments and opens the dedup colony with a smaller hot
// index. Both are noted in the results.
const (
	smokeSegmentTarget = 4 << 20
	smokeHotCommands   = 256
)

func timesFor(smoke bool) runTimes {
	if smoke {
		return runTimes{steady: smokeRun, burstAt: smokeRun * 7 / 15, saturation: smokeRun, crash: smokeRun}
	}
	return runTimes{steady: 15 * time.Minute, burstAt: 7 * time.Minute, saturation: time.Minute, crash: 20 * time.Second}
}

// colonySpec names a generated colony.
type colonySpec struct {
	contexts int
	days     int // modelled days of 24 hours
	bodies   bool
}

func (c colonySpec) name() string {
	s := fmt.Sprintf("%dx%d", c.contexts, c.days)
	if c.bodies {
		s += "b"
	}
	return s
}

func (c colonySpec) hours(smoke bool) int {
	if smoke {
		return 1
	}
	return c.days * 24
}

// colony20x30 is generated only if the disk has room for it.
var colony20x30 = colonySpec{contexts: 20, days: 30}

// room20x30 is the margin of free space, over the colony's expected size,
// that the 20x30 colony needs once the others are written. The brief asked for
// 15 GB free for a colony of about 10 GB. The doubled model makes the colony
// twice as large, so the expected size is taken from the 20x7 colony, which is
// written the same way, and not from a fixed figure.
const room20x30 = 1.5

// ---------------------------------------------------------------------------
// Results

type latencyStats struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// runResult is one line of the results file.
type runResult struct {
	Scenario string `json:"scenario"`
	Colony   string `json:"colony,omitempty"`
	Contexts int    `json:"contexts,omitempty"`
	Hours    int    `json:"hours,omitempty"`
	Cache    string `json:"cache,omitempty"`
	Form     string `json:"form,omitempty"`

	ScriptRepeats int    `json:"scriptRepeats"`
	Model         string `json:"model"`
	Smoke         bool   `json:"smoke,omitempty"`
	StartedAt     string `json:"startedAt"`
	GoVersion     string `json:"go"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	CPUs          int    `json:"cpus"`
	Procs         int    `json:"gomaxprocs"`

	Counts    map[string]int64        `json:"counts,omitempty"`
	Failures  map[string]int64        `json:"failures,omitempty"`
	LatencyMS map[string]latencyStats `json:"latencyMs,omitempty"`
	TimesMS   map[string]float64      `json:"timesMs,omitempty"`
	Rates     map[string]float64      `json:"rates,omitempty"`
	PerUnit   map[string]float64      `json:"perUnit,omitempty"`
	Notes     []string                `json:"notes,omitempty"`

	WallSec    float64 `json:"wallSec,omitempty"`
	CPUUserSec float64 `json:"cpuUserSec,omitempty"`
	CPUSysSec  float64 `json:"cpuSysSec,omitempty"`

	PeakWorkingSetBytes uint64 `json:"peakWorkingSetBytes,omitempty"`
	PeakCommitBytes     uint64 `json:"peakCommitBytes,omitempty"`
	HeapInUseBytes      uint64 `json:"heapInUseBytes,omitempty"`

	HotEntries *int64 `json:"hotEntries,omitempty"`
	Segments   *int64 `json:"segments,omitempty"`
	PinPairs   *int64 `json:"pinPairs,omitempty"`
	LogBytes   *int64 `json:"logBytes,omitempty"`
	BodyBytes  *int64 `json:"bodyBytes,omitempty"`
}

func newResult(spec childSpec) *runResult {
	return &runResult{
		Scenario: spec.Scenario, Colony: spec.Colony, Contexts: spec.Contexts, Hours: spec.Hours, Form: spec.Form,
		ScriptRepeats: scriptRepeats, Model: modelLabel(), Smoke: spec.Smoke,
		StartedAt: time.Now().UTC().Format(time.RFC3339), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(), Procs: runtime.GOMAXPROCS(0),
	}
}

func i64[T ~int | ~int64 | ~uint64](v T) *int64 { n := int64(v); return &n }

func (r *runResult) count(k string, v int64) {
	if r.Counts == nil {
		r.Counts = map[string]int64{}
	}
	r.Counts[k] = v
}

func (r *runResult) timeMS(k string, d time.Duration) {
	if r.TimesMS == nil {
		r.TimesMS = map[string]float64{}
	}
	r.TimesMS[k] = millis(d)
}

func (r *runResult) rate(k string, v float64) {
	if r.Rates == nil {
		r.Rates = map[string]float64{}
	}
	r.Rates[k] = round(v, 4)
}

func (r *runResult) perUnit(k string, v float64) {
	if r.PerUnit == nil {
		r.PerUnit = map[string]float64{}
	}
	r.PerUnit[k] = round(v, 4)
}

func (r *runResult) latency(k string, st latencyStats) {
	if r.LatencyMS == nil {
		r.LatencyMS = map[string]latencyStats{}
	}
	r.LatencyMS[k] = st
}

func (r *runResult) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

func (r *runResult) addFailures(f map[string]int64) {
	for k, n := range f {
		if r.Failures == nil {
			r.Failures = map[string]int64{}
		}
		r.Failures[k] += n
	}
}

// heapInUse returns the Go heap in use after a collection.
func heapInUse() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

// storeFacts records what the open store holds in memory. It is for a store
// that no other goroutine is using.
func (r *runResult) storeFacts(s *Store) {
	r.HotEntries, r.Segments, r.PinPairs = i64(len(s.hot)), i64(len(s.segs)), i64(s.pins.pairs)
	r.count("lastSeq", int64(s.Durable()))
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// millis returns d in milliseconds to the nanosecond, so that a lookup that
// costs under a microsecond is still told from nothing.
func millis(d time.Duration) float64 { return round(msFloat(d), 6) }

// msFloat returns d in milliseconds, unrounded.
func msFloat(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// nearestRank returns the sample at rank ceil(p*n/100) of ascending ds.
func nearestRank(ds []time.Duration, p int) time.Duration {
	return ds[max((p*len(ds)+99)/100, 1)-1]
}

// latencyOf summarizes ds, which it sorts, in milliseconds.
func latencyOf(ds []time.Duration) latencyStats {
	slices.Sort(ds)
	return latencyStats{
		N: len(ds), P50: millis(nearestRank(ds, 50)), P95: millis(nearestRank(ds, 95)), P99: millis(nearestRank(ds, 99)), Max: millis(ds[len(ds)-1]),
	}
}

// timedCall is one call into the store, or one request from its arrival to
// its acknowledgement, with times measured from the start of the run.
type timedCall struct {
	name       string
	start, end time.Duration
	request    bool // an arrival-to-acknowledgement record, not a call
}

func groupStats(calls []timedCall) map[string]latencyStats {
	byName := map[string][]time.Duration{}
	for _, c := range calls {
		byName[c.name] = append(byName[c.name], c.end-c.start)
	}
	stats := make(map[string]latencyStats, len(byName))
	for name, ds := range byName {
		stats[name] = latencyOf(ds)
	}
	return stats
}

// maxOverlap returns the most calls open at one instant.
func maxOverlap(calls []timedCall) int {
	type edge struct {
		at    time.Duration
		delta int
	}
	edges := make([]edge, 0, 2*len(calls))
	for _, c := range calls {
		edges = append(edges, edge{c.start, 1}, edge{c.end, -1})
	}
	// A call that ends when another starts does not overlap it.
	slices.SortFunc(edges, func(a, b edge) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.delta, b.delta)) })
	var open, most int
	for _, e := range edges {
		open += e.delta
		most = max(most, open)
	}
	return most
}

// sizeLabel names a body size: 4KiB, 1MiB.
func sizeLabel(n int) string {
	switch {
	case n%(1<<20) == 0:
		return fmt.Sprintf("%dMiB", n>>20)
	case n%(1<<10) == 0:
		return fmt.Sprintf("%dKiB", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}

// osError returns the operation and the operating system's error of the first
// *fs.PathError or *os.LinkError in err's tree, the primary cause of a
// failure that also carries the errors of its cleanup.
func osError(err error) (op string, cause error, ok bool) {
	switch e := err.(type) {
	case *fs.PathError:
		return e.Op, e.Err, true
	case *os.LinkError:
		return e.Op, e.Err, true
	case interface{ Unwrap() []error }:
		for _, c := range e.Unwrap() {
			if op, cause, ok := osError(c); ok {
				return op, cause, true
			}
		}
	case interface{ Unwrap() error }:
		return osError(e.Unwrap())
	}
	return "", nil, false
}

// knownErrors are the errors of the store that errorKey names by their
// message, so that errors that carry an ID or a digest still fall in one class.
var knownErrors = []error{
	appender.ErrRefused, appender.ErrPoisoned, appender.ErrUncertain, appender.ErrClosed,
	ErrBodyTooLarge, ErrBodyUnpublished, ErrBodyCorrupt, ErrBodyMissing, ErrBodyUnavailable, ErrBodyStore,
	ErrCommandMismatch, ErrCommandTime, ErrInconsistent, context.Canceled, context.DeadlineExceeded,
}

// errorKey classifies err for counting failures by error. An operating system
// failure is its operation and error without the paths, which differ for every
// call, and with the error number, which does not depend on the language of
// the message. Another failure is the store errors that it wraps.
func errorKey(err error) string {
	if op, cause, ok := osError(err); ok {
		key := fmt.Sprintf("%s: %v", op, cause)
		var errno syscall.Errno
		if errors.As(cause, &errno) {
			key = fmt.Sprintf("%s [errno %d]", key, uintptr(errno))
		}
		return key
	}
	var known []string
	for _, e := range knownErrors {
		if errors.Is(err, e) {
			known = append(known, e.Error())
		}
	}
	if len(known) > 0 {
		return strings.Join(known, "; ")
	}
	key, _, _ := strings.Cut(err.Error(), "\n")
	return key
}

// ---------------------------------------------------------------------------
// Seeded identifiers and bodies

// mintID returns a UUIDv7 dated ms Unix milliseconds whose other bits come
// from r.
func mintID(ms int64, r *rand.Rand) uuid.UUID {
	var id uuid.UUID
	binary.BigEndian.PutUint64(id[0:8], uint64(ms)<<16|r.Uint64()&0xffff)
	binary.BigEndian.PutUint64(id[8:16], r.Uint64())
	id[6] = id[6]&0x0f | 0x70
	id[8] = id[8]&0x3f | 0x80
	return id
}

// fillRandom fills b with bytes from r.
func fillRandom(r *rand.Rand, b []byte) {
	for len(b) >= 8 {
		binary.LittleEndian.PutUint64(b, r.Uint64())
		b = b[8:]
	}
	if len(b) > 0 {
		var tail [8]byte
		binary.LittleEndian.PutUint64(tail[:], r.Uint64())
		copy(b, tail[:])
	}
}

const padAlphabet = "abcdefghijklmnopqrstuvwxyz012345"

// samplePayload returns a fixture.sample payload of exactly n bytes, an
// object with one string of letters and digits from r.
func samplePayload(r *rand.Rand, n int) json.RawMessage {
	const frame = len(`{"pad":""}`)
	b := make([]byte, 0, n)
	b = append(b, `{"pad":"`...)
	for left := n - frame; left > 0; {
		v := r.Uint64()
		for k := 0; k < 8 && left > 0; k, left = k+1, left-1 {
			b = append(b, padAlphabet[v&31])
			v >>= 8
		}
	}
	return append(b, `"}`...)
}

// pinPayloadBytes is the size of a body_pin payload: {"pin_id":"<uuid>"}.
var pinPayloadBytes = len(mustValue(ledger.BodyPin{PinID: mintID(0, rand.New(rand.NewPCG(1, 1)))}.Encode()))

func mustValue[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// submissionDigest is the submission digest of the workload's command id.
func submissionDigest(id uuid.UUID) ledger.Digest {
	return sha256.Sum256([]byte("workload submission " + id.String()))
}

// workloadCommand returns the ledger.command draft of the agent for id.
func workloadCommand(id uuid.UUID, dg ledger.Digest) ledger.Draft {
	return ledger.Draft{
		Type: ledger.TypeCommand, TypeVersion: 1, Actor: ledger.ActorAgent,
		Payload: mustValue(ledger.Command{CommandID: id, SubmissionSHA256: dg}.Encode()),
	}
}

// ---------------------------------------------------------------------------
// The script's records

// scriptWalker produces, one commit at a time, the script of one context. The
// same walker feeds the generator and the live loads, so that both write the
// same records.
type scriptWalker struct {
	r                       *rand.Rand
	agent, session, context uuid.UUID
	bodies                  bool // false: no body is published, and pins become plain records
	pos                     int  // the next commit of the script
	runLogs                 []ledger.Digest
	pinID                   uuid.UUID
	pinned                  bool
}

// newScriptWalker returns the walker of one context; stream distinguishes the
// contexts of a seed.
func newScriptWalker(seed, stream uint64, bodies bool, ms int64) *scriptWalker {
	r := rand.New(rand.NewPCG(seed, stream))
	return &scriptWalker{r: r, agent: mintID(ms, r), session: mintID(ms, r), context: mintID(ms, r), bodies: bodies}
}

// scriptStep is the next commit of a script.
type scriptStep struct {
	command   *ledger.Draft  // the command record of a Submit
	commandID uuid.UUID      // the ID that command carries
	drafts    []ledger.Draft // the commit's other records
	skip      bool           // nothing to commit, because a publication it depended on failed
}

func (s scriptStep) records() int {
	n := len(s.drafts)
	if s.command != nil {
		n++
	}
	return n
}

// all returns every draft of the commit, the command first.
func (s scriptStep) all() []ledger.Draft {
	if s.command == nil {
		return s.drafts
	}
	return append([]ledger.Draft{*s.command}, s.drafts...)
}

// recordSize draws a plain record's payload size: 128 B in 70% of records,
// 1 KiB in 25% and 8 KiB in 5%.
func (w *scriptWalker) recordSize() int {
	switch v := w.r.IntN(100); {
	case v < 70:
		return 128
	case v < 95:
		return 1 << 10
	}
	return 8 << 10
}

// step produces the next commit of the script. ms dates its turn, operation
// and pin IDs and cmdMS its command ID. publish stores one body and returns
// its digest. If it fails, step returns its error and the commit is not made.
func (w *scriptWalker) step(ms, cmdMS int64, publish func(body []byte) (ledger.Digest, error)) (scriptStep, error) {
	k := script[w.pos]
	if w.pos == 0 {
		w.runLogs, w.pinned = w.runLogs[:0], false
	}
	w.pos = (w.pos + 1) % len(script)
	var st scriptStep
	turn := mintID(ms, w.r)
	base := func() ledger.Draft {
		return ledger.Draft{
			Actor: ledger.ActorAgent, AgentID: w.agent, ExecutionSessionID: w.session, ContextID: w.context, TurnID: turn,
		}
	}
	record := func(size int) ledger.Draft {
		d := base()
		d.Type, d.TypeVersion, d.Payload = "fixture.sample", 1, samplePayload(w.r, size)
		return d
	}

	var refs []ledger.Digest
	if n := k.bodyBytes(); n > 0 && w.bodies {
		body := make([]byte, n)
		fillRandom(w.r, body)
		d, err := publish(body)
		if err != nil {
			return st, err
		}
		refs = []ledger.Digest{d}
		if k == stepRunLog {
			w.runLogs = append(w.runLogs, d)
		}
	}

	switch k {
	case stepPlain, stepRunLog:
		a, b := record(w.recordSize()), record(w.recordSize())
		a.OperationID = mintID(ms, w.r)
		a.BodyRefs = refs
		st.drafts = []ledger.Draft{a, b}
	case stepRequest, stepMessage, stepOther:
		effect := record(requestRecordBytes)
		effect.BodyRefs = refs
		id := mintID(cmdMS, w.r)
		command := base()
		command.Type, command.TypeVersion = ledger.TypeCommand, 1
		command.Payload = workloadCommand(id, submissionDigest(id)).Payload
		st.command, st.commandID, st.drafts = &command, id, []ledger.Draft{effect}
	case stepPin:
		switch {
		case !w.bodies:
			st.drafts = []ledger.Draft{record(pinPayloadBytes)}
		case len(w.runLogs) == 0:
			st.skip = true
		default:
			w.pinID, w.pinned = mintID(ms, w.r), true
			pin := base()
			pin.Type, pin.TypeVersion = ledger.TypeBodyPin, 1
			pin.BodyRefs = slices.Clone(w.runLogs)
			slices.SortFunc(pin.BodyRefs, func(a, b ledger.Digest) int { return bytes.Compare(a[:], b[:]) })
			pin.Payload = mustValue(ledger.BodyPin{PinID: w.pinID}.Encode())
			st.drafts = []ledger.Draft{pin}
		}
	case stepUnpin:
		switch {
		case !w.bodies:
			st.drafts = []ledger.Draft{record(pinPayloadBytes)}
		case !w.pinned:
			st.skip = true
		default:
			w.pinned = false
			unpin := base()
			unpin.Type, unpin.TypeVersion = ledger.TypeBodyUnpin, 1
			unpin.Payload = mustValue(ledger.BodyUnpin{PinID: w.pinID}.Encode())
			st.drafts = []ledger.Draft{unpin}
		}
	}
	return st, nil
}

// ---------------------------------------------------------------------------
// Colonies and the fast generator

const (
	// firstDaySample and recentSample are the commands that the sidecar of a
	// colony remembers, so that the dedup scenario can retry them.
	firstDaySample = 20
	recentSample   = 100
	// generationLag ends a colony's history just before generation starts, so
	// that history never lies in the future of a measurement.
	generationLag = time.Minute
	// generateWriters is the number of goroutines that write body files.
	generateWriters = 8
	firstDayHours   = 24
)

// knownCommand is a command that a colony holds, as its sidecar names it.
type knownCommand struct {
	ID         string `json:"id"`
	Submission string `json:"submission"`
}

// colonyInfo is the sidecar that the generator writes beside a colony, last,
// so that its presence marks a complete colony. It is not in the colony
// directory, which holds nothing that the store did not write.
type colonyInfo struct {
	Name          string         `json:"name"`
	Contexts      int            `json:"contexts"`
	Hours         int            `json:"hours"` // modelled hours
	Bodies        bool           `json:"bodies"`
	ScriptRepeats int            `json:"scriptRepeats"`
	Seed          uint64         `json:"seed"`
	ColonyID      string         `json:"colonyId"`
	StartNS       int64          `json:"startNs"` // the start of the first modelled hour
	EndNS         int64          `json:"endNs"`
	GeneratedNS   int64          `json:"generatedNs"`
	SegmentTarget int64          `json:"segmentTarget"`
	Commits       int64          `json:"commits"`
	Records       int64          `json:"records"`
	Commands      int64          `json:"commands"`
	BodyFiles     int64          `json:"bodyFiles"`
	BodyBytes     int64          `json:"bodyBytes"`
	LogBytes      int64          `json:"logBytes"`
	Segments      int64          `json:"segments"`
	FirstDay      []knownCommand `json:"firstDay"` // commands spread over the first modelled day
	Recent        []knownCommand `json:"recent"`   // the newest commands, oldest first
	Collected     bool           `json:"collected,omitempty"`
}

func sidecarPath(dir, name string) string { return filepath.Join(dir, name+".workload.json") }

func readSidecar(dir, name string) (*colonyInfo, error) {
	data, err := os.ReadFile(sidecarPath(dir, name))
	if err != nil {
		return nil, err
	}
	var info colonyInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("%s: %w", sidecarPath(dir, name), err)
	}
	return &info, nil
}

// writeSidecar publishes info beside the colony in dir through a flushed
// temporary file.
func writeSidecar(dir string, info *colonyInfo) error {
	path := sidecarPath(dir, info.Name)
	data := append(mustValue(json.MarshalIndent(info, "", " ")), '\n')
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

type bodyJob struct {
	path string
	data []byte
}

// generator writes a colony without the store: Create and Close make its
// directories and colony.json, and ledger.EncodeCommit then writes the log
// straight into segments, which the generator flushes at the end. The result
// is a valid log that Check and Open must accept. The generator writes body
// files one by one, with no flush of each: a clean shutdown flushes the file
// cache, and the generated colony is a fixture, not a durable record.
type generator struct {
	info      *colonyInfo
	logDir    string
	bodiesDir string
	target    int64 // the segment size past which a commit starts a new segment
	colony    uuid.UUID
	r         *rand.Rand // commit IDs and event times
	chain     ledger.Chain

	f    *os.File
	w    *bufio.Writer
	size int64

	jobs     chan bodyJob
	writers  sync.WaitGroup
	failed   atomic.Bool
	firstErr error
	errOnce  sync.Once

	firstIdx []int64 // the indexes, among the commands, of the first-day sample
	ring     [recentSample]uuid.UUID
}

func (g *generator) fail(err error) {
	g.errOnce.Do(func() { g.firstErr = err })
	g.failed.Store(true)
}

// publish stands for PublishBody: it names the body by its digest and queues
// its file.
func (g *generator) publish(body []byte) (ledger.Digest, error) {
	d := ledger.Digest(sha256.Sum256(body))
	g.jobs <- bodyJob{path: bodyPath(g.bodiesDir, d), data: body}
	g.info.BodyFiles++
	g.info.BodyBytes += int64(len(body))
	return d, nil
}

func (g *generator) startWriters() {
	g.jobs = make(chan bodyJob, 256)
	for range generateWriters {
		g.writers.Go(func() {
			for j := range g.jobs {
				if g.failed.Load() {
					continue // drain, so that publish never blocks
				}
				f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err == nil {
					_, err = f.Write(j.data)
					if cerr := f.Close(); err == nil {
						err = cerr
					}
				}
				if err != nil {
					g.fail(err)
				}
			}
		})
	}
}

// write appends one commit's bytes to the log, starting a new segment before
// a commit that would take a non-empty segment past the target, as the store
// does.
func (g *generator) write(data []byte, first uint64) error {
	if g.f != nil && g.size > 0 && g.size+int64(len(data)) > g.target {
		if err := g.endSegment(); err != nil {
			return err
		}
	}
	if g.f == nil {
		f, err := os.OpenFile(filepath.Join(g.logDir, ledger.SegmentName(first)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		g.f, g.w, g.size = f, bufio.NewWriterSize(f, 1<<20), 0
		g.info.Segments++
	}
	if _, err := g.w.Write(data); err != nil {
		return err
	}
	g.size += int64(len(data))
	g.info.LogBytes += int64(len(data))
	return nil
}

// endSegment flushes and closes the current segment.
func (g *generator) endSegment() error {
	if g.f == nil {
		return nil
	}
	err := g.w.Flush()
	if err == nil {
		err = g.f.Sync()
	}
	if cerr := g.f.Close(); err == nil {
		err = cerr
	}
	g.f, g.w = nil, nil
	return err
}

// event is one commit of the generated history: the step of the script that a
// context takes at a time.
type event struct {
	at      int64 // event time in nanoseconds
	context int
	index   int // the commit's place in its context's change
}

// commit writes the commit of event e, the next step of its context's walker.
func (g *generator) commit(w *scriptWalker, e event) error {
	ms := e.at / int64(time.Millisecond)
	// A command ID is minted a second before its commit, which satisfies the
	// issuance bound.
	cmdMS := (e.at - int64(time.Second)) / int64(time.Millisecond)
	st, err := w.step(ms, cmdMS, g.publish)
	switch {
	case err != nil:
		return err
	case st.skip:
		return errors.New("the script skipped a commit")
	}
	hdr := ledger.CommitHeader{ColonyID: g.colony, CommitID: mintID(ms, g.r), EventTimeNS: e.at}
	data, next, err := ledger.EncodeCommit(g.chain, hdr, st.all())
	if err != nil {
		return fmt.Errorf("encoding the commit after seq %d: %w", g.chain.Seq, err)
	}
	if err := g.write(data, g.chain.Seq+1); err != nil {
		return err
	}
	g.chain = next
	g.info.Commits++
	g.info.Records += int64(st.records())
	if st.command != nil {
		idx := g.info.Commands
		g.info.Commands++
		g.ring[idx%recentSample] = st.commandID
		if len(g.firstIdx) > 0 && idx == g.firstIdx[0] {
			g.firstIdx = g.firstIdx[1:]
			g.info.FirstDay = append(g.info.FirstDay, known(st.commandID))
		}
	}
	return nil
}

func known(id uuid.UUID) knownCommand {
	return knownCommand{ID: id.String(), Submission: submissionDigest(id).String()}
}

// genTimes are the times of one generation.
type genTimes struct{ write, check time.Duration }

// generateColony writes the colony of spec in spec.Dir and its sidecar beside
// it. Event times are spread over spec.Hours modelled hours that end a minute
// before now. Each context runs one change in every ten-minute slot of every
// hour, whose commits fall at uniformly random times in the slot, which is how
// a Poisson process with a known number of arrivals spreads them. Randomness
// comes from the seed: the payloads, the bodies, the random bits of the IDs
// and the offsets of the events repeat for a seed, while the times follow now.
func generateColony(cat ledger.Catalogue, spec childSpec, now time.Time) (*colonyInfo, genTimes, error) {
	var gt genTimes
	s, err := Create(spec.Dir, cat)
	if err != nil {
		return nil, gt, err
	}
	colony := s.Colony()
	if err := s.Close(); err != nil {
		return nil, gt, err
	}

	target := int64(segmentTarget)
	if spec.Smoke {
		target = smokeSegmentTarget
	}
	endNS := now.Add(-generationLag).UnixNano()
	startNS := endNS - int64(spec.Hours)*int64(time.Hour)
	g := &generator{
		info: &colonyInfo{
			Name: spec.Colony, Contexts: spec.Contexts, Hours: spec.Hours, Bodies: spec.Bodies, ScriptRepeats: scriptRepeats, Seed: spec.Seed,
			ColonyID: colony.String(), StartNS: startNS, EndNS: endNS, GeneratedNS: now.UnixNano(), SegmentTarget: target,
		},
		logDir: filepath.Join(spec.Dir, logName), bodiesDir: filepath.Join(spec.Dir, bodiesName), target: target,
		colony: colony, r: rand.New(rand.NewPCG(spec.Seed, ^uint64(0))), chain: ledger.Genesis(),
	}
	firstDayCommands := int64(spec.Contexts) * int64(min(spec.Hours, firstDayHours)) * int64(contextHourCounts().commands)
	for j := range firstDaySample {
		g.firstIdx = append(g.firstIdx, int64(2*j+1)*firstDayCommands/(2*firstDaySample))
	}
	if spec.Bodies {
		g.startWriters()
	}

	begin := time.Now()
	walkers := make([]*scriptWalker, spec.Contexts)
	for c := range walkers {
		walkers[c] = newScriptWalker(spec.Seed, uint64(c), spec.Bodies, startNS/int64(time.Millisecond))
	}
	perChange := len(script) * scriptRepeats
	slotNS := int64(changeDuration)
	slots := spec.Hours * changesPerHour
	var events []event
	times := make([]int64, perChange)
	for slot := range slots {
		slotStart := startNS + int64(slot)*slotNS
		events = events[:0]
		for c := range spec.Contexts {
			for i := range times {
				times[i] = slotStart + g.r.Int64N(slotNS)
			}
			slices.Sort(times)
			for i, at := range times {
				events = append(events, event{at: at, context: c, index: i})
			}
		}
		slices.SortFunc(events, func(a, b event) int {
			return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.context, b.context), cmp.Compare(a.index, b.index))
		})
		for _, e := range events {
			if g.failed.Load() {
				break
			}
			if err := g.commit(walkers[e.context], e); err != nil {
				g.fail(err)
				break
			}
		}
		if g.failed.Load() {
			break
		}
		if (slot+1)%(4*changesPerHour) == 0 || slot+1 == slots {
			fmt.Fprintf(os.Stderr, "generated %d of %d hours: %d commits, %.2f GiB of log, %d body files\n",
				(slot+1)/changesPerHour, spec.Hours, g.info.Commits, float64(g.info.LogBytes)/(1<<30), g.info.BodyFiles)
		}
	}
	if g.jobs != nil {
		close(g.jobs)
		g.writers.Wait()
	}
	if err := g.firstErr; err != nil {
		return nil, gt, err
	}
	if err := g.endSegment(); err != nil {
		return nil, gt, err
	}
	dirs := []string{g.logDir, spec.Dir}
	if spec.Bodies {
		for i := range 256 {
			dirs = append(dirs, filepath.Join(g.bodiesDir, fmt.Sprintf("%02x", i)))
		}
		dirs = append(dirs, g.bodiesDir)
	}
	for _, dir := range dirs {
		if err := (osFiles{}).syncDir(dir); err != nil {
			return nil, gt, err
		}
	}
	gt.write = time.Since(begin)

	// The generator and the model must agree on what was written.
	want := contextHourCounts().times(spec.Contexts * spec.Hours)
	if !spec.Bodies {
		want.bodies, want.bodyBytes = 0, 0
	}
	got := modelCounts{int(g.info.Commits), int(g.info.Records), int(g.info.Commands), int(g.info.BodyFiles), g.info.BodyBytes}
	if got != want {
		return nil, gt, fmt.Errorf("the generator wrote %+v, but the model says %+v", got, want)
	}
	n := min(g.info.Commands, recentSample)
	for i := g.info.Commands - n; i < g.info.Commands; i++ {
		g.info.Recent = append(g.info.Recent, known(g.ring[i%recentSample]))
	}

	checkStart := time.Now()
	res, err := ledger.Check(g.logDir, colony)
	gt.check = time.Since(checkStart)
	switch {
	case err != nil:
		return nil, gt, fmt.Errorf("Check rejects the generated log: %w", err)
	case res.Last.Seq != uint64(g.info.Records) || res.TornBytes != 0:
		return nil, gt, fmt.Errorf("Check reports seq %d and %d torn bytes, want seq %d and none", res.Last.Seq, res.TornBytes, g.info.Records)
	}
	return g.info, gt, writeSidecar(filepath.Dir(spec.Dir), g.info)
}

// ---------------------------------------------------------------------------
// Live loads

// liveRun is one timed run of the script against a store.
type liveRun struct {
	s       *Store
	begin   time.Time
	commits atomic.Int64
}

func (lr *liveRun) since() time.Duration { return time.Since(lr.begin) }

// worker is one context, or one request of a burst, running the script.
type worker struct {
	lr     *liveRun
	walker *scriptWalker
	prefix string // names the series of a burst's requests

	calls                                                []timedCall
	fails                                                map[string]int64
	commits, records, publishes, publishedBytes, skipped int64
	fatal                                                error
}

func newWorker(lr *liveRun, seed, stream uint64, prefix string) *worker {
	return &worker{lr: lr, prefix: prefix, walker: newScriptWalker(seed, stream, true, lr.begin.UnixMilli())}
}

// fail counts a failed call by its error. A failure that leaves the store
// unusable ends the worker, and the run is invalid.
func (wk *worker) fail(op string, err error) {
	if wk.fails == nil {
		wk.fails = map[string]int64{}
	}
	wk.fails[op+": "+errorKey(err)]++
	if errors.Is(err, appender.ErrUncertain) || errors.Is(err, appender.ErrPoisoned) || errors.Is(err, appender.ErrClosed) {
		wk.fatal = err
	}
}

// runStep runs the script's next commit for a request that arrived at the
// given time: it publishes the commit's body, if any, and then commits. A
// failed call is counted and not repeated; the commit that needed a body that
// failed to publish is skipped.
func (wk *worker) runStep(arrival time.Duration) {
	lr := wk.lr
	ctx := context.Background()
	nowMS := time.Now().UnixMilli()
	st, err := wk.walker.step(nowMS, nowMS, func(body []byte) (ledger.Digest, error) {
		label := sizeLabel(len(body))
		t0 := lr.since()
		d, n, err := lr.s.PublishBody(ctx, bytes.NewReader(body))
		t1 := lr.since()
		if err != nil {
			wk.fail("publish "+label, err)
			return ledger.Digest{}, err
		}
		wk.calls = append(wk.calls, timedCall{name: wk.prefix + "publish/" + label, start: t0, end: t1})
		wk.publishes++
		wk.publishedBytes += n
		return d, nil
	})
	if err != nil || st.skip {
		wk.skipped++
		return
	}
	name := "append"
	t0 := lr.since()
	if st.command != nil {
		name = "submit"
		_, err = lr.s.Submit(ctx, *st.command, st.drafts)
	} else {
		_, err = lr.s.Append(ctx, st.drafts)
	}
	t1 := lr.since()
	if err != nil {
		wk.fail(name, err)
		return
	}
	wk.calls = append(wk.calls,
		timedCall{name: wk.prefix + name, start: t0, end: t1},
		timedCall{name: wk.prefix + "ack", start: arrival, end: t1, request: true})
	wk.commits++
	wk.records += int64(st.records())
	lr.commits.Add(1)
}

// loadParams describe a timed load.
type loadParams struct {
	contexts int
	duration time.Duration
	poisson  bool          // arrivals follow the model's Poisson process; otherwise a context never waits
	burstAt  time.Duration // if above 0, a burst of burstFactor*contexts requests is released then
	seed     uint64
}

// arrivals returns the times of a Poisson process of the given rate within d.
func arrivals(r *rand.Rand, perSecond float64, d time.Duration) []time.Duration {
	var out []time.Duration
	for t := 0.0; ; {
		t += r.ExpFloat64() / perSecond
		if t >= d.Seconds() {
			return out
		}
		out = append(out, time.Duration(t*float64(time.Second)))
	}
}

// liveOutcome is what a load measured.
type liveOutcome struct {
	calls                                                         []timedCall
	fails                                                         map[string]int64
	offered, commits, records, publishes, publishedBytes, skipped int64
	burstRequests                                                 int
	elapsed, release, drain                                       time.Duration
	fatal                                                         error
}

func (o *liveOutcome) merge(wk *worker) {
	o.calls = append(o.calls, wk.calls...)
	for k, n := range wk.fails {
		if o.fails == nil {
			o.fails = map[string]int64{}
		}
		o.fails[k] += n
	}
	o.commits += wk.commits
	o.records += wk.records
	o.publishes += wk.publishes
	o.publishedBytes += wk.publishedBytes
	o.skipped += wk.skipped
	if o.fatal == nil {
		o.fatal = wk.fatal
	}
}

// drive runs the script against s, one goroutine for each of p.contexts
// contexts: with Poisson arrivals at the model's rate, or without waits. With
// p.burstAt it also releases a burst of requests at once, on top of the load.
func drive(s *Store, p loadParams) *liveOutcome {
	lr := &liveRun{s: s, begin: time.Now()}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				fmt.Fprintf(os.Stderr, "t=%s: %d commits acknowledged\n", lr.since().Round(time.Second), lr.commits.Load())
			case <-stop:
				return
			}
		}
	}()

	out := &liveOutcome{}
	rate := float64(len(script)*scriptRepeats) / changeDuration.Seconds()
	workers := make([]*worker, p.contexts)
	var wg sync.WaitGroup
	for c := range workers {
		wk := newWorker(lr, p.seed, uint64(c), "")
		workers[c] = wk
		var schedule []time.Duration
		if p.poisson {
			schedule = arrivals(rand.New(rand.NewPCG(p.seed, 1<<20+uint64(c))), rate, p.duration)
			out.offered += int64(len(schedule))
		}
		wg.Go(func() {
			if !p.poisson {
				for lr.since() < p.duration && wk.fatal == nil {
					wk.runStep(lr.since())
				}
				return
			}
			for _, at := range schedule {
				if wait := at - lr.since(); wait > 0 {
					time.Sleep(wait)
				}
				if wk.runStep(at); wk.fatal != nil {
					return
				}
			}
		})
	}

	var burst []*worker
	if p.burstAt > 0 {
		wg.Go(func() {
			time.Sleep(p.burstAt - lr.since())
			gate := make(chan struct{})
			var bwg sync.WaitGroup
			for i := range burstFactor * p.contexts {
				bw := newWorker(lr, p.seed, 1<<32+uint64(i), "burst/")
				burst = append(burst, bw)
				bwg.Go(func() {
					<-gate
					bw.runStep(out.release)
				})
			}
			out.release = lr.since()
			close(gate)
			bwg.Wait()
		})
	}
	wg.Wait()
	out.elapsed = lr.since()

	for _, wk := range append(workers, burst...) {
		out.merge(wk)
	}
	out.burstRequests = len(burst)
	for _, c := range out.calls {
		if c.name == "burst/ack" {
			out.drain = max(out.drain, c.end-out.release)
		}
	}
	return out
}

// fill records the outcome of a load, which it consumes.
func (o *liveOutcome) fill(r *runResult, p loadParams) {
	r.addFailures(o.fails)
	r.count("commits", o.commits)
	r.count("records", o.records)
	r.count("bodiesPublished", o.publishes)
	r.count("bodyBytesPublished", o.publishedBytes)
	r.count("skipped", o.skipped)
	if p.poisson {
		r.count("offered", o.offered)
	}
	if o.burstRequests > 0 {
		r.count("burstRequests", int64(o.burstRequests))
		r.timeMS("burstRelease", o.release)
		r.timeMS("burstDrain", o.drain)
		// A steady call that overlaps the burst is set apart from the others.
		for i, c := range o.calls {
			if !strings.HasPrefix(c.name, "burst/") && c.start <= o.release+o.drain && c.end >= o.release {
				o.calls[i].name += "@burst"
			}
		}
		r.note("@burst marks steady calls whose duration overlaps [burstRelease, burstRelease+burstDrain]")
	}
	for name, st := range groupStats(o.calls) {
		r.latency(name, st)
	}
	var requests, store []timedCall
	for _, c := range o.calls {
		if c.request {
			requests = append(requests, c)
		} else {
			store = append(store, c)
		}
	}
	r.count("maxInFlight", int64(maxOverlap(requests)))
	r.count("maxInStore", int64(maxOverlap(store)))
	r.note("maxInFlight counts requests from arrival to acknowledgement, maxInStore calls into the store; CPU time includes the load generator in the same process")

	// The rates are over the load's duration, or over the time to the last
	// acknowledgement if the store was still working after it.
	window := max(o.elapsed, p.duration)
	secs := window.Seconds()
	r.timeMS("elapsed", o.elapsed)
	r.timeMS("window", window)
	r.rate("commitsPerSec", float64(o.commits)/secs)
	r.rate("recordsPerSec", float64(o.records)/secs)
	model := modelCommitsPerSecond(p.contexts)
	r.rate("modelCommitsPerSec", model)
	if !p.poisson {
		r.rate("headroom", float64(o.commits)/secs/model)
		r.note("headroom is a calculation: commits per second divided by the model's commits per second at this many contexts")
	}
	o.calls = nil
}

// ---------------------------------------------------------------------------
// Child roles

const (
	roleGenerate      = "generate"
	roleRecover       = "recover"
	roleSequential    = "sequential"
	roleSteady        = "steady"
	roleSaturation    = "saturation"
	roleCrashSaturate = "crash-saturate"
	rolePublish       = "publish"
	roleDedup         = "dedup"
	roleCollect       = "collect"

	// formOpen is one Open, the first access to a colony; formCheckOpen is
	// Check alone and then Open three times.
	formOpen      = "open"
	formCheckOpen = "check-open3"
)

// childSpec is what the parent tells a child.
type childSpec struct {
	Role       string `json:"role"`
	Scenario   string `json:"scenario"`
	Dir        string `json:"dir"` // the colony to use, or a fresh scratch directory
	Colony     string `json:"colony,omitempty"`
	Contexts   int    `json:"contexts,omitempty"`
	Hours      int    `json:"hours,omitempty"`
	Bodies     bool   `json:"bodies,omitempty"`
	Smoke      bool   `json:"smoke,omitempty"`
	Seed       uint64 `json:"seed,omitempty"`
	DurationNS int64  `json:"durationNs,omitempty"`
	BurstAtNS  int64  `json:"burstAtNs,omitempty"`
	Form       string `json:"form,omitempty"`
	CutoffNS   int64  `json:"cutoffNs,omitempty"`   // collect: the policy's BeforeNS
	MinDurable uint64 `json:"minDurable,omitempty"` // recover after a kill: the last acknowledged seq
	Scratch    bool   `json:"scratch,omitempty"`    // the parent removes Dir after the run
}

var roles = map[string]func(testing.TB, childSpec) (*runResult, error){
	roleGenerate:      runGenerate,
	roleRecover:       runRecover,
	roleSequential:    runSequential,
	roleSteady:        runLive,
	roleSaturation:    runLive,
	roleCrashSaturate: runCrashSaturate,
	rolePublish:       runPublish,
	roleDedup:         runDedup,
	roleCollect:       runCollect,
}

// dirBytes returns the number of files under dir and their bytes.
func dirBytes(dir string) (files, size int64, err error) {
	err = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		files++
		size += fi.Size()
		return nil
	})
	return files, size, err
}

func runGenerate(t testing.TB, spec childSpec) (*runResult, error) {
	info, gt, err := generateColony(testCatalogue(t), spec, time.Now())
	if err != nil {
		return nil, err
	}
	r := newResult(spec)
	r.count("commits", info.Commits)
	r.count("records", info.Records)
	r.count("commands", info.Commands)
	r.count("bodyFiles", info.BodyFiles)
	r.LogBytes, r.BodyBytes, r.Segments = i64(info.LogBytes), i64(info.BodyBytes), i64(info.Segments)
	r.timeMS("generate", gt.write)
	r.timeMS("check", gt.check)
	hours := float64(spec.Contexts * spec.Hours)
	r.perUnit("generateMsPerContextHour", msFloat(gt.write)/hours)
	r.perUnit("bytesPerContextHour", float64(info.LogBytes+info.BodyBytes)/hours)
	r.perUnit("logBytesPerContextHour", float64(info.LogBytes)/hours)
	r.perUnit("logBytesPerCommit", float64(info.LogBytes)/float64(info.Commits))
	r.perUnit("logBytesPerRecord", float64(info.LogBytes)/float64(info.Records))
	if info.BodyFiles > 0 {
		r.perUnit("bodyBytesPerBody", float64(info.BodyBytes)/float64(info.BodyFiles))
	}
	if spec.Smoke {
		r.note("smoke: segments are %d bytes, so that the colony spans several", info.SegmentTarget)
	}
	r.note("body files are written without a flush of each; Check verified the log")
	return r, nil
}

func median(ds []time.Duration) time.Duration {
	s := slices.Sorted(slices.Values(ds))
	return s[len(s)/2]
}

func runRecover(t testing.TB, spec childSpec) (*runResult, error) {
	cat := testCatalogue(t)
	r := newResult(spec)
	heapBefore := heapInUse()
	var s *Store
	var rec Recovery
	var open time.Duration
	switch spec.Form {
	case formOpen:
		var err error
		begin := time.Now()
		s, rec, err = Open(spec.Dir, cat)
		open = time.Since(begin)
		if err != nil {
			return nil, err
		}
		r.timeMS("open", open)
	case formCheckOpen:
		id, err := readColony(spec.Dir)
		if err != nil {
			return nil, err
		}
		begin := time.Now()
		if _, err := ledger.Check(filepath.Join(spec.Dir, logName), id); err != nil {
			return nil, err
		}
		r.timeMS("check", time.Since(begin))
		var opens []time.Duration
		for i := range 3 {
			begin := time.Now()
			s, rec, err = Open(spec.Dir, cat)
			opens = append(opens, time.Since(begin))
			if err != nil {
				return nil, err
			}
			r.timeMS(fmt.Sprintf("open%d", i+1), opens[i])
			if i < 2 {
				if err := s.Close(); err != nil {
					return nil, err
				}
			}
		}
		open = median(opens)
		r.timeMS("open", open)
		r.note("open is the median of three; rebuild is about open minus check")
	default:
		return nil, fmt.Errorf("unknown form %q", spec.Form)
	}

	heapAfter := heapInUse()
	r.HeapInUseBytes = heapAfter
	r.count("heapInUseBeforeBytes", int64(heapBefore))
	r.storeFacts(s)
	r.count("truncatedBytes", rec.TruncatedBytes)
	_, logBytes, err := dirBytes(s.logDir)
	if err != nil {
		return nil, err
	}
	r.LogBytes = i64(logBytes)
	if hot := len(s.hot); hot > 0 && heapAfter > heapBefore {
		r.perUnit("heapBytesPerHotEntry", float64(heapAfter-heapBefore)/float64(hot))
		r.note("heapBytesPerHotEntry is a calculation: the heap in use after Open less before it, per hot entry")
	}
	if logBytes > 0 {
		r.perUnit("openMsPerGiBLog", msFloat(open)/(float64(logBytes)/(1<<30)))
	}
	if seq := s.Durable(); seq > 0 {
		r.perUnit("openMsPerMillionRecords", msFloat(open)/(float64(seq)/1e6))
	}
	if spec.MinDurable > 0 {
		r.count("ackedBeforeKill", int64(spec.MinDurable))
		r.count("durableAfterOpen", int64(s.Durable()))
	}
	if info, err := readSidecar(filepath.Dir(spec.Dir), spec.Colony); err == nil && info.Bodies {
		r.BodyBytes = i64(info.BodyBytes)
		r.count("bodyFiles", info.BodyFiles)
	}
	return r, s.Close()
}

// sequentialPayloadBytes returns the payload size at which the line of a
// one-record commit by actor core with no optional field is about 650 bytes.
func sequentialPayloadBytes() int {
	const probe = 10 // the payload bytes of the probe below
	r := rand.New(rand.NewPCG(1, 1))
	after := ledger.Chain{Seq: 999, Hash: strings.Repeat("a", 64), EventTimeNS: 1}
	hdr := ledger.CommitHeader{ColonyID: mintID(0, r), CommitID: mintID(0, r), EventTimeNS: 2}
	draft := ledger.Draft{Type: "fixture.sample", TypeVersion: 1, Actor: ledger.ActorCore, Payload: samplePayload(r, probe)}
	data, _, err := ledger.EncodeCommit(after, hdr, []ledger.Draft{draft})
	if err != nil {
		panic(err)
	}
	return 650 - (len(data) - probe)
}

func runSequential(t testing.TB, spec childSpec) (*runResult, error) {
	s, err := Create(spec.Dir, testCatalogue(t))
	if err != nil {
		return nil, err
	}
	const n = 2000
	size := sequentialPayloadBytes()
	rng := rand.New(rand.NewPCG(spec.Seed, 7))
	ctx := context.Background()
	ds := make([]time.Duration, 0, n)
	begin := time.Now()
	for range n {
		d := ledger.Draft{Type: "fixture.sample", TypeVersion: 1, Actor: ledger.ActorCore, Payload: samplePayload(rng, size)}
		t0 := time.Now()
		_, err := s.Append(ctx, []ledger.Draft{d})
		ds = append(ds, time.Since(t0))
		if err != nil {
			return nil, errors.Join(err, s.Close())
		}
	}
	elapsed := time.Since(begin)
	if got := s.Durable(); got != n {
		return nil, errors.Join(fmt.Errorf("the log holds %d records, but %d commits were acknowledged", got, n), s.Close())
	}
	r := newResult(spec)
	r.latency("append", latencyOf(ds))
	r.count("commits", n)
	r.count("payloadBytes", int64(size))
	r.timeMS("elapsed", elapsed)
	r.rate("commitsPerSec", n/elapsed.Seconds())
	r.HeapInUseBytes = heapInUse()
	_, logBytes, err := dirBytes(s.logDir)
	if err != nil {
		return nil, err
	}
	r.LogBytes = i64(logBytes)
	r.perUnit("logBytesPerCommit", float64(logBytes)/n)
	r.note("comparable in form only to Appendix A.8: one record of about 650 bytes per commit, one writer")
	return r, s.Close()
}

// runLive runs the steady load, with a burst if spec asks for one, or the
// saturation load, on a fresh colony.
func runLive(t testing.TB, spec childSpec) (*runResult, error) {
	s, err := Create(spec.Dir, testCatalogue(t))
	if err != nil {
		return nil, err
	}
	p := loadParams{
		contexts: spec.Contexts, duration: time.Duration(spec.DurationNS), poisson: spec.Role == roleSteady,
		burstAt: time.Duration(spec.BurstAtNS), seed: spec.Seed,
	}
	out := drive(s, p)
	if out.fatal != nil {
		return nil, errors.Join(fmt.Errorf("the load stopped: %w", out.fatal), s.Close())
	}
	r := newResult(spec)
	out.fill(r, p)
	// Every record of a fresh colony was acknowledged to this load.
	if got, want := int64(s.Durable()), r.Counts["records"]; got != want {
		return nil, errors.Join(fmt.Errorf("the log holds %d records, but the load counted %d acknowledged", got, want), s.Close())
	}
	r.HeapInUseBytes = heapInUse()
	r.storeFacts(s)
	_, logBytes, err := dirBytes(s.logDir)
	if err != nil {
		return nil, err
	}
	_, bodyBytes, err := dirBytes(s.bodiesDir)
	if err != nil {
		return nil, err
	}
	r.LogBytes, r.BodyBytes = i64(logBytes), i64(bodyBytes)
	if r.Counts["commits"] > 0 {
		r.perUnit("logBytesPerCommit", float64(logBytes)/float64(r.Counts["commits"]))
	}
	return r, s.Close()
}

// runCrashSaturate is the writer that TestWorkload kills. It opens the colony
// and runs the saturation load until it is killed, printing "running" when the
// load starts and "ack <seq>" every 100 ms with the newest acknowledged
// sequence. It ends by itself after spec.DurationNS, so that an orphan does
// not run for ever; that is a failure of the run.
func runCrashSaturate(t testing.TB, spec childSpec) (*runResult, error) {
	s, _, err := Open(spec.Dir, testCatalogue(t))
	if err != nil {
		return nil, err
	}
	fmt.Println("running")
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if _, err := fmt.Printf("ack %d\n", s.Durable()); err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()
	out := drive(s, loadParams{contexts: spec.Contexts, duration: time.Duration(spec.DurationNS), seed: spec.Seed})
	return nil, errors.Join(fmt.Errorf("the writer was not killed before its limit (fatal: %v)", out.fatal), s.Close())
}

// publishClasses are the bodies/publish scenario's sizes and counts.
var publishClasses = []struct{ size, count int }{
	{4 << 10, 200}, {64 << 10, 200}, {1 << 20, 50}, {16 << 20, 10}, {64 << 20, 5},
}

func runPublish(t testing.TB, spec childSpec) (*runResult, error) {
	s, err := Create(spec.Dir, testCatalogue(t))
	if err != nil {
		return nil, err
	}
	r := newResult(spec)
	ctx := context.Background()
	var publishedBytes int64
	fails := map[string]int64{}
	for ci, cl := range publishClasses {
		label := sizeLabel(cl.size)
		var ds []time.Duration
		var total time.Duration
		var classBytes int64
		for i := range cl.count {
			var seed [32]byte
			binary.LittleEndian.PutUint64(seed[:], uint64(ci)<<32|uint64(i))
			body := io.LimitReader(rand.NewChaCha8(seed), int64(cl.size))
			t0 := time.Now()
			_, n, err := s.PublishBody(ctx, body)
			d := time.Since(t0)
			if err != nil {
				fails["publish "+label+": "+errorKey(err)]++
				continue
			}
			ds = append(ds, d)
			total += d
			classBytes += n
		}
		r.count("attempted/"+label, int64(cl.count))
		r.count("published/"+label, int64(len(ds)))
		if len(ds) > 0 {
			r.latency("publish/"+label, latencyOf(ds))
			r.rate("MiBPerSec/"+label, float64(classBytes)/(1<<20)/total.Seconds())
		}
		publishedBytes += classBytes
	}
	r.addFailures(fails)
	r.count("bodyBytesPublished", publishedBytes)

	// The bodies come from a seeded generator inside the timed call; this is
	// how fast it reads, so that a reader can see how much of a call it is.
	var seed [32]byte
	begin := time.Now()
	if _, err := io.CopyN(io.Discard, rand.NewChaCha8(seed), 64<<20); err != nil {
		return nil, err
	}
	r.rate("sourceMiBPerSec", 64/time.Since(begin).Seconds())
	r.note("each body is read from a seeded generator inside the timed call; sourceMiBPerSec is how fast that generator reads")
	r.HeapInUseBytes = heapInUse()
	return r, s.Close()
}

func runDedup(t testing.TB, spec childSpec) (*runResult, error) {
	info, err := readSidecar(filepath.Dir(spec.Dir), spec.Colony)
	if err != nil {
		return nil, err
	}
	r := newResult(spec)
	d := realDeps()
	if spec.Smoke {
		d.hotCommands = smokeHotCommands
		r.note("smoke: the hot index holds %d commands, so that the cold path runs", smokeHotCommands)
	}
	s, _, err := open(spec.Dir, testCatalogue(t), d)
	if err != nil {
		return nil, err
	}
	r.count("hotCommandsBound", int64(d.hotCommands))

	type probe struct {
		id     uuid.UUID
		digest ledger.Digest
	}
	parse := func(ks []knownCommand) ([]probe, error) {
		var ps []probe
		for _, k := range ks {
			id, err := uuid.Parse(k.ID)
			if err != nil {
				return nil, err
			}
			dg, err := ledger.ParseDigest(k.Submission)
			if err != nil {
				return nil, err
			}
			ps = append(ps, probe{id, dg})
		}
		return ps, nil
	}
	recent, err := parse(info.Recent)
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}
	firstDay, err := parse(info.FirstDay)
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}

	ctx := context.Background()
	rng := rand.New(rand.NewPCG(spec.Seed, 99))
	effect := func() []ledger.Draft {
		return []ledger.Draft{{Type: "fixture.sample", TypeVersion: 1, Actor: ledger.ActorAgent, Payload: samplePayload(rng, requestRecordBytes)}}
	}
	// measure times n Submit calls, counts the log segments they read, and
	// counts the outcomes that are not the case's expected one.
	measure := func(name string, n int, call func(i int) (Receipt, error), ok func(Receipt, error) bool) {
		ds := make([]time.Duration, 0, n)
		scans := s.coldScans.Load()
		var unexpected int64
		for i := range n {
			t0 := time.Now()
			rc, err := call(i)
			ds = append(ds, time.Since(t0))
			if !ok(rc, err) {
				unexpected++
			}
		}
		r.latency("submit/"+name, latencyOf(ds))
		r.count("coldScans/"+name, s.coldScans.Load()-scans)
		r.count("unexpected/"+name, unexpected)
	}
	duplicate := func(rc Receipt, err error) bool { return err == nil && rc.Duplicate }
	recorded := func(rc Receipt, err error) bool { return err == nil && !rc.Duplicate }
	retry := func(ps []probe) func(int) (Receipt, error) {
		return func(i int) (Receipt, error) { return s.Submit(ctx, workloadCommand(ps[i].id, ps[i].digest), nil) }
	}
	fresh := func(ms func() int64) func(int) (Receipt, error) {
		return func(int) (Receipt, error) {
			id := mintID(ms(), rng)
			return s.Submit(ctx, workloadCommand(id, submissionDigest(id)), effect())
		}
	}

	measure("hotDuplicate", len(recent), retry(recent), duplicate)
	measure("freshID", 100, fresh(func() int64 { return time.Now().UnixMilli() }), recorded)
	measure("firstDayDuplicate", len(firstDay), retry(firstDay), duplicate)
	measure("firstDayMismatch", len(firstDay), func(i int) (Receipt, error) {
		other := sha256.Sum256([]byte("mismatch " + firstDay[i].id.String()))
		return s.Submit(ctx, workloadCommand(firstDay[i].id, other), nil)
	}, func(_ Receipt, err error) bool { return errors.Is(err, ErrCommandMismatch) })
	// An ID dated in the first day that the log never held is recorded, so
	// these run last.
	firstDayMS, span := info.StartNS/int64(time.Millisecond), int64(min(info.Hours, firstDayHours))*int64(time.Hour/time.Millisecond)
	measure("firstDayNeverRecorded", firstDaySample, fresh(func() int64 { return firstDayMS + rng.Int64N(span) }), recorded)

	r.count("coldScans", s.coldScans.Load())
	r.HeapInUseBytes = heapInUse()
	r.storeFacts(s)
	r.note("the cases that record a command append to the colony; run dedup once per colony")
	return r, s.Close()
}

// fsEvent is one file-system call that a collection made.
type fsEvent struct {
	start, end int64 // Unix nanoseconds, the clock that event times use
}

// timedFiles is the real file system, timing the calls that a collection
// batch makes on body files and their directories.
type timedFiles struct {
	osFiles
	bodies string // the body store's directory
	mu     sync.Mutex
	events []fsEvent
}

func (f *timedFiles) timed(path string, start int64) {
	if !strings.HasPrefix(path, f.bodies+string(filepath.Separator)) {
		return
	}
	end := time.Now().UnixNano()
	f.mu.Lock()
	f.events = append(f.events, fsEvent{start, end})
	f.mu.Unlock()
}

func (f *timedFiles) remove(path string) error {
	start := time.Now().UnixNano()
	err := f.osFiles.remove(path)
	f.timed(path, start)
	return err
}

func (f *timedFiles) syncDir(dir string) error {
	start := time.Now().UnixNano()
	err := f.osFiles.syncDir(dir)
	f.timed(dir, start)
	return err
}

// batchTimes returns, for each collection record, the time from its event
// time to the end of the last body-file call before the next record. A batch
// stamps its record, then deletes the files and flushes their directories, so
// this is the batch's work after its candidates were chosen; the scan of the
// log tail and the choice precede it.
func batchTimes(recTimes []int64, events []fsEvent) []time.Duration {
	out := make([]time.Duration, len(recTimes))
	for i, start := range recTimes {
		stop := int64(math.MaxInt64)
		if i+1 < len(recTimes) {
			stop = recTimes[i+1]
		}
		end := start
		for _, e := range events {
			if e.start >= start && e.start < stop {
				end = max(end, e.end)
			}
		}
		out[i] = time.Duration(end - start)
	}
	return out
}

func runCollect(t testing.TB, spec childSpec) (*runResult, error) {
	r := newResult(spec)
	dir, err := filepath.Abs(spec.Dir)
	if err != nil {
		return nil, err
	}
	tf := &timedFiles{bodies: filepath.Join(dir, bodiesName)}
	d := realDeps()
	d.files = tf
	var collectStart time.Time
	var phaseA time.Duration
	d.afterCollectScan = func() { phaseA = time.Since(collectStart) }
	s, _, err := open(spec.Dir, testCatalogue(t), d)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*runResult, error) { return nil, errors.Join(err, s.Close()) }

	logFilesBefore, logBytesBefore, err := dirBytes(s.logDir)
	if err != nil {
		return fail(err)
	}
	bodyFilesBefore, bodyBytesBefore, err := dirBytes(s.bodiesDir)
	if err != nil {
		return fail(err)
	}
	freeBefore, freeErr := freeDiskBytes(spec.Dir)

	// One appender issues a commit every 100 ms beside the collection.
	ctx := context.Background()
	begin := time.Now()
	stop := make(chan struct{})
	var appends []timedCall
	appendFails := map[string]int64{}
	rng := rand.New(rand.NewPCG(spec.Seed, 5))
	var appender sync.WaitGroup
	appender.Go(func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			draft := ledger.Draft{Type: "fixture.sample", TypeVersion: 1, Actor: ledger.ActorCore, Payload: samplePayload(rng, requestRecordBytes)}
			t0 := time.Since(begin)
			_, err := s.Append(ctx, []ledger.Draft{draft})
			t1 := time.Since(begin)
			if err != nil {
				appendFails["append: "+errorKey(err)]++
				continue
			}
			appends = append(appends, timedCall{name: "append", start: t0, end: t1})
		}
	})
	time.Sleep(time.Second) // appends with no collection running

	collectStart = time.Now()
	collectBegan := time.Since(begin)
	rep, collectErr := s.Collect(ctx, CollectPolicy{Actor: ledger.ActorOwner, BeforeNS: spec.CutoffNS})
	collectDur := time.Since(collectStart)
	collectEnded := time.Since(begin)
	close(stop)
	appender.Wait()
	if collectErr != nil {
		return fail(fmt.Errorf("Collect: %w", collectErr))
	}

	r.addFailures(appendFails)
	r.timeMS("collect", collectDur)
	r.timeMS("phaseA", phaseA)
	r.count("recorded", int64(rep.Recorded))
	r.count("recordedBytes", rep.RecordedBytes)
	r.count("unreferenced", int64(rep.Unreferenced))
	r.count("unreferencedBytes", rep.UnreferencedBytes)
	r.count("leftovers", int64(rep.Leftovers))
	r.count("leftoversDeleted", int64(rep.LeftoversDeleted))
	r.count("presentBytes", rep.PresentBytes)
	r.count("batches", int64(len(rep.Records)))
	var idle, during []time.Duration
	for _, c := range appends {
		switch {
		case c.end <= collectBegan:
			idle = append(idle, c.end-c.start)
		case c.start <= collectEnded:
			during = append(during, c.end-c.start)
		}
	}
	if len(idle) > 0 {
		r.latency("append@idle", latencyOf(idle))
	}
	if len(during) > 0 {
		r.timeMS("maxAppendStall", slices.Max(during))
		r.latency("append@collect", latencyOf(during))
	}
	r.note("append@idle are the appends before the collection began, append@collect those that overlapped it")

	// The records that the collection wrote, for the batches' times and for
	// bodies to classify. The first record starts a commit, so the commit
	// before it ends at the sequence before.
	type collectedBody struct {
		d   ledger.Digest
		seq uint64
	}
	var recTimes []int64
	var bodies []collectedBody
	if len(rep.Records) > 0 {
		rd, err := ledger.OpenReader(s.LogDir(), ledger.ReadOptions{
			Colony: s.Colony(), After: rep.Records[0] - 1, Through: s.Durable(),
			Consume: ledger.Consumption{ledger.TypeBodiesCollected: {1}},
		})
		if err != nil {
			return fail(err)
		}
		for {
			c, err := rd.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fail(errors.Join(err, rd.Close()))
			}
			for _, rec := range c.Records {
				p, err := ledger.DecodeBodiesCollected(rec.Payload)
				if err != nil {
					return fail(errors.Join(err, rd.Close()))
				}
				recTimes = append(recTimes, rec.EventTimeNS)
				for _, b := range p.Bodies {
					bodies = append(bodies, collectedBody{b.SHA256, rec.Seq})
				}
			}
		}
		if err := rd.Close(); err != nil {
			return fail(err)
		}
		if len(recTimes) != len(rep.Records) {
			return fail(fmt.Errorf("the log holds %d collection records after seq %d, Collect reported %d", len(recTimes), rep.Records[0], len(rep.Records)))
		}
		tf.mu.Lock()
		batches := batchTimes(recTimes, tf.events)
		tf.mu.Unlock()
		if st := latencyOf(batches); st.N > 0 {
			r.latency("batch", st)
		}
		r.note("batch is the time from a batch's record being stamped to its last body-file call; the scan of the log tail and the choice of bodies come before")
	}

	// ReadBody of ten collected bodies, spread over the collection.
	if len(bodies) > 0 {
		var ds []time.Duration
		var mismatches int64
		for i := range 10 {
			want := bodies[(2*i+1)*len(bodies)/20]
			t0 := time.Now()
			_, err := s.ReadBody(ctx, want.d)
			ds = append(ds, time.Since(t0))
			var ue *BodyUnavailableError
			if !errors.As(err, &ue) || ue.Digest != want.d || ue.Seq != want.seq {
				mismatches++
			}
		}
		r.latency("readBodyCollected", latencyOf(ds))
		r.count("classificationMismatches", mismatches)
	}

	logFiles, logBytes, err := dirBytes(s.logDir)
	if err != nil {
		return fail(err)
	}
	bodyFiles, bodyBytes, err := dirBytes(s.bodiesDir)
	if err != nil {
		return fail(err)
	}
	r.LogBytes, r.BodyBytes = i64(logBytes), i64(bodyBytes)
	r.count("logFilesBefore", logFilesBefore)
	r.count("logBytesBefore", logBytesBefore)
	r.count("bodyFilesBefore", bodyFilesBefore)
	r.count("bodyBytesBefore", bodyBytesBefore)
	r.count("logFilesAfter", logFiles)
	r.count("bodyFilesAfter", bodyFiles)
	r.count("bodyBytesAfter", bodyBytes)
	if freeAfter, err := freeDiskBytes(spec.Dir); freeErr == nil && err == nil {
		r.count("freeBytesBefore", int64(freeBefore))
		r.count("freeBytesAfter", int64(freeAfter))
	}
	r.HeapInUseBytes = heapInUse()
	r.storeFacts(s)
	return r, s.Close()
}

// ---------------------------------------------------------------------------
// The parent: TestWorkload

// raceDetector reports whether this binary was built with -race.
func raceDetector() bool {
	bi, ok := debug.ReadBuildInfo()
	return ok && slices.ContainsFunc(bi.Settings, func(s debug.BuildSetting) bool { return s.Key == "-race" && s.Value == "true" })
}

func uniqueSorted(ns ...int) []int {
	slices.Sort(ns)
	return slices.Compact(ns)
}

func mib(n uint64) float64 { return float64(n) / (1 << 20) }

// progressWriter logs the lines that a child writes to its standard error,
// and keeps the last of them for a failure message.
type progressWriter struct {
	t       *testing.T
	prefix  string
	partial []byte
	tail    []string
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := strings.TrimRight(string(w.partial[:i]), "\r")
		w.partial = w.partial[i+1:]
		w.t.Logf("%s: %s", w.prefix, line)
		if w.tail = append(w.tail, line); len(w.tail) > 40 {
			w.tail = w.tail[1:]
		}
	}
}

func (w *progressWriter) tailText() string { return strings.Join(w.tail, "\n") }

// harness is the parent's state.
type harness struct {
	out      string // the results file
	dir      string // the directory of the generated colonies
	cache    string // cold, warm or empty
	smoke    bool
	contexts []int // 12, 20 and the larger population
	times    runTimes
	results  []*runResult
	logged   int // the results that a summary table has shown
}

// moduleRoot returns the directory of the go.mod above the working directory.
func moduleRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// within reports whether path is root or lies below it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func newHarness(t *testing.T, out string) *harness {
	h := &harness{out: out, cache: os.Getenv(workloadCacheEnv), smoke: os.Getenv(workloadSmokeEnv) == "1"}
	switch h.cache {
	case "", "cold", "warm":
	default:
		t.Fatalf("%s is %q, want cold or warm", workloadCacheEnv, h.cache)
	}
	larger := ownerLarger
	if v := os.Getenv(workloadLargerEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("%s is %q, want a positive integer", workloadLargerEnv, v)
		}
		larger = n
	}
	h.contexts = uniqueSorted(12, 20, larger)
	h.times = timesFor(h.smoke)
	var err error
	if h.out, err = filepath.Abs(out); err != nil {
		t.Fatal(err)
	}
	switch dir := os.Getenv(workloadDirEnv); {
	case dir != "":
		if h.dir, err = filepath.Abs(dir); err != nil {
			t.Fatal(err)
		}
		if root, ok := moduleRoot(); ok && within(root, h.dir) {
			t.Fatalf("%s is %s, inside the repository %s; colonies take tens of gigabytes and belong outside it", workloadDirEnv, h.dir, root)
		}
	case h.smoke:
		h.dir = t.TempDir()
	default:
		t.Fatalf("set %s to a directory outside the repository: the colonies must survive the restart of the machine", workloadDirEnv)
	}
	for _, dir := range []string{filepath.Dir(h.out), h.dir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := peakMemory(); err != nil {
		t.Fatalf("this platform cannot report peak memory: %v", err)
	}
	t.Logf("results %s; colonies %s; N %d; smoke %v; cache label %q; %s/%s, %d CPUs, %s",
		h.out, h.dir, larger, h.smoke, h.cache, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	t.Log("\n" + modelSummary(h.contexts))
	return h
}

// scratch returns a fresh directory for a run on a new colony.
func (h *harness) scratch(slug string) string {
	dir := filepath.Join(h.dir, "scratch", slug)
	os.RemoveAll(dir)
	return dir
}

func (h *harness) timeout(spec childSpec) time.Duration {
	switch spec.Role {
	case roleSteady, roleSaturation, roleCrashSaturate:
		return time.Duration(spec.DurationNS) + 30*time.Minute
	case roleGenerate, roleRecover, roleDedup, roleCollect:
		return 6 * time.Hour
	}
	return time.Hour
}

func childCommand(ctx context.Context, spec childSpec) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperWorkloadChild$")
	cmd.Env = append(os.Environ(), workloadChildEnv+"="+string(mustValue(json.Marshal(spec))))
	return cmd
}

func parseResult(out []byte) (*runResult, error) {
	lines := bytes.Split(out, []byte("\n"))
	for _, line := range slices.Backward(lines) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var r runResult
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			return nil, err
		}
		return &r, nil
	}
	return nil, errors.New("the child printed no JSON object")
}

// exec runs a child to its end and returns its result with the child's CPU
// time. A failure is reported and returns false.
func (h *harness) exec(t *testing.T, spec childSpec) (*runResult, bool) {
	t.Helper()
	spec.Smoke = h.smoke
	ctx, cancel := context.WithTimeout(context.Background(), h.timeout(spec))
	defer cancel()
	cmd := childCommand(ctx, spec)
	var stdout bytes.Buffer
	progress := &progressWriter{t: t, prefix: spec.Scenario}
	cmd.Stdout, cmd.Stderr = &stdout, progress
	begin := time.Now()
	err := cmd.Run()
	wall := time.Since(begin)
	if spec.Scratch {
		if rerr := os.RemoveAll(spec.Dir); rerr != nil {
			t.Logf("%s: removing %s: %v", spec.Scenario, spec.Dir, rerr)
		}
	}
	if err != nil {
		t.Errorf("%s: %v\nstdout:\n%s\nstderr, last lines:\n%s", spec.Scenario, err, stdout.String(), progress.tailText())
		return nil, false
	}
	r, err := parseResult(stdout.Bytes())
	if err != nil {
		t.Errorf("%s: %v\nstdout:\n%s", spec.Scenario, err, stdout.String())
		return nil, false
	}
	r.Cache = h.cache
	r.WallSec = round(wall.Seconds(), 3)
	r.CPUUserSec = round(cmd.ProcessState.UserTime().Seconds(), 3)
	r.CPUSysSec = round(cmd.ProcessState.SystemTime().Seconds(), 3)
	return r, true
}

// run is exec followed by appending the result to the results file.
func (h *harness) run(t *testing.T, spec childSpec) *runResult {
	t.Helper()
	r, ok := h.exec(t, spec)
	if !ok {
		return nil
	}
	line := append(mustValue(json.Marshal(r)), '\n')
	f, err := os.OpenFile(h.out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		_, err = f.Write(line)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		t.Fatalf("appending to %s: %v", h.out, err)
	}
	h.results = append(h.results, r)
	t.Log(r.summary())
	return r
}

// summary is one line for the log.
func (r *runResult) summary() string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(r.TimesMS)) {
		parts = append(parts, fmt.Sprintf("%s %.1f ms", k, r.TimesMS[k]))
	}
	for _, k := range slices.Sorted(maps.Keys(r.Rates)) {
		parts = append(parts, fmt.Sprintf("%s %.4g", k, r.Rates[k]))
	}
	for _, k := range slices.Sorted(maps.Keys(r.LatencyMS)) {
		l := r.LatencyMS[k]
		parts = append(parts, fmt.Sprintf("%s p50/p99/max %.2f/%.2f/%.2f ms (n=%d)", k, l.P50, l.P99, l.Max, l.N))
	}
	for _, k := range slices.Sorted(maps.Keys(r.Failures)) {
		parts = append(parts, fmt.Sprintf("FAILED %d x %s", r.Failures[k], k))
	}
	return fmt.Sprintf("%s: %s", r.Scenario, strings.Join(parts, "; "))
}

// table logs a summary of the runs since the last one.
func (h *harness) table(t *testing.T) {
	if h.logged == len(h.results) {
		return
	}
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "scenario\tcache\twall s\tcpu s\tpeak ws MiB\tpeak commit MiB\theap MiB\t")
	for _, r := range h.results[h.logged:] {
		fmt.Fprintf(w, "%s\t%s\t%.1f\t%.1f\t%.1f\t%.1f\t%.1f\t\n",
			r.Scenario, cmp.Or(r.Cache, "-"), r.WallSec, r.CPUUserSec+r.CPUSysSec, mib(r.PeakWorkingSetBytes), mib(r.PeakCommitBytes), mib(r.HeapInUseBytes))
	}
	w.Flush()
	h.logged = len(h.results)
	t.Logf("summary\n%s", b.String())
}

// colonies returns the colonies of the generate group, 20x30 apart.
func (h *harness) colonies() []colonySpec {
	var specs []colonySpec
	for _, n := range h.contexts {
		specs = append(specs, colonySpec{contexts: n, days: 1, bodies: true}, colonySpec{contexts: n, days: 7})
	}
	return specs
}

func colonySeed(c colonySpec) uint64 {
	return workloadSeed ^ uint64(c.contexts)<<16 ^ uint64(c.days)<<8
}

func (h *harness) generateSpec(c colonySpec, name string) childSpec {
	return childSpec{
		Role: roleGenerate, Scenario: "generate/" + name, Dir: filepath.Join(h.dir, name), Colony: name,
		Contexts: c.contexts, Hours: c.hours(h.smoke), Bodies: c.bodies, Seed: colonySeed(c),
	}
}

// checkInfo stops the test if a colony in the directory was generated for
// another model or run size, which would make its results mean something else.
func (h *harness) checkInfo(t *testing.T, info *colonyInfo, c colonySpec) {
	t.Helper()
	if hours := c.hours(h.smoke); info.Contexts != c.contexts || info.Hours != hours || info.Bodies != c.bodies || info.ScriptRepeats != scriptRepeats {
		t.Fatalf("%s in %s was generated with %d contexts, %d hours and scriptRepeats %d; this run needs %d, %d and %d. Remove the colony and %s to generate it again",
			c.name(), h.dir, info.Contexts, info.Hours, info.ScriptRepeats, c.contexts, hours, scriptRepeats, sidecarPath(h.dir, c.name()))
	}
}

// generateOne generates colony c unless its sidecar says that it exists.
func (h *harness) generateOne(t *testing.T, c colonySpec) {
	t.Helper()
	name := c.name()
	if info, err := readSidecar(h.dir, name); err == nil {
		h.checkInfo(t, info, c)
		t.Logf("%s is already generated: %d commits, %d body files", name, info.Commits, info.BodyFiles)
		return
	}
	// A colony without its sidecar is an interrupted generation.
	if err := os.RemoveAll(filepath.Join(h.dir, name)); err != nil {
		t.Fatal(err)
	}
	h.run(t, h.generateSpec(c, name))
}

// colony returns the sidecar of colony c, generating the colony first in a
// smoke run if it is missing. It returns nil for the optional 20x30 if that
// is missing.
func (h *harness) colony(t *testing.T, c colonySpec) *colonyInfo {
	t.Helper()
	name := c.name()
	info, err := readSidecar(h.dir, name)
	if errors.Is(err, fs.ErrNotExist) {
		switch {
		case h.smoke:
			h.generateOne(t, c)
			info, err = readSidecar(h.dir, name)
		case c == colony20x30:
			t.Logf("%s is not generated; its scenarios are skipped", name)
			return nil
		default:
			t.Fatalf("%s is not generated in %s; run the generate group first", name, h.dir)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	h.checkInfo(t, info, c)
	return info
}

func (h *harness) generateGroup(t *testing.T) {
	defer h.table(t)
	for _, c := range append(h.colonies(), colony20x30) {
		if c == colony20x30 && !h.smoke {
			need, free, err := h.space20x30()
			if err != nil {
				t.Errorf("room for %s: %v", c.name(), err)
				continue
			}
			if free < need {
				t.Logf("%s is not generated: %d bytes are free, below the %d that it needs after the others (%.1f x its expected size)", c.name(), free, need, room20x30)
				continue
			}
		}
		h.generateOne(t, c)
	}
}

// space20x30 returns the free space that the 20x30 colony needs, and the free
// space of the colonies' volume.
func (h *harness) space20x30() (need, free uint64, err error) {
	info, err := readSidecar(h.dir, colonySpec{contexts: 20, days: 7}.name())
	if err != nil {
		return 0, 0, err
	}
	perContextHour := float64(info.LogBytes) / float64(info.Contexts*info.Hours)
	expected := perContextHour * float64(colony20x30.contexts*colony20x30.hours(false))
	free, err = freeDiskBytes(h.dir)
	return uint64(expected * room20x30), free, err
}

func (h *harness) recoverGroup(t *testing.T) {
	defer h.table(t)
	var forms []string
	switch h.cache {
	case "cold":
		forms = []string{formOpen}
	case "warm":
		forms = []string{formCheckOpen}
	default:
		if !h.smoke {
			t.Fatalf("set %s to cold or warm: the harness cannot observe the file cache", workloadCacheEnv)
		}
		forms = []string{formOpen, formCheckOpen}
	}
	// The parent reads only sidecars, which lie outside the colonies, so that
	// each colony's first access is its cold Open.
	for _, c := range append(h.colonies(), colony20x30) {
		info := h.colony(t, c)
		if info == nil {
			continue
		}
		for _, form := range forms {
			h.run(t, childSpec{
				Role: roleRecover, Scenario: "recover/" + c.name(), Dir: filepath.Join(h.dir, c.name()), Colony: c.name(),
				Contexts: info.Contexts, Hours: info.Hours, Form: form, Seed: workloadSeed,
			})
		}
	}
}

func (h *harness) appendGroup(t *testing.T) {
	defer h.table(t)
	run := func(role, scenario string, n int, tm time.Duration, burstAt time.Duration) {
		r := h.run(t, childSpec{
			Role: role, Scenario: scenario, Dir: h.scratch(strings.ReplaceAll(scenario, "/", "-")), Scratch: true,
			Contexts: n, DurationNS: int64(tm), BurstAtNS: int64(burstAt), Seed: workloadSeed,
		})
		if r != nil && r.Counts["commits"] == 0 {
			t.Errorf("%s: no commit was acknowledged", scenario)
		}
	}
	r := h.run(t, childSpec{Role: roleSequential, Scenario: "append/sequential", Dir: h.scratch("append-sequential"), Scratch: true, Seed: workloadSeed})
	if r != nil && r.Counts["commits"] == 0 {
		t.Error("append/sequential: no commit was acknowledged")
	}
	for _, n := range h.contexts {
		run(roleSteady, fmt.Sprintf("append/steady/%d", n), n, h.times.steady, 0)
	}
	for _, n := range h.contexts {
		run(roleSteady, fmt.Sprintf("append/burst/%d", n), n, h.times.steady, h.times.burstAt)
	}
	for _, n := range h.contexts {
		run(roleSaturation, fmt.Sprintf("append/saturation/%d", n), n, h.times.saturation, 0)
	}
}

func (h *harness) bodiesGroup(t *testing.T) {
	defer h.table(t)
	h.run(t, childSpec{Role: rolePublish, Scenario: "bodies/publish", Dir: h.scratch("bodies-publish"), Scratch: true, Seed: workloadSeed})
}

// killedWriter starts the writer child of spec, waits until it runs and a
// further after, kills it and returns the newest sequence it reported as
// acknowledged.
func (h *harness) killedWriter(t *testing.T, spec childSpec, after time.Duration) (uint64, error) {
	spec.Smoke = h.smoke
	cmd := childCommand(context.Background(), spec)
	progress := &progressWriter{t: t, prefix: spec.Scenario}
	cmd.Stderr = progress
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	var lastAck atomic.Uint64
	running, scanned := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(scanned)
		lines := bufio.NewReader(stdout)
		var once sync.Once
		for {
			// A final line without its LF was cut by the kill and is ignored.
			line, err := lines.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "running" {
				once.Do(func() { close(running) })
			} else if rest, ok := strings.CutPrefix(line, "ack "); ok {
				if n, err := strconv.ParseUint(rest, 10, 64); err == nil {
					lastAck.Store(n)
				}
			}
		}
	}()
	var killOnce sync.Once
	kill := func() {
		killOnce.Do(func() {
			cmd.Process.Kill()
			<-scanned
			cmd.Wait()
		})
	}
	defer kill()
	select {
	case <-running:
	case <-scanned:
		return 0, fmt.Errorf("the writer ended before it started: %s", progress.tailText())
	case <-time.After(h.timeout(spec)):
		return 0, errors.New("the writer did not start")
	}
	time.Sleep(after)
	kill()
	return lastAck.Load(), nil
}

func (h *harness) crashGroup(t *testing.T) {
	defer h.table(t)
	c := colonySpec{contexts: 20, days: 7}
	name := "crash-" + c.name()
	// The writer that is killed changes the colony, so it is generated again
	// for every run.
	for _, p := range []string{filepath.Join(h.dir, name), sidecarPath(h.dir, name)} {
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := h.exec(t, h.generateSpec(c, name)); !ok {
		t.FailNow()
	}
	info, err := readSidecar(h.dir, name)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.dir, name)
	writer := childSpec{
		Role: roleCrashSaturate, Scenario: "recover/crash (writer)", Dir: dir, Colony: name, Contexts: c.contexts,
		DurationNS: int64(h.times.crash + 5*time.Minute), Seed: workloadSeed,
	}
	acked, err := h.killedWriter(t, writer, h.times.crash)
	if err != nil {
		t.Fatal(err)
	}
	r := h.run(t, childSpec{
		Role: roleRecover, Scenario: "recover/crash", Dir: dir, Colony: name, Contexts: info.Contexts, Hours: info.Hours,
		Form: formOpen, MinDurable: acked, Seed: workloadSeed,
	})
	if r != nil && r.Counts["durableAfterOpen"] < r.Counts["ackedBeforeKill"] {
		t.Errorf("Open after the kill reached seq %d, below the %d that the writer had acknowledged", r.Counts["durableAfterOpen"], r.Counts["ackedBeforeKill"])
	}
}

func (h *harness) dedupGroup(t *testing.T) {
	defer h.table(t)
	c := colonySpec{contexts: 20, days: 7}
	info := h.colony(t, c)
	r := h.run(t, childSpec{
		Role: roleDedup, Scenario: "dedup/" + c.name(), Dir: filepath.Join(h.dir, c.name()), Colony: c.name(),
		Contexts: info.Contexts, Hours: info.Hours, Seed: workloadSeed,
	})
	if r == nil {
		return
	}
	for _, k := range slices.Sorted(maps.Keys(r.Counts)) {
		if strings.HasPrefix(k, "unexpected/") && r.Counts[k] > 0 {
			t.Errorf("dedup: %d submissions of the case %s ended otherwise than the case expects", r.Counts[k], strings.TrimPrefix(k, "unexpected/"))
		}
	}
}

func (h *harness) collectGroup(t *testing.T) {
	defer h.table(t)
	c := colonySpec{contexts: 20, days: 1, bodies: true}
	info := h.colony(t, c)
	if info.Collected {
		t.Skipf("%s was collected by an earlier run; remove the colony and %s to generate it again", c.name(), sidecarPath(h.dir, c.name()))
	}
	r := h.run(t, childSpec{
		Role: roleCollect, Scenario: "collect/" + c.name(), Dir: filepath.Join(h.dir, c.name()), Colony: c.name(),
		Contexts: info.Contexts, Hours: info.Hours, Seed: workloadSeed, CutoffNS: info.StartNS + 12*int64(time.Hour),
	})
	if r == nil {
		return
	}
	info.Collected = true
	if err := writeSidecar(h.dir, info); err != nil {
		t.Errorf("recording that %s was collected: %v", c.name(), err)
	}
	if n := r.Counts["classificationMismatches"]; n > 0 {
		t.Errorf("collect: ReadBody misclassified %d of the collected bodies", n)
	}
}

// TestWorkload runs the scenario groups, each run in a child process, and
// appends one JSON line per run to the file that DWARF_LEDGER_WORKLOAD_OUT
// names. See the comment at the top of this file.
func TestWorkload(t *testing.T) {
	out := os.Getenv(workloadOutEnv)
	if out == "" {
		t.Skipf("set %s to a results file to run the workload measurements", workloadOutEnv)
	}
	if raceDetector() {
		t.Skip("the race detector distorts every figure; measure without it")
	}
	h := newHarness(t, out)
	defer func() { t.Logf("%d result lines appended to %s", len(h.results), h.out) }()
	t.Run("generate", h.generateGroup)
	t.Run("recover", h.recoverGroup)
	t.Run("append", h.appendGroup)
	t.Run("bodies", h.bodiesGroup)
	t.Run("crash", h.crashGroup)
	t.Run("dedup", h.dedupGroup)
	t.Run("collect", h.collectGroup)
}

// TestHelperWorkloadChild is the process of one run of TestWorkload: it runs
// the role that the JSON in workloadChildEnv names and prints its result as
// one JSON object. The peak memory figures are taken last, because they are
// the peak of the whole process.
func TestHelperWorkloadChild(t *testing.T) {
	raw := os.Getenv(workloadChildEnv)
	if raw == "" {
		t.Skip("runs only as the child of TestWorkload")
	}
	var spec childSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatalf("the child's spec: %v", err)
	}
	role := roles[spec.Role]
	if role == nil {
		t.Fatalf("unknown role %q", spec.Role)
	}
	r, err := role(t, spec)
	if err != nil {
		t.Fatalf("%s: %v", spec.Scenario, err)
	}
	if r.HeapInUseBytes == 0 {
		r.HeapInUseBytes = heapInUse()
	}
	if r.PeakWorkingSetBytes, r.PeakCommitBytes, err = peakMemory(); err != nil {
		t.Fatalf("%s: %v", spec.Scenario, err)
	}
	fmt.Println(string(mustValue(json.Marshal(r))))
}
