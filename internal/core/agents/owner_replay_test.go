package agents

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestContinuationFrozenReplay(t *testing.T) {
	t.Parallel()
	for _, chapter := range []string{"10", "11", "12", "13", "14", "139", "15", "16"} {
		t.Run("frozen_"+chapter, func(t *testing.T) {
			t.Parallel()
			f, commits := scopedGoldenFixture(t, chapter, -1)
			through := f.store.Durable()
			s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			f.service = s
			if len(report.Issues) != 0 || f.store.Durable() != through {
				t.Fatal("Open changed the ledger or reported an integrity issue")
			}
			original := readTestFile(t, filepath.Join("../../../contract/ledger/testdata/f3", chapter, "log", ledger.SegmentName(1)))
			if !bytes.Equal(original, readTestFile(t, filepath.Join(f.root, "log", ledger.SegmentName(1)))) {
				t.Fatal("Open rewrote frozen bytes")
			}
			if chapter == "16" {
				if len(commits) != 33 || through != 106 {
					t.Fatal("full F3-16 history was not replayed")
				}
				if len(report.PendingLoops) != 0 || len(s.p.owners) != 0 {
					t.Fatal("terminal owner retained executable recovery work")
				}
				for _, c := range commits {
					for _, r := range c.Records {
						if r.Type != ledger.TypeRuntimeContinuationReceipt {
							continue
						}
						v, err := ledger.DecodeRuntimeContinuationReceipt(r.Payload)
						if err != nil {
							t.Fatal(err)
						}
						m, err := s.findCommand(context.Background(), nil, v.CommandID, 0, through)
						if err != nil || m.continuation == nil || m.continuation.receipt.SubmissionSHA256 != v.SubmissionSHA256 {
							t.Fatalf("schema-7 receipt lost through command lookup: %v", err)
						}
					}
				}
			}
		})
	}
}

func continuationFrozen(t testing.TB) ([]ledger.Commit, map[string]int) {
	t.Helper()
	_, commits := scopedGoldenFixture(t, "16", 0)
	at := map[string]int{}
	for i, c := range commits {
		_, scoped, _, _, _, _, owner, err := decodeFamily(draftsOf(c), c.First)
		if err != nil {
			t.Fatal(err)
		}
		if owner != nil {
			if _, found := at[owner.receipt.Submission.Operation]; !found {
				at[owner.receipt.Submission.Operation] = i
			}
		}
		if scoped != nil && scoped.receipt.Submission.Operation == "turn.cancel" {
			at["turn.cancel"] = i
		}
	}
	return commits, at
}

func replayDraftCommit(drafts []ledger.Draft, first uint64) ledger.Commit {
	c := ledger.Commit{First: first, Last: first + uint64(len(drafts)) - 1}
	for i, draft := range drafts {
		c.Records = append(c.Records, ledger.Record{Envelope: ledger.Envelope{Seq: first + uint64(i), CommitIndex: i, CommitCount: len(drafts), Draft: draft}})
	}
	return c
}

func TestContinuationCoreCancelBinding(t *testing.T) {
	t.Parallel()
	commits, at := continuationFrozen(t)
	for _, mode := range []string{"matching_stop", "no_stop", "different_command", "owner_unchanged"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			prefix := at["turn.cancel"]
			if mode == "no_stop" {
				prefix = at["owner.stop"]
			}
			f, _ := scopedGoldenFixture(t, "16", prefix)
			s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil || len(report.Issues) != 0 {
				t.Fatal("invalid prefix", report, err)
			}
			f.service = s
			baseline, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer baseline.Close()
			d := draftsOf(commits[at["turn.cancel"]])
			receipt := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[len(d)-1].Payload)
			if mode == "different_command" {
				id := uuid.NewV7()
				for i := range d {
					d[i].OperationID = id
				}
				command := scopedDecode(t, ledger.DecodeCommand, d[0].Payload)
				command.CommandID, receipt.CommandID = id, id
				d[0].Payload = testPayload(t, command)
				d[len(d)-1].Payload = testPayload(t, receipt)
			}
			if mode == "owner_unchanged" {
				receipt.Submission.Actor = "owner"
				d[0].Actor, d[len(d)-1].Actor = ledger.ActorOwner, ledger.ActorOwner
				scopedRewriteReceipt(t, d, receipt)
			}
			valid := mode == "matching_stop" || mode == "owner_unchanged"
			before := f.store.Durable()
			_, err = scopedPreflightAgainst(t, s, baseline, d, receipt)
			if (err == nil) != valid {
				t.Fatalf("native cancel preflight %s: %v", mode, err)
			}
			s.mu.Lock()
			if valid {
				_, _, err = s.stageCommitLocked(context.Background(), nil, replayDraftCommit(d, before+1), nil)
			} else {
				_, err = s.acceptCommitLocked(context.Background(), replayDraftCommit(d, before+1), nil)
			}
			s.mu.Unlock()
			if (err == nil) != valid || !sameProjectionState(s.p, baseline.p) || f.store.Durable() != before {
				t.Fatalf("native cancel replay %s changed a refused projection: %v", mode, err)
			}
			if !valid {
				_, err := s.executeScoped(context.Background(), scopedCaller{actor: "core"}, "turn.cancel", TurnCancellation{CommandID: receipt.CommandID, TurnID: d[0].TurnID, ExpectedVersion: 1})
				if err == nil || f.store.Durable() != before {
					t.Fatalf("native producer bypassed the cancelled-stop binding: %v", err)
				}
			}
		})
	}
}

