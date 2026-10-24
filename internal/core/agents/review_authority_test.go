package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// The request survives a later authority revision, but never a revoked selected
// chain. Its observation and deadline come from a real non-review envelope, so
// the clock cannot mask a chain failure as expiration.
func testReviewResultRechecksPrefixChains(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name  string
		grant int
		valid bool
	}{{"reviewer-revoked", 0x302, false}, {"requester-revoked", 0x300, false}, {"unrelated-revision", 0x303, true}, {"pending-replaced", 0, false}} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f, commits := openReviewFixture(t, 17)
			ctx := context.Background()
			cursor := f.store.Durable()
			readTime := func() int64 {
				t.Helper()
				var sample int64
				through := f.store.Durable()
				if err := f.service.scan(ctx, cursor, through, func(c ledger.Commit) error {
					for _, r := range c.Records {
						sample = max(sample, r.EventTimeNS)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				cursor = through
				return sample
			}
			book, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ConfigureCapacity(ctx, reviewOwner(), CapacitySpec{CommandID: privateID(979), ExpectedVersion: uint64(book.Version), Scope: book.Scope, Limits: book.Limits}); err != nil {
				t.Fatal(err)
			}
			d := scopedGoldenDrafts(commits[17])
			q := scopedDecode(t, ledger.DecodeRuntimeReviewRequested, d[1].Payload)
			q.ObservedUTCNS = readTime()
			q.Binding.DeadlineUTCNS = q.ObservedUTCNS + 86_400_000_000_000
			requestSequence := int64(f.store.Durable() + 2)
			d[1].Payload = testPayload(t, q)
			rewriteReviewEffect(t, d)
			if err := reviewPreflight(t, f, d); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Submit(ctx, d[0], d[1:]); err != nil {
				t.Fatal(err)
			}
			if err := f.service.refreshLocked(ctx, f.store.Durable()); err != nil {
				t.Fatal(err)
			}
			if row.grant == 0 {
				replacement := q
				replacement.RequestID = reviewGoldenID(0x401)
				replacement.ReplacesRequestID = q.RequestID
				replacement.ObservedUTCNS = readTime()
				next := scopedGoldenDrafts(commits[17])
				next[1].Payload = testPayload(t, replacement)
				rewriteReviewEffect(t, next)
				receipt := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, next[2].Payload)
				receipt.CommandID = privateID(983)
				rewriteReviewReceipt(t, next, receipt)
				if err := reviewPreflight(t, f, next); err != nil {
					t.Fatal(err)
				}
				if _, err := f.store.Submit(ctx, next[0], next[1:]); err != nil {
					t.Fatal(err)
				}
				if err := f.service.refreshLocked(ctx, f.store.Durable()); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.service.RevokeGrant(ctx, reviewOwner(), GrantRevocation{CommandID: privateID(980), RevokeGrantID: reviewGoldenID(row.grant), ExpectedVersion: 1, ExpectedAuthorityVersion: 4}); err != nil {
				t.Fatal(err)
			}
			d = scopedGoldenDrafts(commits[18])
			v := scopedDecode(t, ledger.DecodeRuntimeReviewResult, d[1].Payload)
			v.Binding = q.Binding
			v.RequestSequence = requestSequence
			v.ObservedUTCNS = readTime()
			d[1].Payload = testPayload(t, v)
			rewriteReviewEffect(t, d)
			r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, d[2].Payload)
			r.Usage.AuthorityVersion = f.service.p.authority
			rewriteReviewReceipt(t, d, r)
			before := f.store.Durable()
			err = reviewPreflight(t, f, d)
			if (err == nil) != row.valid {
				t.Fatalf("chain recheck valid=%t: %v", row.valid, err)
			}
			if f.store.Durable() != before {
				t.Fatal("preflight appended")
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			f.service = nil
			if _, err := f.store.Submit(ctx, d[0], d[1:]); err != nil {
				t.Fatal(err)
			}
			f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
			if row.valid {
				if err != nil {
					t.Fatal(err)
				}
				view, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: q.RequestID})
				if err != nil || view.State != "rejected" || !view.Current {
					t.Fatalf("unrelated authority staled review: %+v %v", view, err)
				}
			} else if !errors.Is(err, ErrInconsistent) {
				t.Fatalf("revoked callback replayed: %v", err)
			}
		})
	}
}
