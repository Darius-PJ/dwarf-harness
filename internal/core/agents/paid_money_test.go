package agents

import (
	"context"
	"crypto/sha256"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func paidCorrection(f *paidFixture, t testing.TB, p ledger.RuntimePaidReserved, prior ledger.RuntimePaidResult, input, output int64) ledger.RuntimePaidResult {
	t.Helper()
	v := f.result(t, p, input, output)
	v.Kind, v.ExpectedVersion, v.Version = "correction", prior.Version, prior.Version+1
	v.PreviousResultSequence = int64(f.view(t, p.AttemptID).ResultSequence)
	v.PreviousEvidenceSHA256 = prior.EvidenceSHA256
	return v
}

func TestPaidSettlementAndCorrection(t *testing.T) {
	t.Parallel()
	t.Run("original-tariff-period-and-one-delta", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		p := f.reserve(t)
		f.dispatch(t, p)
		r := f.result(t, p, 10, 2)
		f.append(t, r)
		if v := f.budget(t, p.AdmissionPeriod); v.SettledMicros != "12" || v.HeldMicros != "0" {
			t.Fatalf("first settlement: %+v", v)
		}
		c := paidCorrection(f, t, p, r, 19, 2)
		f.append(t, c)
		if v := f.budget(t, p.AdmissionPeriod); v.SettledMicros != "21" || v.HeldMicros != "0" {
			t.Fatalf("correction did not replace the hot-book cost once: %+v", v)
		}
		// Compute the next correction under the admission tariff before replacing
		// the route. Its later transition must not consult the new tariff.
		late := paidCorrection(f, t, p, c, 20, 2)
		next := f.route
		next.Profile.CapabilityVersion += "-next"
		next.Profile.TariffVersion += "-next"
		next.Profile.Tariff.Rates = append([]ledger.TariffRate(nil), next.Profile.Tariff.Rates...)
		for i := range next.Profile.Tariff.Rates {
			next.Profile.Tariff.Rates[i].Numerator = "10"
		}
		next.ExpectedVersion, next.Version = next.Version, next.Version+1
		next.ObservedUTCNS = p.AdmissionUTCNS + int64(24*time.Hour)
		f.append(t, next)
		f.append(t, late)
		if v := f.budget(t, p.AdmissionPeriod); v.SettledMicros != "22" || v.HeldMicros != "0" {
			t.Fatalf("correction used current tariff, added twice or lost original period: %+v", v)
		}
		if v := f.budget(t, ""); v.SettledMicros != "0" || v.HeldMicros != "0" {
			t.Fatalf("old correction charged current period: %+v", v)
		}
		reopenScoped(t, f.serviceFixture, DefaultLimits())
		if v := f.view(t, p.AttemptID); v.SettledMicros != "22" || v.Version != 5 || v.Fenced {
			t.Fatalf("correction recovery: %+v", v)
		}
	})
	for _, name := range []string{"same-evidence", "stale-predecessor", "wrong-message", "wrong-request", "wrong-cost", "output-regression", "older-evidence"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newPaidFixture(t)
			p := f.reserve(t)
			f.dispatch(t, p)
			r := f.result(t, p, 10, 2)
			f.append(t, r)
			c := paidCorrection(f, t, p, r, 19, 2)
			switch name {
			case "same-evidence":
				c.EvidenceSHA256, c.EvidenceObject = r.EvidenceSHA256, r.EvidenceObject
			case "stale-predecessor":
				c.PreviousResultSequence--
			case "wrong-message":
				c.ProviderMessageID = "other-message"
			case "wrong-request":
				c.ProviderRequestID = "other-request"
			case "wrong-cost":
				*c.CostMicros++
			case "output-regression":
				c.OutputStarted = new(false)
			case "older-evidence":
				f.append(t, c)
				next := paidCorrection(f, t, p, c, 20, 2)
				next.EvidenceSHA256, next.EvidenceObject = r.EvidenceSHA256, r.EvidenceObject
				c = next
			}
			f.refuse(t, f.drafts(t, c))
		})
	}
	t.Run("unfamiliar-financial-tokens-hold", func(t *testing.T) {
		t.Parallel()
		for _, token := range []string{"usage-class", "tier", "error-class", "receipt"} {
			t.Run(token, func(t *testing.T) {
				t.Parallel()
				f := newPaidFixture(t)
				p := f.reserve(t)
				f.dispatch(t, p)
				r := f.result(t, p, 10, 2)
				switch token {
				case "usage-class":
					r.Usage.Classes[0].Class = "future.dimension"
				case "tier":
					r.Usage.PricingTier = "future_tier"
				case "error-class":
					r.ErrorClass = "future_error"
				}
				d := f.drafts(t, r)
				if token == "receipt" {
					receipt := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, d[2].Payload)
					receipt.Outcome = "future_outcome"
					d[2].Payload = paidEncode(t, receipt)
				}
				if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
					t.Fatal(err)
				}
				reopenScoped(t, f.serviceFixture, DefaultLimits())
				if v := f.view(t, p.AttemptID); v.HeldMicros != "120" || v.SettledMicros != "0" || v.State != "unknown" || !v.Ended {
					t.Fatalf("future token invented financial effect: %+v", v)
				}
			})
		}
	})
}

