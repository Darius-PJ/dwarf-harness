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
	"strconv"
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
	Root, Boundary, HeldMicros                    string
	Origin                                        ledger.Origin
	Turn, Attempt                                 uuid.UUID
	Through                                       uint64
	Settings                                      []LoopContextSettings
	Gate                                          EligibilityReceipt
	Admissions, Intents                           int
	FirstSnapshot                                 ledger.RuntimeEligibilityChanged
	NativeSequence, BoundSequence, EndingSequence uint64
	SettledMicros                                 string
}

func TestEligibilityCrashChild(t *testing.T) {
	boundary := os.Getenv("DWARF_ELIGIBILITY_CRASH")
	if boundary == "" {
		return
	}
	if strings.HasPrefix(boundary, "first_") {
		eligibilityFirstCrashChild(t, boundary)
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
	for _, boundary := range []string{"first_declared", "first_native", "first_before_intent", "first_after_intent", "first_terminal"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			testEligibilityFirstRestart(t, boundary)
		})
	}
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
			if state := status.Data.(session.TurnView); state.State != "recovery_pending" || state.SavedPhase != "model_pending" || state.Result != nil || state.Eligibility == nil {
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

func eligibilityFirstCrashChild(t *testing.T, boundary string) {
	ctx := context.Background()
	var emit func()
	f := newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		if boundary == "first_after_intent" {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			emit()
			return
		}
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "completed first"))
	}, nil)
	command := turnMessage(f.origin.ContextID, "first kill boundary")
	request := firstActionRequest(f.origin, command, nil)
	gate := requireFirstAction(t, f.service, request)
	emit = func() {
		signal := eligibilityCrashSignal{Root: f.root, Boundary: boundary, Origin: f.origin, Turn: request.FirstActionTurn.TurnID, Through: f.store.Durable(), Settings: []LoopContextSettings{f.setting}, Gate: gate, HeldMicros: "0", SettledMicros: "0"}
		if err := f.service.scan(ctx, 0, signal.Through, func(c ledger.Commit) error {
			for _, r := range c.Records {
				switch r.Type {
				case ledger.TypeRuntimeEligibilityChanged:
					signal.FirstSnapshot = scopedDecode(t, ledger.DecodeRuntimeEligibilityChanged, r.Payload)
				case ledger.TypeRuntimeWorkAdmitted:
					signal.Admissions++
					if r.TurnID == signal.Turn {
						signal.NativeSequence = r.Seq
					}
				case ledger.TypeRuntimeTurnBound:
					signal.BoundSequence = r.Seq
				case ledger.TypeRuntimeWorkEnded:
					if r.TurnID == signal.Turn {
						signal.EndingSequence = r.Seq
					}
				case ledger.TypeRuntimePaidReserved:
					reserved := scopedDecode(t, ledger.DecodeRuntimePaidReserved, r.Payload)
					signal.Attempt = reserved.AttemptID
					signal.HeldMicros = strconv.FormatInt(reserved.Quote.MaximumMicros, 10)
				case ledger.TypeRuntimePaidDispatchIntent:
					signal.Intents++
				case ledger.TypeRuntimePaidResult:
					result := scopedDecode(t, ledger.DecodeRuntimePaidResult, r.Payload)
					if result.CostMicros != nil {
						signal.HeldMicros, signal.SettledMicros = "0", strconv.FormatInt(*result.CostMicros, 10)
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := gob.NewEncoder(os.Stdout).Encode(signal); err != nil {
			t.Fatal(err)
		}
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		t.Fatal("first-action real-kill barrier unexpectedly released")
	}
	f.service.store = &passiveCrashStore{Store: f.store, before: func(drafts []ledger.Draft) {
		for _, draft := range drafts {
			if boundary == "first_native" && draft.Type == ledger.TypeRuntimeTurnBound ||
				boundary == "first_before_intent" && draft.Type == ledger.TypeRuntimePaidDispatchIntent {
				emit()
			}
		}
	}}
	f.enable(t)
	if boundary == "first_declared" {
		emit()
	}
	if _, err := f.view.Do(ctx, command); err != nil {
		t.Fatal(err)
	}
	f.finished(t, request.FirstActionTurn.TurnID)
	if boundary == "first_terminal" {
		emit()
	}
	t.Fatal("first-action crash barrier was not reached")
}

func testEligibilityFirstRestart(t *testing.T, boundary string) {
	t.Helper()
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
		t.Fatal("first kill boundary not isolated", signal, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var killed *exec.ExitError
	if !errors.As(err, &killed) {
		t.Fatal("first child was not actually killed", err)
	}
	store, recovery, err := ledgerstore.Open(signal.Root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if recovery.TruncatedBytes != 0 || store.Durable() != signal.Through {
		t.Fatal("first kill lost acknowledged boundary")
	}
	wantDisposition := map[string]string{"first_declared": "not_admitted", "first_native": "admitted_unbound", "first_before_intent": "bound", "first_after_intent": "bound", "first_terminal": "terminal"}[boundary]
	wantPending := 0
	if boundary == "first_before_intent" || boundary == "first_after_intent" {
		wantPending = 1
	}
	if boundary != "first_declared" && signal.NativeSequence == 0 || wantPending == 1 && (signal.BoundSequence == 0 || signal.HeldMicros == "0") ||
		boundary == "first_after_intent" && signal.Intents != 1 || boundary == "first_before_intent" && signal.Intents != 0 ||
		boundary == "first_terminal" && (signal.EndingSequence == 0 || signal.SettledMicros == "0") {
		t.Fatal("child did not prove requested first boundary", signal)
	}
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	original := readTestFile(t, segment)
	var posts atomic.Int32
	server := paidServer(t, func(http.ResponseWriter, *http.Request) { posts.Add(1) })
	deps := paidServerDeps(server, nil)
	checkMoney := func(s *Service, held, settled string) {
		t.Helper()
		if signal.Attempt == (uuid.UUID{}) {
			return
		}
		attempt, err := s.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: signal.Attempt})
		if err != nil || attempt.HeldMicros != held || attempt.SettledMicros != settled {
			t.Fatal("first-turn money changed", attempt, held, settled, err)
		}
		for _, scope := range attempt.Admission.Scopes {
			budget, err := s.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: scope, Period: attempt.Admission.AdmissionPeriod})
			if err != nil || budget.HeldMicros != held || budget.SettledMicros != settled {
				t.Fatal("first-turn scope exposure changed", scope, budget, err)
			}
		}
	}
	var service *Service
	for range 2 {
		service, report, err := openPaidService(ctx, store, signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
		if err != nil {
			t.Fatal(err)
		}
		gate := firstActionStatus(t, service, signal.Origin.AgentID)
		if len(report.PendingLoops) != wantPending || len(report.Issues) != 0 || !bytes.Equal(original, readTestFile(t, segment)) || gate.State != "first_action_required" || gate.Version != signal.Gate.Version || gate.OperationID != signal.Gate.OperationID ||
			!sameScopedValue(gate.FirstAction.Declaration, *signal.FirstSnapshot.FirstAction) || gate.FirstAction.Disposition != wantDisposition || gate.FirstAction.NativeSequence != signal.NativeSequence || gate.FirstAction.BoundSequence != signal.BoundSequence || gate.FirstAction.NativeEndingSequence != signal.EndingSequence {
			t.Fatal("Open advanced or misclassified declared first work", gate, report)
		}
		checkMoney(service, signal.HeldMicros, signal.SettledMicros)
		if err := service.Close(); err != nil {
			t.Fatal(err)
		}
	}
	service, _, err = openPaidService(ctx, store, signal.Root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ws, _, err := workspace.Open(ctx, store, signal.Root, workspace.Config{Capacity: &workspace.CapacityProfile{MaxFiles: 100, MaxBytes: 1 << 20, BytesInFlight: 2 << 20, CheckpointsPerPair: 4, GenerationWindow: 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := service.EnableTurnLoop(ctx, ws, LoopOptions{Contexts: signal.Settings}); err != nil {
		t.Fatal(err)
	}
	view, err := service.OpenSession(ctx, bindingOf(signal.Origin))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if signal.BoundSequence != 0 {
		result, err := view.Do(ctx, session.Command{Schema: 1, CommandID: uuid.NewV7(), ContextID: signal.Origin.ContextID, Operation: "turn.status", Arguments: session.TurnStatus{TurnID: signal.Turn}})
		if err != nil {
			t.Fatal(err)
		}
		status := result.Data.(session.TurnView)
		if wantPending == 1 && (status.State != "recovery_pending" || status.SavedPhase != "model_pending" || status.Result != nil) || boundary == "first_terminal" && status.Result == nil {
			t.Fatal("Enable enqueued first recovered turn or lost terminal result", status)
		}
	}
	held := holdEligibility(t, service, signal.Origin.AgentID)
	checkMoney(service, signal.HeldMicros, signal.SettledMicros)
	releaseEligibility(t, service, signal.Origin.AgentID, held)
	checkMoney(service, signal.HeldMicros, signal.SettledMicros)
	wantHeld := signal.HeldMicros
	if boundary == "first_native" {
		request := EndingRequest{CommandID: uuid.NewV7(), WorkID: signal.Turn, ExpectedVersion: 1}
		first, err := service.ReconcileAdmissions(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		through := store.Durable()
		retry, err := service.ReconcileAdmissions(ctx, request)
		if err != nil || !reflect.DeepEqual(first, retry) || store.Durable() != through {
			t.Fatal("second native reconcile duplicated ending", err)
		}
	} else if wantPending == 1 {
		owner, err := service.currentContinuation(ctx, signal.Origin, signal.Turn)
		if err != nil {
			t.Fatal(err)
		}
		request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
		first, err := service.ReconcileLoop(ctx, request)
		if err != nil {
			t.Fatal("first recovered turn was runnable instead of inert", err)
		}
		through := store.Durable()
		retry, err := service.ReconcileLoop(ctx, request)
		if err != nil || !reflect.DeepEqual(first, retry) || store.Durable() != through {
			t.Fatal("second first-turn reconcile duplicated recovery", err)
		}
		if boundary == "first_before_intent" {
			wantHeld = "0"
		}
		checkMoney(service, wantHeld, signal.SettledMicros)
		attempt, err := service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: signal.Attempt})
		want := "unknown"
		if boundary == "first_before_intent" {
			want = "released_unsent"
		}
		if err != nil || attempt.State != want {
			t.Fatal("reconcile changed paid certainty incorrectly", attempt, err)
		}
	}
	if eligibilityCount(t, service, 0, ledger.TypeRuntimeWorkAdmitted) != signal.Admissions || eligibilityCount(t, service, 0, ledger.TypeRuntimePaidDispatchIntent) != signal.Intents || eligibilityCount(t, service, 0, ledger.TypeRuntimeToolStarted) != 0 || posts.Load() != 0 {
		t.Fatal("first Open/Enable/query/release/reconcile readmitted or dispatched")
	}
	t.Logf("real child killed: boundary=%s frontier=%d native=%d bound=%d intents=%d held=%s settled=%s; inert twice, exact recovery and no new dispatch", boundary, signal.Through, signal.NativeSequence, signal.BoundSequence, signal.Intents, wantHeld, signal.SettledMicros)
}
