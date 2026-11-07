package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"
)

const continuationDir = "testdata/f3/16/log"

var continuationColony = continuationID(0x100)
var continuationTypes = func() Consumption {
	c := maps.Clone(loopTypes)
	c[TypeRuntimeContinuationChanged], c[TypeRuntimeContinuationReceipt] = []int{1}, []int{1}
	return c
}()
var continuationLog = sync.OnceValues(func() (*goldenLog, error) {
	inputs, _ := continuationInputs()
	return buildLog(continuationDir, continuationColony, continuationTypes, inputs)
})

// continuationInputs preserves the service-ordered F3-15 setup through binding.
// Its new namespace, commands and objects do not rewrite any earlier fixture.
func continuationInputs() ([]goldenCommit, map[string][]byte) {
	prior, priorFiles := loopInputs()
	var out []goldenCommit
	var sequence, boundSequence int64
	var binding RuntimeTurnBound
	var origin Origin
	files := map[string][]byte{}
	appendCommit := func(ds []Draft) {
		now := prior[0].header.EventTimeNS
		if len(out) > 0 {
			now = out[len(out)-1].header.EventTimeNS + int64(time.Second)
		}
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: continuationColony, CommitID: continuationID(0x2000 + len(out)), EventTimeNS: now}, drafts: ds})
		sequence += int64(len(ds))
	}
	for _, c := range prior {
		appendCommit(c.drafts)
		out[len(out)-1].header.EventTimeNS = c.header.EventTimeNS
		for _, d := range c.drafts {
			for path, data := range priorFiles {
				if bytes.Contains(d.Payload, []byte(filepath.Base(path))) {
					files[path] = data
				}
			}
			if d.Type == TypeRuntimeTurnBound {
				binding, _ = DecodeRuntimeTurnBound(d.Payload)
				boundSequence = sequence - 1
				origin = Origin{d.AgentID, d.ExecutionSessionID, d.ContextID}
			}
		}
		if boundSequence != 0 {
			break
		}
	}
	draft := func(command uuid.UUID, typ string, actor Actor, p payload) Draft {
		return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: origin.AgentID, ExecutionSessionID: origin.SessionID, ContextID: origin.ContextID, TurnID: binding.TurnID, OperationID: command, Payload: mustEncode(p)}
	}
	p := RuntimeContinuationChanged{TurnID: binding.TurnID, BoundSequence: boundSequence, ExpectedState: "absent", State: "ready", Generation: 1, WaitReason: "none", DeadlineUTCNS: out[len(out)-1].header.EventTimeNS + int64(time.Hour), Pending: []ContinuationPending{}, NoticeCursors: []ContinuationNoticeCursor{}}
	addOwner := func(operation, actor string, newOperation bool) {
		command := continuationID(0x1000 + len(out))
		if newOperation {
			p.OperationID = command
		}
		p.Version = p.ExpectedVersion + 1
		s := ContinuationSubmission{7, actor, origin, p.TurnID, operation, p.ExpectedVersion, p.ExpectedState, p.arguments()}
		h := Digest(sha256.Sum256(mustEncode(s)))
		r := RuntimeContinuationReceipt{command, h, s, "recorded", sequence + 2}
		appendCommit([]Draft{draft(command, TypeCommand, Actor(actor), Command{command, h}), draft(command, TypeRuntimeContinuationChanged, ActorCore, p), draft(command, TypeRuntimeContinuationReceipt, Actor(actor), r)})
		p.ExpectedVersion, p.ExpectedState = p.Version, p.State
	}
	addOwner("owner.initialize", "core", true)
	p.State, p.Generation = "model_pending", 2
	p.Pending = []ContinuationPending{{Kind: "paid_attempt", ID: continuationID(0x500), CommandID: continuationID(0x501), ExpectedVersion: 0}}
	addOwner("owner.advance", "core", true)
	p.State, p.ResumeState, p.WaitReason = "waiting_resource", "model_pending", "paid_prepare"
	addOwner("owner.advance", "core", false)
	p.State, p.ResumeState, p.WaitReason = "stopping", "", "none"
	p.StopReason, p.CancelCommandID, p.Generation = "cancelled", continuationID(0x502), 3
	addOwner("owner.stop", "owner", true)
	// A stale model completion has no group at this prefix. The test below
	// challenges that prefix's CAS; cancellation never dispatches the promise.
	p.State = "finishing"
	addOwner("owner.advance", "core", false)
	// Reopen is inert; explicit recovery drops the never-committed child.
	p.State, p.Generation, p.Pending = "reconciling", 4, []ContinuationPending{}
	addOwner("owner.reconcile", "core", true)
	cancel := ScopedSubmission{Schema: 2, Actor: "core", Origin: origin, Operation: "turn.cancel", Target: Resource{Kind: "context", Binding: Binding{origin.AgentID, origin.ContextID}, Scope: "context", Slots: []string{}}, GrantID: paidID(0x413), Expected: ScopedExpected{Version: 1}, Arguments: WorkEndArguments{p.TurnID, "cancelled"}}
	h := Digest(sha256.Sum256(mustEncode(cancel)))
	ending := RuntimeWorkEnded{WorkID: p.TurnID, ExpectedVersion: 1, Version: 2, Outcome: "cancelled"}
	nativeReceipt := RuntimeScopedReceipt{CommandID: p.CancelCommandID, SubmissionSHA256: h, Submission: cancel, Outcome: "recorded", Version: 2, WorkID: p.TurnID}
	appendCommit([]Draft{draft(p.CancelCommandID, TypeCommand, ActorCore, Command{p.CancelCommandID, h}), draft(p.CancelCommandID, TypeRuntimeWorkEnded, ActorCore, ending), draft(p.CancelCommandID, TypeRuntimeScopedReceipt, ActorCore, nativeReceipt)})
	p.NativeEndingSequence = sequence - 1
	object := func(data []byte) ObjectRef {
		h := Digest(sha256.Sum256(data))
		files["objects/agents/Ada-a1/contexts/"+origin.ContextID.String()+"/core/objects/"+h.String()] = data
		return ObjectRef{h, int64(len(data))}
	}
	input, _ := json.Marshal(binding.InputObject)
	o, _ := json.Marshal(origin)
	manifest := object(fmt.Appendf(nil, `{"schema":1,"entries":[{"schema":1,"kind":"turn_input","origin":%s,"turn_id":%q,"ordinal":1,"content":{"input_object":%s}},{"schema":1,"kind":"turn_end","origin":%s,"turn_id":%q,"ordinal":2,"content":{"outcome":"cancelled","reason":"cancelled"}}]}`, o, p.TurnID, input, o, p.TurnID))
	command := continuationID(0x1000 + len(out))
	end := LoopSubmission{6, "core", origin, p.TurnID, "loop.end", 1, LoopEndArguments{TurnID: p.TurnID, BoundSequence: boundSequence, Outcome: "cancelled", Reason: "cancelled", EndingCommandID: p.CancelCommandID}}
	h = Digest(sha256.Sum256(mustEncode(end)))
	result := RuntimeTurnResult{TurnID: p.TurnID, BoundSequence: boundSequence, Outcome: "cancelled", Reason: "cancelled", EndingSequence: p.NativeEndingSequence}
	loopReceipt := RuntimeLoopReceipt{command, h, end, "applied", sequence + 3}
	appendCommit([]Draft{draft(command, TypeCommand, ActorCore, Command{command, h}), draft(command, TypePrivateTurnRevision, ActorCore, PrivateTurnRevision{"context", 1, 2, []PrivateChange{{Slot: "transcript", Mode: "append", SHA256: manifest.SHA256, Bytes: manifest.Bytes}}}), draft(command, TypeRuntimeTurnResult, ActorCore, result), draft(command, TypeRuntimeLoopReceipt, ActorCore, loopReceipt)})
	p.State = "terminal"
	addOwner("owner.close", "core", false)
	return out, files
}

