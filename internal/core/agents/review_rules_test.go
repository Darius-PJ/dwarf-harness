package agents

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
	// Every valid frozen row stays, with result-current-chains (revoked
	// requester or reviewer). Refusals keep one structural row (reordered),
	// one semantic row (unknown-request) and the authority rows: the request's
	// authorization prefix and the reviewer's binding.
	rows := []row{
		{name: "reordered", at: 20, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1], d[2] = d[2], d[1]
			return d
		}},
		{name: "unknown-request", at: 18, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.RequestID = privateID(999) })},
		{name: "result-at-deadline", at: 18, valid: true, mutate: effect(func(v *ledger.RuntimeReviewResult) { v.ObservedUTCNS = v.Binding.DeadlineUTCNS })},
		{name: "request-authorization-prefix", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.Binding.AuthorizationRevision++ })},
		{name: "reviewer-wrong-context", at: 17, mutate: request(func(q *ledger.RuntimeReviewRequested) { q.Binding.Reviewer.Origin.ContextID = reviewGoldenID(0xd3) })},
	}
	for _, at := range []int{17, 18, 19, 20, 21, 22, 23, 24} {
		rows = append(rows, row{name: fmt.Sprintf("valid-group-%d", at), at: at, valid: true})
	}
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
