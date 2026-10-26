package agents

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestPaidStatusViews(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPaidFixture(t)
	p := f.reserve(t)
	before := f.store.Durable()
	v, err := f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: p.AttemptID, VerifyInput: true})
	if err != nil || v.InputIntegrity != "verified" || v.State != "reserved" || v.HeldMicros != "120" || v.SettledMicros != "0" || v.ThroughSequence != before {
		t.Fatalf("reserved status: %+v %v", v, err)
	}
	v.Admission.Quote.Components[0].Classes[0] = "changed"
	if got := f.view(t, p.AttemptID); got.Admission.Quote.Components[0].Classes[0] == "changed" {
		t.Fatal("view aliases retained admission")
	}
	account := f.budget(t, "")
	*account.Terms.CeilingMicros = 0
	if got := f.budget(t, ""); *got.Terms.CeilingMicros != 100000 {
		t.Fatal("budget terms alias retained state")
	}
	r := f.service.p.paidAttempts[p.AttemptID].receipt
	receipt, err := f.service.PaidCommandStatus(ctx, PaidCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256})
	if err != nil || !reflect.DeepEqual(receipt.RuntimePaidReceipt, r) || receipt.Sequence != before || receipt.EffectSequence != v.AdmissionSequence {
		t.Fatalf("paid receipt: %+v %v", receipt, err)
	}
	if _, err := f.service.ScopedCommandStatus(ctx, ScopedCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("scoped receipt adopted paid command: %v", err)
	}
	if _, err := f.service.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("review receipt adopted paid command: %v", err)
	}
	if _, err := f.service.CommandStatus(ctx, CommandLookup{Binding: bindingOf(f.origin), CommandID: r.CommandID, SubmissionSHA256: r.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("private receipt adopted paid command: %v", err)
	}
	report := reopenScoped(t, f.serviceFixture, DefaultLimits())
	if len(report.PendingPaid) != 1 || report.PendingPaid[0].Admission.AttemptID != p.AttemptID || len(report.UnboundCredentials) != 1 || report.UnboundCredentials[0].Version != f.credential.CredentialVersion || report.UnboundCredentials[0].SourceLocator != f.credential.SourceLocator {
		t.Fatalf("reconciliation dropped pending authorization: %+v", report)
	}
	if err := os.Remove(filepath.Join(f.root, "agents", f.service.p.agents[f.origin.AgentID].directory, "contexts", f.origin.ContextID.String(), "core", "objects", p.InputObject.SHA256.String())); err != nil {
		t.Fatal(err)
	}
	reopenScoped(t, f.serviceFixture, DefaultLimits())
	v, err = f.service.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: p.AttemptID, VerifyInput: true})
	if err != nil || v.InputIntegrity != "missing_or_invalid" || v.HeldMicros != "120" {
		t.Fatalf("missing input forgave exposure: %+v %v", v, err)
	}
	if f.store.Durable() != before {
		t.Fatal("passive query or recovery appended")
	}
}

func TestPaidParentEndingAndDisclosure(t *testing.T) {
	t.Parallel()
	t.Run("live-and-replay-child", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		p := f.reserve(t)
		before := f.store.Durable()
		_, err := f.service.EndTurn(context.Background(), TurnEnding{CommandID: f.id(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "interrupted"})
		var busy *BusyError
		if !errors.As(err, &busy) || busy.Reason != "child_unended" || f.store.Durable() != before {
			t.Fatalf("unended paid child escaped parent: %v", err)
		}
		f.dispatch(t, p)
		r := f.result(t, p, 0, 0)
		r.Kind, r.Usage, r.CostMicros, r.ErrorClass = "unknown", nil, nil, "timeout"
		f.append(t, r)
		if _, err := f.service.EndTurn(context.Background(), TurnEnding{CommandID: f.id(), TurnID: f.turn, ExpectedVersion: 1, Outcome: "interrupted"}); err != nil {
			t.Fatal(err)
		}
		reopenScoped(t, f.serviceFixture, DefaultLimits())
		if v := f.view(t, p.AttemptID); v.State != "unknown" || !v.Ended || v.HeldMicros != "120" {
			t.Fatalf("interruption released unknown exposure: %+v", v)
		}
	})
	t.Run("replay-requires-child-ending", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		f.reserve(t)
		sub := ledger.ScopedSubmission{Schema: 2, Actor: "core", Origin: f.origin, Operation: "turn.end", Target: f.service.p.work[f.turn].admission.Target, GrantID: f.inspect, Expected: ledger.ScopedExpected{Version: 1}, Arguments: ledger.WorkEndArguments{WorkID: f.turn, Outcome: "interrupted"}}
		input := scopedInput{id: f.id(), sub: sub, digest: ledger.Digest(sha256.Sum256(paidEncode(t, sub))), turn: f.turn}
		d, err := f.service.scopedDrafts(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		f.service = nil
		if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
			t.Fatal(err)
		}
		service, _, err := Open(context.Background(), f.store, f.root, DefaultLimits())
		if service != nil {
			_ = service.Close()
		}
		if !errors.Is(err, ErrInconsistent) || service != nil {
			t.Fatalf("replay accepted an unended paid child: %v", err)
		}
	})
	t.Run("other-context-is-missing", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		p := f.reserve(t)
		other := scopedWorkContext(t, f.service, bindingOf(f.origin), 0xc00, 0xc01)
		grant := privateScopedGrant(other, 0xc02, scopedCapacity(), "turn.inspect")
		issueScopedRoot(t, f.service, bindingOf(f.origin), 0xc03, grant)
		agent := scopedBound(t, f.service, other)
		for _, id := range []int{0, 0xc04} {
			target := p.AttemptID
			if id != 0 {
				target = privateID(id)
			}
			if _, err := agent.PaidAttemptStatus(context.Background(), PaidAttemptLookup{AttemptID: target, GrantID: grant.GrantID}); err != ErrDenied {
				t.Fatalf("other context disclosed existence: %v", err)
			}
		}
		own := scopedBound(t, f.service, bindingOf(f.origin))
		view, err := own.PaidBudgetStatus(context.Background(), PaidBudgetLookup{Scope: ledger.PaidScope{Kind: "colony"}, GrantID: f.inspect})
		if err != nil || view.Terms != nil || view.HeldMicros != "120" {
			t.Fatalf("shared budget disclosed administrative terms: %+v %v", view, err)
		}
	})
}