func TestPaidPeriodsWarningsAndOverrun(t *testing.T) {
	t.Parallel()
	t.Run("live-controls-preserve-exposure", paidLivePeriodRows)
	t.Run("holds-warning-ceiling-and-rollover", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		terms := ledger.SpendTerms{Enabled: true, PeriodKind: "utc_day", Mode: "finite", CeilingMicros: new(int64(241)), WarningPPM: new(int64(500000))}
		f.spend(t, ledger.PaidScope{Kind: "colony"}, terms)
		p := f.reserve(t)
		if v := f.budget(t, p.AdmissionPeriod); v.Warning {
			t.Fatalf("warning rounded down instead of ceil(120.5): %+v", v)
		}
		other := f.reserve(t)
		if v := f.budget(t, p.AdmissionPeriod); !v.Warning || v.HeldMicros != "240" {
			t.Fatalf("overlap/warning lost: %+v", v)
		}
		terms.CeilingMicros = new(int64(119))
		f.spend(t, ledger.PaidScope{Kind: "colony"}, terms)
		if v := f.budget(t, p.AdmissionPeriod); v.Available || v.HeldMicros != "240" {
			t.Fatalf("cap cut refunded or allowed exposure: %+v", v)
		}
		f.append(t, ledger.RuntimePaidResult{AttemptID: other.AttemptID, ExpectedVersion: 1, Version: 2, Kind: "released_unsent"})
		terms.CeilingMicros = new(int64(120))
		f.spend(t, ledger.PaidScope{Kind: "colony"}, terms)
		v := ledger.RuntimePaidDispatchIntent{AttemptID: p.AttemptID, ExpectedVersion: 1, Version: 2, AuthorityVersion: f.service.p.authority, RequestSHA256: p.Quote.RequestSHA256, QuoteSHA256: p.Quote.QuoteSHA256, Credential: p.Request.Credential, ObservedUTCNS: p.AdmissionUTCNS + int64(24*time.Hour)}
		f.append(t, v)
		if got := f.budget(t, ""); got.HeldMicros != "120" || got.SettledMicros != "0" {
			t.Fatalf("rollover refunded hold: %+v", got)
		}
		f.append(t, f.result(t, p, 11, 2))
		if got := f.budget(t, p.AdmissionPeriod); got.HeldMicros != "0" || got.SettledMicros != "13" {
			t.Fatalf("late usage lost original period: %+v", got)
		}
		if got := f.budget(t, ""); got.SettledMicros != "0" {
			t.Fatalf("late usage charged current period: %+v", got)
		}
	})
	t.Run("unlimited-exact-aggregate-and-fence", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		for _, scope := range f.template.Scopes {
			if scope.Kind == "task" || scope.Kind == "grant" {
				continue
			}
			f.spend(t, scope, ledger.SpendTerms{Enabled: true, Mode: "unlimited", PeriodKind: "utc_day"})
		}
		f.spend(t, ledger.PaidScope{Kind: "grant", ID: f.grant}, ledger.SpendTerms{Enabled: true, Mode: "unlimited", PeriodKind: "utc_day"})
		p := f.reserve(t)
		q := f.reserve(t)
		f.dispatch(t, p)
		f.dispatch(t, q)
		for _, a := range []ledger.RuntimePaidReserved{p, q} {
			r := f.result(t, a, math.MaxInt64, 0)
			f.append(t, r)
		}
		if got := f.budget(t, p.AdmissionPeriod); got.SettledMicros != "18446744073709551614" || !got.Fenced || got.HeldMicros != "0" || got.Available {
			t.Fatalf("aggregate overflow or unlimited bypassed fence: %+v", got)
		}
		f.spend(t, ledger.PaidScope{Kind: "colony"}, ledger.SpendTerms{Enabled: true, Mode: "finite", PeriodKind: "utc_day", CeilingMicros: new(int64(math.MaxInt64))})
		if !f.budget(t, p.AdmissionPeriod).Fenced {
			t.Fatal("cap change erased violation")
		}
		for i, a := range []ledger.RuntimePaidReserved{p, q} {
			view := f.view(t, a.AttemptID)
			f.append(t, ledger.RuntimePaidOverrunAcknowledged{AttemptID: a.AttemptID, ExpectedVersion: view.Version, Version: view.Version + 1, ResultSequence: int64(view.ResultSequence), EvidenceSHA256: view.Result.EvidenceSHA256, RetiredProfileSHA256: a.ProfileSHA256})
			if got := f.budget(t, p.AdmissionPeriod); got.Fenced != (i == 0) || got.SettledMicros != "18446744073709551614" {
				t.Fatalf("acknowledgement cleared other fence or money: %+v", got)
			}
		}
		retired := f.route
		retired.ExpectedVersion, retired.Version = retired.Version, retired.Version+1
		retired.ObservedUTCNS = f.service.p.reviewFloor
		f.refuse(t, f.drafts(t, retired))
	})
	t.Run("unknown-violation-fences-with-hold", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		p := f.reserve(t)
		f.dispatch(t, p)
		r := f.result(t, p, 0, 0)
		r.Kind, r.Usage, r.CostMicros, r.ErrorClass, r.BoundViolation = "unknown", nil, nil, "protocol_error", true
		f.append(t, r)
		if got := f.budget(t, p.AdmissionPeriod); got.HeldMicros != "120" || !got.Fenced {
			t.Fatalf("unknown violation lost fence/hold: %+v", got)
		}
		c := paidCorrection(f, t, p, r, 1, 0)
		f.append(t, c)
		if got := f.budget(t, p.AdmissionPeriod); got.HeldMicros != "0" || got.Fenced || got.SettledMicros != strconv.Itoa(1) {
			t.Fatalf("correction failed to replace unknown: %+v", got)
		}
	})
	t.Run("component-overrun-even-at-small-cost", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		p := f.reserve(t)
		f.dispatch(t, p)
		r := f.result(t, p, 101, 0)
		r.BoundViolation = true
		r.EvidenceSHA256 = ledger.Digest(sha256.Sum256([]byte("component-overrun")))
		r.EvidenceObject.SHA256 = r.EvidenceSHA256
		f.append(t, r)
		if got := f.budget(t, p.AdmissionPeriod); !got.Fenced || got.SettledMicros != "101" {
			t.Fatalf("dimension excess under total bound escaped: %+v", got)
		}
	})
}
