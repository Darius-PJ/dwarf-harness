package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"uuid"
)

func reviewReceiptFixture(agent bool) RuntimeReviewReceipt {
	sub := ReviewSubmission{Schema: 3, Actor: "owner", Origin: scopedOriginFixture(), Operation: "review.request", Arguments: ReviewRequestArguments{RequestID: fid("41"), Binding: reviewBindingFixture()}}
	var usage *ReviewUsage
	if agent {
		sub.Actor, sub.GrantID = "agent", fid("11")
		usage = &ReviewUsage{7, sub.GrantID, scopedScopes(), 1, 0}
	}
	return RuntimeReviewReceipt{CommandID: fid("31"), SubmissionSHA256: Digest(sha256.Sum256(mustEncode(sub))), Submission: sub, Outcome: "future_outcome", Version: 1, RequestID: fid("41"), TaskVersion: 3, Usage: usage}
}

func TestF3_12Values(t *testing.T) {
	t.Parallel()
	t.Run("encoder_zero_identity_digests", func(t *testing.T) {
		t.Parallel()
		for _, field := range []string{"acceptance", "check", "config"} {
			binding := reviewBindingFixture()
			switch field {
			case "acceptance":
				binding.AcceptanceSHA256 = Digest{}
			case "check":
				binding.CheckSHA256 = Digest{}
			case "config":
				binding.ConfigSHA256 = Digest{}
			}
			request := reviewRequestFixture()
			request.Binding = binding
			if _, err := request.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("encoder accepted zero %s identity digest: %v", field, err)
			}
		}
	})
	request := reviewRequestFixture()
	failure := request
	failure.Kind, failure.FailureCause, failure.FailingEvidence = "failure", "future_cause", &failure.Binding.RequiredEvidence[0]
	receipt := reviewReceiptFixture(true)
	for _, row := range []struct {
		name, typ        string
		p                payload
		old, replacement string
	}{
		{"nested_duplicate", TypeRuntimeReviewRequested, request, `"binding":{`, `"binding":{"task_version":3,`},
		{"null_optional", TypeRuntimeReviewRequested, request, `"kind":"review"`, `"kind":"review","replaces_request_id":null`},
		{"unknown_kind", TypeRuntimeReviewRequested, request, `"kind":"review"`, `"kind":"future"`},
		{"unknown_reviewer", TypeRuntimeReviewRequested, request, `"actor":"agent"`, `"actor":"future"`},
		{"reviewer_owner_grant", TypeRuntimeReviewRequested, request, `"actor":"agent"`, `"actor":"owner"`},
		{"requester_agent_without_grant", TypeRuntimeReviewRequested, request, `"requester_actor":"owner"`, `"requester_actor":"agent"`},
		{"reviewer_missing_grant", TypeRuntimeReviewRequested, request, `,"grant_id":"` + fid("11").String() + `"`, ``},
		{"replacement_self", TypeRuntimeReviewRequested, request, `"kind":"review"`, `"kind":"review","replaces_request_id":"` + request.RequestID.String() + `"`},
		{"check_zero", TypeRuntimeReviewRequested, request, request.Binding.CheckSHA256.String(), strings.Repeat("0", 64)},
		{"config_zero", TypeRuntimeReviewRequested, request, request.Binding.ConfigSHA256.String(), strings.Repeat("0", 64)},
		{"acceptance_zero", TypeRuntimeReviewRequested, request, request.Binding.AcceptanceSHA256.String(), strings.Repeat("0", 64)},
		{"task_version_zero", TypeRuntimeReviewRequested, request, `"task_version":3`, `"task_version":0`},
		{"task_version_max", TypeRuntimeReviewRequested, request, `"task_version":3`, `"task_version":9223372036854775807`},
		{"generation_zero", TypeRuntimeReviewRequested, request, `"generation":1`, `"generation":0`},
		{"deadline_zero", TypeRuntimeReviewRequested, request, `"deadline_utc_ns":100`, `"deadline_utc_ns":0`},
		{"observed_zero", TypeRuntimeReviewRequested, request, `"observed_utc_ns":90`, `"observed_utc_ns":0`},
		{"review_empty_cause", TypeRuntimeReviewRequested, request, `"kind":"review"`, `"kind":"review","failure_cause":""`},
		{"review_extra_evidence", TypeRuntimeReviewRequested, failure, `"kind":"failure"`, `"kind":"review"`},
		{"failure_missing_cause", TypeRuntimeReviewRequested, failure, `,"failure_cause":"future_cause"`, ``},
		{"unknown_verdict", TypeRuntimeReviewResult, reviewResultFixture("approve"), `"verdict":"approve"`, `"verdict":"future"`},
		{"approve_empty_cause", TypeRuntimeReviewResult, reviewResultFixture("approve"), `"verdict":"approve"`, `"verdict":"approve","cause":""`},
		{"approve_extra_failure", TypeRuntimeReviewResult, reviewResultFixture("fail"), `"verdict":"fail"`, `"verdict":"approve"`},
		{"reject_extra_failure", TypeRuntimeReviewResult, reviewResultFixture("fail"), `"verdict":"fail"`, `"verdict":"reject"`},
		{"fail_missing_cause", TypeRuntimeReviewResult, reviewResultFixture("fail"), `,"cause":"future_cause"`, ``},
		{"fail_missing_evidence", TypeRuntimeReviewResult, reviewResultFixture("fail"), `,"failing_evidence":` + string(mustJSON(request.Binding.Subject)), ``},
		{"reject_advances_task", TypeRuntimeReviewResult, reviewResultFixture("reject"), `"task_version":3,"observed`, `"task_version":4,"observed`},
		{"approve_retains_task", TypeRuntimeReviewResult, reviewResultFixture("approve"), `"task_version":4,"observed`, `"task_version":3,"observed`},
		{"sequence_zero", TypeRuntimeReviewResult, reviewResultFixture("approve"), `"request_sequence":9`, `"request_sequence":0`},
		{"expected_version", TypeRuntimeReviewResult, reviewResultFixture("approve"), `"expected_version":1`, `"expected_version":0`},
		{"result_version", TypeRuntimeReviewResult, reviewResultFixture("approve"), `"version":2`, `"version":3`},
		{"receipt_hash", TypeRuntimeReviewReceipt, receipt, receipt.SubmissionSHA256.String(), scopedHash("wrong").String()},
		{"receipt_usage_null", TypeRuntimeReviewReceipt, receipt, `"usage":{`, `"usage":null,"ignored":{`},
		{"usage_units", TypeRuntimeReviewReceipt, receipt, `"units":1`, `"units":2`},
		{"usage_bytes", TypeRuntimeReviewReceipt, receipt, `"bytes":0}`, `"bytes":1}`},
		{"usage_grant", TypeRuntimeReviewReceipt, receipt, `"authority_version":7,"grant_id":"` + fid("11").String() + `"`, `"authority_version":7,"grant_id":"` + fid("12").String() + `"`},
		{"usage_missing_scope", TypeRuntimeReviewReceipt, receipt, `{"kind":"colony"},`, ``},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wire := string(mustEncode(row.p))
			bad := strings.Replace(wire, row.old, row.replacement, 1)
			if bad == wire {
				t.Fatal("mutation did not apply")
			}
			assertReviewInvalid(t, row.typ, bad)
		})
	}
}

