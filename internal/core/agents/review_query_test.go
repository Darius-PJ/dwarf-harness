package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func TestReviewStatusViews(t *testing.T) {
	t.Parallel()
	f, commits := openReviewFixture(t, -1)
	ctx := context.Background()
	before := f.store.Durable()
	t.Run("revoked-access", func(t *testing.T) {
		t.Parallel()
		f, commits := openReviewFixture(t, 18)
		a, err := f.service.BindAgent(ctx, Binding{reviewGoldenID(0xa1), reviewGoldenID(0xd3)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.RevokeGrant(ctx, reviewOwner(), GrantRevocation{CommandID: privateID(981), RevokeGrantID: reviewGoldenID(0x300), ExpectedVersion: 1, ExpectedAuthorityVersion: 4}); err != nil {
			t.Fatal(err)
		}
		before := f.store.Durable()
		v, err := a.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x400), GrantID: reviewGoldenID(0x300)})
		if err != ErrDenied || !reflect.DeepEqual(v, ReviewView{}) {
			t.Fatalf("revoked view leaked: %+v %v", v, err)
		}
		r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, commits[17].Records[2].Payload)
		receipt, err := a.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: r.CommandID, GrantID: reviewGoldenID(0x300), SubmissionSHA256: r.SubmissionSHA256})
		if err != ErrDenied || !reflect.DeepEqual(receipt, ReviewReceipt{}) {
			t.Fatalf("revoked receipt leaked: %+v %v", receipt, err)
		}
		if f.store.Durable() != before {
			t.Fatal("denied view appended")
		}
	})
	t.Run("pending-replaced", func(t *testing.T) {
		t.Parallel()
		f, commits := openReviewFixture(t, 18)
		d := scopedGoldenDrafts(commits[19])
		if _, err := f.store.Submit(ctx, d[0], d[1:]); err != nil {
			t.Fatal(err)
		}
		old, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x400)})
		if err != nil || old.State != "superseded" || old.Current || old.Result != nil || old.SupersededByRequestID != reviewGoldenID(0x401) {
			t.Fatalf("pending replacement lost: %+v %v", old, err)
		}
		current, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x401)})
		if err != nil || current.State != "pending" || !current.Current {
			t.Fatalf("replacement not current: %+v %v", current, err)
		}
	})
	for _, row := range []struct {
		name                  string
		agent, context, grant int
	}{{"requester", 0xa1, 0xd3, 0x300}, {"reviewer", 0xa2, 0xd2, 0x302}} {
		t.Run(row.name, func(t *testing.T) {
			// This row shares the read-only service; the owned-value checks mutate only
			// returned snapshots and synchronize through the service's normal query lock.
			t.Parallel()
			a, err := f.service.BindAgent(ctx, Binding{reviewGoldenID(row.agent), reviewGoldenID(row.context)})
			if err != nil {
				t.Fatal(err)
			}
			lookup := ReviewLookup{RequestID: reviewGoldenID(0x400), GrantID: reviewGoldenID(row.grant)}
			v, err := a.ReviewStatus(ctx, lookup)
			if err != nil || v.State != "rejected" || v.Current || v.SupersededByRequestID != reviewGoldenID(0x401) {
				t.Fatalf("participant lost historical outcome: %+v %v", v, err)
			}
			saved := v.Request.Binding.CheckSHA256
			v.Request.Binding.CheckSHA256[0] ^= 1
			v.Request.Binding.RequiredEvidence[0].Artifact.SourceSequence++
			v.Result.Binding.ConfigSHA256[0] ^= 1
			again, err := a.ReviewStatus(ctx, lookup)
			if err != nil || again.Request.Binding.CheckSHA256 != saved || again.Request.Binding.RequiredEvidence[0].Artifact.SourceSequence != 36 {
				t.Fatalf("caller mutation changed review authority: %+v %v", again, err)
			}
			hidden, hiddenErr := a.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x402), GrantID: reviewGoldenID(row.grant)})
			absent, absentErr := a.ReviewStatus(ctx, ReviewLookup{RequestID: privateID(999), GrantID: reviewGoldenID(row.grant)})
			if hiddenErr != ErrDenied || absentErr != ErrDenied || !reflect.DeepEqual(hidden, ReviewView{}) || !reflect.DeepEqual(absent, hidden) {
				t.Fatalf("hidden/nonexistent distinction: %+v %v / %+v %v", hidden, hiddenErr, absent, absentErr)
			}
			index := 17
			if row.name == "reviewer" {
				index = 18
			}
			c := commits[index]
			r := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, c.Records[len(c.Records)-1].Payload)
			receipt, err := a.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: r.CommandID, GrantID: reviewGoldenID(row.grant), SubmissionSHA256: r.SubmissionSHA256})
			if err != nil || receipt.Sequence != c.Last || receipt.Outcome != r.Outcome {
				t.Fatalf("participant receipt: %+v %v", receipt, err)
			}
			receipt.Submission.Arguments = nil
			receipt.Usage.Scopes[0].Kind = "grant"
			againReceipt, err := a.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: r.CommandID, GrantID: reviewGoldenID(row.grant), SubmissionSHA256: r.SubmissionSHA256})
			if err != nil || !reflect.DeepEqual(againReceipt.RuntimeReviewReceipt, r) {
				t.Fatalf("receipt not owned: %+v %v", againReceipt, err)
			}
			foreign := commits[17]
			if index == 17 {
				foreign = commits[18]
			}
			other := scopedDecode(t, ledger.DecodeRuntimeReviewReceipt, foreign.Records[len(foreign.Records)-1].Payload)
			denied, err := a.ReviewCommandStatus(ctx, ReviewCommandLookup{CommandID: other.CommandID, GrantID: reviewGoldenID(row.grant), SubmissionSHA256: other.SubmissionSHA256})
			if !errors.Is(err, ErrDenied) || !reflect.DeepEqual(denied, ReviewReceipt{}) {
				t.Fatalf("foreign principal receipt leaked: %+v %v", denied, err)
			}
			if f.store.Durable() != before {
				t.Fatal("passive query appended")
			}
		})
	}
	owner, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x402)})
	if err != nil || owner.Result == nil || owner.Result.FailingEvidence.PrivateRevision == nil {
		t.Fatalf("owner private review: %+v %v", owner, err)
	}
	owner.Result.FailingEvidence.PrivateRevision.Revision++
	again, err := f.service.ReviewStatus(ctx, ReviewLookup{RequestID: reviewGoldenID(0x402)})
	if err != nil || again.Result.FailingEvidence.PrivateRevision.Revision != 1 {
		t.Fatal("private reference pointer escaped")
	}
}
