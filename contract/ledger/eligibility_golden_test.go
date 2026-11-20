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

const eligibilityDir = "testdata/f3/90/log"

var eligibilityColony = eligibilityID(0x100)
var eligibilityTypes = func() Consumption {
	c := maps.Clone(continuationTypes)
	c[TypeRuntimeEligibilityChanged], c[TypeRuntimeEligibilityReceipt] = []int{1}, []int{1}
	return c
}()
var eligibilityLog = sync.OnceValues(func() (*goldenLog, error) {
	inputs, _ := eligibilityInputs()
	return buildLog(eligibilityDir, eligibilityColony, eligibilityTypes, inputs)
})

// eligibilityInputs reuses the service-ordered F3-16 setup and first turn.
// Only new-family records are inserted. The later source sequences are rebased
// in this new chapter; every earlier fixture and its generator stay unchanged.
func eligibilityInputs() ([]goldenCommit, map[string][]byte) {
	prior, files := continuationInputs()
	var turn EligibilityTurn
	var at int
	for i, c := range prior {
		for _, d := range c.drafts {
			if d.Type == TypeRuntimeWorkAdmitted {
				p, _ := DecodeRuntimeWorkAdmitted(d.Payload)
				if p.WorkID == paidID(0x421) {
					at = i
					turn = EligibilityTurn{ContextID: d.ContextID, TurnID: p.WorkID, AdmissionCommandID: d.OperationID, TaskBinding: p.TaskBinding}
				}
			}
			if d.Type == TypeRuntimeTurnBound {
				turn.StartCommandID = d.OperationID
			}
		}
	}
	origin := scopedOriginFixture()
	inputDigest := func(turn EligibilityTurn, text string) Digest {
		input := struct {
			Schema      int           `json:"schema"`
			Origin      Origin        `json:"origin"`
			CommandID   uuid.UUID     `json:"command_id"`
			TurnID      uuid.UUID     `json:"turn_id"`
			Text        string        `json:"text"`
			Artifacts   []ArtifactRef `json:"artifacts"`
			TaskBinding *TaskBinding  `json:"task_binding,omitempty"`
		}{1, Origin{origin.AgentID, origin.SessionID, turn.ContextID}, turn.StartCommandID, turn.TurnID, text, []ArtifactRef{}, turn.TaskBinding}
		inputBytes, err := json.Marshal(input)
		if err != nil {
			panic(err)
		}
		return Digest(sha256.Sum256(inputBytes))
	}
	turn.InputSHA256 = inputDigest(turn, "Synthetic loop input.")
	var out []goldenCommit
	var sequence int64
	appendCommit := func(ds []Draft) {
		now := prior[0].header.EventTimeNS + int64(len(out))*int64(time.Second)
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: eligibilityColony, CommitID: eligibilityID(0x2000 + len(out)), EventTimeNS: now}, drafts: ds})
		sequence += int64(len(ds))
	}
	for _, c := range prior[:at] {
		appendCommit(c.drafts)
	}
	p := RuntimeEligibilityChanged{}
	add := func(operation string, arguments any, existing bool) {
		command := eligibilityID(0x1000 + len(out))
		actor := "owner"
		if operation == "eligibility.first_refused" {
			actor = "core"
		}
		s := EligibilitySubmission{Schema: 8, Actor: actor, Origin: origin, Operation: operation, ExpectedVersion: p.Version, Arguments: arguments}
		if existing {
			s.ExpectedOperationID = p.OperationID
		} else {
			p.OperationID = command
		}
		p.ExpectedVersion, p.Version = p.Version, p.Version+1
		h := Digest(sha256.Sum256(mustEncode(s)))
		r := RuntimeEligibilityReceipt{command, h, s, "recorded", sequence + 2}
		draft := func(typ string, actor Actor, payload payload) Draft {
			return Draft{Type: typ, TypeVersion: 1, Actor: actor, AgentID: origin.AgentID, ExecutionSessionID: origin.SessionID, ContextID: origin.ContextID, OperationID: command, Payload: mustEncode(payload)}
		}
		appendCommit([]Draft{draft(TypeCommand, Actor(actor), Command{command, h}), draft(TypeRuntimeEligibilityChanged, ActorCore, p), draft(TypeRuntimeEligibilityReceipt, Actor(actor), r)})
	}
	p.State, p.Reason = "ordinary_work_ineligible", "explicit_hold"
	add("eligibility.hold", EligibilityHoldArguments{p.Reason}, false)
	p.State, p.Reason = "eligible", "none"
	add("eligibility.release", EligibilityReleaseArguments{}, true)
	initial := turn
	initial.TurnID, initial.StartCommandID, initial.AdmissionCommandID = eligibilityID(0x300), eligibilityID(0x301), eligibilityID(0x302)
	initial.InputSHA256 = inputDigest(initial, "Synthetic refused owner input.")
	p.State, p.Reason = "first_action_required", "first_action"
	p.FirstAction = &EligibilityFirstAction{eligibilityID(0x310), initial, initial, nil}
	add("eligibility.require_first", EligibilityRequireFirstArguments{p.Reason, p.FirstAction.ActionID, initial}, false)
	p.FirstAction.Refusal = &EligibilityRefusal{initial.TurnID, initial.StartCommandID, "turn_admission", "unavailable"}
	add("eligibility.first_refused", EligibilityFirstRefusedArguments{p.FirstAction.ActionID, initial.TurnID, initial.StartCommandID, "turn_admission", "unavailable"}, true)
	p.FirstAction.PermittedTurn, p.FirstAction.Refusal = turn, nil
	add("eligibility.first_next", EligibilityFirstNextArguments{p.FirstAction.ActionID, turn}, true)
	// All prior references before the insertion stay fixed. Later references
	// advance by exactly the five new three-record groups.
	var oldPrefix int64
	for _, c := range prior[:at] {
		oldPrefix += int64(len(c.drafts))
	}
	shift := sequence - oldPrefix
	for _, c := range prior[at:] {
		appendCommit(eligibilityRebaseEarlier(c.drafts, oldPrefix, shift))
	}
	next := turn
	next.TurnID, next.StartCommandID, next.AdmissionCommandID = eligibilityID(0x320), eligibilityID(0x321), eligibilityID(0x322)
	next.InputSHA256 = inputDigest(next, "Synthetic next owner input.")
	p.FirstAction.PermittedTurn = next
	add("eligibility.first_next", EligibilityFirstNextArguments{p.FirstAction.ActionID, next}, true)
	p.State, p.Reason = "ordinary_work_ineligible", "explicit_hold"
	add("eligibility.hold", EligibilityHoldArguments{p.Reason}, true)
	p.State, p.Reason = "eligible", "none"
	add("eligibility.release", EligibilityReleaseArguments{}, true)
	p.State, p.Reason, p.FirstAction = "ordinary_work_ineligible", "explicit_hold", nil
	add("eligibility.hold", EligibilityHoldArguments{p.Reason}, false)
	initial = next
	initial.TurnID, initial.StartCommandID, initial.AdmissionCommandID = eligibilityID(0x330), eligibilityID(0x331), eligibilityID(0x332)
	initial.InputSHA256 = inputDigest(initial, "Synthetic later owner input.")
	p.State, p.Reason = "first_action_required", "first_action"
	p.FirstAction = &EligibilityFirstAction{eligibilityID(0x340), initial, initial, nil}
	add("eligibility.require_first", EligibilityRequireFirstArguments{p.Reason, p.FirstAction.ActionID, initial}, true)
	p.State, p.Reason = "eligible", "none"
	add("eligibility.release", EligibilityReleaseArguments{}, true)
	return out, files
}

