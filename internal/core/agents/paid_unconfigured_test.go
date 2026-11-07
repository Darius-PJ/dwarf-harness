package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

type paidUnconfiguredFixture struct {
	*paidFixture
	owner Binding
	task  *ledger.TaskBinding
	deps  serviceDeps
	posts chan struct{}
}

func newPaidUnconfiguredFixture(t *testing.T, input int) *paidUnconfiguredFixture {
	t.Helper()
	posts := make(chan struct{}, 8)
	server := paidServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts <- struct{}{}
		model := paidRequestModel(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Request-Id", "request-one")
		usage := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":20}`, input)
		_, _ = io.WriteString(w, paidSSE(model, usage, usage, "answer", "end_turn", "", true))
	})
	deps := paidServerDeps(server, nil)
	f := newLivePaidFixture(t, deps)
	ctx := context.Background()
	owner := bindingOf(f.origin)
	work := scopedWorkContext(t, f.service, owner, 3000, 3001)
	task := scopedNewTask(3002, 3003, ledger.CredentialRef{}.Version, owner.AgentID)
	task.Assignment = &ledger.AssignmentInput{AssignmentID: f.id(), Assignee: ledger.Binding(work)}
	if _, err := f.service.CreateTask(ctx, owner, task); err != nil {
		t.Fatal(err)
	}
	binding := &ledger.TaskBinding{TaskID: task.TaskID, AssignmentID: task.Assignment.AssignmentID, Generation: 1}
	model := GrantSpec{GrantID: f.id(), Grantee: work, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + f.route.RouteID.String()}}, DataClass: "external", Recipients: []Binding{work}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked", TaskBinding: binding}
	if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: model}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ConfigureSpend(ctx, owner, SpendConfiguration{CommandID: f.id(), Scope: ledger.PaidScope{Kind: "grant", ID: model.GrantID}, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"}}); err != nil {
		t.Fatal(err)
	}
	turn := model
	turn.GrantID = f.id()
	turn.Operations = []string{"turn.start", "turn.inspect"}
	turn.Resources = []ledger.Resource{{Kind: "context", Binding: ledger.Binding(work), Scope: "context", Slots: []string{}}}
	turn.DataClass = "private"
	if _, err := f.service.IssueGrant(ctx, owner, GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: turn}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.TransitionTask(ctx, owner, TaskMutation{CommandID: f.id(), TaskID: task.TaskID, ExpectedVersion: 1, ExpectedAssignmentID: binding.AssignmentID, ExpectedGeneration: 1, Action: "start"}); err != nil {
		t.Fatal(err)
	}
	f.turn = f.id()
	if _, err := scopedBound(t, f.service, work).StartTurn(ctx, TurnAdmission{CommandID: f.id(), TurnID: f.turn, GrantID: turn.GrantID, TaskBinding: binding}); err != nil {
		t.Fatal(err)
	}
	f.origin.ContextID, f.grant, f.inspect = work.ContextID, model.GrantID, turn.GrantID
	return &paidUnconfiguredFixture{paidFixture: f, owner: owner, task: binding, deps: deps, posts: posts}
}

func (f *paidUnconfiguredFixture) taskScope() ledger.PaidScope {
	return ledger.PaidScope{Kind: "task", ID: f.task.TaskID}
}

func (f *paidUnconfiguredFixture) prepareTask(t *testing.T) PaidAdmission {
	t.Helper()
	input := f.input()
	input.TaskBinding = f.task
	return PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, TaskBinding: input.TaskBinding, Prepared: paidPrepareInput(t, f.paidFixture, input)}
}