// checkContinuationGoldenGroup validates the fixture's recorded references and
// CAS. It is not a Core producer/reducer or evidence of live cancellation.
func checkContinuationGoldenGroup(c Commit, commits []Commit) error {
	if len(c.Records) != 3 {
		return errors.New("continuation group count")
	}
	r, err := DecodeRuntimeContinuationReceipt(c.Records[2].Payload)
	if err != nil {
		return err
	}
	s := r.Submission
	for i, typ := range []string{TypeCommand, TypeRuntimeContinuationChanged, TypeRuntimeContinuationReceipt} {
		x := c.Records[i]
		actor := Actor(s.Actor)
		if i == 1 {
			actor = ActorCore
		}
		if x.Type != typ || x.TypeVersion != 1 || x.Actor != actor || x.OperationID != r.CommandID || x.TurnID != s.TurnID || x.AgentID != s.Origin.AgentID || x.ExecutionSessionID != s.Origin.SessionID || x.ContextID != s.Origin.ContextID || x.BodyRefs != nil || x.MeasurementPath != "" {
			return errors.New("continuation envelope/order")
		}
	}
	cmd, err := DecodeCommand(c.Records[0].Payload)
	if err != nil || cmd.CommandID != r.CommandID || cmd.SubmissionSHA256 != r.SubmissionSHA256 || r.Outcome != "recorded" || r.EffectSequence != int64(c.Records[1].Seq) {
		return errors.New("continuation receipt identity")
	}
	p, err := DecodeRuntimeContinuationChanged(c.Records[1].Payload)
	if err != nil {
		return err
	}
	if p.TurnID != s.TurnID || p.ExpectedVersion != s.ExpectedVersion || p.ExpectedState != s.ExpectedState || !reflect.DeepEqual(p.arguments(), s.Arguments) {
		return errors.New("continuation submission mismatch")
	}
	var previous RuntimeContinuationChanged
	bound, native, terminal := false, false, false
	for _, prior := range commits {
		for _, x := range prior.Records {
			if x.Seq >= c.First || x.TurnID != p.TurnID {
				continue
			}
			if x.AgentID != s.Origin.AgentID || x.ContextID != s.Origin.ContextID || x.ExecutionSessionID != s.Origin.SessionID {
				return errors.New("source origin")
			}
			switch x.Type {
			case TypeRuntimeContinuationChanged:
				previous, err = DecodeRuntimeContinuationChanged(x.Payload)
				if err != nil {
					return err
				}
			case TypeRuntimeTurnBound:
				b, e := DecodeRuntimeTurnBound(x.Payload)
				if e != nil {
					return e
				}
				bound = int64(x.Seq) == p.BoundSequence && b.TurnID == p.TurnID
			case TypeRuntimeWorkEnded:
				n, e := DecodeRuntimeWorkEnded(x.Payload)
				if e != nil {
					return e
				}
				native = int64(x.Seq) == p.NativeEndingSequence && n.WorkID == p.TurnID && n.Outcome == "cancelled" && x.OperationID == p.CancelCommandID
			case TypeRuntimeTurnResult:
				n, e := DecodeRuntimeTurnResult(x.Payload)
				if e != nil {
					return e
				}
				terminal = n.TurnID == p.TurnID && n.BoundSequence == p.BoundSequence && n.EndingSequence == p.NativeEndingSequence && n.Reason == p.StopReason
			}
		}
	}
	if !bound {
		return errors.New("missing exact binding")
	}
	if previous.Version == 0 {
		if s.Operation != "owner.initialize" || p.ExpectedVersion != 0 || p.OperationID != r.CommandID {
			return errors.New("invalid initial group")
		}
	} else {
		if p.ExpectedVersion != previous.Version || p.ExpectedState != previous.State || previous.State == "terminal" {
			return errors.New("stale continuation")
		}
		if p.BoundSequence != previous.BoundSequence || p.DeadlineUTCNS != previous.DeadlineUTCNS || p.ModelCalls < previous.ModelCalls || p.ToolDispatches < previous.ToolDispatches {
			return errors.New("changed immutable binding or regressed counter")
		}
		if p.OperationID == previous.OperationID {
			if p.Generation != previous.Generation {
				return errors.New("same-operation generation")
			}
		} else if p.OperationID != r.CommandID || p.Generation != previous.Generation+1 {
			return errors.New("new-operation generation")
		}
		if previous.StopReason != "" && (p.StopReason != previous.StopReason || p.CancelCommandID != previous.CancelCommandID) {
			return errors.New("stop relabeled")
		}
	}
	if p.CancelCommandID == r.CommandID {
		return errors.New("native cancel reuses stop command")
	}
	if p.NativeEndingSequence != 0 && !native || s.Operation == "owner.close" && (!native || !terminal || len(p.Pending) != 0) {
		return errors.New("missing terminal/native evidence")
	}
	return nil
}

