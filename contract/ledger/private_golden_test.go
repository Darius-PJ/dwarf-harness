package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"uuid"
)

const privateDir = "testdata/f3/10/log"

var privateColony = fid("07")
var privateTypes = Consumption{
	TypeCommand: {1}, TypeRuntimeAgentRegistered: {1}, TypeRuntimeContextCreated: {1},
	TypePrivateRevision: {1}, TypeRuntimeCommandReceipt: {1},
}
var privateLog = sync.OnceValues(func() (*goldenLog, error) {
	return buildLog(privateDir, privateColony, privateTypes, privateInputs())
})

func mustPrivate(t testing.TB) *goldenLog {
	t.Helper()
	return mustBuild(t, privateLog)
}

func privateFixtureChange(slot, mode, text string) PrivateChange {
	return PrivateChange{Slot: slot, Mode: mode, SHA256: Digest(sha256.Sum256([]byte(text))), Bytes: int64(len(text))}
}

// privateInputs covers F3-10, proposed for freeze (D125), with complete groups.
func privateInputs() []goldenCommit {
	agent, session, general, work := fid("a1"), fid("b1"), fid("d1"), fid("d2")
	instructions := privateFixtureChange("instructions", "replace", "# Ada\n")
	emptyMemory := privateFixtureChange("memory", "replace", "")
	emptyHandoff := privateFixtureChange("handoff", "replace", "")
	first := privateFixtureChange("transcript", "append", "opaque chunk\n")
	second := first // Equal content still represents a second occurrence.
	second.PreviousSHA256 = first.SHA256
	third := privateFixtureChange("transcript", "append", "another chunk\n")
	third.PreviousSHA256 = second.SHA256
	var out []goldenCommit
	add := func(context uuid.UUID, operation string, target PrivateTarget, expected int64, arguments any, effects ...payload) {
		i := len(out) + 1
		command := fid(fmt.Sprintf("%02x", 0x30+i))
		// The schema-1 producer rule excludes command ID and derived predecessor.
		submission := struct {
			Schema          int           `json:"schema"`
			Actor           string        `json:"actor"`
			AgentID         uuid.UUID     `json:"agent_id"`
			SessionID       uuid.UUID     `json:"session_id"`
			ContextID       uuid.UUID     `json:"context_id"`
			Operation       string        `json:"operation"`
			Target          PrivateTarget `json:"target"`
			ExpectedVersion int64         `json:"expected_version"`
			Arguments       any           `json:"arguments"`
		}{1, "owner", agent, session, context, operation, target, expected, arguments}
		js, err := json.Marshal(submission)
		if err != nil {
			panic(err)
		}
		digest := Digest(sha256.Sum256(js))
		draft := func(typ string, actor Actor, p payload) Draft {
			return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: agent, ExecutionSessionID: session, ContextID: context, OperationID: command, Payload: mustEncode(p)}
		}
		drafts := []Draft{draft(TypeCommand, ActorOwner, Command{CommandID: command, SubmissionSHA256: digest})}
		for _, p := range effects {
			switch p.(type) {
			case RuntimeAgentRegistered:
				drafts = append(drafts, draft(TypeRuntimeAgentRegistered, ActorOwner, p))
			case RuntimeContextCreated:
				drafts = append(drafts, draft(TypeRuntimeContextCreated, ActorOwner, p))
			case PrivateRevision:
				drafts = append(drafts, draft(TypePrivateRevision, ActorCore, p))
			default:
				panic("unexpected private golden effect")
			}
		}
		drafts = append(drafts, draft(TypeRuntimeCommandReceipt, ActorOwner, RuntimeCommandReceipt{CommandID: command, SubmissionSHA256: digest, Operation: operation, Target: target, ExpectedVersion: expected, Outcome: "applied"}))
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: privateColony, CommitID: fid(fmt.Sprintf("%02x", 0xc0+i)), EventTimeNS: 1791000600000000000 + int64(i)*1000000000}, drafts: drafts})
	}
	add(general, "agent_register", PrivateTarget{"agent", []string{}}, 0, struct {
		Directory   string `json:"directory"`
		AgentSHA256 Digest `json:"agent_sha256"`
		AgentBytes  int64  `json:"agent_bytes"`
	}{"Ada-a1", instructions.SHA256, instructions.Bytes},
		RuntimeAgentRegistered{"Ada-a1", general}, RuntimeContextCreated{"general"},
		PrivateRevision{"agent", 0, 1, []PrivateChange{instructions, emptyMemory}},
		PrivateRevision{"context", 0, 1, []PrivateChange{emptyHandoff, emptyMemory}})
	add(work, "context_create", PrivateTarget{"context", []string{}}, 0, RuntimeContextCreated{"work"},
		RuntimeContextCreated{"work"}, PrivateRevision{"context", 0, 1, []PrivateChange{emptyHandoff, emptyMemory}})
	write := func(context uuid.UUID, scope string, expected int64, changes ...PrivateChange) {
		slots := make([]string, len(changes))
		args := struct {
			Changes []privateChangeWire `json:"changes"`
		}{Changes: make([]privateChangeWire, len(changes))}
		for i, c := range changes {
			slots[i] = c.Slot
			args.Changes[i] = privateChangeWire{Slot: c.Slot, Mode: c.Mode, SHA256: c.SHA256}
			if c.Mode != "clear" {
				args.Changes[i].Bytes = &changes[i].Bytes
			}
		}
		add(context, "private_write", PrivateTarget{scope, slots}, expected, args, PrivateRevision{scope, expected, expected + 1, changes})
	}
	write(general, "context", 1, privateFixtureChange("memory", "replace", "general lesson\n"))
	write(work, "context", 1, first)
	write(work, "context", 2, second)
	write(work, "context", 3, privateFixtureChange("continuation", "replace", "opaque continuation"))
	write(work, "context", 4, PrivateChange{Slot: "continuation", Mode: "clear"})
	write(work, "agent", 1, privateFixtureChange("memory", "replace", "retained lesson\n"))
	write(work, "context", 5, privateFixtureChange("handoff", "replace", "next step\n"), privateFixtureChange("memory", "replace", "local lesson\n"), third)
	write(general, "agent", 2, privateFixtureChange("instructions", "replace", "# Ada\nRevised instructions\n"))
	return out
}