// Rebase only the earlier-family groups used after this chapter's insertion.
// Canonical command hashes change exactly when a referenced sequence changes.
func eligibilityRebaseEarlier(drafts []Draft, prefix, shift int64) []Draft {
	ds := slices.Clone(drafts)
	sequence := func(n int64) int64 {
		if n > prefix {
			return n + shift
		}
		return n
	}
	var h Digest
	var command uuid.UUID
	for i, d := range ds {
		switch d.Type {
		case TypeRuntimeTurnBound:
			p, _ := DecodeRuntimeTurnBound(d.Payload)
			p.AdmissionSequence = sequence(p.AdmissionSequence)
			ds[i].Payload = mustEncode(p)
		case TypeRuntimeTurnResult:
			p, _ := DecodeRuntimeTurnResult(d.Payload)
			p.BoundSequence, p.EndingSequence = sequence(p.BoundSequence), sequence(p.EndingSequence)
			ds[i].Payload = mustEncode(p)
		case TypeRuntimeLoopReceipt:
			r, _ := DecodeRuntimeLoopReceipt(d.Payload)
			if a, ok := r.Submission.Arguments.(LoopEndArguments); ok {
				a.BoundSequence = sequence(a.BoundSequence)
				r.Submission.Arguments = a
			}
			r.EffectSequence = sequence(r.EffectSequence)
			r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
			command, h, ds[i].Payload = r.CommandID, r.SubmissionSHA256, mustEncode(r)
		case TypeRuntimeContinuationChanged:
			p, _ := DecodeRuntimeContinuationChanged(d.Payload)
			p.BoundSequence, p.NativeEndingSequence = sequence(p.BoundSequence), sequence(p.NativeEndingSequence)
			ds[i].Payload = mustEncode(p)
		case TypeRuntimeContinuationReceipt:
			r, _ := DecodeRuntimeContinuationReceipt(d.Payload)
			a := &r.Submission.Arguments
			a.BoundSequence, a.NativeEndingSequence = sequence(a.BoundSequence), sequence(a.NativeEndingSequence)
			r.EffectSequence = sequence(r.EffectSequence)
			r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
			command, h, ds[i].Payload = r.CommandID, r.SubmissionSHA256, mustEncode(r)
		}
	}
	if command != (uuid.UUID{}) {
		ds[0].Payload = mustEncode(Command{command, h})
	}
	return ds
}