func TestGoldenF3_16(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, continuationLog)
	_, files := continuationInputs()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(continuationDir)
	if *update {
		for name, data := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(name)]; !ok {
			return fmt.Errorf("unexpected fixture %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("fixture %s differs: %v", name, err)
		}
		if bytes.Contains(data, []byte{'\r'}) {
			t.Fatalf("CR in %s", name)
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(data))
	}
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: continuationTypes})
	if err != nil {
		t.Fatal(err)
	}
	shapes := map[string]bool{}
	states := map[string]bool{}
	for _, c := range commits {
		last := c.Records[len(c.Records)-1]
		if last.Type == TypeRuntimeLoopReceipt {
			if err := checkLoopGoldenGroup(c, commits, files); err != nil {
				t.Fatal(err)
			}
		}
		if last.Type != TypeRuntimeContinuationReceipt {
			continue
		}
		if err := checkContinuationGoldenGroup(c, commits); err != nil {
			t.Fatal(err)
		}
		r, _ := DecodeRuntimeContinuationReceipt(last.Payload)
		shapes[r.Submission.Operation], states[r.Submission.Arguments.State] = true, true
	}
	if len(shapes) != 5 || !states["waiting_resource"] || !states["stopping"] || !states["reconciling"] || !states["terminal"] {
		t.Fatalf("missing operation/state: %v %v", shapes, states)
	}
	t.Logf("commits=%d records=%d shapes=%s", len(commits), cursor, strings.Join(slices.Sorted(maps.Keys(shapes)), ","))
}

