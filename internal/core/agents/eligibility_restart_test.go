package agents

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type eligibilityCrashSignal struct {
	Root, Boundary, HeldMicros string
	Origin                     ledger.Origin
	Turn, Attempt              uuid.UUID
	Through                    uint64
	Settings                   []LoopContextSettings
	Gate                       EligibilityReceipt
	Admissions, Intents        int
}

func TestEligibilityCrashChild(t *testing.T) {
	boundary := os.Getenv("DWARF_ELIGIBILITY_CRASH")
	if boundary == "" {
		return
	}
	entered := make(chan struct{})
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}, nil)
	f.enable(t)
	_, sent := f.send(t)
	<-entered
	gate := holdEligibility(t, f.service, f.origin.AgentID)
	if boundary == "eligible_released" {
		gate = releaseEligibility(t, f.service, f.origin.AgentID, gate)
	}
	r := f.service.loop
	r.mu.Lock()
	attempt := r.turns[sent.TurnID].paid.id
	r.mu.Unlock()
	budget := f.budget(t, "")
	signal := eligibilityCrashSignal{Root: f.root, Boundary: boundary, Origin: f.origin, Turn: sent.TurnID, Attempt: attempt, Through: f.store.Durable(), Settings: r.options.Contexts, Gate: gate, HeldMicros: budget.HeldMicros, Admissions: eligibilityCount(t, f.service, 0, ledger.TypeRuntimeWorkAdmitted), Intents: eligibilityCount(t, f.service, 0, ledger.TypeRuntimePaidDispatchIntent)}
	if signal.HeldMicros == "0" || signal.Intents != 1 {
		t.Fatal("kill boundary lacks a sent exposure")
	}
	if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	_, _ = os.Stdin.Read(one[:])
	t.Fatal("real-kill barrier unexpectedly released")
}

func TestEligibilityRestart(t *testing.T) {
	t.Parallel()
	for _, boundary := range []string{"ordinary_held", "eligible_released"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			container := testRoot(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestEligibilityCrashChild$")
			cmd.Env = append(os.Environ(), "DWARF_ELIGIBILITY_CRASH="+boundary, "TMP="+container, "TEMP="+container, "TMPDIR="+container)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr, protocol bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			t.Cleanup(func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			})
			var signal eligibilityCrashSignal
			if err := gob.NewDecoder(io.TeeReader(stdout, &protocol)).Decode(&signal); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				waited = true
				t.Fatalf("child failed before %s: %v stdout=%q stderr=%s", boundary, err, protocol.String(), stderr.String())
			}
			relative, err := filepath.Rel(container, signal.Root)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || signal.Boundary != boundary || signal.Through == 0 {
				t.Fatalf("invalid isolated kill boundary: %+v %v", signal, err)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			var killed *exec.ExitError
			if !errors.As(err, &killed) {
				t.Fatal("child did not actually die", err)
			}
			store, recovery, err := ledgerstore.Open(signal.Root, ledger.Defined())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if recovery.TruncatedBytes != 0 || store.Durable() != signal.Through {
				t.Fatal("kill did not preserve acknowledged boundary")
			}
			segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
			original := readTestFile(t, segment)
			var posts atomic.Int32
			server := paidServer(t, func(http.ResponseWriter, *http.Request) { posts.Add(1) })
			deps := paidServerDeps(server, nil)
			var service *Service
			for range 2 {
				service, report, err := openPaidService(ctx, store, signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
				if err != nil {
					t.Fatal(err)
				}
				if len(report.PendingLoops) != 1 || report.PendingLoops[0].TurnID != signal.Turn || len(report.Issues) != 0 || !bytes.Equal(original, readTestFile(t, segment)) {
					t.Fatal("Open advanced recovered eligibility work")
				}
				gate, err := service.EligibilityStatus(ctx, signal.Origin.AgentID)
				want := "ordinary_work_ineligible"
				if boundary == "eligible_released" {
					want = "eligible"
				}
				if err != nil || gate.State != want || gate.Version != signal.Gate.Version || gate.OperationID != signal.Gate.OperationID {
					t.Fatal("Open lost eligibility decision", err)
				}
				attempt, err := service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: signal.Attempt})
				if err != nil || attempt.State != "sent" || attempt.HeldMicros != signal.HeldMicros || attempt.SettledMicros != "0" {
					t.Fatal("Open changed sent exposure", err)
				}
				if err := service.Close(); err != nil {
					t.Fatal(err)
				}
			}
			service, _, err = openPaidService(ctx, store, signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
			if err != nil {
				t.Fatal(err)
			}
			ws, _, err := workspace.Open(ctx, store, signal.Root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			defer service.Close()
			if err := service.EnableTurnLoop(ctx, ws, LoopOptions{Contexts: signal.Settings}); err != nil {
				t.Fatal(err)
			}
			view, err := service.OpenSession(ctx, bindingOf(signal.Origin))
			if err != nil {
				t.Fatal(err)
			}
			defer view.Close()
			status, err := view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: signal.Origin.ContextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: signal.Turn}})
			if err != nil {
				t.Fatal(err)
			}
			if state := status.Data.(session.TurnView); state.State != "recovery_pending" && state.State != "eligibility_held" || state.SavedPhase != "model_pending" || state.Result != nil || state.Eligibility == nil {
				t.Fatal("Enable enqueued recovered turn")
			}
			owner, err := service.currentContinuation(ctx, signal.Origin, signal.Turn)
			if err != nil {
				t.Fatal(err)
			}
			request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
			receipt, err := service.ReconcileLoop(ctx, request)
			if err != nil {
				t.Fatal("eligibility fenced explicit recovery endings", err)
			}
			through := store.Durable()
			retry, err := service.ReconcileLoop(ctx, request)
			if err != nil || !reflect.DeepEqual(receipt, retry) || store.Durable() != through {
				t.Fatal("second reconcile repeated recovery", err)
			}
			if eligibilityCount(t, service, 0, ledger.TypeRuntimeWorkAdmitted) != signal.Admissions || eligibilityCount(t, service, 0, ledger.TypeRuntimePaidDispatchIntent) != signal.Intents || eligibilityCount(t, service, 0, ledger.TypeRuntimeToolStarted) != 0 || eligibilityCount(t, service, 0, ledger.TypeRuntimeTurnResult) != 1 || posts.Load() != 0 {
				t.Fatal("restart/reconcile re-admitted or dispatched work")
			}
			attempt, err := service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: signal.Attempt})
			if err != nil || attempt.State != "unknown" || attempt.HeldMicros != signal.HeldMicros || attempt.SettledMicros != "0" {
				t.Fatal("recovery refunded unknown exposure", err)
			}
			if boundary == "ordinary_held" {
				releaseEligibility(t, service, signal.Origin.AgentID, signal.Gate)
				if eligibilityCount(t, service, through, ledger.TypeRuntimeWorkAdmitted) != 0 || eligibilityCount(t, service, through, ledger.TypeRuntimePaidDispatchIntent) != 0 {
					t.Fatal("release recreated recovered request")
				}
			}
			t.Logf("real child killed: boundary=%s frontier=%d held=%s; two inert Opens, explicit reconcile retry, no new admissions/intents/posts", boundary, signal.Through, signal.HeldMicros)
		})
	}
}
