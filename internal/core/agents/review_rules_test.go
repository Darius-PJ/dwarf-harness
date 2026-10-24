package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestReviewGroupRules(t *testing.T) {
	t.Parallel()
	t.Run("result-current-chains", testReviewResultRechecksPrefixChains)
	type row struct {
		name       string
		at, prefix int
		valid      bool
		mutate     func(*testing.T, *serviceFixture, []ledger.Draft) []ledger.Draft
	}
	effect := func(change func(*ledger.RuntimeReviewResult)) func(*testing.T, *serviceFixture, []ledger.Draft) []ledger.Draft {
		return func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			change(&v)
			d[1].Payload = testPayload(t, v)
			rewriteReviewEffect(t, d)
			return d
		}
	}
	request := func(change func(*ledger.RuntimeReviewRequested)) func(*testing.T, *serviceFixture, []ledger.Draft) []ledger.Draft {
		return func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, d[1].Payload)
			change(&q)
			d[1].Payload = testPayload(t, q)
			rewriteReviewEffect(t, d)
			return d
		}
	}
	receipt := func(change func(*ledger.RuntimeReviewReceipt)) func(*testing.T, *serviceFixture, []ledger.Draft) []ledger.Draft {
		return func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, d[len(d)-1].Payload)
			change(&r)
			rewriteReviewReceipt(t, d, r)
			return d
		}
	}
	rawReceipt := func(change func(*ledger.RuntimeReviewReceipt)) func(*testing.T, *serviceFixture, []ledger.Draft) []ledger.Draft {
		return func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, d[len(d)-1].Payload)
			change(&r)
			payload, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			d[len(d)-1].Payload = payload
			return d
		}
	}
	rows := []row{
		{name: "mixed-family", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].Type = ledger.TypePrivateRevision
			return d
		}},
		{name: "detached-effect", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft { return d[:2] }},
		{name: "missing-unpin", at: 20, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft { return append(d[:2], d[3:]...) }},
		{name: "extra-record", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft { return append(d, d[1]) }},
		{name: "reordered", at: 20, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1], d[2] = d[2], d[1]
			return d
		}},
		{name: "wrong-actor", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].Actor = ledger.ActorOwner
			return d
		}},
		{name: "wrong-origin", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].ContextID = reviewGoldenID(0xd3)
			return d
		}},
		{name: "wrong-operation-id", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].OperationID = privateID(999)
			return d
		}},
		{name: "turn-envelope", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].TurnID = privateID(999)
			return d
		}},
		{name: "measurement-envelope", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].MeasurementPath = "run"
			return d
		}},
		{name: "body-reference", at: 18, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			d[1].BodyRefs = []ledger.Digest{v.Binding.Subject.Artifact.SHA256}
			return d
		}},
		{name: "canonical-digest", at: 18, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			c := scopedDecode(t, ledger.DecodeCommand, d[0].Payload)
			c.SubmissionSHA256[0] ^= 1
			d[0].Payload = testPayload(t, c)
			return d
		}},
		{name: "wrong-task-unpin", at: 20, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[2].Payload = testPayload(t, ledger.BodyUnpin{PinID: reviewGoldenID(0x201)})
			return d
		}},
		{name: "receipt-outcome", at: 18, mutate: receipt(func(r *ledger.RuntimeReviewReceipt) { r.Outcome = "approved" })},
		{name: "unknown-receipt-outcome", at: 18, mutate: receipt(func(r *ledger.RuntimeReviewReceipt) { r.Outcome = "future_judgment" })},
		{name: "receipt-version", at: 18, mutate: receipt(func(r *ledger.RuntimeReviewReceipt) { r.Version = 1 })},
		{name: "receipt-task-version", at: 18, mutate: receipt(func(r *ledger.RuntimeReviewReceipt) { r.TaskVersion++ })},
		{name: "usage-authority", at: 18, mutate: receipt(func(r *ledger.RuntimeReviewReceipt) { r.Usage.AuthorityVersion++ })},
		{name: "usage-scopes", at: 18, mutate: receipt(func(r *ledger.RuntimeReviewReceipt) { r.Usage.Scopes[2].ID = reviewGoldenID(0xd3) })},
		{name: "omitted-agent-charge", at: 18, mutate: rawReceipt(func(r *ledger.RuntimeReviewReceipt) { r.Usage = nil })},
		{name: "extra-owner-charge", at: 20, mutate: rawReceipt(func(r *ledger.RuntimeReviewReceipt) { r.Usage = &ledger.ReviewUsage{} })},
		{name: "extra-agent-unit", at: 18, mutate: rawReceipt(func(r *ledger.RuntimeReviewReceipt) { r.Usage.Units = 2 })},
		{name: "illegal-effect-task-version", at: 18, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			v.TaskVersion++
			payload, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			d[1].Payload = payload
			return d
		}},
		{name: "request-sequence", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.RequestSequence++ })},
		{name: "unknown-request", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.RequestID = privateID(999) })},
		{name: "different-subject-source-same-hash", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.Binding.Subject.Artifact.SourceSequence = 62 })},
		{name: "different-private-revision-same-hash", at: 22, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.Binding.Subject.PrivateRevision.Revision++ })},
		{name: "different-check", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.Binding.CheckSHA256[0] ^= 1 })},
		{name: "different-config", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.Binding.ConfigSHA256[0] ^= 1 })},
		{name: "different-cause", at: 22, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.Cause = "another_failure" })},
		{name: "late-result", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.ObservedUTCNS = v.Binding.DeadlineUTCNS + 1 })},
		{name: "below-floor", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.ObservedUTCNS = 1 })},
		{name: "result-at-deadline", at: 18, valid: true, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.ObservedUTCNS = v.Binding.DeadlineUTCNS })},
		{name: "request-at-deadline", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.ObservedUTCNS = q.Binding.DeadlineUTCNS })},
		{name: "request-authorization-prefix", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.Binding.AuthorizationRevision++ })},
		{name: "reviewer-wrong-session", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.Binding.Reviewer.Origin.SessionID = reviewGoldenID(0xb1) })},
		{name: "reviewer-wrong-context", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.Binding.Reviewer.Origin.ContextID = reviewGoldenID(0xd3) })},
		{name: "submitted-subject-wrong-occurrence", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.Binding.Subject.Artifact.SourceSequence = 62 })},
		{name: "missing-replacement", at: 19, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.ReplacesRequestID = uuid.UUID{} })},
		{name: "wrong-replacement", at: 19, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.ReplacesRequestID = privateID(999) })},
		{name: "historical-id-reused-below-watermark", at: 23, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.RequestID = reviewGoldenID(0x400) })},
		{name: "historical-id-reused-at-watermark", at: 23, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.RequestID = reviewGoldenID(0x402) })},
		{name: "replaced-request-result", at: 18, prefix: 20, mutate: func(t *testing.T, f *serviceFixture, d []ledger.Draft) []ledger.Draft {
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			v.ObservedUTCNS = f.service.p.reviewFloor
			d[1].Payload = testPayload(t, v)
			r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, d[2].Payload)
			r.CommandID = privateID(999)
			rewriteReviewReceipt(t, d, r)
			return d
		}},
		{name: "private-source-wrong-occurrence", at: 21, mutate: request(func(q *ledger.RuntimeReviewRequested) {
			q.Binding.Subject.PrivateRevision.SourceSequence = 15
			q.Binding.RequiredEvidence[0] = q.Binding.Subject
			q.FailingEvidence = &q.Binding.RequiredEvidence[0]
		})},
		{name: "private-source-wrong-revision", at: 21, mutate: request(func(q *ledger.RuntimeReviewRequested) {
			q.Binding.Subject.PrivateRevision.Revision++
			q.Binding.RequiredEvidence[0] = q.Binding.Subject
			q.FailingEvidence = &q.Binding.RequiredEvidence[0]
		})},
		{name: "private-source-wrong-scope", at: 21, mutate: request(func(q *ledger.RuntimeReviewRequested) {
			q.Binding.Subject.PrivateRevision.Scope = "agent"
			q.Binding.RequiredEvidence[0] = q.Binding.Subject
			q.FailingEvidence = &q.Binding.RequiredEvidence[0]
		})},
		{name: "private-source-wrong-size", at: 21, mutate: request(func(q *ledger.RuntimeReviewRequested) {
			q.Binding.Subject.PrivateRevision.Bytes++
			q.Binding.RequiredEvidence[0] = q.Binding.Subject
			q.FailingEvidence = &q.Binding.RequiredEvidence[0]
		})},
		{name: "private-source-wrong-binding", at: 21, mutate: request(func(q *ledger.RuntimeReviewRequested) {
			q.Binding.Subject.PrivateRevision.Binding.ContextID = reviewGoldenID(0xd3)
			q.Binding.RequiredEvidence[0] = q.Binding.Subject
			q.FailingEvidence = &q.Binding.RequiredEvidence[0]
		})},
	}
	for _, at := range []int{17, 18, 19, 20, 21, 22, 23, 24} {
		rows = append(rows, row{name: fmt.Sprintf("valid-group-%d", at), at: at, valid: true})
	}
	for _, index := range []int{1, 2} {
		rows = append(rows, row{name: fmt.Sprintf("newer-request-type-%d", index), at: 17, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[index].TypeVersion = 2
			return d
		}})
	}
	rows = append(rows, row{name: "newer-result-type", at: 18, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft { d[1].TypeVersion = 2; return d }})
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			prefix := row.at
			if row.prefix != 0 {
				prefix = row.prefix
			}
			f, commits := openReviewFixture(t, prefix)
			d := scopedGoldenDrafts(commits[row.at])
			if row.mutate != nil {
				d = row.mutate(t, f, d)
			}
			before := f.store.Durable()
			err := reviewPreflight(t, f, d)
			if (err == nil) != row.valid || f.store.Durable() != before {
				t.Fatalf("preflight accepted=%t want=%t: %v", err == nil, row.valid, err)
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			f.service = nil
			for _, draft := range d {
				if draft.TypeVersion > 1 {
					reviewFutureCatalogue(t, f, commits, d)
					break
				}
			}
			// Raw store publication deliberately bypasses the agents reducer. Submit
			// encodes a real whole commit, so Open must independently reject the rule.
			if _, err := f.store.Submit(context.Background(), d[0], d[1:]); err != nil {
				t.Fatalf("store fixture: %v", err)
			}
			before = f.store.Durable()
			f.service, _, err = Open(context.Background(), f.store, f.root, DefaultLimits())
			var unsupported *ledger.UnsupportedVersionError
			if row.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !errors.Is(err, ErrInconsistent) && !errors.As(err, &unsupported) {
				t.Fatalf("Open accepted malformed group: %v", err)
			}
			if f.store.Durable() != before {
				t.Fatal("Open appended")
			}
		})
	}
}
