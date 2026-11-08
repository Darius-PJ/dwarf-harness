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
	"sync"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/session"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
)

type continuationCrashSignal struct {
	Root, Boundary string
	Origin         ledger.Origin
	Turn           uuid.UUID
	Through        uint64
	Scratch        workspace.Scratch
	CaptureCalls   int
}

type continuationRestart struct {
	service *Service
	store   *ledgerstore.Store
	signal  continuationCrashSignal
	report  Reconciliation
}

// These are process-kill boundaries, not a power-loss or shared-colony proof.
// The child positively announces the exact boundary and then blocks; the parent
// kills and waits for it before opening any colony file.
func continuationKilled(t *testing.T, boundary string) *continuationRestart {
	t.Helper()
	container := testRoot(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestContinuationCrashChild$")
	cmd.Env = append(os.Environ(), "DWARF_CONTINUATION_CRASH="+boundary, "TMP="+container, "TEMP="+container, "TMPDIR="+container)
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
	var signal continuationCrashSignal
	var protocol bytes.Buffer
	if err := gob.NewDecoder(io.TeeReader(stdout, &protocol)).Decode(&signal); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		waited = true
		t.Fatalf("child failed before %s: %v stdout=%q stderr=%s", boundary, err, protocol.String(), stderr.String())
	}
	if signal.Boundary != boundary || signal.Through == 0 {
		t.Fatalf("wrong kill boundary: %+v", signal)
	}
	relative, err := filepath.Rel(container, signal.Root)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("child escaped its isolated root: %q %v", signal.Root, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var killed *exec.ExitError
	if !errors.As(err, &killed) {
		t.Fatalf("child was not killed: %v %s", err, stderr.String())
	}
	store, recovery, err := ledgerstore.Open(signal.Root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if recovery.TruncatedBytes != 0 || store.Durable() != signal.Through {
		t.Fatalf("kill boundary did not preserve the acknowledged tail: %+v", recovery)
	}
	segment := filepath.Join(store.LogDir(), ledger.SegmentName(1))
	before := readTestFile(t, segment)
	s, report, err := Open(context.Background(), store, signal.Root, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if len(report.Issues) != 0 || store.Durable() != signal.Through || !bytes.Equal(before, readTestFile(t, segment)) {
		t.Fatalf("Open was not inert: %+v", report)
	}
	return &continuationRestart{service: s, store: store, signal: signal, report: report}
}

func TestContinuationCrashChild(t *testing.T) {
	boundary := os.Getenv("DWARF_CONTINUATION_CRASH")
	if boundary == "" {
		return
	}
	var f *turnFixture
	var turn uuid.UUID
	var scratch workspace.Scratch
	var captureCalls int
	var once sync.Once
	barrier := func() {
		once.Do(func() {
			if err := gob.NewEncoder(os.Stdout).Encode(continuationCrashSignal{Root: f.root, Boundary: boundary, Origin: f.origin, Turn: turn, Through: f.store.Durable(), Scratch: scratch, CaptureCalls: captureCalls}); err != nil {
				t.Fatal(err)
			}
			var one [1]byte
			_, _ = os.Stdin.Read(one[:])
			t.Fatal("kill barrier unexpectedly released")
		})
	}
	if boundary == "local_claim" || boundary == "local_effect" {
		continuationLocalCrashChild(t, boundary, func(local *turnFixture, id uuid.UUID) { f, turn = local, id; barrier() })
		return
	}
	if strings.HasPrefix(boundary, "capture_") {
		continuationCaptureCrashChild(t, boundary, func(capture *continuationCaptureFixture, id uuid.UUID, source workspace.Scratch) {
			f, turn, scratch, captureCalls = capture.turnFixture, id, source, capture.probe.count()
			barrier()
		})
		return
	}
	f = newTurnFixture(t, "anthropic", func(w http.ResponseWriter, r *http.Request) {
		model, _ := turnRequest(t, r)
		if boundary == "paid_sent" {
			barrier()
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, turnAnthropic(model, nil, "recovered answer"))
	}, func(deps *serviceDeps) {
		deps.beforeSubmit = func(d []ledger.Draft) []ledger.Draft {
			if len(d) == 0 || d[len(d)-1].Type != ledger.TypeRuntimeContinuationReceipt {
				return d
			}
			receipt, err := ledger.DecodeRuntimeContinuationReceipt(d[len(d)-1].Payload)
			if err != nil {
				t.Fatal(err)
			}
			sub := receipt.Submission
			if boundary == "legacy_bind" && sub.Operation == "owner.initialize" || boundary == "ready" && sub.Operation == "owner.advance" && sub.ExpectedState == "ready" || boundary == "native_ended" && sub.Arguments.NativeEndingSequence != 0 {
				barrier()
			}
			return d
		}
	})
	f.enable(t)
	command := turnMessage(f.origin.ContextID, "recovery request")
	turn = command.Arguments.(session.MessageSend).TurnID
	if _, err := f.view.Do(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	f.finished(t, turn)
	t.Fatal("requested crash boundary was not reached")
}

func (f *continuationRestart) owner(t testing.TB) continuationState {
	t.Helper()
	owner, err := f.service.currentContinuation(context.Background(), f.signal.Origin, f.signal.Turn)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func (f *continuationRestart) records(t testing.TB) []ledger.Record {
	t.Helper()
	var records []ledger.Record
	if err := f.service.scan(context.Background(), 0, f.store.Durable(), func(c ledger.Commit) error {
		for _, record := range c.Records {
			if record.TurnID == f.signal.Turn {
				records = append(records, record)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return records
}

func (f *continuationRestart) assertEnded(t testing.TB, outcome string, expectedStops int) {
	t.Helper()
	native, loops, closes, stops := 0, 0, 0, 0
	for _, record := range f.records(t) {
		switch record.Type {
		case ledger.TypeRuntimeScopedReceipt:
			v := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, record.Payload)
			if v.Submission.Operation == "turn.end" || v.Submission.Operation == "turn.cancel" {
				native++
			}
		case ledger.TypeRuntimeTurnResult:
			v := scopedDecode(t, ledger.DecodeRuntimeTurnResult, record.Payload)
			loops++
			if v.Outcome != outcome {
				t.Fatalf("wrong recovered ending: %+v", v)
			}
		case ledger.TypeRuntimeContinuationReceipt:
			v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
			if v.Submission.Operation == "owner.close" {
				closes++
			}
			if v.Submission.Operation == "owner.stop" {
				stops++
			}
		}
	}
	owner := f.owner(t)
	if native != 1 || loops != 1 || closes != 1 || stops != expectedStops || owner.Snapshot.State != "terminal" || len(owner.Snapshot.Pending) != 0 {
		t.Fatalf("recovery duplicated, stopped or left work: native=%d loop=%d close=%d stop=%d owner=%+v", native, loops, closes, stops, owner.Snapshot)
	}
}

func TestTurnLoopCrashRecovery(t *testing.T) {
	for _, row := range []struct {
		name, boundary, operation, outcome string
		legacyEnd                          bool
	}{
		{"legacy_bind_initialize_is_callers_command", "legacy_bind", "owner.initialize", "interrupted", false},
		{"legacy_native_ended_close_only", "legacy_bind", "owner.initialize", "failed", true},
		{"restart_ready", "ready", "owner.reconcile", "interrupted", false},
		{"native_ended_before_loop_end", "native_ended", "owner.reconcile", "finished", false},
		{"reconcile_paid_unknown_completes", "paid_sent", "owner.reconcile", "interrupted", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			f := continuationKilled(t, row.boundary)
			ctx := context.Background()
			owner := f.owner(t)
			if len(f.report.PendingLoops) != 1 || f.report.PendingLoops[0].TurnID != f.signal.Turn || f.report.PendingLoops[0].OwnerVersion != owner.Snapshot.Version {
				t.Fatalf("unclosed owner not reported: %+v", f.report.PendingLoops)
			}
			if row.legacyEnd {
				if _, err := f.service.EndTurn(ctx, TurnEnding{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedVersion: 1, Outcome: "released"}); err != nil {
					t.Fatal(err)
				}
			}
			beforeBudget, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			request := LoopReconciliation{CommandID: uuid.NewV7(), TurnID: f.signal.Turn, ExpectedOwnerVersion: owner.Snapshot.Version}
			receipt, err := f.service.ReconcileLoop(ctx, request)
			if err != nil || receipt.CommandID != request.CommandID {
				t.Fatalf("caller binding changed: %+v %v", receipt, err)
			}
			matched := false
			for _, record := range f.records(t) {
				if record.Type != ledger.TypeRuntimeContinuationReceipt {
					continue
				}
				v := scopedDecode(t, ledger.DecodeRuntimeContinuationReceipt, record.Payload)
				if v.CommandID != request.CommandID {
					continue
				}
				matched = true
				if v.Submission.Operation != row.operation || v.Submission.ExpectedVersion != request.ExpectedOwnerVersion {
					t.Fatalf("caller preimage was normalized or rebound: %+v", v)
				}
			}
			if !matched {
				t.Fatal("caller receipt is absent from the durable schema-7 journal")
			}
			f.assertEnded(t, row.outcome, 0)
			afterBudget, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}})
			if err != nil || afterBudget.HeldMicros != beforeBudget.HeldMicros || afterBudget.SettledMicros != beforeBudget.SettledMicros {
				t.Fatalf("recovery changed money exposure: before=%+v after=%+v err=%v", beforeBudget, afterBudget, err)
			}
			if row.boundary == "paid_sent" && afterBudget.HeldMicros == "0" {
				t.Fatal("sent uncertainty was refunded")
			}
			through := f.store.Durable()
			if retry, err := f.service.ReconcileLoop(ctx, request); err != nil || !reflect.DeepEqual(retry, receipt) || f.store.Durable() != through {
				t.Fatalf("same-ID recovery retry performed more work: %+v %v", retry, err)
			}
			reopened, report, err := Open(ctx, f.store, f.signal.Root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if len(report.PendingLoops) != 0 || len(report.Issues) != 0 || f.store.Durable() != through {
				t.Fatalf("second Open reintroduced work: %+v", report)
			}
		})
	}
	testContinuationRecoveryBindings(t)
	testContinuationCaptureRestart(t)
	testContinuationLocalRestart(t)
}
