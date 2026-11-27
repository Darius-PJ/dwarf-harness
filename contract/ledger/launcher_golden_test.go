package ledger

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
	"uuid"
)

const launcherDir = "testdata/f7/30/log"

var launcherColony = eligibilityID(0x7300)
var launcherTypes = func() Consumption {
	c := maps.Clone(continuationTypes)
	c[TypeLauncherCommandBound], c[TypeLauncherCommandReceipt] = []int{1}, []int{1}
	return c
}()
var launcherLog = sync.OnceValues(func() (*goldenLog, error) {
	inputs, _ := launcherInputs()
	return buildLog(launcherDir, launcherColony, launcherTypes, inputs)
})

// This chapter inserts two bindings into the service-ordered F3-16 prefix.
// Earlier fixture bytes and generators are untouched. Its object references
// retain the same bytes as that chapter; only the new log is stored here.
func launcherInputs() ([]goldenCommit, map[string][]byte) {
	prior, files := continuationInputs()
	var send, cancel LauncherCommandBound
	uid := int64(1000)
	principal := LauncherPrincipal{Kind: "unix_uid", UID: &uid}
	for _, c := range prior {
		for _, d := range c.drafts {
			switch d.Type {
			case TypeRuntimeTurnBound:
				b, _ := DecodeRuntimeTurnBound(d.Payload)
				send = LauncherCommandBound{RequestID: d.OperationID, Principal: principal, Origin: Origin{d.AgentID, d.ExecutionSessionID, d.ContextID}, Operation: "message.send", TurnID: b.TurnID, Target: LauncherTarget{"context", d.ContextID, 1}}
			case TypeRuntimeContinuationReceipt:
				r, _ := DecodeRuntimeContinuationReceipt(d.Payload)
				if r.Submission.Operation == "owner.stop" {
					cancel = LauncherCommandBound{RequestID: r.CommandID, Principal: principal, Origin: r.Submission.Origin, Operation: "turn.cancel", TurnID: r.Submission.TurnID, Target: LauncherTarget{"continuation", r.Submission.TurnID, r.Submission.ExpectedVersion}}
				}
			}
		}
	}
	for _, c := range prior {
		for _, d := range c.drafts {
			if d.Type != TypeRuntimeWorkAdmitted {
				continue
			}
			p, _ := DecodeRuntimeWorkAdmitted(d.Payload)
			if p.WorkID == send.TurnID {
				send.NativeAdmissionID, send.TaskBinding = d.OperationID, p.TaskBinding
			}
		}
	}
	var err error
	send.RequestSHA256, err = (LauncherMeaning{LauncherMeaningVersion, send.Origin, send.TaskBinding, send.RequestID, send.Operation, send.Target, LauncherMessageSend{send.TurnID, "Synthetic loop input.", []ArtifactRef{}}}).Digest()
	if err != nil {
		panic(err)
	}
	cancel.RequestSHA256, err = (LauncherMeaning{LauncherMeaningVersion, cancel.Origin, nil, cancel.RequestID, cancel.Operation, cancel.Target, LauncherTurnCancel{cancel.TurnID}}).Digest()
	if err != nil {
		panic(err)
	}
	var out []goldenCommit
	var sequence, firstCut, secondCut int64
	appendCommit := func(ds []Draft) {
		now := prior[0].header.EventTimeNS + int64(len(out))*int64(time.Second)
		out = append(out, goldenCommit{header: CommitHeader{ColonyID: launcherColony, CommitID: eligibilityID(0x8000 + len(out)), EventTimeNS: now}, drafts: ds})
		sequence += int64(len(ds))
	}
	bind := func(p LauncherCommandBound) {
		command := eligibilityID(0x9000 + len(out))
		s := launcherSubmission(p)
		h := Digest(sha256.Sum256(mustEncode(s)))
		r := LauncherCommandReceipt{command, h, s, "recorded", sequence + 2}
		draft := func(typ string, value payload) Draft {
			return Draft{Type: typ, TypeVersion: 1, Actor: ActorOwner, AgentID: p.Origin.AgentID, ExecutionSessionID: p.Origin.SessionID, ContextID: p.Origin.ContextID, OperationID: command, Payload: mustEncode(value)}
		}
		appendCommit([]Draft{draft(TypeCommand, Command{command, h}), draft(TypeLauncherCommandBound, p), draft(TypeLauncherCommandReceipt, r)})
	}
	for _, c := range prior {
		command := c.drafts[0].OperationID
		if command == send.NativeAdmissionID {
			firstCut = sequence
			bind(send)
		}
		if command == cancel.RequestID {
			secondCut = sequence
			bind(cancel)
		}
		ds := c.drafts
		if firstCut != 0 {
			ds = eligibilityRebaseEarlier(ds, firstCut, 3)
		}
		if secondCut != 0 {
			ds = eligibilityRebaseEarlier(ds, secondCut, 3)
		}
		appendCommit(ds)
		if command == cancel.RequestID {
			break
		}
	}
	return out, files
}

