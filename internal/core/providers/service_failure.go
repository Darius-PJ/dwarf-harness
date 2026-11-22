package providers

import (
	"net/http"
	"strconv"
	"time"
)

// ServiceFailure is a nonsecret service-loss diagnosis of one captured failure.
// The scope flags name which sealed request qualifiers bind the cause; the
// caller supplies their values from the sealed request, never from a response.
type ServiceFailure struct {
	Code, ScopeKind                                              string
	CredentialScope, CredentialVersion, Model, Region, Authority bool
}

// DiagnoseServiceFailure maps an existing classifier label and the dispatch
// provenance token to the evidence-bounded service causes. provenance is
// "provider_response" for a response that was read, "transport" for a
// transport or read network failure with an uncancelled exchange, and empty
// otherwise. Every other combination establishes nothing.
func DiagnoseServiceFailure(errorClass string, status int, provenance string) (ServiceFailure, bool) {
	switch provenance {
	case "transport":
		if errorClass == "timeout" || errorClass == "unreachable" {
			return ServiceFailure{Code: "unreachable", ScopeKind: "endpoint", Authority: true}, true
		}
	case "provider_response":
		switch {
		case errorClass == "denied" && status == 401:
			return ServiceFailure{Code: "credential_denied", ScopeKind: "credential", CredentialScope: true, CredentialVersion: true}, true
		case errorClass == "denied" && status == 403:
			return ServiceFailure{Code: "scoped_denied", ScopeKind: "account_route", CredentialScope: true, CredentialVersion: true, Model: true, Region: true, Authority: true}, true
		case errorClass == "exhausted":
			return ServiceFailure{Code: "quota_exhausted", ScopeKind: "account", CredentialScope: true, Authority: true}, true
		case errorClass == "rate_limited":
			return ServiceFailure{Code: "rate_limited", ScopeKind: "account_route", CredentialScope: true, Model: true, Region: true, Authority: true}, true
		case errorClass == "overloaded":
			return ServiceFailure{Code: "overloaded", ScopeKind: "provider_route", Model: true, Region: true, Authority: true}, true
		}
	}
	return ServiceFailure{}, false
}

// StatedServiceReset accepts exactly one retry-after value: positive plain
// delta seconds or an HTTP date, strictly after observedUTCNS. Only quota and
// rate-limit evidence can state a reset. Anything else, including repeated,
// malformed, past or overflowing values, is no stated reset.
func StatedServiceReset(code string, retryAfter []string, observedUTCNS int64) (int64, bool) {
	const maxInt64 = 1<<63 - 1
	if code != "quota_exhausted" && code != "rate_limited" || len(retryAfter) != 1 || observedUTCNS <= 0 {
		return 0, false
	}
	value := retryAfter[0]
	digits := value != ""
	for i := range len(value) {
		digits = digits && value[i] >= '0' && value[i] <= '9'
	}
	if digits {
		seconds, err := strconv.ParseInt(value, 10, 64)
		if err != nil || seconds <= 0 || seconds > (maxInt64-observedUTCNS)/int64(time.Second) {
			return 0, false
		}
		return observedUTCNS + seconds*int64(time.Second), true
	}
	at, err := http.ParseTime(value)
	if err != nil || at.Year() < 1678 || at.Year() > 2261 {
		return 0, false
	}
	reset := at.UnixNano()
	if reset <= observedUTCNS {
		return 0, false
	}
	return reset, true
}