func (f *paidUnconfiguredFixture) admitTask(t *testing.T) ledger.RuntimePaidReserved {
	t.Helper()
	request := f.prepareTask(t)
	_, bound, err := scopedBound(t, f.service, bindingOf(f.origin)).AdmitPaid(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := bound.Admission()
	if err != nil {
		t.Fatal(err)
	}
	if len(admission.Scopes) != 5 || !slices.Contains(admission.Scopes, f.taskScope()) || admission.Quote.MaximumMicros != 120 {
		t.Fatalf("task admission omitted scope or changed quote: %+v", admission)
	}
	return admission
}

func (f *paidUnconfiguredFixture) unconfigured(t *testing.T) {
	t.Helper()
	view, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: f.taskScope()})
	if err != nil || view.Version != 0 || view.Terms != nil {
		t.Fatalf("task unexpectedly configured: %+v %v", view, err)
	}
}

func (f *paidUnconfiguredFixture) resident(t *testing.T, want bool) {
	t.Helper()
	f.service.mu.Lock()
	_, present := f.service.p.book.paid[f.taskScope()]
	f.service.mu.Unlock()
	if present != want {
		t.Errorf("task exposure residency = %v, want %v", present, want)
	}
}

func (f *paidUnconfiguredFixture) settle(t *testing.T, cost string, fenced bool) (ledger.RuntimePaidReserved, PaidCorrection) {
	t.Helper()
	admission := f.admitTask(t)
	f.unconfigured(t)
	dispatch := PaidDispatch{CommandID: f.id(), AttemptID: admission.AttemptID, ExpectedVersion: 1}
	out, err := f.service.DispatchPaid(context.Background(), dispatch)
	if err != nil || out.IntentReceipt.Outcome != "sent" || out.ObservedPaidCall == nil {
		t.Fatalf("task dispatch: %+v %v", out, err)
	}
	paidRecord(t, f.paidFixture, dispatch, out)
	view := f.view(t, admission.AttemptID)
	if view.Version != 3 || view.SettledMicros != cost || view.HeldMicros != "0" || view.Fenced != fenced {
		t.Fatalf("original settlement: %+v", view)
	}
	f.resident(t, fenced)
	late := paidLateEvidence(t, f.paidFixture, out, func(o *paidObservationObject) { o.RawUsageMerged["input_tokens"] = json.RawMessage(`9`) })
	return admission, PaidCorrection{PaidObservation: PaidObservation{CommandID: f.id(), AttemptID: admission.AttemptID, ExpectedVersion: 3, TrustedEvidence: &late}, PreviousResultSequence: int64(view.ResultSequence), PreviousEvidenceSHA256: view.Result.EvidenceSHA256}
}

func (f *paidUnconfiguredFixture) totals(t *testing.T, admission ledger.RuntimePaidReserved, settled, held string, fenced bool) []PaidBudgetView {
	t.Helper()
	if len(admission.Scopes) != 5 || !slices.Contains(admission.Scopes, f.taskScope()) {
		t.Fatal("money oracle requires all five immutable admission scopes")
	}
	before := f.store.Durable()
	agent := scopedBound(t, f.service, bindingOf(f.origin))
	var views []PaidBudgetView
	for _, scope := range admission.Scopes {
		request := PaidBudgetLookup{Scope: scope, Period: admission.AdmissionPeriod}
		owner, err := f.service.PaidBudgetStatus(context.Background(), request)
		if err != nil || owner.SettledMicros != settled || owner.HeldMicros != held || owner.Fenced != fenced || owner.ThroughSequence != before {
			t.Fatalf("owner scope %s: settled/held/fenced = %s/%s/%v, want %s/%s/%v: %v", scope.Kind, owner.SettledMicros, owner.HeldMicros, owner.Fenced, settled, held, fenced, err)
		}
		request.GrantID = f.inspect
		bound, err := agent.PaidBudgetStatus(context.Background(), request)
		if err != nil || bound.SettledMicros != settled || bound.HeldMicros != held || bound.Fenced != fenced || bound.ThroughSequence != before {
			t.Fatalf("agent scope %s: %+v %v; want %s/%s/%v", scope.Kind, bound, err, settled, held, fenced)
		}
		if scope == f.taskScope() && owner.Version == 0 && (owner.Terms != nil || bound.Terms != nil) {
			t.Fatal("unconfigured task disclosed spend terms")
		}
		views = append(views, owner)
	}
	if f.store.Durable() != before || len(f.posts) != 1 {
		t.Fatalf("status changed ledger or send count: posts=%d", len(f.posts))
	}
	return views
}

