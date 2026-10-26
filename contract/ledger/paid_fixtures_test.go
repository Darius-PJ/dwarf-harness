package ledger

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"uuid"
)

func paidReservationFixture() RuntimePaidReserved {
	profile := paidProfileFixture()
	profileHash, err := profile.SHA256()
	if err != nil {
		panic(err)
	}
	tariffHash, err := profile.TariffSHA256()
	if err != nil {
		panic(err)
	}
	object := ObjectRef{scopedHash("sealed input"), 12}
	request := SealedDescriptor{Provider: "anthropic", Authority: profile.Authority, Model: profile.ModelID, Region: profile.Region, Method: "POST", Path: profile.Path, RequestedTier: profile.RequestedTier, Credential: CredentialRef{fid("52"), fid("53")}, Headers: []SemanticHeader{{"accept", "text/event-stream"}, {"anthropic-version", profile.APIVersion}, {"content-type", "application/json"}}, BodySHA256: object.SHA256, BodyBytes: object.Bytes}
	quote := CostBound{Currency: "USD", MaximumMicros: 120, Components: []PricedDimension{{[]string{"input.uncached"}, 100, "1", "1"}, {[]string{"output.generated"}, 20, "1", "1"}}, RequestSHA256: scopedHash("request framing"), CapabilityVersion: profile.CapabilityVersion, TariffVersion: profile.TariffVersion, TariffSHA256: tariffHash, QuoteSHA256: scopedHash("quote framing")}
	return RuntimePaidReserved{AttemptID: fid("61"), Version: 1, GrantID: fid("54"), ParentTurnID: fid("55"), RouteID: fid("51"), RouteVersion: 1, ProfileSHA256: profileHash, Request: request, Quote: quote, AdmissionUTCNS: 110, AdmissionPeriod: "lifetime", Scopes: []PaidScope{{Kind: "colony"}, {"agent", fid("a1")}, {"credential", fid("52")}, {"grant", fid("54")}}, InputObject: object}
}

func paidAdmitArguments(p RuntimePaidReserved) PaidAdmitArguments {
	return PaidAdmitArguments{p.AttemptID, p.GrantID, p.ParentTurnID, p.TaskBinding, p.RouteID, p.RouteVersion, p.ProfileSHA256, p.Request, p.Quote, p.InputObject}
}

func paidSubmissionFixtures() []PaidSubmission {
	route, reserve, spend := paidRouteFixture(), paidReservationFixture(), paidSpendFixture()
	profileHash, _ := route.Profile.SHA256()
	usage, correction := paidResultFixture("usage"), paidResultFixture("correction")
	rows := []struct {
		operation string
		arguments any
	}{
		{"paid.route.configure", PaidRouteArguments{route.RouteID, 0, true, route.CredentialScopeID, profileHash}},
		{"paid.credential.bind", PaidCredentialBindArguments{ScopeID: fid("52"), Provider: "anthropic", CredentialVersion: fid("53"), SourceKind: "synthetic_source", SourceLocator: "synthetic-only:not-a-variable-or-target"}},
		{"paid.credential.revoke", PaidCredentialRevokeArguments{fid("52"), 1, "anthropic", fid("53")}},
		{"paid.spend.configure", PaidSpendArguments{Scope: spend.Scope, Terms: spend.Terms}},
		{"paid.spend.delegate", PaidSpendArguments{Scope: PaidScope{"grant", fid("56")}, Terms: spend.Terms, ParentGrantID: fid("54")}},
		{"paid.admit", paidAdmitArguments(reserve)},
		{"paid.dispatch", PaidAttemptArguments{fid("61"), 1}},
		{"paid.observe", PaidObserveArguments{usage.AttemptID, usage.ExpectedVersion, usage.Kind, usage.EvidenceSHA256, *usage.EvidenceObject, usage.MessageObjects}},
		{"paid.release", PaidAttemptArguments{fid("61"), 1}},
		{"paid.correct", PaidCorrectArguments{correction.AttemptID, correction.ExpectedVersion, correction.PreviousResultSequence, correction.PreviousEvidenceSHA256, correction.EvidenceSHA256, *correction.EvidenceObject, correction.MessageObjects}},
		{"paid.overrun.acknowledge", PaidOverrunArguments{fid("61"), 3, 10, usage.EvidenceSHA256, profileHash}},
	}
	var out []PaidSubmission
	for _, row := range rows {
		s := PaidSubmission{Schema: 4, Actor: "owner", Origin: scopedOriginFixture(), Operation: row.operation, Arguments: row.arguments}
		switch row.operation {
		case "paid.admit", "paid.spend.delegate":
			s.Actor, s.GrantID = "agent", reserve.GrantID
		case "paid.dispatch", "paid.observe", "paid.release", "paid.correct":
			s.Actor, s.GrantID = "core", reserve.GrantID
		}
		out = append(out, s)
	}
	return out
}