// checkEligibilityGoldenGroup checks the contract's fixture references and
// transition rules. It is not a Core producer or evidence of live dispatch.
func checkEligibilityGoldenGroup(c Commit, history []Commit) error {
	if len(c.Records) != 3 {
		return errors.New("eligibility group count")
	}
	r, err := DecodeRuntimeEligibilityReceipt(c.Records[2].Payload)
	if err != nil {
		return err
	}
	s := r.Submission
	for i, typ := range []string{TypeCommand, TypeRuntimeEligibilityChanged, TypeRuntimeEligibilityReceipt} {
		x, actor := c.Records[i], Actor(s.Actor)
		if i == 1 {
			actor = ActorCore
		}
		if x.Type != typ || x.TypeVersion != 1 || x.Actor != actor || x.OperationID != r.CommandID || x.TurnID != (uuid.UUID{}) || x.AgentID != s.Origin.AgentID || x.ExecutionSessionID != s.Origin.SessionID || x.ContextID != s.Origin.ContextID || x.BodyRefs != nil || x.MeasurementPath != "" {
			return errors.New("eligibility envelope/order")
		}
	}
	cmd, err := DecodeCommand(c.Records[0].Payload)
	if err != nil || cmd.CommandID != r.CommandID || cmd.SubmissionSHA256 != r.SubmissionSHA256 || r.Outcome != "recorded" || r.EffectSequence != int64(c.Records[1].Seq) {
		return errors.New("eligibility receipt identity")
	}
	p, err := DecodeRuntimeEligibilityChanged(c.Records[1].Payload)
	if err != nil {
		return err
	}
	previous := RuntimeEligibilityChanged{State: "eligible", Reason: "none"}
	var registered bool
	for _, prior := range history {
		for _, x := range prior.Records {
			if x.Seq >= c.First {
				continue
			}
			if x.Type == TypeCommand {
				v, e := DecodeCommand(x.Payload)
				if e != nil || v.CommandID == r.CommandID {
					return errors.New("reused command")
				}
			}
			if x.AgentID != s.Origin.AgentID {
				continue
			}
			if x.Type == TypeRuntimeAgentRegistered {
				v, e := DecodeRuntimeAgentRegistered(x.Payload)
				if e != nil {
					return e
				}
				registered = v.GeneralContextID == s.Origin.ContextID && x.ExecutionSessionID == s.Origin.SessionID
			}
			if x.Type == TypeRuntimeEligibilityChanged {
				previous, err = DecodeRuntimeEligibilityChanged(x.Payload)
				if err != nil {
					return err
				}
			}
		}
	}
	if !registered || s.ExpectedVersion != previous.Version || p.ExpectedVersion != previous.Version {
		return errors.New("eligibility origin/version")
	}
	existing := previous.State != "eligible"
	if existing && s.ExpectedOperationID != previous.OperationID || !existing && s.ExpectedOperationID != (uuid.UUID{}) {
		return errors.New("wrong held operation")
	}
	want := previous
	want.ExpectedVersion, want.Version = previous.Version, previous.Version+1
	if previous.FirstAction != nil {
		first := *previous.FirstAction
		want.FirstAction = &first
	}
	if !existing {
		want.OperationID = r.CommandID
	}
	switch a := s.Arguments.(type) {
	case EligibilityHoldArguments:
		if previous.State != "eligible" && previous.State != "first_action_required" {
			return errors.New("hold edge")
		}
		want.State, want.Reason = "ordinary_work_ineligible", a.Reason
		if !existing {
			want.FirstAction = nil
		}
	case EligibilityRequireFirstArguments:
		if previous.State != "eligible" && (previous.State != "ordinary_work_ineligible" || previous.FirstAction != nil) {
			return errors.New("require edge")
		}
		if err := checkEligibilityGoldenTurn(a.Turn, s.Origin, c.First, history); err != nil {
			return err
		}
		want.State, want.Reason = "first_action_required", a.Reason
		want.FirstAction = &EligibilityFirstAction{a.ActionID, a.Turn, a.Turn, nil}
	case EligibilityFirstNextArguments:
		if previous.State != "first_action_required" || previous.FirstAction.ActionID != a.ActionID {
			return errors.New("next action identity")
		}
		f := previous.FirstAction
		native, bound, terminal, ownerTerminal := false, false, false, false
		admitted := false
		for _, prior := range history {
			for _, x := range prior.Records {
				if x.Seq >= c.First || x.TurnID != f.PermittedTurn.TurnID {
					continue
				}
				switch x.Type {
				case TypeRuntimeWorkAdmitted:
					admitted = true
				case TypeRuntimeWorkEnded:
					native = true
				case TypeRuntimeTurnBound:
					bound = true
				case TypeRuntimeTurnResult:
					terminal = true
				case TypeRuntimeContinuationChanged:
					v, e := DecodeRuntimeContinuationChanged(x.Payload)
					ownerTerminal = e == nil && v.State == "terminal"
				}
			}
		}
		if !(native && (!bound || terminal && ownerTerminal) || !admitted && f.Refusal != nil) {
			return errors.New("next before prior terminal/refusal")
		}
		if a.Turn.ContextID != f.PermittedTurn.ContextID || !reflect.DeepEqual(a.Turn.TaskBinding, f.PermittedTurn.TaskBinding) || a.Turn.TurnID == f.PermittedTurn.TurnID || a.Turn.StartCommandID == f.PermittedTurn.StartCommandID || a.Turn.AdmissionCommandID == f.PermittedTurn.AdmissionCommandID || a.Turn.InputSHA256 == f.PermittedTurn.InputSHA256 {
			return errors.New("next changed binding or reused identity")
		}
		if err := checkEligibilityGoldenTurn(a.Turn, s.Origin, c.First, history); err != nil {
			return err
		}
		want.FirstAction.PermittedTurn, want.FirstAction.Refusal = a.Turn, nil
	case EligibilityReleaseArguments:
		if !existing {
			return errors.New("release without hold")
		}
		want.State, want.Reason = "eligible", "none"
	case EligibilityFirstRefusedArguments:
		if previous.State != "first_action_required" || previous.FirstAction.ActionID != a.ActionID || previous.FirstAction.PermittedTurn.TurnID != a.TurnID || previous.FirstAction.PermittedTurn.StartCommandID != a.StartCommandID {
			return errors.New("refusal identity")
		}
		for _, prior := range history {
			for _, x := range prior.Records {
				if x.Seq < c.First && x.TurnID == a.TurnID && (x.Type == TypeRuntimeWorkAdmitted || x.Type == TypeRuntimeTurnBound) {
					return errors.New("refusal after native admission")
				}
			}
		}
		want.FirstAction.Refusal = &EligibilityRefusal{a.TurnID, a.StartCommandID, a.Stage, a.Code}
		if reflect.DeepEqual(want.FirstAction.Refusal, previous.FirstAction.Refusal) {
			return errors.New("duplicate refusal")
		}
	default:
		return errors.New("unknown arguments")
	}
	if !reflect.DeepEqual(want, p) {
		return errors.New("snapshot not derived from command and prefix")
	}
	return nil
}

