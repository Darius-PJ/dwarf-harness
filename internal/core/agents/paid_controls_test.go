package agents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func TestPaidProfileAndFeatureRefusal(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"disabled", "unpriced", "incomplete-source", "unknown-endpoint", "unknown-model", "unknown-tier", "expiry-at-configuration", "expiry-at-admission", "beta", "media", "hosted-tool", "prior-tool-history-sonnet", "prior-tool-history-opus"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newLivePaidFixture(t)
			ctx := context.Background()
			p := providerProfile(f.route.Profile)
			p.CapabilityVersion += "-refused"
			input := f.input()
			var err error
			before := f.store.Durable()
			switch name {
			case "disabled":
				raw, _ := json.Marshal(f.route.Profile)
				_, err = f.service.ConfigurePaidRoute(ctx, bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: false, CredentialScopeID: f.credential.ScopeID, Profile: raw})
				if err != nil {
					t.Fatal(err)
				}
				before = f.store.Durable()
				_, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), input)
			case "media":
				input.Request.Messages[0].Content[0].Type = "image"
				_, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), input)
			case "hosted-tool":
				input.Request.Tools = []providers.Tool{{Name: "remote", InputSchema: json.RawMessage(`{"type":"server_tool"}`)}}
				_, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), input)
			case "prior-tool-history-sonnet", "prior-tool-history-opus":
				f.liveProfile(t, func(p *providers.Profile) {
					p.ModelID, p.RequestedTier = providers.Sonnet, "auto"
					if name == "prior-tool-history-opus" {
						p.ModelID = providers.Opus
					}
				})
				input.ExpectedRouteVersion = f.route.Version
				input.Request.Messages = []providers.Message{
					{Role: "assistant", Content: []providers.Block{{Type: "tool_use", ID: "prior", Name: "local", Input: json.RawMessage(`{}`)}}},
					{Role: "user", Content: []providers.Block{{Type: "tool_result", ToolUseID: "prior", Content: []providers.Text{{Type: "text", Text: "result"}}}}},
				}
				before = f.store.Durable()
				_, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), input)
			case "beta":
				input.Request, err = providers.ParseRequest([]byte(`{"max_tokens":20,"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}],"betas":["unsupported"]}`))
				if err == nil {
					_, err = f.service.PrepareAnthropic(ctx, bindingOf(f.origin), input)
				}
			case "expiry-at-admission":
				r := f.prepare(t)
				f.service.deps.reviewClock = func() (int64, error) { return p.ExpiresUTCNS, nil }
				_, _, err = scopedBound(t, f.service, bindingOf(f.origin)).AdmitPaid(ctx, r)
			default:
				switch name {
				case "unpriced":
					p.Tariff.Rates = p.Tariff.Rates[:len(p.Tariff.Rates)-1]
					p.TariffVersion += "-unpriced"
				case "incomplete-source":
					p.Sources = p.Sources[1:]
				case "unknown-endpoint":
					p.Authority = "unrecognized.example"
				case "unknown-model":
					p.ModelID = "unrecognized-model"
				case "unknown-tier":
					p.RequestedTier = "unrecognized"
				case "expiry-at-configuration":
					f.service.deps.reviewClock = func() (int64, error) { return p.ExpiresUTCNS, nil }
				}
				raw, _ := json.Marshal(p)
				_, err = f.service.ConfigurePaidRoute(ctx, bindingOf(f.origin), PaidRouteConfiguration{CommandID: f.id(), RouteID: f.route.RouteID, ExpectedVersion: f.route.Version, Enabled: true, CredentialScopeID: f.credential.ScopeID, Profile: raw})
			}
			if err == nil || f.store.Durable() != before || f.budget(t, "").HeldMicros != "0" {
				t.Fatalf("unsupported request/configuration installed: %v", err)
			}
		})
	}
}

func TestPaidControlsCASAndIdempotency(t *testing.T) {
	t.Parallel()
	f := newLivePaidFixture(t)
	ctx := context.Background()
	r := SpendConfiguration{CommandID: f.id(), Scope: ledger.PaidScope{Kind: "colony"}, ExpectedVersion: f.service.p.book.paid[ledger.PaidScope{Kind: "colony"}].version, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000120", PeriodKind: "utc_day"}}
	original, err := f.service.ConfigureSpend(ctx, bindingOf(f.origin), r)
	if err != nil {
		t.Fatal(err)
	}
	f.service.deps.reviewClock = func() (int64, error) { t.Error("retry sampled clock"); return 0, ErrPaidClock }
	before := f.store.Durable()
	retry, err := f.service.ConfigureSpend(ctx, bindingOf(f.origin), r)
	if err != nil || retry.Sequence != original.Sequence || f.store.Durable() != before {
		t.Fatalf("spend retry: %v", err)
	}
	r.Terms.USD = "0.000121"
	if _, err = f.service.ConfigureSpend(ctx, bindingOf(f.origin), r); !errors.Is(err, ErrCommandMismatch) {
		t.Fatalf("changed spend reused ID: %v", err)
	}
	r.CommandID = f.id()
	r.GrantID = f.grant
	if _, err = f.service.ConfigureSpend(ctx, bindingOf(f.origin), r); !errors.Is(err, ErrInvalid) {
		t.Fatalf("owner selected authority: %v", err)
	}
	r.GrantID = ledger.CredentialRef{}.Version
	if _, err = f.service.ConfigureSpend(ctx, bindingOf(f.origin), r); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale CAS replaced terms: %v", err)
	}
	r.ExpectedVersion++
	r.Terms.PeriodKind = "lifetime"
	if _, err = f.service.ConfigureSpend(ctx, bindingOf(f.origin), r); !errors.Is(err, ErrPaidPeriod) {
		t.Fatalf("colony period reset: %v", err)
	}
}

