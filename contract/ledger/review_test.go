package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func reviewBindingFixture() ReviewBinding {
	subject := SubjectRef{Kind: "artifact", Artifact: ArtifactRef{4, scopedHash("artifact"), 8}}
	return ReviewBinding{TaskID: fid("21"), TaskVersion: 3, AssignmentID: fid("22"), Generation: 1, Assignee: Binding{fid("a1"), fid("d1")}, Reviewer: ReviewPrincipal{Actor: "agent", Origin: scopedOriginFixture(), GrantID: fid("11")}, Subject: subject, AcceptanceSHA256: scopedHash("acceptance"), CheckSHA256: scopedHash("check"), ConfigSHA256: scopedHash("config"), RequiredEvidence: []SubjectRef{subject}, AuthorizationRevision: 7, DeadlineUTCNS: 100}
}

func reviewRequestFixture() RuntimeReviewRequested {
	return RuntimeReviewRequested{RequestID: fid("41"), Kind: "review", Version: 1, RequesterActor: "owner", Requester: scopedOriginFixture(), Binding: reviewBindingFixture(), ObservedUTCNS: 90}
}

func reviewResultFixture(verdict string) RuntimeReviewResult {
	p := RuntimeReviewResult{RequestID: fid("41"), RequestSequence: 9, ExpectedVersion: 1, Version: 2, Binding: reviewBindingFixture(), Verdict: verdict, TaskVersion: 4, ObservedUTCNS: 100}
	if verdict == "reject" {
		p.TaskVersion = 3
	}
	if verdict == "fail" {
		p.Cause, p.FailingEvidence = "future_cause", &p.Binding.RequiredEvidence[0]
	}
	return p
}

func decodeF3_12(typ string, js json.RawMessage) (payload, error) {
	switch typ {
	case TypeRuntimeReviewRequested:
		return DecodeRuntimeReviewRequested(js)
	case TypeRuntimeReviewResult:
		return DecodeRuntimeReviewResult(js)
	case TypeRuntimeReviewReceipt:
		return DecodeRuntimeReviewReceipt(js)
	case "schema-3":
		return DecodeReviewSubmission(js)
	}
	panic(typ)
}

func f3_12Payloads() []scopedFixture {
	id := func(s string) string { return `"` + fid(s).String() + `"` }
	origin := `{"agent_id":` + id("a1") + `,"session_id":` + id("b1") + `,"context_id":` + id("d1") + `}`
	subject := `{"kind":"artifact","artifact":{"source_sequence":4,"sha256":"` + scopedHash("artifact").String() + `","bytes":8}}`
	binding := `{"task_id":` + id("21") + `,"task_version":3,"assignment_id":` + id("22") + `,"generation":1,"assignee":{"agent_id":` + id("a1") + `,"context_id":` + id("d1") + `},"reviewer":{"actor":"agent","origin":` + origin + `,"grant_id":` + id("11") + `},"subject":` + subject + `,"acceptance_sha256":"` + scopedHash("acceptance").String() + `","check_sha256":"` + scopedHash("check").String() + `","config_sha256":"` + scopedHash("config").String() + `","required_evidence":[` + subject + `],"authorization_revision":7,"deadline_utc_ns":100}`
	request := reviewRequestFixture()
	failure := reviewRequestFixture()
	failure.Kind, failure.RequesterActor, failure.RequesterGrantID, failure.ReplacesRequestID = "failure", "agent", fid("11"), fid("40")
	failure.FailureCause, failure.FailingEvidence = "future_cause", &failure.Binding.RequiredEvidence[0]
	fixtures := []scopedFixture{
		{"request", TypeRuntimeReviewRequested, request, `{"request_id":` + id("41") + `,"kind":"review","version":1,"requester_actor":"owner","requester":` + origin + `,"binding":` + binding + `,"observed_utc_ns":90}`},
		{"failure_request", TypeRuntimeReviewRequested, failure, `{"request_id":` + id("41") + `,"kind":"failure","version":1,"requester_actor":"agent","requester":` + origin + `,"requester_grant_id":` + id("11") + `,"binding":` + binding + `,"replaces_request_id":` + id("40") + `,"failure_cause":"future_cause","failing_evidence":` + subject + `,"observed_utc_ns":90}`},
	}
	for _, verdict := range []string{"approve", "reject", "fail"} {
		p := reviewResultFixture(verdict)
		optional := ""
		if verdict == "fail" {
			optional = `,"cause":"future_cause","failing_evidence":` + subject
		}
		wire := `{"request_id":` + id("41") + `,"request_sequence":9,"expected_version":1,"version":2,"binding":` + binding + `,"verdict":"` + verdict + `"` + optional + fmt.Sprintf(`,"task_version":%d,"observed_utc_ns":100}`, p.TaskVersion)
		fixtures = append(fixtures, scopedFixture{verdict, TypeRuntimeReviewResult, p, wire})
	}
	for _, operation := range []string{"review.request", "failure.request", "review.resolve", "failure.resolve"} {
		sub := ReviewSubmission{Schema: 3, Actor: "owner", Origin: scopedOriginFixture(), Operation: operation}
		args := `{"request_id":` + id("41")
		switch operation {
		case "review.request", "failure.request":
			a := ReviewRequestArguments{RequestID: fid("41"), Binding: reviewBindingFixture()}
			args += `,"binding":` + binding
			if operation == "failure.request" {
				a.FailureCause, a.FailingEvidence = "future_cause", &a.Binding.RequiredEvidence[0]
				args += `,"failure_cause":"future_cause","failing_evidence":` + subject
			}
			sub.Arguments = a
		case "review.resolve":
			sub.Arguments = ReviewResolveArguments{fid("41"), 9, reviewBindingFixture(), "approve"}
			args += `,"request_sequence":9,"binding":` + binding + `,"verdict":"approve"`
		case "failure.resolve":
			a := FailureResolveArguments{RequestID: fid("41"), RequestSequence: 9, Binding: reviewBindingFixture(), Cause: "future_cause"}
			a.FailingEvidence = a.Binding.RequiredEvidence[0]
			sub.Arguments = a
			args += `,"request_sequence":9,"binding":` + binding + `,"cause":"future_cause","failing_evidence":` + subject
		}
		args += `}`
		wire := `{"schema":3,"actor":"owner","origin":` + origin + `,"operation":"` + operation + `","arguments":` + args + `}`
		fixtures = append(fixtures, scopedFixture{operation, "schema-3", sub, wire})
		if operation == "review.request" {
			receipt := RuntimeReviewReceipt{CommandID: fid("31"), SubmissionSHA256: Digest(sha256.Sum256([]byte(wire))), Submission: sub, Outcome: "requested", Version: 1, RequestID: fid("41"), TaskVersion: 3}
			fixtures = append(fixtures, scopedFixture{"receipt", TypeRuntimeReviewReceipt, receipt, `{"command_id":` + id("31") + `,"submission_sha256":"` + receipt.SubmissionSHA256.String() + `","submission":` + wire + `,"outcome":"requested","version":1,"request_id":` + id("41") + `,"task_version":3}`})
		}
	}
	return fixtures
}