// checkLauncherGoldenGroup checks the contract's group/history rules, not a
// live producer. Native command admission and replay enforce them in Core.
func checkLauncherGoldenGroup(c Commit, history []Commit) error {
	if len(c.Records) != 3 {
		return errors.New("launcher group count")
	}
	r, err := DecodeLauncherCommandReceipt(c.Records[2].Payload)
	if err != nil {
		return err
	}
	s := r.Submission
	for i, typ := range []string{TypeCommand, TypeLauncherCommandBound, TypeLauncherCommandReceipt} {
		x := c.Records[i]
		if x.Type != typ || x.TypeVersion != 1 || x.Actor != ActorOwner || x.OperationID != r.CommandID || x.TurnID != (uuid.UUID{}) || x.AgentID != s.Origin.AgentID || x.ExecutionSessionID != s.Origin.SessionID || x.ContextID != s.Origin.ContextID || x.BodyRefs != nil || x.MeasurementPath != "" {
			return errors.New("launcher envelope/order")
		}
	}
	cmd, err := DecodeCommand(c.Records[0].Payload)
	if err != nil || cmd.CommandID != r.CommandID || cmd.SubmissionSHA256 != r.SubmissionSHA256 || r.EffectSequence != int64(c.Records[1].Seq) {
		return errors.New("launcher receipt identity")
	}
	p, err := DecodeLauncherCommandBound(c.Records[1].Payload)
	if err != nil {
		return err
	}
	if p.RequestID != s.RequestID || !reflect.DeepEqual(p.arguments(), s.Arguments) {
		return errors.New("launcher submission/effect mismatch")
	}
	var context bool
	var task *RuntimeTaskChanged
	var ownerVersion int64
	for _, prior := range history {
		for _, x := range prior.Records {
			if x.Seq >= c.First {
				continue
			}
			if x.Type == TypeCommand {
				v, err := DecodeCommand(x.Payload)
				if err != nil {
					return err
				}
				if v.CommandID == r.CommandID || v.CommandID == p.RequestID || v.CommandID == p.NativeAdmissionID {
					return errors.New("launcher identity already consumed")
				}
			}
			if x.Type == TypeLauncherCommandBound {
				v, err := DecodeLauncherCommandBound(x.Payload)
				if err != nil {
					return err
				}
				for _, id := range []uuid.UUID{r.CommandID, p.RequestID, p.NativeAdmissionID} {
					if id != (uuid.UUID{}) && (id == v.RequestID || id == v.NativeAdmissionID) {
						return errors.New("launcher identity already promised")
					}
				}
			}
			if x.Type == TypeRuntimeContextCreated && x.AgentID == p.Origin.AgentID && x.ExecutionSessionID == p.Origin.SessionID && x.ContextID == p.Origin.ContextID {
				v, err := DecodeRuntimeContextCreated(x.Payload)
				if err != nil {
					return err
				}
				context = p.Operation != "message.send" || v.Kind == "general" && p.TaskBinding == nil || v.Kind == "work" && p.TaskBinding != nil
			}
			if x.Type == TypeRuntimeTaskChanged && p.TaskBinding != nil {
				v, err := DecodeRuntimeTaskChanged(x.Payload)
				if err != nil {
					return err
				}
				if v.TaskID == p.TaskBinding.TaskID {
					task = &v
				}
			}
			if x.Type == TypeRuntimeContinuationChanged && x.TurnID == p.TurnID {
				v, err := DecodeRuntimeContinuationChanged(x.Payload)
				if err != nil {
					return err
				}
				ownerVersion = v.Version
			}
		}
	}
	if !context || p.Operation == "turn.cancel" && ownerVersion != p.Target.Revision {
		return errors.New("unregistered origin or stale continuation")
	}
	if p.TaskBinding != nil && (task == nil || task.Assignment == nil || task.Assignment.AssignmentID != p.TaskBinding.AssignmentID || task.Assignment.Generation != p.TaskBinding.Generation || task.Assignment.Assignee != (Binding{p.Origin.AgentID, p.Origin.ContextID})) {
		return errors.New("launcher task binding")
	}
	return nil
}