func paidLivePeriodRows(t *testing.T) {
	t.Parallel()
	f := newLivePaidFixture(t)
	ctx := context.Background()
	f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000120", PeriodKind: "utc_day"})
	r := f.prepare(t)
	_, b := f.admit(t, r)
	p, _ := b.Admission()
	if f.budget(t, p.AdmissionPeriod).Warning {
		t.Fatal("invented warning")
	}
	f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "finite", USD: "0.000119", PeriodKind: "utc_day", WarningPPM: new(int64(500000))})
	v := f.budget(t, p.AdmissionPeriod)
	if v.HeldMicros != "120" || v.Available || !v.Warning {
		t.Fatalf("cap reduction erased hold: %+v", v)
	}
	f.liveSpend(t, ledger.PaidScope{Kind: "colony"}, SpendTermsInput{Enabled: true, Mode: "unlimited", PeriodKind: "utc_day"})
	next := f.prepare(t)
	f.service.deps.reviewClock = func() (int64, error) { return p.AdmissionUTCNS + int64(24*time.Hour), nil }
	_, b = f.admit(t, next)
	later, _ := b.Admission()
	if later.AdmissionPeriod == p.AdmissionPeriod || f.budget(t, later.AdmissionPeriod).HeldMicros != "240" {
		t.Fatal("rollover refunded unresolved hold")
	}
	if _, err := f.service.ReconcilePaid(ctx, PaidReconciliation{CommandID: f.id(), AttemptID: r.AttemptID, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if f.budget(t, later.AdmissionPeriod).HeldMicros != "120" {
		t.Fatal("release affected another period hold")
	}
}

func TestPaidSharedClock(t *testing.T) {
	t.Parallel()
	for _, direction := range []string{"paid-to-review", "review-to-paid"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()
			f, q := liveReviewFixture(t)
			q.Binding.Reviewer = reviewOwnerPrincipal()
			ctx := context.Background()
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			var err error
			f.service, _, err = openPaidService(ctx, f.store, f.root, DefaultLimits(), PaidOptions{Limits: DefaultPaidLimits()}, serviceDeps{})
			if err != nil {
				t.Fatal(err)
			}
			entry := q.Binding.DeadlineUTCNS - 100
			f.service.deps.reviewClock = func() (int64, error) { return entry, nil }
			spend := SpendConfiguration{CommandID: privateID(2200), Scope: ledger.PaidScope{Kind: "colony"}, Terms: SpendTermsInput{Enabled: true, Mode: "finite", USD: "1", PeriodKind: "lifetime"}}
			if direction == "paid-to-review" {
				if _, err = f.service.ConfigureSpend(ctx, reviewOwner(), spend); err != nil {
					t.Fatal(err)
				}
				f.service.deps.reviewClock = func() (int64, error) { return entry - 1, nil }
				if _, err = f.service.RequestReview(ctx, reviewOwner(), q); !errors.Is(err, ErrReviewClock) {
					t.Fatalf("paid sample did not fence review: %v", err)
				}
			} else {
				stale := q
				stale.ReplacesRequestID = privateID(2201)
				if _, err = f.service.RequestReview(ctx, reviewOwner(), stale); !errors.Is(err, ErrReviewStale) {
					t.Fatal(err)
				}
				f.service.deps.reviewClock = func() (int64, error) { return entry - 1, nil }
				if _, err = f.service.ConfigureSpend(ctx, reviewOwner(), spend); !errors.Is(err, ErrPaidClock) {
					t.Fatalf("refused review sample did not fence paid: %v", err)
				}
				f.service.deps.reviewClock = func() (int64, error) { return entry + 1, nil }
				if _, err = f.service.ConfigureSpend(ctx, reviewOwner(), spend); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("final-sample-and-reusable-handle", func(t *testing.T) {
		t.Parallel()
		f := newLivePaidFixture(t)
		r := f.prepare(t)
		ctx := context.Background()
		a := scopedBound(t, f.service, bindingOf(f.origin))
		floor := f.service.p.reviewFloor + int64(time.Hour)
		calls := 0
		f.service.deps.reviewClock = func() (int64, error) {
			calls++
			if calls == 1 {
				return floor, nil
			}
			return floor - 1, nil
		}
		before := f.store.Durable()
		if _, _, err := a.AdmitPaid(ctx, r); !errors.Is(err, ErrPaidClock) || f.store.Durable() != before {
			t.Fatalf("regressed final observation admitted: %v", err)
		}
		f.service.deps.reviewClock = func() (int64, error) { return floor + 1, nil }
		original, _, err := a.AdmitPaid(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		f.service.deps.reviewClock = func() (int64, error) { t.Error("matched admission sampled clock"); return 0, ErrPaidClock }
		retry, _, err := a.AdmitPaid(ctx, r)
		if err != nil || retry.Sequence != original.Sequence {
			t.Fatalf("admission retry: %v", err)
		}
	})
}