func TestContinuationGoldenBindingsAndStalePrefix(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, continuationLog)
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: continuationTypes})
	if err != nil {
		t.Fatal(err)
	}
	var staleChecked bool
	for index, c := range commits {
		if c.Records[len(c.Records)-1].Type != TypeRuntimeContinuationReceipt {
			continue
		}
		if err := checkContinuationGoldenGroup(c, commits); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"order", "origin", "turn", "effect", "command", "operation", "generation", "deadline", "binding"} {
			bad := c
			bad.Records = slices.Clone(c.Records)
			switch kind {
			case "order":
				bad.Records[0], bad.Records[1] = bad.Records[1], bad.Records[0]
			case "origin":
				bad.Records[1].ContextID = continuationID(0x999)
			case "turn":
				bad.Records[1].TurnID = continuationID(0x999)
			case "effect":
				bad.Records[1].Seq++
			case "command":
				bad.Records[0].Payload = mustEncode(Command{continuationID(0x999), scopedHash("changed")})
			default:
				p, _ := DecodeRuntimeContinuationChanged(bad.Records[1].Payload)
				switch kind {
				case "operation":
					p.OperationID = continuationID(0x999)
				case "generation":
					p.Generation++
				case "deadline":
					p.DeadlineUTCNS++
				case "binding":
					p.BoundSequence++
				}
				// Keep submission and digest coherent to exercise the history check.
				if p.ExpectedVersion == 0 && (kind == "generation" || kind == "deadline") {
					continue
				}
				r, _ := DecodeRuntimeContinuationReceipt(bad.Records[2].Payload)
				r.Submission.Arguments = p.arguments()
				r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
				bad.Records[0].Payload = mustEncode(Command{r.CommandID, r.SubmissionSHA256})
				bad.Records[1].Payload, bad.Records[2].Payload = mustEncode(p), mustEncode(r)
			}
			if err := checkContinuationGoldenGroup(bad, commits); err == nil {
				t.Fatalf("accepted corrupt %s", kind)
			}
		}
		r, _ := DecodeRuntimeContinuationReceipt(c.Records[2].Payload)
		if r.Submission.Operation == "owner.stop" {
			// Re-present the earlier wait transition after the durable stop, without
			// appending it. The frozen prefix must stay byte-identical.
			before, err := os.ReadFile(filepath.Join(g.dir, SegmentName(1)))
			if err != nil {
				t.Fatal(err)
			}
			candidate := commits[index-1]
			candidate.First = c.Last + 1
			if err := checkContinuationGoldenGroup(candidate, commits[:index+1]); err == nil {
				t.Fatal("stale callback accepted after stop")
			}
			after, err := os.ReadFile(filepath.Join(g.dir, SegmentName(1)))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("stale refusal changed prefix")
			}
			staleChecked = true
		}
	}
	if !staleChecked {
		t.Fatal("missing stop prefix")
	}
}