// Each nested member is known: deleting it, setting it to null or coercing an
// integer must not make a partial binding or usage appear authoritative.
func TestF3_12NestedMembers(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, typ string
		p         payload
	}{
		{"request", TypeRuntimeReviewRequested, reviewRequestFixture()},
		{"result", TypeRuntimeReviewResult, reviewResultFixture("fail")},
		{"receipt", TypeRuntimeReviewReceipt, reviewReceiptFixture(true)},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wire := mustEncode(row.p)
			var visit func(json.RawMessage)
			visit = func(raw json.RawMessage) {
				if raw[0] == '[' {
					var entries []json.RawMessage
					if err := json.Unmarshal(raw, &entries); err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						visit(entry)
					}
					return
				}
				if raw[0] != '{' {
					return
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				for key, value := range fields {
					token := `"` + key + `":` + string(value)
					for _, replacement := range []string{`null`, `null,"` + key + `":` + string(value)} {
						bad := bytes.Replace(wire, []byte(token), []byte(`"`+key+`":`+replacement), 1)
						assertReviewInvalid(t, row.typ, string(bad))
					}
					if value[0] >= '0' && value[0] <= '9' {
						for _, badNumber := range []string{"-1", "-0", "1.0", "1e0", "9223372036854775808", `"1"`} {
							bad := bytes.Replace(wire, []byte(token), []byte(`"`+key+`":`+badNumber), 1)
							assertReviewInvalid(t, row.typ, string(bad))
						}
					}
					without := make(map[string]json.RawMessage, len(fields)-1)
					for k, v := range fields {
						if k != key {
							without[k] = v
						}
					}
					missing := bytes.Replace(wire, raw, mustJSON(without), 1)
					assertReviewInvalid(t, row.typ, string(missing))
					visit(value)
				}
			}
			visit(wire)
		})
	}
}