// TestGoldenF3_10 pins the complete ten-commit, thirty-four-record history.
func TestGoldenF3_10(t *testing.T) {
	t.Parallel()
	g := mustPrivate(t)
	if *update {
		if err := os.MkdirAll(g.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(g.dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := snapshot(t, g.dir)
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, g.names) {
		t.Fatalf("files=%v, want %v", got, g.names)
	}
	for _, name := range g.names {
		if !bytes.Equal(stored[name], g.files[name]) {
			t.Errorf("%s differs from F3-10 encoding", name)
		}
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: privateTypes})
	if err != nil || len(commits) != 10 || cursor != 34 {
		t.Fatalf("read %d commits cursor %d: %v; want 10,34", len(commits), cursor, err)
	}
	wantLasts := []uint64{6, 10, 13, 16, 19, 22, 25, 28, 31, 34}
	if !slices.Equal(lasts(commits), wantLasts) {
		t.Fatalf("commit boundaries %v, want %v", lasts(commits), wantLasts)
	}
	for i, c := range commits {
		wantTypes := []string{TypeCommand, TypePrivateRevision, TypeRuntimeCommandReceipt}
		if i == 0 {
			wantTypes = []string{TypeCommand, TypeRuntimeAgentRegistered, TypeRuntimeContextCreated, TypePrivateRevision, TypePrivateRevision, TypeRuntimeCommandReceipt}
		}
		if i == 1 {
			wantTypes = []string{TypeCommand, TypeRuntimeContextCreated, TypePrivateRevision, TypeRuntimeCommandReceipt}
		}
		command, err := DecodeCommand(c.Records[0].Payload)
		if err != nil {
			t.Fatal(err)
		}
		for j, r := range c.Records {
			if r.Type != wantTypes[j] || !Defined().Contains(TypeVersion{r.Type, r.TypeVersion}) {
				t.Errorf("seq %d unexpected type/version %s/%d", r.Seq, r.Type, r.TypeVersion)
			}
			actor := ActorOwner
			if r.Type == TypePrivateRevision {
				actor = ActorCore
			}
			context := fid("d2")
			if i == 0 || i == 2 || i == 9 {
				context = fid("d1")
			}
			if r.Actor != actor || r.AgentID != fid("a1") || r.ExecutionSessionID != fid("b1") || r.ContextID != context || r.OperationID != command.CommandID || r.TurnID != (uuid.UUID{}) || r.MeasurementPath != "" || len(r.BodyRefs) != 0 {
				t.Errorf("seq %d invalid private envelope: %+v", r.Seq, r.Envelope)
			}
			var p payload
			if r.Type == TypeCommand {
				p, err = DecodeCommand(r.Payload)
			} else {
				p, err = decodeF3_10(r.Type, r.Payload)
			}
			if err != nil {
				t.Fatalf("seq %d: %v", r.Seq, err)
			}
			if js, err := p.Encode(); err != nil || !bytes.Equal(js, r.Payload) || !bytes.Equal(js, g.inputs[i].drafts[j].Payload) {
				t.Errorf("seq %d re-encoding differs: %v", r.Seq, err)
			}
			if p, ok := p.(RuntimeCommandReceipt); ok {
				if p.CommandID != command.CommandID || p.SubmissionSHA256 != command.SubmissionSHA256 || p.Outcome != "applied" || p.RequestID != (uuid.UUID{}) {
					t.Errorf("seq %d receipt does not bind its command", r.Seq)
				}
			}
		}
	}
	res, err := Check(g.dir, g.colony)
	want := CheckResult{Last: g.end(), Segment: g.final(), Size: int64(len(g.files[g.final()]))}
	if err != nil || res != want {
		t.Errorf("Check=%+v,%v want %+v", res, err, want)
	}
	t.Logf("golden SHA-256=%x commits=10 records=34", sha256.Sum256(stored[g.final()]))
}

func TestF3_10Compatibility(t *testing.T) {
	t.Parallel()
	g := mustPrivate(t)
	older := Consumption{TypeCommand: {1}, TypeBodyPin: {1}, TypeBodyUnpin: {1}, TypeBodiesCollected: {1}}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: older})
	wantSeqs := []uint64{1, 7, 11, 14, 17, 20, 23, 26, 29, 32}
	wantLasts := []uint64{6, 10, 13, 16, 19, 22, 25, 28, 31, 34}
	if err != nil || cursor != 34 || !slices.Equal(seqs(commits), wantSeqs) || !slices.Equal(lasts(commits), wantLasts) {
		t.Fatalf("older consumer: seqs %v lasts %v cursor %d error %v", seqs(commits), lasts(commits), cursor, err)
	}
	for i, c := range commits {
		got, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, After: c.Last, Through: ToEnd, Consume: older})
		if err != nil || cursor != 34 || !slices.Equal(seqs(got), wantSeqs[i+1:]) {
			t.Errorf("After %d: seqs %v cursor %d error %v", c.Last, seqs(got), cursor, err)
		}
	}
}