func f3_13Payloads() []scopedFixture {
	r := paidReservationFixture()
	rows := []scopedFixture{
		{"route", TypeRuntimePaidRouteConfigured, paidRouteFixture(), ""},
		{"bind", TypeRuntimeCredentialChanged, RuntimeCredentialChanged{ScopeID: fid("52"), Provider: "anthropic", Version: 1, Action: "bind", CredentialVersion: fid("53"), SourceKind: "synthetic_source", SourceLocator: "synthetic-only:not-a-variable-or-target"}, ""},
		{"revoke", TypeRuntimeCredentialChanged, RuntimeCredentialChanged{ScopeID: fid("52"), Provider: "anthropic", ExpectedVersion: 1, Version: 2, Action: "revoke", CredentialVersion: fid("53")}, ""},
		{"spend", TypeRuntimeSpendConfigured, paidSpendFixture(), ""},
		{"reserve", TypeRuntimePaidReserved, r, ""},
		{"intent", TypeRuntimePaidDispatchIntent, RuntimePaidDispatchIntent{r.AttemptID, 1, 2, 0, r.Quote.RequestSHA256, r.Quote.QuoteSHA256, r.Request.Credential, 111}, ""},
		{"ack", TypeRuntimePaidOverrunAcknowledged, RuntimePaidOverrunAcknowledged{r.AttemptID, 3, 4, 10, scopedHash("paid observation"), r.ProfileSHA256}, ""},
	}
	for _, kind := range []string{"released_unsent", "unknown", "usage", "no_charge", "correction"} {
		rows = append(rows, scopedFixture{kind, TypeRuntimePaidResult, paidResultFixture(kind), ""})
	}
	for _, s := range paidSubmissionFixtures() {
		rows = append(rows, scopedFixture{s.Operation, "schema-4", s, ""})
		r := RuntimePaidReceipt{CommandID: fid("71"), Submission: s, SubmissionSHA256: Digest(sha256.Sum256(mustEncode(s))), Outcome: "future_outcome", Version: 1}
		if paidAttemptOperation(s.Operation) {
			r.AttemptID = fid("61")
		}
		if s.Operation == "paid.spend.delegate" {
			r.Usage = &PaidLocalUsage{GrantID: s.GrantID, Scopes: []ScopeKey{{Kind: "colony"}, {"agent", fid("a1")}, {"context", fid("d1")}, {"grant", s.GrantID}}, Units: 1}
		}
		rows = append(rows, scopedFixture{"receipt_" + s.Operation, TypeRuntimePaidReceipt, r, ""})
	}
	return rows
}

// Key-order assertions are independent of the struct and golden constructors.
// A reordered member changes the digest contract even with equivalent values.
func assertPaidOrder(t *testing.T, js []byte, want string) {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(string(js)))
	if _, err := d.Token(); err != nil {
		t.Fatal(err)
	}
	var got []string
	for d.More() {
		token, err := d.Token()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, token.(string))
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(got, ",") != want {
		t.Fatalf("key order = %s; want %s", strings.Join(got, ","), want)
	}
}