func TestF3_12EvidenceAndOpenTokens(t *testing.T) {
	t.Parallel()
	private := PrivateRevisionRef{Binding: Binding{fid("a1"), fid("d1")}, Scope: "agent", Slot: "instructions", Revision: math.MaxInt64, SourceSequence: math.MaxInt64, SHA256: scopedHash("private"), Bytes: math.MaxInt64}
	for _, subject := range []SubjectRef{
		{Kind: "artifact", Artifact: ArtifactRef{math.MaxInt64, scopedHash("artifact"), math.MaxInt64}},
		{Kind: "private_revision", PrivateRevision: &private},
		{Kind: "future_subject", Members: json.RawMessage(`{"z":{"n":18446744073709551615},"text":"\u003c\u003e\u0026\u2028\u2029","absent":null}`)},
	} {
		t.Run(subject.Kind, func(t *testing.T) {
			t.Parallel()
			p := reviewRequestFixture()
			p.Kind, p.FailureCause = "failure", strings.Repeat("x", 64)
			p.Binding.Subject, p.Binding.RequiredEvidence = subject, []SubjectRef{subject}
			p.FailingEvidence = &p.Binding.RequiredEvidence[0]
			p.Binding.TaskVersion, p.Binding.Generation, p.Binding.AuthorizationRevision, p.Binding.DeadlineUTCNS, p.ObservedUTCNS = math.MaxInt64-1, math.MaxInt64, math.MaxInt64, 1, math.MaxInt64
			wire := mustEncode(p)
			got, err := DecodeRuntimeReviewRequested(wire)
			if err != nil || !reflect.DeepEqual(got, p) {
				t.Fatalf("open/boundary value changed: %#v %v", got, err)
			}
			p.Binding.RequiredEvidence = append(p.Binding.RequiredEvidence, subject)
			if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("duplicate evidence encoded")
			}
		})
	}
	p := reviewRequestFixture()
	second := p.Binding.Subject
	second.Artifact.SourceSequence = 5
	p.Binding.RequiredEvidence = []SubjectRef{second, p.Binding.Subject}
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("unordered evidence encoded")
	}
	p.Binding.RequiredEvidence = nil
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty evidence encoded")
	}
	p = reviewRequestFixture()
	p.Kind, p.FailureCause, p.FailingEvidence = "failure", "future_cause", &second
	if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("equal hash with a different occurrence used as failing evidence")
	}
	for _, token := range []string{"", "Bad", "two.words", "x-y", strings.Repeat("x", 65)} {
		p.FailingEvidence = &p.Binding.RequiredEvidence[0]
		p.FailureCause = token
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid cause %q encoded", token)
		}
	}
	for _, agent := range []bool{false, true} {
		p := reviewReceiptFixture(agent)
		wire := mustEncode(p)
		got, err := DecodeRuntimeReviewReceipt(wire)
		if err != nil || !reflect.DeepEqual(got, p) {
			t.Fatalf("unknown outcome lost: %#v %v", got, err)
		}
		if agent {
			p.Usage = nil
		} else {
			p.Usage = &ReviewUsage{7, fid("11"), scopedScopes(), 1, 0}
		}
		if _, err := p.Encode(); !errors.Is(err, ErrInvalid) {
			t.Fatal("actor/usage mismatch encoded")
		}
	}
}

