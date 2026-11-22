package agents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/gitprocess"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

// serviceCorruptTurnMemory plants foreign bytes over the turn fixture's
// context memory materialization.
func serviceCorruptTurnMemory(t *testing.T, f *turnFixture) {
	t.Helper()
	p, err := f.service.scopePath(submissionIDs{f.origin.AgentID, f.origin.SessionID, f.origin.ContextID}, "context")
	if err != nil {
		t.Fatal(err)
	}
	file, _ := p.markdownPath("memory")
	writeTestFile(t, file, []byte("foreign"))
}

func (f *turnFixture) scopedMemoryWrite(t *testing.T, revision uint64) error {
	t.Helper()
	agent, err := f.service.BindAgent(context.Background(), bindingOf(f.origin))
	if err != nil {
		t.Fatal(err)
	}
	_, err = agent.WritePrivate(context.Background(), ScopedPrivateWrite{CommandID: uuid.NewV7(), GrantID: f.privateGrant, Scope: "context", ExpectedVersion: revision, Changes: []Change{{Slot: "memory", Mode: "replace", Bytes: []byte("next")}}})
	return err
}

// TestServiceIntegrityDemand: an integrity issue refusing the agent's own
// live work is demand; inspection, owner and handle-only refusals are not.
func TestServiceIntegrityDemand(t *testing.T) {
	t.Parallel()
	t.Run("refused_admission", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		serviceIntegrityLoss(t, l)
		through := l.store.Durable()
		if _, err := scopedBound(t, l.service, bindingOf(l.origin)).StartTurn(context.Background(), TurnAdmission{CommandID: uuid.NewV7(), GrantID: l.inspect, TurnID: uuid.NewV7()}); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("second admission was not refused: %v", err)
		}
		if l.store.Durable() != through {
			t.Fatal("a repeated refusal for the lost member appended")
		}
	})
	t.Run("handle_write_not_counted", func(t *testing.T) {
		t.Parallel()
		f := newTurnFixture(t, "anthropic", func(http.ResponseWriter, *http.Request) {}, nil)
		revision := f.seedMemory(t, "current")
		serviceCorruptTurnMemory(t, f)
		before := f.store.Durable()
		if err := f.scopedMemoryWrite(t, revision); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("live inspection did not refuse the write: %v", err)
		}
		if serviceCountRecords(t, f.service, ledger.TypeRuntimeServiceObserved) != 0 || f.store.Durable() != before {
			t.Fatal("a refusal outside a live turn's own effect counted as demand")
		}
	})
	for _, row := range []string{"tool_effect_counted", "scope_issue_stop"} {
		t.Run(row, func(t *testing.T) {
			t.Parallel()
			var posts atomic.Int32
			var revision uint64
			entered, release := make(chan struct{}), make(chan struct{})
			f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
				model, _ := turnRequest(t, r)
				w.Header().Set("Content-Type", "text/event-stream")
				if posts.Add(1) == 1 {
					close(entered)
					<-release
					tool := turnCalls("core_memory_v1", fmt.Sprintf(`{"schema":1,"action":"write","scope":"context","slot":"memory","revision":%d,"text":"after corruption"}`, revision))
					if row == "scope_issue_stop" {
						tool = turnCalls("core_status_v1", `{"schema":1,"subject":"context"}`)
					}
					_, _ = io.WriteString(w, turnAnthropic(model, tool, ""))
					return
				}
				_, _ = io.WriteString(w, turnAnthropic(model, nil, "Final answer."))
			}, nil)
			revision = f.seedMemory(t, "current")
			f.raiseDispatchBound(t, &f.setting)
			f.enable(t)
			_, start := f.send(t)
			<-entered
			serviceCorruptTurnMemory(t, f)
			if row == "scope_issue_stop" {
				// A handle-only refusal finds the issue but is not the turn's
				// own effect; the loop's next boundary is.
				if err := f.scopedMemoryWrite(t, revision); !errors.Is(err, ErrIntegrity) {
					close(release)
					t.Fatalf("handle write did not find the issue: %v", err)
				}
				if serviceCountRecords(t, f.service, ledger.TypeRuntimeServiceObserved) != 0 {
					close(release)
					t.Fatal("handle refusal counted")
				}
			}
			close(release)
			f.finished(t, start.TurnID)
			es := serviceObservations(t, f.service)
			if len(es) != 1 || es[0].Source.Kind != "integrity_refusal" || es[0].Source.TurnID != start.TurnID || es[0].Cause.PrivateScope != "context" {
				t.Fatalf("turn's integrity refusal witness: %+v", es)
			}
			if row == "tool_effect_counted" && (es[0].Source.CommandID == (uuid.UUID{}) || es[0].Source.SourceSequence != nil) {
				t.Fatalf("effect refusal not named by its effect command: %+v", es[0].Source)
			}
			if row == "scope_issue_stop" && (es[0].Source.SourceSequence == nil || es[0].Source.CommandID != (uuid.UUID{})) {
				t.Fatalf("boundary stop not named by its bound sequence: %+v", es[0].Source)
			}
			if v, err := f.service.ServiceStatus(context.Background(), f.origin.AgentID); err != nil || v.State != "lost" {
				t.Fatalf("integrity demand did not lose the member: %+v %v", v, err)
			}
		})
	}
}