func TestLauncherBindGolden(t *testing.T) {
	t.Parallel()
	g := mustBuild(t, launcherLog)
	if *update {
		if err := os.MkdirAll(launcherDir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, data := range g.files {
			if err := os.WriteFile(filepath.Join(launcherDir, name), data, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, want := range g.files {
		got, err := os.ReadFile(filepath.Join(launcherDir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("golden differs: %v", err)
		}
		if bytes.Contains(got, []byte{'\r'}) {
			t.Fatal("CR in golden")
		}
		t.Logf("%s SHA-256=%x", name, sha256.Sum256(got))
	}
	commits, _, err := readAll(g.dir, ReadOptions{Colony: g.colony, Through: ToEnd, Consume: launcherTypes})
	if err != nil {
		t.Fatal(err)
	}
	_, objects := launcherInputs()
	chain := Genesis()
	var reencoded []byte
	var binds []LauncherCommandBound
	for _, c := range commits {
		switch c.Records[len(c.Records)-1].Type {
		case TypeLauncherCommandReceipt:
			err = checkLauncherGoldenGroup(c, commits)
			p, e := DecodeLauncherCommandBound(c.Records[1].Payload)
			if e != nil {
				t.Fatal(e)
			}
			binds = append(binds, p)
			t.Logf("%s bound=%d receipt=%d", p.Operation, c.Records[1].Seq, c.Records[2].Seq)
		case TypeRuntimeLoopReceipt:
			err = checkLoopGoldenGroup(c, commits, objects)
		case TypeRuntimeContinuationReceipt:
			err = checkContinuationGoldenGroup(c, commits)
		}
		if err != nil {
			t.Fatalf("group %d-%d: %v", c.First, c.Last, err)
		}
		ds := make([]Draft, len(c.Records))
		for i, r := range c.Records {
			ds[i] = r.Draft
			switch r.Type {
			case TypeLauncherCommandBound:
				p, e := DecodeLauncherCommandBound(r.Payload)
				if e != nil {
					t.Fatal(e)
				}
				ds[i].Payload = mustEncode(p)
			case TypeLauncherCommandReceipt:
				p, e := DecodeLauncherCommandReceipt(r.Payload)
				if e != nil {
					t.Fatal(e)
				}
				ds[i].Payload = mustEncode(p)
			}
		}
		first := c.Records[0]
		var data []byte
		data, chain, err = EncodeCommit(chain, CommitHeader{first.ColonyID, first.CommitID, first.EventTimeNS, first.ClockHeld}, ds)
		if err != nil {
			t.Fatal(err)
		}
		reencoded = append(reencoded, data...)
	}
	if !bytes.Equal(reencoded, g.files[SegmentName(1)]) {
		t.Fatal("decoded golden did not re-encode identically")
	}
	if len(binds) != 2 {
		t.Fatalf("want send and cancel bindings, got %d", len(binds))
	}
	for _, p := range binds {
		witnesses := map[string]bool{}
		for _, c := range commits {
			for _, r := range c.Records {
				if r.Type == TypeRuntimeScopedReceipt && r.OperationID == p.NativeAdmissionID {
					v, e := DecodeRuntimeScopedReceipt(r.Payload)
					if e != nil {
						t.Fatal(e)
					}
					witnesses[v.Submission.Operation] = v.WorkID == p.TurnID
				}
				if r.Type == TypeRuntimeLoopReceipt && r.OperationID == p.RequestID {
					v, e := DecodeRuntimeLoopReceipt(r.Payload)
					if e != nil {
						t.Fatal(e)
					}
					witnesses[v.Submission.Operation] = v.Submission.TurnID == p.TurnID
				}
				if r.Type == TypeRuntimeContinuationReceipt && r.OperationID == p.RequestID {
					v, e := DecodeRuntimeContinuationReceipt(r.Payload)
					if e != nil {
						t.Fatal(e)
					}
					witnesses[v.Submission.Operation] = v.Submission.TurnID == p.TurnID
				}
			}
		}
		if p.Operation == "message.send" && (!witnesses["turn.start"] || !witnesses["loop.bind"]) || p.Operation == "turn.cancel" && !witnesses["owner.stop"] {
			t.Fatalf("missing promised native witnesses: %v", witnesses)
		}
	}
}