func checkEligibilityGoldenTurn(turn EligibilityTurn, origin Origin, first uint64, history []Commit) error {
	var found bool
	var task *RuntimeTaskChanged
	for _, c := range history {
		for _, r := range c.Records {
			if r.Seq >= first {
				continue
			}
			if r.OperationID == turn.StartCommandID || r.OperationID == turn.AdmissionCommandID || r.TurnID == turn.TurnID {
				return errors.New("declared identity already used")
			}
			if r.Type == TypeRuntimeContextCreated && r.AgentID == origin.AgentID && r.ExecutionSessionID == origin.SessionID && r.ContextID == turn.ContextID {
				v, err := DecodeRuntimeContextCreated(r.Payload)
				if err != nil {
					return err
				}
				found = v.Kind == "general" && turn.TaskBinding == nil || v.Kind == "work" && turn.TaskBinding != nil
			}
			if r.Type == TypeRuntimeTaskChanged && turn.TaskBinding != nil {
				v, err := DecodeRuntimeTaskChanged(r.Payload)
				if err != nil {
					return err
				}
				if v.TaskID == turn.TaskBinding.TaskID {
					task = &v
				}
			}
		}
	}
	if !found || turn.TaskBinding != nil && (task == nil || task.Assignment == nil || task.Assignment.AssignmentID != turn.TaskBinding.AssignmentID || task.Assignment.Generation != turn.TaskBinding.Generation || task.Assignment.Assignee != (Binding{origin.AgentID, turn.ContextID})) {
		return errors.New("unregistered context or wrong task binding")
	}
	return nil
}

