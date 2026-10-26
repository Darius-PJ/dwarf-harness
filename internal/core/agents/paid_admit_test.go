package agents

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func paidInputPath(f *paidFixture, p ledger.RuntimePaidReserved) string {
	return filepath.Join(f.root, "agents", f.service.p.agents[f.origin.AgentID].directory, "contexts", f.origin.ContextID.String(), "core", "objects", p.InputObject.SHA256.String())
}

func TestPaidAuthorityAndAttenuation(t *testing.T) {
	t.Parallel()
	t.Run("task-authority", paidTaskAuthorityRows)
	t.Run("prepare-time", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{"turn-only", "missing-model", "wrong-route", "wrong-parent", "missing-spend-leaf", "model-only"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				f := newLivePaidFixture(t)
				ctx := context.Background()
				origin := bindingOf(f.origin)
				input := f.input()
				switch name {
				case "turn-only":
					input.GrantID = f.inspect
				case "missing-model":
					input.GrantID = f.id()
				case "wrong-parent":
					input.ParentTurnID = f.id()
				case "wrong-route", "missing-spend-leaf", "model-only":
					input.GrantID = f.id()
					spec := GrantSpec{GrantID: input.GrantID, Grantee: origin, Operations: []string{"model.call"}, Resources: []ledger.Resource{{Kind: "external", ResourceKey: "paid-route/" + input.RouteID.String()}}, DataClass: "external", Recipients: []Binding{origin}, Limits: paidPhysicalCapacity(), Lifetime: "until_revoked"}
					issue := GrantIssue{CommandID: f.id(), ExpectedAuthorityVersion: uint64(f.service.p.authority), Spec: spec}
					if name != "missing-spend-leaf" {
						if name == "wrong-route" {
							issue.Spec.Resources[0].ResourceKey = "paid-route/" + f.id().String()
						}
						if _, err := f.service.IssueGrant(ctx, origin, issue); err != nil {
							t.Fatal(err)
						}
						f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: input.GrantID}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"})
					} else {
						issue.GrantID, issue.Spec.ParentGrantID = f.grant, f.grant
						if _, err := scopedBound(t, f.service, origin).IssueGrant(ctx, issue); err != nil {
							t.Fatal(err)
						}
					}
				}
				before := f.store.Durable()
				prepared, err := f.service.PrepareAnthropic(ctx, origin, input)
				if prepared != nil {
					t.Cleanup(func() { _ = prepared.Close() })
				}
				if name == "model-only" {
					if err != nil || prepared == nil {
						t.Fatalf("model.call required unrelated grant authority: %v", err)
					}
					if f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
						t.Fatal("preparation committed a paid hold")
					}
					f.admit(t, PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: prepared})
					if f.budget(t, "").HeldMicros != "120" {
						t.Fatal("model.call-only admission did not reserve its quote")
					}
					return
				}
				var busy *BusyError
				if !errors.Is(err, ErrDenied) || errors.As(err, &busy) || prepared != nil {
					t.Fatalf("unauthorized preparation: %v", err)
				}
				if f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" || len(f.service.p.book.preparing) != 0 {
					t.Fatal("refused preparation committed or retained a hold")
				}
			})
		}
	})
	for _, name := range []string{"handle-mismatch-missing-model", "revoked-model", "disabled-spend-ancestor", "missing-spend-leaf", "wrong-origin", "handle-mismatch-parent", "handle-mismatch-task-generation", "handle-mismatch-turn-only", "unrelated-revision"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			root := f.grant
			f.grant = f.child(t, root, 100000)
			r := f.prepare(t)
			a := scopedBound(t, f.service, bindingOf(f.origin))
			switch name {
			case "handle-mismatch-missing-model":
				r.GrantID = f.id()
			case "revoked-model":
				_, err := f.service.RevokeGrant(context.Background(), bindingOf(f.origin), GrantRevocation{CommandID: f.id(), RevokeGrantID: root, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)})
				if err != nil {
					t.Fatal(err)
				}
			case "disabled-spend-ancestor":
				f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: root}, SpendTermsInput{Enabled: false, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"})
			case "missing-spend-leaf":
				f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: f.grant}, SpendTermsInput{Enabled: false, Mode: "finite", USD: "0.100000", PeriodKind: "utc_day"})
			case "wrong-origin":
				other := scopedWorkContext(t, f.service, bindingOf(f.origin), 0xe00, 0xe01)
				a = scopedBound(t, f.service, other)
			case "handle-mismatch-parent":
				r.ParentTurnID = f.id()
			case "handle-mismatch-task-generation":
				r.TaskBinding = &ledger.TaskBinding{TaskID: f.id(), AssignmentID: f.id(), Generation: 2}
			case "handle-mismatch-turn-only":
				r.GrantID = f.inspect
			case "unrelated-revision":
				grant := privateScopedGrant(bindingOf(f.origin), 0xe10, scopedCapacity(), "turn.inspect")
				issueScopedRoot(t, f.service, bindingOf(f.origin), 0xe11, grant)
			}
			before := f.store.Durable()
			_, _, err := a.AdmitPaid(context.Background(), r)
			if name == "unrelated-revision" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var busy *BusyError
			if !errors.Is(err, ErrDenied) || errors.As(err, &busy) || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
				t.Fatalf("unauthorized admission: %v", err)
			}
		})
	}
	t.Run("delegation-never-expands-or-reenables", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		child := f.child(t, f.grant, 100)
		a := scopedBound(t, f.service, bindingOf(f.origin))
		for _, terms := range []SpendTermsInput{{Enabled: true, Mode: "finite", USD: "0.100001", PeriodKind: "utc_day"}, {Enabled: true, Mode: "finite", USD: "0.000101", PeriodKind: "utc_day"}} {
			before := f.store.Durable()
			_, err := a.DelegateSpend(context.Background(), SpendDelegation{CommandID: f.id(), GrantID: f.grant, ChildGrantID: child, ExpectedVersion: 1, Terms: terms})
			if !errors.Is(err, ErrDenied) || f.store.Durable() != before {
				t.Fatalf("child expanded: %v", err)
			}
		}
		_, err := a.DelegateSpend(context.Background(), SpendDelegation{CommandID: f.id(), GrantID: f.grant, ChildGrantID: child, ExpectedVersion: 1, Terms: SpendTermsInput{Enabled: false, Mode: "finite", USD: "0.000100", PeriodKind: "utc_day"}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.DelegateSpend(context.Background(), SpendDelegation{CommandID: f.id(), GrantID: f.grant, ChildGrantID: child, ExpectedVersion: 2, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000100", PeriodKind: "utc_day"}})
		if !errors.Is(err, ErrDenied) {
			t.Fatalf("issuer reenabled terms: %v", err)
		}
	})
}

func TestPaidAtomicScopes(t *testing.T) {
	t.Parallel()
	f := newLivePaidFixture(t)
	root := f.grant
	f.liveSpend(t, ledger.PaidScope{Kind: "grant", ID: root}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000120", PeriodKind: "utc_day"})
	first, second := f.child(t, root, 120), f.child(t, root, 120)
	if f.budget(t, "").HeldMicros != "0" {
		t.Fatal("issuance or spend delegation reserved money")
	}
	f.grant = first
	r1 := f.prepare(t)
	f.grant = second
	r2 := f.prepare(t)
	a := scopedBound(t, f.service, bindingOf(f.origin))
	gate := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, r := range []PaidAdmission{r1, r2} {
		wg.Go(func() { <-gate; _, _, err := a.AdmitPaid(context.Background(), r); results <- err })
	}
	close(gate)
	wg.Wait()
	close(results)
	wins, busy := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			var b *BusyError
			if errors.As(err, &b) && b.Reason == "budget_exposure" {
				busy++
			} else {
				t.Fatal(err)
			}
		}
	}
	if wins != 1 || busy != 1 {
		t.Fatalf("shared parent admitted %d siblings; busy=%d", wins, busy)
	}
	view, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "grant", ID: root}})
	if err != nil || view.HeldMicros != "120" {
		t.Fatalf("parent hold not exact: %+v %v", view, err)
	}
	if f.budget(t, "").HeldMicros != "120" {
		t.Fatal("colony rollup counted a scope twice")
	}
	for _, r := range []PaidAdmission{r1, r2} {
		v, err := f.service.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: r.AttemptID})
		if errors.Is(err, ErrDenied) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, scope := range v.Admission.Scopes {
			b, err := f.service.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: scope, Period: v.Admission.AdmissionPeriod})
			if err != nil || b.HeldMicros != "120" {
				t.Fatalf("partial hold at %s: %+v %v", scope.Kind, b, err)
			}
		}
	}
}

func TestPaidUnsentRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newLivePaidFixture(t)
	r := f.prepare(t)
	_, bound := f.admit(t, r)
	admission, _ := bound.Admission()
	release := PaidReconciliation{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}
	f.service.deps.reviewClock = func() (int64, error) { t.Error("release sampled clock"); return 0, ErrPaidClock }
	original, err := f.service.ReconcilePaid(ctx, release)
	if err != nil || original.Outcome != "released_unsent" || f.budget(t, admission.AdmissionPeriod).HeldMicros != "0" {
		t.Fatalf("release: %+v %v", original, err)
	}
	before := f.store.Durable()
	retry, err := f.service.ReconcilePaid(ctx, release)
	if err != nil || retry.Sequence != original.Sequence || f.store.Durable() != before {
		t.Fatalf("release retry: %+v %v", retry, err)
	}
	release.CommandID = f.id()
	release.ExpectedVersion = 2
	if _, err = f.service.ReconcilePaid(ctx, release); err == nil || f.store.Durable() != before {
		t.Fatal("released twice")
	}
	if len(f.service.p.book.preparing) != 0 {
		t.Fatal("unsent release retained physical buffers")
	}
}

func TestPaidAdmissionHandleLifetime(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"matched-fresh-handle", "closed-unused-handle"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			input := f.input()
			p, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
			if err != nil {
				t.Fatal(err)
			}
			request := PaidAdmission{CommandID: f.id(), AttemptID: input.AttemptID, GrantID: input.GrantID, ParentTurnID: input.ParentTurnID, Prepared: p}
			a := scopedBound(t, f.service, bindingOf(f.origin))
			if name == "closed-unused-handle" {
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
				before := f.store.Durable()
				_, _, err := a.AdmitPaid(context.Background(), request)
				if !errors.Is(err, ErrClosed) || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
					t.Fatal("closed preparation remained usable or lost ErrClosed")
				}
				return
			}
			first, bound := f.admit(t, request)
			recorded, err := bound.Admission()
			if err != nil {
				t.Fatal(err)
			}
			f.service.paid.limits.MaxPreparedPaid = 1
			p, err = f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), input)
			if err != nil {
				t.Fatal(err)
			}
			request.Prepared = p
			before := f.store.Durable()
			retry, matched, err := a.AdmitPaid(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			got, err := matched.Admission()
			if err != nil || !sameScopedValue(got, recorded) || retry.Sequence != first.Sequence || f.store.Durable() != before {
				t.Fatal("matching admission manufactured incomplete metadata")
			}
			next, err := f.service.PrepareAnthropic(context.Background(), bindingOf(f.origin), f.input())
			if err != nil {
				t.Fatal("matching preparation retained its only physical slot")
			}
			if err := next.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