func (f *paidUnconfiguredFixture) correct(t *testing.T, request PaidCorrection) PaidReceipt {
	t.Helper()
	receipt, err := f.service.CorrectPaidSettlement(context.Background(), request)
	if err != nil {
		t.Fatalf("downward correction 120/125 -> 29 refused: %v", err)
	}
	view := f.view(t, request.AttemptID)
	if receipt.Outcome != "corrected" || receipt.Version != 4 || view.Version != 4 || view.SettledMicros != "29" || view.HeldMicros != "0" || view.Fenced {
		t.Fatalf("correction did not replace settlement: %+v %+v", receipt, view)
	}
	return receipt
}

// Both open modes use the same closed durable store, never a copied projection.
func (f *paidUnconfiguredFixture) reopen(t *testing.T, paid bool) {
	t.Helper()
	before, posts := f.store.Durable(), len(f.posts)
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, _, err = ledgerstore.Open(f.root, ledger.Defined())
	if err != nil {
		t.Fatal(err)
	}
	var report Reconciliation
	if paid {
		f.service, report, err = openPaidService(context.Background(), f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, f.deps)
	} else {
		f.service, report, err = Open(context.Background(), f.store, f.root, DefaultLimits())
	}
	if err != nil || len(report.Issues) != 0 || f.store.Durable() != before || len(f.posts) != posts {
		t.Fatalf("paid=%v inert service reopen: %+v %v", paid, report, err)
	}
}

func (f *paidUnconfiguredFixture) replayTotals(t *testing.T, admission ledger.RuntimePaidReserved, settled, held string, fenced bool, resident bool) {
	t.Helper()
	live := f.totals(t, admission, settled, held, fenced)
	f.resident(t, resident)
	f.reopen(t, false)
	replayed := f.totals(t, admission, settled, held, fenced)
	if !reflect.DeepEqual(replayed, live) {
		t.Fatalf("fresh plain replay changed scope views: live=%+v replay=%+v", live, replayed)
	}
	f.resident(t, resident)
}

