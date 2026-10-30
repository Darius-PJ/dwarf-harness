package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

// checkLoopGoldenGroup checks F3-15, proposed for freeze (D125), in the sample.
// It is not Core's reducer: executable replay enforcement arrives in S3.
func checkLoopGoldenGroup(c Commit, commits []Commit, files map[string][]byte) error {
	if len(c.Records) < 3 {
		return errors.New("short group")
	}
	last := c.Records[len(c.Records)-1]
	r, err := DecodeRuntimeLoopReceipt(last.Payload)
	if err != nil {
		return err
	}
	s := r.Submission
	types := []string{TypeCommand, "", TypeRuntimeLoopReceipt}
	switch s.Operation {
	case "loop.bind":
		types[1] = TypeRuntimeTurnBound
	case "tool.begin":
		types[1] = TypeRuntimeToolStarted
	case "tool.result":
		types[1] = TypeRuntimeToolResult
	case "loop.end":
		types = []string{TypeCommand, TypePrivateTurnRevision, TypeRuntimeTurnResult, TypeRuntimeLoopReceipt}
	}
	if len(c.Records) != len(types) {
		return errors.New("group count")
	}
	for i, record := range c.Records {
		actor := ActorCore
		if i == 0 || i == len(types)-1 {
			actor = Actor(s.Actor)
		}
		if record.Type != types[i] || record.TypeVersion != 1 || record.Actor != actor || record.OperationID != r.CommandID || record.TurnID != s.TurnID || record.AgentID != s.Origin.AgentID || record.ExecutionSessionID != s.Origin.SessionID || record.ContextID != s.Origin.ContextID || record.BodyRefs != nil || record.MeasurementPath != "" {
			return errors.New("group envelope or order")
		}
	}
	command, err := DecodeCommand(c.Records[0].Payload)
	if err != nil || command.CommandID != r.CommandID || command.SubmissionSHA256 != r.SubmissionSHA256 || r.EffectSequence != int64(c.Records[len(types)-2].Seq) {
		return errors.New("command, receipt or effect identity")
	}
	sources := map[int64]Record{}
	commands := map[uuid.UUID]Record{}
	children := map[uuid.UUID]bool{}
	var revision int64
	var previous Digest
	for _, prior := range commits {
		for _, record := range prior.Records {
			if record.Seq >= c.First {
				continue
			}
			sources[int64(record.Seq)] = record
			if record.Type == TypeCommand {
				commands[record.OperationID] = record
			}
			if record.TurnID == s.TurnID {
				if record.Type == TypeRuntimeToolStarted {
					p, e := DecodeRuntimeToolStarted(record.Payload)
					if e != nil {
						return e
					}
					children[p.ToolID] = true
				}
				if record.Type == TypeRuntimeToolResult {
					p, e := DecodeRuntimeToolResult(record.Payload)
					if e != nil {
						return e
					}
					delete(children, p.ToolID)
				}
			}
			if record.AgentID != s.Origin.AgentID || record.ContextID != s.Origin.ContextID {
				continue
			}
			var p PrivateRevision
			switch record.Type {
			case TypePrivateRevision:
				p, err = DecodePrivateRevision(record.Payload)
			case TypePrivateScopedRevision:
				var q PrivateScopedRevision
				q, err = DecodePrivateScopedRevision(record.Payload)
				p = PrivateRevision(q)
			case TypePrivateTurnRevision:
				var q PrivateTurnRevision
				q, err = DecodePrivateTurnRevision(record.Payload)
				p = PrivateRevision(q)
			default:
				continue
			}
			if err != nil {
				return err
			}
			if p.Scope == "context" {
				revision = p.Revision
				for _, change := range p.Changes {
					if change.Slot == "transcript" {
						previous = change.SHA256
					}
				}
			}
		}
	}
	source := func(seq int64, typ string) (Record, error) {
		x, ok := sources[seq]
		if !ok || x.Type != typ || x.AgentID != s.Origin.AgentID || x.ExecutionSessionID != s.Origin.SessionID || x.ContextID != s.Origin.ContextID || x.TurnID != s.TurnID {
			return Record{}, errors.New("source identity")
		}
		return x, nil
	}
	object := func(o ObjectRef) error {
		path := "objects/agents/Ada-a1/contexts/" + s.Origin.ContextID.String() + "/core/objects/" + o.SHA256.String()
		data, ok := files[path]
		if !ok || int64(len(data)) != o.Bytes || Digest(sha256.Sum256(data)) != o.SHA256 {
			return errors.New("selected object mismatch")
		}
		return nil
	}
	effect := c.Records[len(types)-2]
	switch a := s.Arguments.(type) {
	case LoopBindArguments:
		p, e := DecodeRuntimeTurnBound(effect.Payload)
		if e != nil {
			return e
		}
		if p.TurnID != a.TurnID || p.InputObject != a.InputObject || p.PolicyObject != a.PolicyObject || p.RenderObject != a.RenderObject {
			return errors.New("bind mismatch")
		}
		x, e := source(p.AdmissionSequence, TypeRuntimeWorkAdmitted)
		if e != nil {
			return e
		}
		admission, e := DecodeRuntimeWorkAdmitted(x.Payload)
		if e != nil || admission.Kind != "turn" || admission.WorkID != s.TurnID || x.OperationID != a.AdmissionCommandID || commands[a.AdmissionCommandID].Type != TypeCommand {
			return errors.New("admission mismatch")
		}
		for _, o := range []ObjectRef{p.InputObject, p.PolicyObject, p.RenderObject} {
			if e := object(o); e != nil {
				return e
			}
		}
	case LoopToolBeginArguments:
		p, e := DecodeRuntimeToolStarted(effect.Payload)
		if e != nil {
			return e
		}
		if !reflect.DeepEqual(p, RuntimeToolStarted(a)) {
			return errors.New("tool request mismatch")
		}
		found := false
		for _, x := range sources {
			if x.Type == TypeRuntimePaidReserved {
				q, e := DecodeRuntimePaidReserved(x.Payload)
				if e != nil {
					return e
				}
				if q.AttemptID == p.AttemptID && q.ParentTurnID == s.TurnID && x.ContextID == s.Origin.ContextID {
					found = true
				}
			}
			if x.Type == TypeRuntimeToolStarted {
				q, e := DecodeRuntimeToolStarted(x.Payload)
				if e != nil {
					return e
				}
				if q.ToolID == p.ToolID || q.AttemptID == p.AttemptID && q.ProviderCallID == p.ProviderCallID {
					return errors.New("duplicate tool invocation")
				}
			}
		}
		if !found {
			return errors.New("missing paid attempt")
		}
		if e := object(p.RequestObject); e != nil {
			return e
		}
	case LoopToolResultArguments:
		p, e := DecodeRuntimeToolResult(effect.Payload)
		if e != nil {
			return e
		}
		if p != RuntimeToolResult(a) {
			return errors.New("tool result mismatch")
		}
		x, e := source(p.StartedSequence, TypeRuntimeToolStarted)
		if e != nil {
			return e
		}
		start, e := DecodeRuntimeToolStarted(x.Payload)
		if e != nil || start.ToolID != p.ToolID || !children[p.ToolID] {
			return errors.New("tool start/terminal mismatch")
		}
		if p.EffectReceiptSequence != 0 {
			if start.EffectCommandID == (uuid.UUID{}) {
				return errors.New("unexpected native receipt")
			}
			native, e := source(p.EffectReceiptSequence, TypeRuntimeScopedReceipt)
			if e != nil {
				return e
			}
			receipt, e := DecodeRuntimeScopedReceipt(native.Payload)
			if e != nil {
				return e
			}
			if strings.HasPrefix(start.Operation, "file.") {
				if receipt.Submission.Operation != "local.end" {
					return errors.New("not a local ending receipt")
				}
				found := false
				for _, record := range sources {
					if record.Type == TypeRuntimeWorkAdmitted && record.OperationID == start.EffectCommandID {
						admission, e := DecodeRuntimeWorkAdmitted(record.Payload)
						if e != nil {
							return e
						}
						found = admission.WorkID == receipt.WorkID && admission.ParentTurnID == s.TurnID && admission.ArgumentsSHA256 == start.RequestSHA256
					}
				}
				if !found {
					return errors.New("local receipt has another admission")
				}
			} else if native.OperationID != start.EffectCommandID {
				return errors.New("native receipt mismatch")
			}
		}
		if e := object(p.ResultObject); e != nil {
			return e
		}
	case LoopEndArguments:
		p, e := DecodeRuntimeTurnResult(effect.Payload)
		if e != nil {
			return e
		}
		if p.TurnID != a.TurnID || p.BoundSequence != a.BoundSequence || p.Outcome != a.Outcome || p.Reason != a.Reason || p.ModelCalls != a.ModelCalls || p.ToolDispatches != a.ToolDispatches || !reflect.DeepEqual(p.FinalMessageObject, a.FinalMessageObject) {
			return errors.New("turn result mismatch")
		}
		if _, e := source(p.BoundSequence, TypeRuntimeTurnBound); e != nil {
			return e
		}
		x, e := source(p.EndingSequence, TypeRuntimeWorkEnded)
		if e != nil {
			return e
		}
		if x.OperationID != a.EndingCommandID || len(children) != 0 {
			return errors.New("unresolved children or wrong native ending")
		}
		head, e := DecodePrivateTurnRevision(c.Records[1].Payload)
		if e != nil {
			return e
		}
		if head.ExpectedVersion != revision || head.Changes[0].PreviousSHA256 != previous {
			return errors.New("shared head mismatch")
		}
		if e := object(ObjectRef{head.Changes[0].SHA256, head.Changes[0].Bytes}); e != nil {
			return e
		}
		if p.FinalMessageObject != nil {
			if e := object(*p.FinalMessageObject); e != nil {
				return e
			}
		}
	}
	return nil
}