func TestF3_12OutputCap(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{TypeRuntimeReviewRequested, TypeRuntimeReviewResult, TypeRuntimeReviewReceipt, "schema-3"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			makePayload := func(n int) payload {
				b := reviewBindingFixture()
				b.Subject = SubjectRef{Kind: "future_subject", Members: json.RawMessage(`{"text":"` + strings.Repeat("x", n) + `"}`)}
				s := ReviewSubmission{Schema: 3, Actor: "owner", Origin: scopedOriginFixture(), Operation: "review.request", Arguments: ReviewRequestArguments{RequestID: fid("41"), Binding: b}}
				switch typ {
				case TypeRuntimeReviewRequested:
					p := reviewRequestFixture()
					p.Binding = b
					return p
				case TypeRuntimeReviewResult:
					p := reviewResultFixture("approve")
					p.Binding = b
					return p
				case TypeRuntimeReviewReceipt:
					p := reviewReceiptFixture(false)
					p.Submission = s
					p.SubmissionSHA256 = Digest(sha256.Sum256(mustEncode(s)))
					return p
				default:
					return s
				}
			}
			base := len(mustEncode(makePayload(0)))
			p := makePayload(65536 - base)
			wire, err := p.Encode()
			if err != nil || len(wire) != 65536 {
				t.Fatalf("boundary encode = %d,%v", len(wire), err)
			}
			if _, err := decodeF3_12(typ, wire); err != nil {
				t.Fatal(err)
			}
			if _, err := makePayload(65537 - base).Encode(); !errors.Is(err, ErrInvalid) {
				t.Fatal("oversized output encoded")
			}
			if typ != TypeRuntimeReviewReceipt {
				// A smaller input can still overflow after required HTML escaping.
				expanding := bytes.Replace(wire, []byte(strings.Repeat("x", 65536-base)), []byte(strings.Repeat("<", 65536-base)), 1)
				assertReviewInvalid(t, typ, string(expanding))
			}
		})
	}
}

