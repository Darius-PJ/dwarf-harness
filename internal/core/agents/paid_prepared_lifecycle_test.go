package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestPaidPreparedLifetime(t *testing.T) {
	t.Parallel()
	t.Run("bounded-slots-and-close", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		ctx := context.Background()
		f.service.paid.limits.MaxPreparedPaid = 1
		p, err := f.service.PrepareAnthropic(ctx, bindingOf(f.origin), f.input())
		if err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		var busy *BusyError
		if _, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), f.input()); !errors.As(err, &busy) || f.store.Durable() != before {
			t.Fatalf("preparation did not consume a slot: %v", err)
		}
		if f.budget(t, "").HeldMicros != "0" {
			t.Fatal("preparation reserved money")
		}
		if err = p.Close(); err != nil {
			t.Fatal(err)
		}
		if err = p.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := f.service.PrepareAnthropic(ctx, bindingOf(f.origin), f.input())
		if err != nil {
			t.Fatalf("close leaked slot or physical capacity: %v", err)
		}
		defer next.Close()
	})
	t.Run("close-during-publication", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		r := f.prepare(t)
		ctx := context.Background()
		a := scopedBound(t, f.service, bindingOf(f.origin))
		entered, proceed := make(chan struct{}), make(chan struct{})
		f.service.deps.beforePublish = func(Binding, string) error { close(entered); <-proceed; return nil }
		before := f.store.Durable()
		result := make(chan error, 1)
		go func() { _, _, err := a.AdmitPaid(ctx, r); result <- err }()
		<-entered
		if err := r.Prepared.Close(); err != nil {
			t.Fatal(err)
		}
		close(proceed)
		if err := <-result; !errors.Is(err, ErrClosed) || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
			t.Fatalf("closed handle committed: %v", err)
		}
		f.service.deps.beforePublish = nil
		next := f.prepare(t)
		f.admit(t, next)
	})
	t.Run("cancelled-publication-retries", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		r := f.prepare(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.service.deps.beforePublish = func(Binding, string) error { cancel(); return ctx.Err() }
		before := f.store.Durable()
		a := scopedBound(t, f.service, bindingOf(f.origin))
		if _, _, err := a.AdmitPaid(ctx, r); !errors.Is(err, context.Canceled) || f.store.Durable() != before {
			t.Fatalf("cancelled publication committed: %v", err)
		}
		f.service.deps.beforePublish = nil
		f.admit(t, r)
	})
	t.Run("financial-refusal-retries", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		r := f.prepare(t)
		ctx := context.Background()
		scope := ledger.PaidScope{Kind: "colony"}
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000119", PeriodKind: "utc_day"})
		before := f.store.Durable()
		a := scopedBound(t, f.service, bindingOf(f.origin))
		var busy *BusyError
		if _, _, err := a.AdmitPaid(ctx, r); !errors.As(err, &busy) || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
			t.Fatalf("unaffordable admission changed state: %v", err)
		}
		f.liveSpend(t, scope, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000120", PeriodKind: "utc_day"})
		f.admit(t, r)
	})
	t.Run("incarnation-and-explicit-enable", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		r := f.prepare(t)
		ctx := context.Background()
		if err := f.service.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), f.input()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("plain Open enabled producers: %v", err)
		}
		if _, _, err = scopedBound(t, f.service, bindingOf(f.origin)).AdmitPaid(ctx, r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("old incarnation handle accepted: %v", err)
		}
	})
}

func TestPaidIntegrityOnlyInspection(t *testing.T) {
	t.Parallel()
	f := newLivePaidFixture(t)
	ctx := context.Background()
	r := f.prepare(t)
	f.admit(t, r)
	a := scopedBound(t, f.service, bindingOf(f.origin))
	before := f.store.Durable()
	view, err := a.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: r.AttemptID, GrantID: f.inspect, VerifyInput: true})
	if err != nil || view.InputIntegrity != "verified" || f.store.Durable() != before {
		t.Fatalf("integrity-only inspection required a byte-reading grant: %v", err)
	}
	receipt, err := f.service.ConfigureCapacity(ctx, bindingOf(f.origin), CapacitySpec{CommandID: f.id(), ExpectedVersion: uint64(f.service.p.book.accounts[ledger.ScopeKey{Kind: "colony"}].version), Scope: ledger.ScopeKey{Kind: "colony"}, Limits: paidPhysicalCapacity()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.PaidCommandStatus(ctx, PaidCommandLookup{CommandID: receipt.CommandID, SubmissionSHA256: receipt.SubmissionSHA256}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("owner lookup of nonpaid command: %v", err)
	}
	if _, err = f.service.RevokeGrant(ctx, bindingOf(f.origin), GrantRevocation{CommandID: f.id(), RevokeGrantID: f.inspect, ExpectedVersion: 1, ExpectedAuthorityVersion: uint64(f.service.p.authority)}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.PaidAttemptStatus(ctx, PaidAttemptLookup{AttemptID: r.AttemptID, GrantID: f.inspect, VerifyInput: true}); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked inspector verified private input: %v", err)
	}
}
