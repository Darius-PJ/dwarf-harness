package validation

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type goEvent struct {
	Time        string
	Action      string
	Package     string
	Test        string
	Elapsed     float64
	Output      string
	OutputType  string
	FailedBuild string
	ImportPath  string
	Key         string
	Value       string
	Path        string
}

type nodeState struct {
	state  string
	paused bool
}

type goReducer struct {
	check                                ledger.CheckSpec
	binding                              canaryBinding
	nodes                                map[string]*nodeState
	pending                              []byte
	packageStarted                       bool
	packageTerminal                      string
	executed                             bool
	buildFailed                          bool
	invalid                              bool
	cached                               bool
	canaryRuns, canaryTerminals, markers int
	canaryTerminal                       string
	markerBound                          bool
	markerOutside                        bool
	markerDigest                         ledger.Digest
	markerPID                            int64
}

func newGoReducer(check ledger.CheckSpec, binding canaryBinding) *goReducer {
	r := &goReducer{check: check, binding: binding, nodes: make(map[string]*nodeState)}
	for _, n := range check.Inventory {
		r.nodes[n.Name] = &nodeState{}
	}
	r.nodes[check.Canary.TestName] = &nodeState{}
	return r
}

func decodeGoEvent(line []byte) (goEvent, error) {
	var event goEvent
	d := json.NewDecoder(bytes.NewReader(line))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return event, ledger.ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return event, err
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return event, ledger.ErrInvalid
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil || bytes.Equal(raw, []byte("null")) {
			return event, ledger.ErrInvalid
		}
		var dst any
		switch key {
		case "Time":
			dst = &event.Time
		case "Action":
			dst = &event.Action
		case "Package":
			dst = &event.Package
		case "Test":
			dst = &event.Test
		case "Elapsed":
			dst = &event.Elapsed
		case "Output":
			dst = &event.Output
		case "OutputType":
			dst = &event.OutputType
		case "FailedBuild":
			dst = &event.FailedBuild
		case "ImportPath":
			dst = &event.ImportPath
		case "Key":
			dst = &event.Key
		case "Value":
			dst = &event.Value
		case "Path":
			dst = &event.Path
		default:
			return event, ledger.ErrInvalid
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return event, err
		}
	}
	if _, err := d.Token(); err != nil {
		return event, err
	}
	if _, err := d.Token(); err != io.EOF {
		return event, ledger.ErrInvalid
	}
	if !seen["Action"] {
		return event, ledger.ErrInvalid
	}
	build := event.Action == "build-output" || event.Action == "build-fail"
	if build {
		for key := range seen {
			if key != "Action" && key != "ImportPath" && key != "Output" {
				return event, ledger.ErrInvalid
			}
		}
		if !seen["ImportPath"] || event.Action == "build-output" && event.Output == "" || event.Action == "build-fail" && seen["Output"] {
			return event, ledger.ErrInvalid
		}
		return event, nil
	}
	if seen["ImportPath"] || seen["Output"] && event.Action != "output" || event.Action == "output" && !seen["Output"] ||
		seen["FailedBuild"] && (event.Action != "fail" || event.Test != "") ||
		(seen["Key"] || seen["Value"]) && event.Action != "attr" ||
		seen["Path"] && event.Action != "artifacts" ||
		event.Action == "attr" && event.Key == "" || event.Action == "artifacts" && event.Path == "" || event.Elapsed < 0 {
		return event, ledger.ErrInvalid
	}
	if seen["OutputType"] {
		if event.Action != "output" {
			return event, ledger.ErrInvalid
		}
		switch event.OutputType {
		case "", "frame", "error", "error-continue":
		default:
			return event, ledger.ErrInvalid
		}
	}
	return event, nil
}

func (r *goReducer) write(p []byte) {
	for len(p) != 0 {
		end := bytes.IndexByte(p, '\n')
		if end < 0 {
			r.pending = append(r.pending, p...)
			return
		}
		r.pending = append(r.pending, p[:end]...)
		event, err := decodeGoEvent(r.pending)
		if err != nil {
			r.invalid = true
		} else {
			r.event(event)
		}
		r.pending = r.pending[:0]
		p = p[end+1:]
	}
}

func (r *goReducer) finish() {
	if len(r.pending) != 0 {
		r.invalid = true
	}
}