func testReviewProducerMaximum(t *testing.T) {
	t.Parallel()
	p := reviewRequestFixture()
	p.Kind, p.RequesterActor, p.RequesterGrantID, p.ReplacesRequestID = "failure", "agent", fid("11"), fid("40")
	p.FailureCause = strings.Repeat("x", 64)
	b := &p.Binding
	b.TaskVersion, b.Generation, b.AuthorizationRevision, b.DeadlineUTCNS, p.ObservedUTCNS = math.MaxInt64-1, math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64
	b.RequiredEvidence = nil
	// Sixteen is the producer guard, not a decoder ceiling. Private references
	// are the largest supported references; every quantity uses its widest form.
	for i := range 17 {
		r := PrivateRevisionRef{Binding: b.Assignee, Scope: "context", Slot: "continuation", Revision: math.MaxInt64, SourceSequence: math.MaxInt64, SHA256: scopedHash(fmt.Sprintf("evidence-%d", i)), Bytes: 4 << 20}
		b.RequiredEvidence = append(b.RequiredEvidence, SubjectRef{Kind: "private_revision", PrivateRevision: &r})
	}
	slices.SortFunc(b.RequiredEvidence, func(a, b SubjectRef) int { return bytes.Compare(mustJSON(a), mustJSON(b)) })
	b.Subject = b.RequiredEvidence[0]
	p.FailingEvidence = &b.RequiredEvidence[0]
	if _, err := DecodeRuntimeReviewRequested(mustEncode(p)); err != nil {
		t.Fatalf("producer count became decoder ceiling: %v", err)
	}
	b.RequiredEvidence = b.RequiredEvidence[:16]
	s := ReviewSubmission{Schema: 3, Actor: p.RequesterActor, Origin: p.Requester, Operation: "failure.request", GrantID: p.RequesterGrantID, Arguments: ReviewRequestArguments{p.RequestID, p.Binding, p.ReplacesRequestID, p.FailureCause, p.FailingEvidence}}
	r := RuntimeReviewReceipt{CommandID: fid("31"), SubmissionSHA256: Digest(sha256.Sum256(mustEncode(s))), Submission: s, Outcome: "requested", Version: 1, RequestID: p.RequestID, TaskVersion: b.TaskVersion, Usage: &ReviewUsage{math.MaxInt64, s.GrantID, scopedScopes(), 1, 0}}
	for i := range 8 {
		r.Usage.Scopes = append(r.Usage.Scopes, ScopeKey{Kind: "grant", ID: fid(fmt.Sprintf("%x", 0x20+i))})
	}
	for _, row := range []struct {
		name, typ string
		p         payload
	}{{"request", TypeRuntimeReviewRequested, p}, {"receipt", TypeRuntimeReviewReceipt, r}} {
		wire, err := row.p.Encode()
		if err != nil || len(wire) > 65536 {
			t.Fatalf("producer maximum %s: %d bytes %v", row.name, len(wire), err)
		}
		if _, err := decodeF3_12(row.typ, wire); err != nil {
			t.Fatal(err)
		}
		t.Logf("producer maximum %s: %d bytes", row.name, len(wire))
	}
}

func TestF3_12SubmissionConditions(t *testing.T) {
	t.Parallel()
	for _, fixture := range f3_12Payloads() {
		if fixture.typ != "schema-3" {
			continue
		}
		s := fixture.p.(ReviewSubmission)
		for _, row := range []struct {
			name   string
			change func(*ReviewSubmission)
		}{
			{"schema", func(p *ReviewSubmission) { p.Schema = 2 }},
			{"operation", func(p *ReviewSubmission) { p.Operation = "future.resolve" }},
			{"actor", func(p *ReviewSubmission) { p.Actor = "core" }},
			{"owner_grant", func(p *ReviewSubmission) { p.GrantID = fid("11") }},
			{"agent_missing_grant", func(p *ReviewSubmission) { p.Actor = "agent" }},
			{"map_arguments", func(p *ReviewSubmission) { p.Arguments = map[string]any{"request_id": fid("41")} }},
		} {
			t.Run(fixture.name+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				bad := s
				row.change(&bad)
				if _, err := bad.Encode(); !errors.Is(err, ErrInvalid) {
					t.Fatal("invalid submission encoded")
				}
			})
		}
		agentSub := s
		agentSub.Actor, agentSub.GrantID = "agent", fid("11")
		wire := mustEncode(agentSub)
		got, err := DecodeReviewSubmission(wire)
		if err != nil || !reflect.DeepEqual(got, agentSub) {
			t.Fatalf("agent submission changed: %#v %v", got, err)
		}
	}
	s := ReviewSubmission{Schema: 3, Actor: "owner", Origin: scopedOriginFixture(), Operation: "review.resolve", Arguments: ReviewResolveArguments{fid("41"), 9, reviewBindingFixture(), "fail"}}
	if _, err := s.Encode(); !errors.Is(err, ErrInvalid) {
		t.Fatal("review.resolve accepted fail")
	}
	// UUIDv7 grammar remains canonical rather than silently normalizing input.
	wire := string(mustEncode(reviewRequestFixture()))
	for _, id := range []string{uuid.UUID{}.String(), strings.ToUpper(fid("a1").String()), "0199c000-0000-4000-8000-0000000000a1"} {
		assertReviewInvalid(t, TypeRuntimeReviewRequested, strings.Replace(wire, fid("a1").String(), id, 1))
	}
}