// TestServiceProcessSeam: an unconfirmed-class error at the finish commit is
// process demand only where the Owner proves tree closure; elsewhere it writes
// no F3-91 record and sets no barrier. The turn's native outcome is the same
// either way.
func TestServiceProcessSeam(t *testing.T) {
	t.Parallel()
	f := newContinuationCapture(t, true, nil)
	entered := make(chan uuid.UUID, 1)
	f.service.deps.beforeFinishCommit = func(turn uuid.UUID) error {
		entered <- turn
		return fmt.Errorf("synthetic finish commit: %w", gitprocess.ErrUnconfirmed)
	}
	f.enable(t)
	_, started := f.send(t)
	if <-entered != started.TurnID {
		t.Fatal("wrong turn reached the finish commit")
	}
	f.service.loop.mu.Lock()
	turn := f.service.loop.turns[started.TurnID]
	f.service.loop.mu.Unlock()
	<-turn.done
	f.service.loop.mu.Lock()
	state, terminal := turn.state, turn.terminalError
	f.service.loop.mu.Unlock()
	if state != "recovery_pending" || !errors.Is(terminal, gitprocess.ErrUnconfirmed) {
		t.Fatalf("native finish refusal changed: state=%s err=%v", state, terminal)
	}
	// Per system (PM fix-1 decision): only the Windows Owner proves tree
	// closure (gitprocess/owner.go:93). There the classified refusal records
	// the demand and sets the barrier; elsewhere it writes no F3-91 record and
	// sets no barrier, since no process cause can exist there.
	windows := runtime.GOOS == "windows"
	if f.workspace.ProcessProofSupported() != windows {
		t.Fatalf("tree proof on %s = %v", runtime.GOOS, !windows)
	}
	es := serviceObservations(t, f.service)
	if !windows && len(es) != 0 {
		t.Fatalf("process demand recorded without tree proof: %d", len(es))
	}
	if windows && (len(es) != 1 || es[0].Source.Kind != "process_refusal" || es[0].Source.ExecutionRunID != (uuid.UUID{}) || es[0].Source.TurnID != started.TurnID) {
		t.Fatalf("bare unconfirmed class witness: %+v", es)
	}
	// PM answer 7: where it exists, the barrier blocks execution permits for
	// this incarnation only.
	blocked := func(s *Service) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.serviceExecutionBarrierLocked(ledger.ServiceCauseSpec{Family: "execution", Code: "process_unconfirmed", ScopeKind: "colony"})
	}
	if blocked(f.service) != windows {
		t.Fatalf("process barrier on %s = %v after the classified refusal", runtime.GOOS, !windows)
	}
	if err := f.service.Close(); !errors.Is(err, gitprocess.ErrUnconfirmed) {
		t.Fatalf("shutdown lost the finish refusal: %v", err)
	}
	f.view.Close()
	reopened, _, err := Open(context.Background(), f.turnFixture.store, f.root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	f.service = reopened
	if blocked(reopened) {
		t.Fatal("process barrier survived into a new incarnation")
	}
}