func TestGoldenF3_90(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, eligibilityLog)
	_, files := eligibilityInputs()
	for name, data := range g.files {
		files["log/"+name] = data
	}
	root := filepath.Dir(eligibilityDir)
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
		if !d.IsDir() {
			name, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if _, ok := files[filepath.ToSlash(name)]; !ok {
				return fmt.Errorf("unexpected fixture %s", path)
			}
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
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: eligibilityTypes})
	if err != nil {
		t.Fatal(err)
	}
	shapes := map[string]int{}
	for _, c := range commits {
		last := c.Records[len(c.Records)-1]
		switch last.Type {
		case TypeRuntimeLoopReceipt:
			err = checkLoopGoldenGroup(c, commits, files)
		case TypeRuntimeContinuationReceipt:
			err = checkContinuationGoldenGroup(c, commits)
		case TypeRuntimeEligibilityReceipt:
			err = checkEligibilityGoldenGroup(c, commits)
			r, _ := DecodeRuntimeEligibilityReceipt(last.Payload)
			shapes[r.Submission.Operation]++
		}
		if err != nil {
			t.Fatalf("group %d-%d: %v", c.First, c.Last, err)
		}
	}
	want := map[string]int{"eligibility.hold": 3, "eligibility.release": 3, "eligibility.require_first": 2, "eligibility.first_next": 2, "eligibility.first_refused": 1}
	if !reflect.DeepEqual(shapes, want) {
		t.Fatalf("missing operation edges: %v", shapes)
	}
	t.Logf("commits=%d records=%d shapes=%s", len(commits), cursor, strings.Join(slices.Sorted(maps.Keys(shapes)), ","))
}

