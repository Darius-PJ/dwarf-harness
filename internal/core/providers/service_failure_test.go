package providers

import (
	"crypto/sha256"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// serviceFailureSpec binds a diagnosis to a sealed fixture scope exactly as the
// agents host does. The frozen codec rejects any wrong field presence.
func serviceFailureSpec(f ServiceFailure) ledger.ServiceCauseSpec {
	spec := ledger.ServiceCauseSpec{Family: "provider", Code: f.Code, ScopeKind: f.ScopeKind, Provider: "anthropic"}
	if f.CredentialScope {
		spec.CredentialScopeID = uuid.MustParse("019a0000-0000-7000-8000-000000000001")
	}
	if f.CredentialVersion {
		spec.CredentialVersion = uuid.MustParse("019a0000-0000-7000-8000-000000000002")
	}
	if f.Model {
		spec.Model = "fixture-model"
	}
	if f.Region {
		spec.Region = new("")
	}
	if f.Authority {
		spec.AuthoritySHA256 = ledger.Digest(sha256.Sum256([]byte("api.fixture.invalid")))
	}
	return spec
}

func TestServiceFailureDiagnosis(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, class, provenance, code string
		status                        int
	}{
		{"credential_denied", "denied", "provider_response", "credential_denied", 401},
		{"scoped_denied", "denied", "provider_response", "scoped_denied", 403},
		{"quota", "exhausted", "provider_response", "quota_exhausted", 402},
		{"in_stream_quota", "exhausted", "provider_response", "quota_exhausted", 200},
		{"rate_limited", "rate_limited", "provider_response", "rate_limited", 429},
		{"overloaded", "overloaded", "provider_response", "overloaded", 529},
		{"in_stream_overload", "overloaded", "provider_response", "overloaded", 200},
		{"transport_unreachable", "unreachable", "transport", "unreachable", 0},
		{"transport_timeout", "timeout", "transport", "unreachable", 200},
		{"error_body_read_failure", "unreachable", "provider_response", "", 429},
		{"gateway_timeout", "timeout", "provider_response", "", 504},
		{"provider_error", "provider_error", "provider_response", "", 500},
		{"invalid_request", "invalid_request", "provider_response", "", 400},
		{"unavailable", "unavailable", "provider_response", "", 404},
		{"conflict", "conflict", "provider_response", "", 409},
		{"protocol", "protocol_error", "provider_response", "", 200},
		{"refused", "refused", "provider_response", "", 200},
		{"tariff", "tariff_ambiguous", "provider_response", "", 200},
		{"denied_other_status", "denied", "provider_response", "", 200},
		{"cancelled_or_capture_refused", "unreachable", "", "", 0},
		{"rate_limited_without_response", "rate_limited", "transport", "", 429},
		{"unknown_provenance", "denied", "caller_cancel", "", 401},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, ok := DiagnoseServiceFailure(row.class, row.status, row.provenance)
			if ok != (row.code != "") || f.Code != row.code {
				t.Fatalf("diagnosis = %+v %v, want %q", f, ok, row.code)
			}
			if ok {
				if _, err := serviceFailureSpec(f).Key(); err != nil {
					t.Fatalf("diagnosis scope is not a frozen cause: %v", err)
				}
			}
		})
	}
}

func TestServiceFailureReset(t *testing.T) {
	t.Parallel()
	// 2026-11-21T12:00:00Z, and HTTP-dates 90 s after and 1 s before it.
	const second = int64(1_000_000_000)
	observed := 1795262400 * second
	future, past := "Sat, 21 Nov 2026 12:01:30 GMT", "Sat, 21 Nov 2026 11:59:59 GMT"
	for _, row := range []struct {
		name, code string
		values     []string
		want       int64
	}{
		{"delta", "rate_limited", []string{"30"}, observed + 30*second},
		{"date", "quota_exhausted", []string{future}, observed + 90*second},
		{"zero_delta", "rate_limited", []string{"0"}, 0},
		{"negative", "rate_limited", []string{"-3"}, 0},
		{"fraction", "rate_limited", []string{"1.5"}, 0},
		{"signed", "rate_limited", []string{"+3"}, 0},
		{"repeated", "rate_limited", []string{"30", "30"}, 0},
		{"absent", "quota_exhausted", nil, 0},
		{"past_date", "quota_exhausted", []string{past}, 0},
		{"malformed", "quota_exhausted", []string{"soon"}, 0},
		{"overflow", "rate_limited", []string{"9223372036854775807"}, 0},
		{"overload_never_states_reset", "overloaded", []string{"30"}, 0},
		{"unreachable_never_states_reset", "unreachable", []string{"30"}, 0},
		{"denied_never_states_reset", "credential_denied", []string{"30"}, 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got, ok := StatedServiceReset(row.code, row.values, observed)
			if ok != (row.want != 0) || got != row.want {
				t.Fatalf("reset = %d %v, want %d", got, ok, row.want)
			}
		})
	}
}

func FuzzServiceFailure(f *testing.F) {
	f.Add("denied", 401, "provider_response", "30", int64(1))
	f.Add("exhausted", 429, "provider_response", "Sat, 21 Nov 2026 12:00:00 GMT", int64(1790000000000000000))
	f.Add("timeout", 0, "transport", "", int64(1790000000000000000))
	f.Add("overloaded", 200, "provider_response", "1", int64(9223372036854775806))
	f.Fuzz(func(t *testing.T, class string, status int, provenance, retryAfter string, observed int64) {
		failure, ok := DiagnoseServiceFailure(class, status, provenance)
		if !ok {
			if failure != (ServiceFailure{}) {
				t.Fatalf("refused diagnosis carried a scope: %+v", failure)
			}
		} else {
			if _, err := serviceFailureSpec(failure).Key(); err != nil {
				t.Fatalf("diagnosis is not a frozen cause: %+v %v", failure, err)
			}
			if (provenance == "transport") != (failure.Code == "unreachable") || failure.Code == "credential_denied" && status != 401 || failure.Code == "scoped_denied" && status != 403 {
				t.Fatalf("diagnosis outside its evidence: %q %d %q -> %+v", class, status, provenance, failure)
			}
		}
		reset, stated := StatedServiceReset(failure.Code, []string{retryAfter}, observed)
		if stated && (!ok || reset <= observed || failure.Code != "quota_exhausted" && failure.Code != "rate_limited") {
			t.Fatalf("invented reset %d for %+v at %d", reset, failure, observed)
		}
		if !stated && reset != 0 {
			t.Fatalf("refused reset returned a value: %d", reset)
		}
	})
}