// testServiceLiveNeverCounts: owner-trusted evidence and every non-process
// workspace failure write no F3-91 record.
func testServiceLiveNeverCounts(t *testing.T) {
	for _, row := range []string{"trusted_failure", "trusted_success"} {
		t.Run("live_"+row, func(t *testing.T) {
			t.Parallel()
			l := newServiceLive(t, "anthropic")
			if row == "trusted_success" {
				l.serviceLostByDenial(t)
				l.respond(serviceSuccess(t, "anthropic"))
			} else {
				l.respond(serviceProviderError("anthropic", http.StatusUnauthorized, "authentication_error", nil))
			}
			before := len(serviceObservations(t, l.service))
			r := paidPrepare(t, l.paidFixture)
			l.admit(t, r)
			out, err := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := out.ObservedPaidCall.Evidence()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 2, TrustedEvidence: &evidence}); err != nil {
				t.Fatal(err)
			}
			if n := len(serviceObservations(t, l.service)); n != before {
				t.Fatalf("trusted evidence produced service evidence: %d -> %d", before, n)
			}
			if row == "trusted_success" && l.status(t).State != "lost" {
				t.Fatal("trusted evidence restored the member")
			}
		})
	}
	for _, row := range []struct {
		name string
		err  error
	}{
		{"workspace_busy", &workspace.BusyError{Reason: "context_busy"}},
		{"merge_pending", workspace.ErrMergeBackPending},
		{"git_unavailable", workspace.ErrGitUnavailable},
		{"git_exit", &workspace.GitError{Args: []string{"commit"}, Exit: 1, Err: gitprocess.ErrExit}},
		{"cancelled", context.Canceled},
	} {
		t.Run("live_finish_"+row.name, func(t *testing.T) {
			t.Parallel()
			f := newContinuationCapture(t, true, nil)
			var refused atomic.Bool
			f.service.deps.beforeFinishCommit = func(uuid.UUID) error {
				if refused.CompareAndSwap(false, true) {
					return row.err
				}
				return nil
			}
			f.enable(t)
			_, started := f.send(t)
			f.service.loop.mu.Lock()
			turn := f.service.loop.turns[started.TurnID]
			f.service.loop.mu.Unlock()
			if turn != nil {
				<-turn.done
			}
			if !refused.Load() || serviceCountRecords(t, f.service, ledger.TypeRuntimeServiceObserved) != 0 {
				t.Fatalf("%s at the finish commit counted as service evidence", row.name)
			}
			f.service.loop.mu.Lock()
			terminal := turn.terminalError
			f.service.loop.mu.Unlock()
			if terminal != nil {
				// The native refusal stands; its shutdown reports it.
				_ = f.service.Close()
				f.view.Close()
				reopened, _, err := Open(context.Background(), f.turnFixture.store, f.root, DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				f.service = reopened
			}
		})
	}
}

// TestServiceDemand: live demand and its reducer caches agree with a fresh
// derivation, in the recording incarnation and after reopen (PM answer 8).
func TestServiceDemand(t *testing.T) {
	t.Parallel()
	t.Run("semantic_cache_matches_fresh", func(t *testing.T) {
		t.Parallel()
		l := newServiceLive(t, "anthropic")
		l.serviceLostByDenial(t)
		l.respond(serviceSuccess(t, "anthropic"))
		if c := l.call(t); c.err != nil {
			t.Fatal(c.err)
		}
		check := func(stage string) map[uuid.UUID]ledger.Digest {
			t.Helper()
			l.service.mu.Lock()
			defer l.service.mu.Unlock()
			out := make(map[uuid.UUID]ledger.Digest)
			for id, o := range l.service.p.service.observations {
				if fresh := serviceSemantic(o.observed); o.semantic != fresh {
					t.Fatalf("%s: cached semantic differs from a fresh derivation for %s", stage, id)
				}
				out[id] = o.semantic
			}
			if len(out) != 2 {
				t.Fatalf("%s: want the refusal and the restoration, got %d", stage, len(out))
			}
			return out
		}
		live := check("live")
		serviceReopenLive(t, l)
		if reopened := check("reopened"); !maps.Equal(live, reopened) {
			t.Fatal("replay derived a different semantic cache")
		}
	})
}