func TestEligibilityGoldenBindingsAndRestart(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, eligibilityLog)
	commits, cursor, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: eligibilityTypes})
	if err != nil {
		t.Fatal(err)
	}
	var last RuntimeEligibilityChanged
	for _, c := range commits {
		if c.Records[len(c.Records)-1].Type != TypeRuntimeEligibilityReceipt {
			continue
		}
		for _, kind := range []string{"order", "origin", "turn", "actor", "effect", "command", "version", "snapshot", "operation"} {
			bad := c
			bad.Records = slices.Clone(c.Records)
			switch kind {
			case "order":
				bad.Records[0], bad.Records[1] = bad.Records[1], bad.Records[0]
			case "origin":
				bad.Records[1].ContextID = eligibilityID(99)
			case "turn":
				bad.Records[1].TurnID = eligibilityID(99)
			case "actor":
				bad.Records[1].Actor = ActorOwner
			case "effect":
				bad.Records[1].Seq++
			case "command":
				bad.Records[0].Payload = mustEncode(Command{eligibilityID(99), scopedHash("changed")})
			case "version", "snapshot":
				p, _ := DecodeRuntimeEligibilityChanged(bad.Records[1].Payload)
				if kind == "version" {
					p.ExpectedVersion, p.Version = p.ExpectedVersion+1, p.Version+1
				} else {
					p.OperationID = eligibilityID(99)
				}
				bad.Records[1].Payload = mustEncode(p)
			case "operation":
				r, _ := DecodeRuntimeEligibilityReceipt(bad.Records[2].Payload)
				if r.Submission.ExpectedOperationID == (uuid.UUID{}) {
					continue
				}
				// Keep canonical hash, current version and envelopes coherent: the
				// wrong held-operation identity alone must refuse the release.
				r.Submission.ExpectedOperationID = eligibilityID(99)
				r.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(r.Submission)))
				bad.Records[0].Payload = mustEncode(Command{r.CommandID, r.SubmissionSHA256})
				bad.Records[2].Payload = mustEncode(r)
			}
			if err := checkEligibilityGoldenGroup(bad, commits); err == nil {
				t.Fatalf("accepted corrupt %s at %d", kind, c.First)
			}
		}
		last, _ = DecodeRuntimeEligibilityChanged(c.Records[1].Payload)
	}
	before, err := os.ReadFile(filepath.Join(g.dir, SegmentName(1)))
	if err != nil {
		t.Fatal(err)
	}
	// A second independent reader reconstructs exactly the final snapshot and
	// native first-admission identity; reading never appends a transition.
	again, through, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: eligibilityTypes})
	if err != nil || through != cursor || !reflect.DeepEqual(again, commits) {
		t.Fatalf("restart history changed: %v", err)
	}
	end, _ := DecodeRuntimeEligibilityChanged(again[len(again)-1].Records[1].Payload)
	if !reflect.DeepEqual(end, last) || end.Version != 11 || end.State != "eligible" || end.FirstAction == nil {
		t.Fatalf("restart snapshot: %+v", end)
	}
	after, err := os.ReadFile(filepath.Join(g.dir, SegmentName(1)))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("refusal/read changed prefix")
	}
}