func TestPaidUnconfiguredScopeCorrection(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"live-after-install", "reopen-before-correction", "held-account", "overrun-fence", "first-configuration", "committed-replay"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input, original, fenced := 100, "120", false
			if name == "overrun-fence" {
				input, original, fenced = 105, "125", true
			}
			f := newPaidUnconfiguredFixture(t, input)
			admission, correction := f.settle(t, original, fenced)
			f.totals(t, admission, original, "0", fenced)
			f.resident(t, fenced)
			ctx := context.Background()
			switch name {
			case "live-after-install":
				receipt := f.correct(t, correction)
				f.totals(t, admission, "29", "0", false)
				before := f.store.Durable()
				retry, err := f.service.CorrectPaidSettlement(ctx, correction)
				if err != nil || retry.Sequence != receipt.Sequence || f.store.Durable() != before {
					t.Fatalf("exact correction retry changed ledger: %+v %v", retry, err)
				}
				// A different evidence digest avoids the intentional evidence retry path.
				ref := *correction.TrustedEvidence
				scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
				if err != nil {
					t.Fatal(err)
				}
				raw = append(raw, '\n')
				ref.SHA256, err = scope.publishObject(raw)
				if err != nil {
					t.Fatal(err)
				}
				ref.Bytes = int64(len(raw))
				stale := correction
				stale.CommandID, stale.ExpectedVersion, stale.TrustedEvidence = f.id(), 4, &ref
				if _, err := f.service.CorrectPaidSettlement(ctx, stale); err == nil || f.store.Durable() != before {
					t.Fatalf("stale predecessor replaced settlement: %v", err)
				}
			case "reopen-before-correction":
				f.reopen(t, true)
				f.service.mu.Lock()
				f.service.p.paidHotLimit = 0
				f.service.p.prunePaid()
				f.service.mu.Unlock()
				damageCommandDirectory(f.service, "missing")
				f.resident(t, false)
				f.totals(t, admission, "120", "0", false)
				f.correct(t, correction)
				f.totals(t, admission, "29", "0", false)
				f.reopen(t, true)
			case "held-account":
				second := f.admitTask(t)
				f.resident(t, true)
				f.totals(t, admission, "120", "120", false)
				f.correct(t, correction)
				f.resident(t, true)
				f.totals(t, admission, "29", "120", false)
				f.reopen(t, true)
				f.totals(t, admission, "29", "120", false)
				f.resident(t, true)
				released, err := f.service.ReconcilePaid(ctx, PaidReconciliation{CommandID: f.id(), AttemptID: second.AttemptID, ExpectedVersion: 1})
				if err != nil || released.Outcome != "released_unsent" {
					t.Fatalf("release of B after A correction: %+v %v", released, err)
				}
			case "overrun-fence":
				f.correct(t, correction)
				f.totals(t, admission, "29", "0", false)
				f.resident(t, false)
				f.reopen(t, true)
			case "first-configuration":
				f.correct(t, correction)
				f.totals(t, admission, "29", "0", false)
				f.resident(t, false)
				f.unconfigured(t)
				for version, usd := range []string{"0.000148", "0.000149"} {
					if _, err := f.service.ConfigureSpend(ctx, f.owner, SpendConfiguration{CommandID: f.id(), Scope: f.taskScope(), ExpectedVersion: int64(version), Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: usd, PeriodKind: "utc_day"}}); err != nil {
						t.Fatal(err)
					}
					f.totals(t, admission, "29", "0", false)
					if version == 0 {
						request := f.prepareTask(t)
						before := f.store.Durable()
						_, _, err := scopedBound(t, f.service, bindingOf(f.origin)).AdmitPaid(ctx, request)
						var busy *BusyError
						if !errors.As(err, &busy) || busy.Reason != "budget_exposure" || f.store.Durable() != before || len(f.posts) != 1 {
							t.Fatalf("148 ceiling manufactured allowance: %v", err)
						}
						if err := request.Prepared.Close(); err != nil {
							t.Fatal(err)
						}
						f.totals(t, admission, "29", "0", false)
					} else {
						f.admitTask(t)
					}
				}
				f.totals(t, admission, "29", "120", false)
				f.reopen(t, true)
				view, err := f.service.PaidBudgetStatus(ctx, PaidBudgetLookup{Scope: f.taskScope()})
				if err != nil || view.Version != 2 || view.Terms == nil || view.Terms.CeilingMicros == nil || *view.Terms.CeilingMicros != 149 || !view.Terms.Enabled {
					t.Fatalf("configured task terms lost on reopen: %+v %v", view, err)
				}
				f.replayTotals(t, admission, "29", "120", false, true)
				return
			case "committed-replay":
				ref := *correction.TrustedEvidence
				scope, err := newPrivateScope(f.root, f.service.p.agents[f.origin.AgentID].directory, f.origin.ContextID.String(), &f.service.dirs)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := readObject(scope, Head{SHA256: ref.SHA256, Bytes: ref.Bytes})
				if err != nil {
					t.Fatal(err)
				}
				var observation paidObservationObject
				if err := json.Unmarshal(raw, &observation); err != nil {
					t.Fatal(err)
				}
				f.service.mu.Lock()
				attempt := f.service.p.paidAttempts[admission.AttemptID]
				f.service.mu.Unlock()
				result := derivePaidResult(attempt, observation)
				result.Kind, result.ExpectedVersion, result.Version = "correction", 3, 4
				result.PreviousResultSequence, result.PreviousEvidenceSHA256 = correction.PreviousResultSequence, correction.PreviousEvidenceSHA256
				result.EvidenceObject, result.EvidenceSHA256 = &ref, ref.SHA256
				drafts := f.drafts(t, result)
				recorded := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, drafts[2].Payload)
				if _, err := f.store.Submit(ctx, drafts[0], drafts[1:]); err != nil {
					t.Fatal(err)
				}
				committed := f.store.Durable()
				f.reopen(t, true)
				receipt, err := f.service.PaidCommandStatus(ctx, PaidCommandLookup{CommandID: recorded.CommandID, SubmissionSHA256: recorded.SubmissionSHA256})
				if err != nil || !reflect.DeepEqual(receipt.RuntimePaidReceipt, recorded) || receipt.Sequence != committed || receipt.Outcome != "corrected" || f.store.Durable() != committed || len(f.posts) != 1 {
					t.Fatalf("committed correction receipt lost: %+v %v", receipt, err)
				}
			}
			f.replayTotals(t, admission, "29", "0", false, false)
		})
	}
}

