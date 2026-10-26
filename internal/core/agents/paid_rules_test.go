package agents

import (
	"context"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/providers"
)

func testPaidGroupRules(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"newer-version", "malformed-effect", "mixed-family", "receipt-version", "second-intent", "released-after-intent", "quote-maximum", "quote-digest", "scope-omission", "admission-period", "stale-authority", "observation-regression", "credential-revoked", "disabled-spend", "over-cap", "route-cas", "profile-identity", "period-kind-fixed"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newPaidFixture(t)
			p := f.reservation(t)
			var d []ledger.Draft
			switch name {
			case "second-intent":
				f.append(t, p)
				v := f.dispatch(t, p)
				d = f.drafts(t, v)
			case "released-after-intent":
				f.append(t, p)
				f.dispatch(t, p)
				d = f.drafts(t, ledger.RuntimePaidResult{AttemptID: p.AttemptID, ExpectedVersion: 1, Version: 2, Kind: "released_unsent"})
			case "quote-maximum":
				p.Quote.MaximumMicros--
				p.Quote.Components[0].MaxUnits--
				q, err := providers.QuoteSHA256([32]byte(p.Quote.RequestSHA256), p.Quote.CapabilityVersion, p.Quote.TariffVersion, [32]byte(p.Quote.TariffSHA256), p.Quote.MaximumMicros, paidComponents(p.Quote))
				if err != nil {
					t.Fatal(err)
				}
				p.Quote.QuoteSHA256 = ledger.Digest(q)
			case "quote-digest":
				p.Quote.QuoteSHA256[0] ^= 1
			case "scope-omission":
				p.Scopes[len(p.Scopes)-1].ID = paidGoldenID(0xfff)
			case "admission-period":
				p.AdmissionPeriod = "2001-01-01"
			case "stale-authority":
				p.AuthorityVersion--
			case "observation-regression":
				p.AdmissionUTCNS--
			case "credential-revoked":
				v := f.credential
				v.Action, v.SourceKind, v.SourceLocator = "revoke", "", ""
				v.ExpectedVersion, v.Version = 1, 2
				f.append(t, v)
				p.AdmissionUTCNS = f.service.p.reviewFloor
			case "disabled-spend", "over-cap":
				terms := ledger.SpendTerms{Enabled: name != "disabled-spend", Mode: "finite", PeriodKind: "utc_day", CeilingMicros: new(int64(119))}
				f.spend(t, ledger.PaidScope{Kind: "colony"}, terms)
				p.AdmissionUTCNS = f.service.p.reviewFloor
			case "route-cas":
				v := f.route
				v.ObservedUTCNS = f.service.p.reviewFloor
				d = f.drafts(t, v)
			case "profile-identity":
				v := f.route
				v.ExpectedVersion, v.Version = 1, 2
				v.ObservedUTCNS = f.service.p.reviewFloor
				v.Profile.InputMax--
				d = f.drafts(t, v)
			case "period-kind-fixed":
				d = f.drafts(t, ledger.RuntimeSpendConfigured{Scope: ledger.PaidScope{Kind: "colony"}, ExpectedVersion: 1, Version: 2, Terms: ledger.SpendTerms{Mode: "unlimited", PeriodKind: "utc_month"}, AuthorityVersion: f.service.p.authority})
			}
			if d == nil {
				d = f.drafts(t, p)
			}
			switch name {
			case "newer-version":
				d[1].TypeVersion = 2
			case "malformed-effect":
				d[1].Payload = []byte(`{}`)
			case "mixed-family":
				d[1].Type = ledger.TypeRuntimeReviewRequested
			case "receipt-version":
				r := scopedDecode(t, ledger.DecodeRuntimePaidReceipt, d[2].Payload)
				r.Version++
				d[2].Payload = paidEncode(t, r)
			}
			f.refuse(t, d)
		})
	}
	t.Run("historical-attempt-and-command-after-eviction", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		f.service.p.paidHotLimit = 8
		first := f.reserve(t)
		f.dispatch(t, first)
		r := f.result(t, first, 10, 2)
		receipt := f.append(t, r)
		for range f.service.p.paidHotLimit + 1 {
			p := f.reserve(t)
			f.append(t, ledger.RuntimePaidResult{AttemptID: p.AttemptID, ExpectedVersion: 1, Version: 2, Kind: "released_unsent"})
		}
		if _, hot := f.service.p.paidAttempts[first.AttemptID]; hot {
			t.Fatal("fixture did not exercise actual bounded-set eviction")
		}
		if got := f.view(t, first.AttemptID); got.SettledMicros != "12" || got.Version != 3 {
			t.Fatalf("historical attempt lost: %+v", got)
		}
		view, err := f.service.PaidCommandStatus(context.Background(), PaidCommandLookup{CommandID: receipt.CommandID, SubmissionSHA256: receipt.SubmissionSHA256})
		if err != nil || view.AttemptID != first.AttemptID || view.Outcome != "settled" {
			t.Fatalf("historical receipt lost: %+v %v", view, err)
		}
		fresh := f.reservation(t)
		fresh.AttemptID = paidGoldenID(0x800)
		f.append(t, fresh)
		copy := f.reservation(t)
		copy.AttemptID = first.AttemptID
		f.refuse(t, f.drafts(t, copy))
	})
	t.Run("historical-credential-version-after-revocation", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		old := f.credential
		newer := old
		newer.ExpectedVersion, newer.Version = 1, 2
		newer.CredentialVersion = f.id()
		newer.RevokePreviousVersion = old.CredentialVersion
		f.append(t, newer)
		fresh := old
		fresh.ExpectedVersion, fresh.Version = 2, 3
		fresh.CredentialVersion = paidGoldenID(0x403)
		f.append(t, fresh)
		old.ExpectedVersion, old.Version = 3, 4
		f.refuse(t, f.drafts(t, old))
	})
	t.Run("unknown-source-is-metadata-not-a-secret", func(t *testing.T) {
		t.Parallel()
		f := newPaidFixture(t)
		v := f.credential
		v.ExpectedVersion, v.Version = 1, 2
		v.CredentialVersion = f.id()
		v.SourceKind = "future_store"
		v.RevokePreviousVersion = f.credential.CredentialVersion
		f.append(t, v)
		report := reopenScoped(t, f.serviceFixture, DefaultLimits())
		if len(report.UnboundCredentials) != 1 || report.UnboundCredentials[0].SourceKind != "future_store" || report.UnboundCredentials[0].SourceLocator != v.SourceLocator {
			t.Fatalf("future source failed inert replay: %+v", report)
		}
	})
}