func TestF3_13KeyOrder(t *testing.T) {
	t.Parallel()
	orders := map[string]string{
		"route":           "route_id,expected_version,version,enabled,credential_scope_id,profile,observed_utc_ns",
		"bind":            "scope_id,provider,expected_version,version,action,credential_version,source_kind,source_locator",
		"revoke":          "scope_id,provider,expected_version,version,action,credential_version",
		"spend":           "scope,expected_version,version,terms,authority_version",
		"reserve":         "attempt_id,version,authority_version,grant_id,parent_turn_id,route_id,route_version,profile_sha256,request,quote,admission_utc_ns,admission_period,scopes,input_object",
		"intent":          "attempt_id,expected_version,version,authority_version,request_sha256,quote_sha256,credential,observed_utc_ns",
		"ack":             "attempt_id,expected_version,version,result_sequence,evidence_sha256,retired_profile_sha256",
		"released_unsent": "attempt_id,expected_version,version,kind,bound_violation",
		"unknown":         "attempt_id,expected_version,version,kind,output_started,evidence_sha256,evidence_object,bound_violation,error_class",
		"usage":           "attempt_id,expected_version,version,kind,output_started,provider_message_id,evidence_sha256,evidence_object,message_objects,usage,cost_micros,bound_violation",
		"no_charge":       "attempt_id,expected_version,version,kind,output_started,provider_message_id,evidence_sha256,evidence_object,no_charge,cost_micros,bound_violation",
		"correction":      "attempt_id,expected_version,version,kind,output_started,provider_message_id,evidence_sha256,evidence_object,message_objects,usage,cost_micros,bound_violation,previous_result_sequence,previous_evidence_sha256",
	}
	for _, f := range f3_13Payloads() {
		order := orders[f.name]
		if f.typ == "schema-4" {
			s := f.p.(PaidSubmission)
			order = "schema,actor,origin,operation,"
			if s.GrantID != (uuid.UUID{}) {
				order += "grant_id,"
			}
			order += "arguments"
		}
		if f.typ == TypeRuntimePaidReceipt {
			r := f.p.(RuntimePaidReceipt)
			order = "command_id,submission_sha256,submission,outcome,version"
			if r.AttemptID != (uuid.UUID{}) {
				order += ",attempt_id"
			}
			if r.Usage != nil {
				order += ",usage"
			}
		}
		t.Run(f.name, func(t *testing.T) { t.Parallel(); assertPaidOrder(t, mustEncode(f.p), order) })
	}
	r := paidReservationFixture()
	for _, row := range []struct {
		value any
		order string
	}{
		{paidProfileFixture(), "schema,capability_version,tariff_version,provider,authority,method,path,api_version,model_id,region,requested_tier,allowed_served_tiers,speed,context_max,input_max,output_max,usage_schema,cache_classes,tool_choices,no_charge_rules,tariff,verified_utc_ns,expires_utc_ns,sources"},
		{r.Request, "provider,authority,model,region,method,path,requested_tier,credential,headers,body_sha256,body_bytes"},
		{r.Quote, "currency,maximum_micros,components,request_sha256,capability_version,tariff_version,tariff_sha256,quote_sha256"},
		{r.Quote.Components[0], "classes,max_units,rate_numerator,rate_denominator"},
		{paidSpendFixture().Terms, "enabled,mode,ceiling_micros,period_kind,warning_ppm"},
		{paidResultFixture("no_charge").NoCharge, "rule,stop_reason,stop_details_type,category_kind,complete,content_blocks,output_started"},
	} {
		t.Run(fmt.Sprintf("%T", row.value), func(t *testing.T) { t.Parallel(); assertPaidOrder(t, mustJSON(row.value), row.order) })
	}
}