func TestLoopGroupRulesAndFrozenReplay(t *testing.T) {
	t.Parallel()
	g := mustLoop(t)
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: loopTypes})
	if err != nil {
		t.Fatal(err)
	}
	_, files := loopInputs()
	for _, c := range commits {
		if c.Records[len(c.Records)-1].Type != TypeRuntimeLoopReceipt {
			continue
		}
		if err := checkLoopGoldenGroup(c, commits, files); err != nil {
			t.Fatal(err)
		}
		for _, change := range []string{"order", "origin", "turn", "sequence", "command"} {
			mutated := c
			mutated.Records = slices.Clone(c.Records)
			switch change {
			case "order":
				mutated.Records[0], mutated.Records[1] = mutated.Records[1], mutated.Records[0]
			case "origin":
				mutated.Records[1].ContextID = loopID(0x999)
			case "turn":
				mutated.Records[1].TurnID = loopID(0x999)
			case "sequence":
				mutated.Records[len(c.Records)-2].Seq++
			case "command":
				mutated.Records[0].Payload = mustEncode(Command{loopID(0x999), scopedHash("changed")})
			}
			if err := checkLoopGoldenGroup(mutated, commits, files); err == nil {
				t.Fatalf("group corruption %s accepted", change)
			}
		}
	}
	openAIInputs, _ := paidOpenAIInputs()
	openAI, err := buildLog(paidOpenAIDir, paidColony, paidTypes, openAIInputs)
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []*goldenLog{mustGolden(t), mustCatalogue(t), mustWorkspace(t), mustPromotion(t), mustScratchChange(t), mustFinalization(t), mustActivation(t), mustGraph(t), mustPrivate(t), mustScoped(t), mustReview(t), mustPaid(t), openAI, mustBuild(t, registrationLog)} {
		for name, want := range old.files {
			got, err := os.ReadFile(filepath.Join(old.dir, name))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("earlier golden changed %s/%s: %v", old.dir, name, err)
			}
		}
		if _, cursor, err := readAll(old.dir, ReadOptions{Colony: old.colony, Through: ToEnd, Consume: old.consume}); err != nil || cursor != old.end().Seq {
			t.Fatalf("earlier replay %s: %v", old.dir, err)
		}
	}
}