func (r *goReducer) event(e goEvent) {
	if strings.Contains(e.Output, "(cached)") {
		r.cached = true
	}
	if strings.Contains(e.Output, "DWARF_CANARY_V1:") {
		r.marker(e)
	}
	if e.Action == "build-output" || e.Action == "build-fail" {
		if e.Test != "" {
			r.invalid = true
		}
		r.buildFailed = r.buildFailed || e.Action == "build-fail"
		return
	}
	if e.Package != r.check.Inventory[0].Package {
		r.invalid = true
		return
	}
	if e.FailedBuild != "" {
		r.buildFailed = true
	}
	if e.Test == "" {
		switch e.Action {
		case "start":
			if r.packageStarted || r.packageTerminal != "" {
				r.invalid = true
			}
			r.packageStarted = true
		case "output":
			if !r.packageStarted || r.packageTerminal != "" {
				r.invalid = true
			}
		case "pass", "fail", "skip":
			if !r.packageStarted || r.packageTerminal != "" {
				r.invalid = true
			}
			r.packageTerminal = e.Action
		default:
			r.invalid = true
		}
		return
	}
	node := r.nodes[e.Test]
	if node == nil || !r.packageStarted || r.packageTerminal != "" {
		r.invalid = true
		return
	}
	canary := e.Test == r.check.Canary.TestName
	switch e.Action {
	case "run":
		if node.state != "" {
			r.invalid = true
		}
		node.state = "running"
		r.executed = true
		if canary {
			r.canaryRuns++
		}
		if at := strings.LastIndexByte(e.Test, '/'); at >= 0 {
			parent := r.nodes[e.Test[:at]]
			if parent == nil || parent.state != "running" {
				r.invalid = true
			}
		}
	case "pause":
		if node.state != "running" || node.paused {
			r.invalid = true
		}
		node.paused = true
	case "cont":
		if node.state != "running" || !node.paused {
			r.invalid = true
		}
		node.paused = false
	case "output", "attr", "artifacts":
		if node.state != "running" {
			r.invalid = true
		}
	case "pass", "fail", "skip":
		if node.state != "running" || node.paused {
			r.invalid = true
		}
		node.state = e.Action
		if canary {
			r.canaryTerminals++
			r.canaryTerminal = e.Action
		}
	default:
		r.invalid = true
	}
}

var markerPrefix = regexp.MustCompile(`^    dwarf_canary_v1_test\.go:[1-9][0-9]*: DWARF_CANARY_V1:`)

func (r *goReducer) marker(e goEvent) {
	r.markers += strings.Count(e.Output, "DWARF_CANARY_V1:")
	if e.Test != r.check.Canary.TestName || e.Package != r.check.Inventory[0].Package || e.Action != "output" || !markerPrefix.MatchString(e.Output) {
		r.markerOutside = true
		return
	}
	raw := markerPrefix.ReplaceAllString(e.Output, "")
	if !strings.HasSuffix(raw, "\n") {
		r.markerOutside = true
		return
	}
	raw = strings.TrimSuffix(raw, "\n")
	at := strings.LastIndexByte(raw, ':')
	if at < 0 {
		r.markerOutside = true
		return
	}
	pid, err := strconv.ParseInt(raw[at+1:], 10, 64)
	if err != nil || pid < 1 || strconv.FormatInt(pid, 10) != raw[at+1:] {
		r.markerOutside = true
		return
	}
	expected, _ := json.Marshal(r.binding)
	r.markerBound = raw[:at] == string(expected)
	r.markerPID = pid
	r.markerDigest = sha256.Sum256([]byte("DWARF_CANARY_V1:" + raw))
}

func (r *goReducer) canary() (ledger.ValidationCanaryResult, bool) {
	c := ledger.ValidationCanaryResult{State: "missing", TemplateSHA256: r.check.Canary.TemplateSHA256}
	switch {
	case r.markers > 1 || r.canaryRuns > 1 || r.canaryTerminals > 1 || r.markerOutside:
		c.State = "ambiguous"
	case r.canaryTerminal == "pass":
		c.State = "passed"
	case r.markers == 1 && r.markerPID > 0 && !r.markerBound:
		c.State = "unbound"
		c.MarkerSHA256, c.ObservedPID = r.markerDigest, r.markerPID
	case r.canaryRuns == 1 && r.canaryTerminals == 1 && r.canaryTerminal == "fail" && r.markers == 1 && r.markerBound:
		c.State = "expected_failed"
		c.MarkerSHA256, c.ObservedPID = r.markerDigest, r.markerPID
	}
	return c, c.State != "expected_failed"
}

func (r *goReducer) results() []ledger.ValidationRequiredResult {
	out := make([]ledger.ValidationRequiredResult, 0, len(r.check.Inventory))
	for _, n := range r.check.Inventory {
		state := "missing"
		switch r.nodes[n.Name].state {
		case "running":
			state = "unfinished"
		case "pass":
			state = "passed"
		case "fail":
			state = "failed"
		case "skip":
			state = "skipped"
		}
		out = append(out, ledger.ValidationRequiredResult{Package: n.Package, Name: n.Name, Outcome: state})
	}
	return out
}
