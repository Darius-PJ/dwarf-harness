package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type serviceLiveCrashSignal struct {
	Root, Authority string
	Agent, Attempt  uuid.UUID
	Through         uint64
	Posts           int64
	Held            string
	Evidence        ledger.ObjectRef
}

// testServiceLiveCrash kills a process inside a live restoring call at each
// boundary between its completion object, E, financial result and drain, then
// proves one catch-up converges without resending or moving money.
func testServiceLiveCrash(t *testing.T) {
	for _, row := range []struct {
		boundary string
		e        bool
		result   bool
		restored bool
	}{
		{"completion_object_before_observation", false, false, false},
		{"observation_before_financial", true, false, true},
		{"financial_before_state", true, true, true},
	} {
		t.Run("live_"+row.boundary, func(t *testing.T) {
			t.Parallel()
			container := testRoot(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestServiceCrashConvergence$")
			cmd.Env = append(os.Environ(), "DWARF_SERVICE_LIVE_CRASH="+row.boundary, "TMP="+container, "TEMP="+container, "TMPDIR="+container)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			var signal serviceLiveCrashSignal
			decodeErr := gob.NewDecoder(stdout).Decode(&signal)
			_ = cmd.Process.Kill()
			waitErr := cmd.Wait()
			var killed *exec.ExitError
			if decodeErr != nil || !errors.As(waitErr, &killed) {
				t.Fatalf("child did not reach %s: %v %v %s", row.boundary, decodeErr, waitErr, stderr.Bytes())
			}
			if relative, err := filepath.Rel(container, signal.Root); err != nil || strings.HasPrefix(relative, "..") || signal.Through == 0 {
				t.Fatal("invalid isolated crash boundary")
			}
			store, _, err := ledgerstore.Open(signal.Root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			if store.Durable() != signal.Through {
				t.Fatal("acknowledged boundary was not preserved")
			}
			s, _, err := openPaidService(context.Background(), store, signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, serviceDeps{paidTestAuthority: signal.Authority})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			successes := func() int {
				n := 0
				for _, e := range serviceObservations(t, s) {
					if e.Source.Kind == "successful_call" {
						n++
					}
				}
				return n
			}
			v, err := s.ServiceStatus(context.Background(), signal.Agent)
			if err != nil {
				t.Fatal(err)
			}
			if (successes() == 1) != row.e || successes() > 1 || (v.State == "available") != row.restored {
				t.Fatalf("catch-up after %s: successes=%d state=%s", row.boundary, successes(), v.State)
			}
			attempt, err := s.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: signal.Attempt})
			if err != nil {
				t.Fatal(err)
			}
			if (attempt.Result != nil) != row.result || !row.result && attempt.HeldMicros != signal.Held || signal.Posts != 2 {
				t.Fatalf("catch-up moved money or resent: result=%v held=%s/%s posts=%d", attempt.Result != nil, attempt.HeldMicros, signal.Held, signal.Posts)
			}
			through := store.Durable()
			if _, err := s.ReconcileServiceEvidence(context.Background(), nil); err != nil || store.Durable() != through {
				t.Fatalf("second catch-up appended: %v", err)
			}
			results := serviceCountRecords(t, s, ledger.TypeRuntimePaidResult)
			switch row.boundary {
			case "completion_object_before_observation":
				// The attempt recovers unknown with its full hold.
				if _, err := s.ReconcilePaid(context.Background(), PaidReconciliation{CommandID: uuid.NewV7(), AttemptID: signal.Attempt, ExpectedVersion: 2}); err != nil {
					t.Fatal(err)
				}
				if v, _ := s.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: signal.Attempt}); v.Result == nil || v.Result.Kind != "unknown" || v.HeldMicros != signal.Held {
					t.Fatalf("recovery did not keep the full hold: %+v", v)
				}
			case "observation_before_financial":
				// Recovery records the financial result by TrustedEvidence
				// and writes no second E.
				if _, err := s.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: signal.Attempt, ExpectedVersion: 2, TrustedEvidence: &signal.Evidence}); err != nil {
					t.Fatal(err)
				}
				if v, _ := s.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: signal.Attempt}); v.Result == nil || v.Result.Kind != "usage" || v.HeldMicros != "0" || successes() != 1 {
					t.Fatalf("trusted recovery: %+v successes=%d", v, successes())
				}
			case "financial_before_state":
				if results != 2 {
					t.Fatalf("financial result repeated: %d", results)
				}
			}
		})
	}
	t.Run("live_definite_observation_failure", func(t *testing.T) { t.Parallel(); testServiceRetainedEvidence(t, false) })
	t.Run("live_definite_observation_failure_reopen", func(t *testing.T) { t.Parallel(); testServiceRetainedEvidence(t, true) })
	t.Run("live_same_handle_retry", func(t *testing.T) { t.Parallel(); testServiceSameHandleRetry(t) })
}

// serviceLiveCrashChild loses the member by a credential refusal, then blocks
// the restoring call's process at one boundary and waits to be killed.
func serviceLiveCrashChild(t *testing.T, boundary string) {
	l := newServiceLive(t, "anthropic")
	l.serviceLostByDenial(t)
	l.respond(serviceSuccess(t, "anthropic"))
	var attempt uuid.UUID
	var held string
	var evidence ledger.ObjectRef
	var armed, financial bool
	// The barrier may run under Service.mu, so it reads only the store and
	// values captured before arming.
	barrier := func() {
		signal := serviceLiveCrashSignal{Root: l.root, Authority: l.service.deps.paidTestAuthority, Agent: l.origin.AgentID, Attempt: attempt, Through: l.store.Durable(), Posts: l.posts.Load(), Held: held, Evidence: evidence}
		if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
			t.Fatal(err)
		}
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		t.Fatal("crash barrier unexpectedly released")
	}
	switch boundary {
	case "completion_object_before_observation":
		l.service.deps.afterPublish = func(_ Binding, kind string) {
			if kind == "service_completion" && armed {
				barrier()
			}
		}
	case "observation_before_financial":
		l.service.deps.afterServiceCommit = func(g *serviceGroup) {
			if armed && g.observed != nil {
				barrier()
			}
		}
	case "financial_before_state":
		l.service.deps.afterServiceCommit = func(g *serviceGroup) {
			if armed && g.observed != nil {
				financial = true
			}
		}
		l.service.deps.afterScopedSubmit = func() {
			if armed && financial {
				barrier()
			}
		}
	default:
		t.Fatalf("unknown live crash boundary %s", boundary)
	}
	r := paidPrepare(t, l.paidFixture)
	attempt = r.AttemptID
	l.admit(t, r)
	out, err := l.service.DispatchPaid(context.Background(), PaidDispatch{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	view, err := l.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: attempt})
	if err != nil {
		t.Fatal(err)
	}
	if evidence, err = out.ObservedPaidCall.Evidence(); err != nil {
		t.Fatal(err)
	}
	held, armed = view.HeldMicros, true
	_, err = l.service.RecordPaidObservation(context.Background(), PaidObservation{CommandID: uuid.NewV7(), AttemptID: r.AttemptID, ExpectedVersion: 2, Observed: out.ObservedPaidCall})
	t.Fatalf("restoring call passed its crash boundary: %v", err)
}