func TestPaidUnconfiguredScopeColdStatus(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"correction-race", "read-error"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newPaidUnconfiguredFixture(t, 100)
			admission, correction := f.settle(t, "120", false)
			f.totals(t, admission, "120", "0", false)
			f.admitTask(t)
			f.resident(t, true)
			ctx := context.Background()
			request := PaidBudgetLookup{Scope: f.taskScope(), Period: admission.AdmissionPeriod}
			if name == "correction-race" {
				arrived, release := stopColdHistory(f.service, needPaidTotal)
				defer release()
				type answer struct {
					view PaidBudgetView
					err  error
				}
				done := make(chan answer, 1)
				go func() { view, err := f.service.PaidBudgetStatus(ctx, request); done <- answer{view, err} }()
				select {
				case <-arrived:
				case got := <-done:
					t.Fatalf("owner unconfigured totals did not suspend: %+v %v", got.view, got.err)
				}
				// The one-shot suspension is consumed; these reads prove the prior prefix.
				f.totals(t, admission, "120", "120", false)
				f.correct(t, correction)
				before := f.store.Durable()
				release()
				got := <-done
				if got.err != nil || got.view.SettledMicros != "29" || got.view.HeldMicros != "120" || got.view.Fenced || got.view.ThroughSequence != before || f.store.Durable() != before || len(f.posts) != 1 {
					t.Fatalf("owner cold totals ignored concurrent correction: %+v %v", got.view, got.err)
				}
				f.replayTotals(t, admission, "29", "120", false, true)
			} else {
				f.totals(t, admission, "120", "120", false)
				failure := fmt.Errorf("unconfigured scope totals: %w", ledger.ErrCorrupt)
				f.service.deps.historyResolveError = func(kind historyNeedKind) error {
					if kind == needPaidTotal {
						return failure
					}
					return nil
				}
				before := f.store.Durable()
				view, err := f.service.PaidBudgetStatus(ctx, request)
				f.service.deps.historyResolveError = nil
				if err != failure || !errors.Is(err, ledger.ErrCorrupt) || !reflect.DeepEqual(view, PaidBudgetView{}) || f.store.Durable() != before || len(f.posts) != 1 {
					t.Fatalf("unconfigured total read swallowed error: %+v %v", view, err)
				}
				attempt := f.view(t, admission.AttemptID)
				if attempt.SettledMicros != "120" || attempt.HeldMicros != "0" || attempt.Fenced || attempt.Version != 3 {
					t.Fatalf("read failure changed attempt: %+v", attempt)
				}
				f.replayTotals(t, admission, "120", "120", false, true)
			}
		})
	}
}