func TestContinuationMixedGroupRefused(t *testing.T) {
	t.Parallel()
	commits, at := continuationFrozen(t)
	f, _ := scopedGoldenFixture(t, "16", at["turn.cancel"])
	s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil || len(report.Issues) != 0 {
		t.Fatal("invalid prefix", report, err)
	}
	f.service = s
	baseline, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Close()
	d := draftsOf(commits[at["turn.cancel"]])
	receipt := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[len(d)-1].Payload)
	foreign := commits[at["owner.stop"]].Records[1].Draft
	d = slices.Insert(d, 2, foreign)
	before := f.store.Durable()
	if _, err := scopedPreflightAgainst(t, s, baseline, d, receipt); err == nil {
		t.Fatal("mixed continuation/native group accepted by preflight")
	}
	s.mu.Lock()
	_, err = s.acceptCommitLocked(context.Background(), replayDraftCommit(d, before+1), nil)
	s.mu.Unlock()
	if err == nil || !sameProjectionState(s.p, baseline.p) || f.store.Durable() != before {
		t.Fatalf("mixed group replay installed a partial transition: %v", err)
	}
}

func TestContinuationFinalObjectIntegrity(t *testing.T) {
	t.Parallel()
	armed, entered, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "answer"))
	}, func(deps *serviceDeps) {
		deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			select {
			case <-armed:
			default:
				return d
			}
			if d[len(d)-1].Type != ledger.TypeRuntimeScopedReceipt {
				return d
			}
			receipt, err := ledger.DecodeRuntimeScopedReceipt(d[len(d)-1].Payload)
			if err == nil && receipt.Submission.Operation == "turn.end" {
				close(entered)
				<-release
			}
			return d
		}
	})
	t.Cleanup(func() { close(release) })
	f.enable(t)
	path, err := f.service.scopePath(submissionIDs{f.origin.AgentID, f.origin.SessionID, f.origin.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	close(armed)
	command := turnMessage(f.origin.ContextID, "answer request")
	if _, err := f.view.Do(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	<-entered
	through := f.store.Durable()
	var ref *ledger.ObjectRef
	if err := f.service.scan(context.Background(), 0, through, func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.Type != ledger.TypeRuntimeContinuationChanged {
				continue
			}
			owner, err := ledger.DecodeRuntimeContinuationChanged(record.Payload)
			if err != nil {
				return err
			}
			if owner.FinalMessageObject != nil {
				ref = owner.FinalMessageObject
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ref == nil {
		t.Fatal("native ending began without a selected owner final")
	}
	name := filepath.Join(path.objects(), ref.SHA256.String())
	raw := readTestFile(t, name)
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	defer writeTestFile(t, name, raw)
	s, report, err := Open(context.Background(), f.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	found := false
	for _, issue := range report.Issues {
		if issue.ExpectedSHA256 == ref.SHA256 && issue.Reason == "object_missing_or_invalid" {
			found = true
		}
	}
	if !found || f.store.Durable() != through {
		t.Fatalf("missing continuation-only private object was not reported inertly: %+v", report.Issues)
	}
}