func assertReviewInvalid(t *testing.T, typ, wire string) {
	t.Helper()
	if _, err := decodeF3_12(typ, []byte(wire)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("%s accepted invalid payload: %v", typ, err)
	}
}

func TestF3_12Payloads(t *testing.T) {
	t.Parallel()
	t.Run("producer_maximum", testReviewProducerMaximum)
	for _, fixture := range f3_12Payloads() {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			wire, err := fixture.p.Encode()
			if err != nil || string(wire) != fixture.wire {
				t.Fatalf("canonical bytes = %s, %v; want %s", wire, err, fixture.wire)
			}
			for _, input := range []string{fixture.wire, `{"future":{"nested":[null,1]},` + fixture.wire[1:]} {
				got, err := decodeF3_12(fixture.typ, []byte(input))
				if err != nil || !reflect.DeepEqual(got, fixture.p) {
					t.Fatalf("decode = %#v, %v; want %#v", got, err, fixture.p)
				}
				again, err := got.Encode()
				if err != nil || !bytes.Equal(wire, again) {
					t.Fatalf("canonical roundtrip changed: %s, %v", again, err)
				}
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(wire, &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				assertReviewInvalid(t, fixture.typ, strings.Replace(fixture.wire, `"`+key+`":`+string(value), `"`+key+`":null`, 1))
				assertReviewInvalid(t, fixture.typ, `{"`+key+`":`+string(value)+`,`+fixture.wire[1:])
				if key == "replaces_request_id" {
					continue
				}
				copyFields := make(map[string]json.RawMessage, len(fields))
				for k, v := range fields {
					if k != key {
						copyFields[k] = v
					}
				}
				missing, _ := json.Marshal(copyFields)
				assertReviewInvalid(t, fixture.typ, string(missing))
			}
			for _, bad := range []string{`null`, `[]`, `{}`, fixture.wire + ` {}`, fixture.wire[:len(fixture.wire)-1], string([]byte{0xff}), `{"future":{"nested":[{"x":1,"x":2}]},` + fixture.wire[1:]} {
				assertReviewInvalid(t, fixture.typ, bad)
			}
			for _, size := range []int{65536, 65537} {
				_, err := decodeF3_12(fixture.typ, []byte(fixture.wire+strings.Repeat(" ", size-len(fixture.wire))))
				if size == 65536 && err != nil || size == 65537 && !errors.Is(err, ErrInvalid) {
					t.Fatalf("%d-byte payload = %v", size, err)
				}
			}
		})
	}
}