func TestDefinedF3_15(t *testing.T) {
	t.Parallel()
	c := Defined()
	if len(c.set) != 53+6 {
		t.Fatalf("catalogue has %d types, want 53+6", len(c.set))
	}
	for _, f := range loopCodecFixtures()[:6] {
		if !c.Contains(TypeVersion{f.typ, 1}) || c.Contains(TypeVersion{f.typ, 2}) {
			t.Fatalf("catalogue version %s", f.typ)
		}
	}
}

func TestLoopSubmissionCanonical(t *testing.T) {
	t.Parallel()
	s := loopSubmissionFixture()
	base := loopCodecFixtures()
	begin := LoopToolBeginArguments(loopStartedFixture())
	result := LoopToolResultArguments(base[3].p.(RuntimeToolResult))
	end := LoopEndArguments{s.TurnID, 14, "finished", "natural_stop", 2, 1, base[4].p.(RuntimeTurnResult).FinalMessageObject, loopID(8)}
	for _, row := range []struct {
		actor, op string
		expected  int64
		args      any
	}{
		{"owner", "loop.bind", 0, s.Arguments}, {"agent", "tool.begin", 0, begin}, {"core", "tool.result", 1, result}, {"core", "loop.end", 1, end},
	} {
		s.Actor, s.Operation, s.ExpectedVersion, s.Arguments = row.actor, row.op, row.expected, row.args
		wire := mustEncode(s)
		prefix := fmt.Sprintf(`{"schema":6,"actor":%q,"origin":{"agent_id":%q,"session_id":%q,"context_id":%q},"turn_id":%q,"operation":%q,"expected_version":%d,"arguments":`, row.actor, s.Origin.AgentID, s.Origin.SessionID, s.Origin.ContextID, s.TurnID, row.op, row.expected)
		if !bytes.HasPrefix(wire, []byte(prefix)) {
			t.Fatalf("schema-6 key order: %s", wire)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(wire, &fields); err != nil {
			t.Fatal(err)
		}
		reordered, err := json.MarshalIndent(fields, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		p, err := DecodeLoopSubmission(reordered)
		if err != nil || !bytes.Equal(mustEncode(p), wire) {
			t.Fatalf("canonical meaning changed: %v", err)
		}
		r := RuntimeLoopReceipt{loopID(6), Digest(sha256.Sum256(wire)), s, "applied", 25}
		if _, err := DecodeRuntimeLoopReceipt(mustEncode(r)); err != nil {
			t.Fatal(err)
		}
		changed := s
		changed.Origin.ContextID = loopID(9)
		r.Submission = changed
		if _, err := r.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatal("same receipt accepted different binding")
		}
		s.ExpectedVersion = 1 - row.expected
		if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatal("wrong operation expectation")
		}
	}
}
