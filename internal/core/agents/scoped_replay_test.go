package agents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
)

func scopedDecode[T any](t testing.TB, decode func(json.RawMessage) (T, error), payload json.RawMessage) T {
	t.Helper()
	value, err := decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// Change semantic fixtures without accidentally testing only a stale digest.
func scopedRewriteReceipt(t *testing.T, drafts []ledger.Draft, receipt ledger.RuntimeScopedReceipt) {
	t.Helper()
	receipt.SubmissionSHA256 = ledger.Digest(sha256.Sum256(testPayload(t, receipt.Submission)))
	command := scopedDecode(t, ledger.DecodeCommand, drafts[0].Payload)
	command.SubmissionSHA256 = receipt.SubmissionSHA256
	drafts[0].Payload = testPayload(t, command)
	drafts[len(drafts)-1].Payload = testPayload(t, receipt)
}

// Independently replaying the real prefix avoids a shallow-copy atomicity
// assertion that would miss writes through a nested map or slice.
func scopedPreflightFixture(t *testing.T, f *serviceFixture, drafts []ledger.Draft, intent ledger.RuntimeScopedReceipt) (*scopedGroup, error) {
	t.Helper()
	baseline, report, err := Open(context.Background(), f.store, f.root, f.service.limits)
	if err != nil || len(report.Issues) != 0 {
		t.Fatalf("preflight baseline: %+v %v", report, err)
	}
	defer baseline.Close()
	return scopedPreflightAgainst(t, f.service, baseline, drafts, intent)
}

func scopedPreflightAgainst(t *testing.T, service, baseline *Service, drafts []ledger.Draft, intent ledger.RuntimeScopedReceipt) (*scopedGroup, error) {
	t.Helper()
	for _, draft := range drafts {
		if draft.Type == ledger.TypeRuntimeScopedReceipt {
			if candidate, err := ledger.DecodeRuntimeScopedReceipt(draft.Payload); err == nil {
				intent = candidate
			}
		}
	}
	input := scopedInput{id: intent.CommandID, digest: intent.SubmissionSHA256, sub: intent.Submission}
	service.mu.Lock()
	defer service.mu.Unlock()
	group, err := service.preflightScoped(context.Background(), input, drafts)
	if !reflect.DeepEqual(service.p, baseline.p) {
		t.Fatal("preflight installed part of a group before acceptance")
	}
	return group, err
}

func TestScopedGroupRules(t *testing.T) {
	t.Parallel()
	type ruleCase struct {
		name    string
		at      int
		prefix  *int
		valid   bool
		prepare func(*testing.T, *serviceFixture)
		mutate  func(*testing.T, *serviceFixture, []ledger.Draft) []ledger.Draft
		check   func(*testing.T, *serviceFixture, Reconciliation)
	}
	cases := []ruleCase{
		{name: "payload-shape", at: 16, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].Payload = []byte(`{}`)
			return d
		}},
		{name: "mixed-private-families", at: 16, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].Type = ledger.TypePrivateRevision
			return d
		}},
		{name: "effect-actor", at: 16, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].Actor = ledger.ActorAgent
			return d
		}},
		{name: "record-origin", at: 16, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].ContextID = privateID(999)
			return d
		}},
		{name: "operation-id", at: 16, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].OperationID = privateID(999)
			return d
		}},
		{name: "work-turn-envelope", at: 21, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].TurnID = privateID(999)
			return d
		}},
		{name: "unexpected-measurement", at: 16, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1].MeasurementPath = "run"
			return d
		}},
		{name: "unexpected-body-reference", at: 16, mutate: func(t *testing.T, f *serviceFixture, d []ledger.Draft) []ledger.Draft {
			view, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: scopedGoldenID(0x200)})
			if err != nil {
				t.Fatal(err)
			}
			d[2].BodyRefs = []ledger.Digest{view.DescriptionRef.SHA256}
			return d
		}},
		{name: "missing-description-pin", at: 11, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft { return append(d[:1], d[2:]...) }},
		{name: "missing-terminal-unpin", at: 30, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft { return append(d[:2], d[3:]...) }},
		{name: "reordered-effects", at: 14, mutate: func(_ *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			d[1], d[2] = d[2], d[1]
			return d
		}},
		{name: "command-digest", at: 16, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			command := scopedDecode(t, ledger.DecodeCommand, d[0].Payload)
			command.SubmissionSHA256[0] ^= 1
			d[0].Payload = testPayload(t, command)
			return d
		}},
		{name: "receipt-version", at: 6, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.Version++
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "unregistered-session", at: 16, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[len(d)-1].Payload)
			r.Submission.Origin.SessionID = privateID(999)
			for i := range d {
				d[i].ExecutionSessionID = r.Submission.Origin.SessionID
			}
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "operation-target-kind", at: 11, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[len(d)-1].Payload)
			r.Submission.Target = ledger.Resource{Kind: "context", Binding: ledger.Binding{AgentID: r.Submission.Origin.AgentID, ContextID: r.Submission.Origin.ContextID}, Scope: "context", Slots: []string{}}
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "stale-authority", at: 10, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeGrantIssued, d[1].Payload)
			p.ExpectedAuthorityVersion++
			p.AuthorityVersion++
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.Submission.Expected.AuthorityVersion = new(p.ExpectedAuthorityVersion)
			r.Version = p.AuthorityVersion
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "child-resource-widening", at: 14, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeGrantIssued, d[1].Payload)
			p.Resources[0].PathPrefix = "outside"
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[3].Payload)
			a := r.Submission.Arguments.(ledger.GrantIssueArguments)
			a.Resources = p.Resources
			r.Submission.Arguments, r.Submission.Target = a, p.Resources[0]
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "child-allowance-widening", at: 14, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeGrantIssued, d[1].Payload)
			p.Limits.LocalUnits = 101
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[3].Payload)
			a := r.Submission.Arguments.(ledger.GrantIssueArguments)
			a.Limits = p.Limits
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "revoke-wrong-entity", at: 31, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeGrantRevoked, d[1].Payload)
			p.GrantID = scopedGoldenID(0x300)
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{name: "stale-capacity-version", at: 6, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeCapacityConfigured, d[1].Payload)
			p.ExpectedVersion, p.Version = 1, 2
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.Submission.Expected.Version, r.Version = 1, 2
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "task-definition-snapshot", at: 11, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeTaskChanged, d[2].Payload)
			p.AcceptanceSHA256[0] ^= 1
			d[2].Payload = testPayload(t, p)
			return d
		}},
		{name: "missing-assignment-expectation", at: 19, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.Submission.Expected.AssignmentID, r.Submission.Expected.Generation = uuid.UUID{}, nil
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "reassignment-reuses-old-context", at: 36, mutate: func(t *testing.T, f *serviceFixture, d []ledger.Draft) []ledger.Draft {
			current, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: scopedGoldenID(0x201)})
			if err != nil {
				t.Fatal(err)
			}
			p := scopedDecode(t, ledger.DecodeRuntimeTaskChanged, d[1].Payload)
			p.Assignment.Assignee = current.Task.Assignment.Assignee
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[3].Payload)
			a := r.Submission.Arguments.(ledger.TaskMutationArguments)
			a.NewAssignment.Assignee = p.Assignment.Assignee
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "unfamiliar-subject-is-not-an-artifact", at: 29, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeTaskChanged, d[1].Payload)
			p.Submission = &ledger.SubjectRef{Kind: "future", Members: []byte(`{"id":"opaque"}`)}
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.TaskMutationArguments)
			a.Submission = p.Submission
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "admission-missing-ancestor-scope", at: 21, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkAdmitted, d[1].Payload)
			p.Scopes = append(p.Scopes[:3], p.Scopes[4:]...)
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{name: "admission-wrong-observed-authority", at: 20, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkAdmitted, d[1].Payload)
			p.AuthorityVersion++
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{name: "task-context-admission-without-binding", at: 20, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkAdmitted, d[1].Payload)
			p.TaskBinding = nil
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.TurnStartArguments)
			a.TaskBinding = nil
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "local-admission-with-closed-scratch", at: 21, prepare: func(t *testing.T, f *serviceFixture) {
			ctx := context.Background()
			work := uuid.MustParse("0199c000-0000-7000-8000-0000000000d2")
			draft := ledger.Draft{Type: ledger.TypeScratchClosed, TypeVersion: 1, Actor: ledger.ActorOwner, Payload: testPayload(t, ledger.ScratchClosed{ScratchID: scopedGoldenID(0x401), ContextID: work, Reason: "abandoned"})}
			if _, err := f.store.Append(ctx, []ledger.Draft{draft}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "dispatch-changed-arguments", at: 22, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkDispatched, d[1].Payload)
			p.ArgumentsSHA256[0] ^= 1
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{name: "dispatch-after-ancestor-revocation", at: 22, prepare: func(t *testing.T, f *serviceFixture) {
			origin := Binding{uuid.MustParse("0199c000-0000-7000-8000-0000000000a1"), uuid.MustParse("0199c000-0000-7000-8000-0000000000d1")}
			if _, err := f.service.RevokeGrant(context.Background(), origin, GrantRevocation{CommandID: privateID(950), RevokeGrantID: scopedGoldenID(0x300), ExpectedVersion: 1, ExpectedAuthorityVersion: 4}); err != nil {
				t.Fatal(err)
			}
			book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Held != (Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024, LocalUnits: 1}) || book.Used.LocalUnits != 4 {
				t.Fatalf("revocation released occupied work: %+v %v", book, err)
			}
		}, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkDispatched, d[1].Payload)
			p.AuthorityVersion = 5
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{name: "dispatched-work-is-not-unsent", at: 23, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkEnded, d[1].Payload)
			p.Outcome = "released_unsent"
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.WorkEndArguments)
			a.Outcome = p.Outcome
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "parent-ending-before-child-observation", at: 25, prefix: new(23)},
		{name: "stale-work-ending", at: 24, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkEnded, d[1].Payload)
			p.ExpectedVersion, p.Version = 2, 3
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.Submission.Expected.Version, r.Version = 2, 3
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "retired-work-identity", at: 20, prefix: new(26), mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			command := scopedDecode(t, ledger.DecodeCommand, d[0].Payload)
			command.CommandID = privateID(950)
			d[0].Payload = testPayload(t, command)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.CommandID = command.CommandID
			scopedRewriteReceipt(t, d, r)
			for i := range d {
				d[i].OperationID = command.CommandID
			}
			return d
		}},
		{name: "usage-authority-before-not-after-grant", at: 14, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[2].Payload)
			p.AuthorityVersion++
			d[2].Payload = testPayload(t, p)
			return d
		}},
		{name: "usage-scope-set", at: 16, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[2].Payload)
			p.Scopes[2].ID = uuid.MustParse("0199c000-0000-7000-8000-0000000000d1")
			d[2].Payload = testPayload(t, p)
			return d
		}},
		{name: "creation-description-charge", at: 33, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[3].Payload)
			p.Bytes = 0
			d[3].Payload = testPayload(t, p)
			return d
		}},
		{name: "private-write-charge", at: 16, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[2].Payload)
			p.Bytes++
			d[2].Payload = testPayload(t, p)
			return d
		}},
		{name: "private-read-selected-occurrence", at: 17, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[1].Payload)
			p.Selected[0].Sequence++
			d[1].Payload = testPayload(t, p)
			return d
		}},
		{name: "scoped-private-stale-head", at: 16, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodePrivateScopedRevision, d[1].Payload)
			p.ExpectedVersion, p.Revision = 2, 3
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[3].Payload)
			r.Submission.Expected.Version, r.Version = 2, 3
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "shared-hard-object-support-ceiling", at: 16, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodePrivateScopedRevision, d[1].Payload)
			p.Changes[0].Bytes = maxObjectBytes + 1
			d[1].Payload = testPayload(t, p)
			u := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[2].Payload)
			u.Bytes = p.Changes[0].Bytes
			d[2].Payload = testPayload(t, u)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[3].Payload)
			a := r.Submission.Arguments.(ledger.PrivateWriteArguments)
			a.Changes = p.Changes
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "artifact-source-is-exact", at: 18, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, d[1].Payload)
			p.Artifact.SourceSequence++
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.ArtifactReadArguments)
			a.Artifact = *p.Artifact
			r.Submission.Arguments, r.Submission.Target.Artifact = a, *p.Artifact
			scopedRewriteReceipt(t, d, r)
			return d
		}},
		{name: "unfamiliar-lifetime-is-inert-history", at: 10, valid: true, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeGrantIssued, d[1].Payload)
			p.Lifetime = "future_epoch"
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.GrantIssueArguments)
			a.Lifetime = p.Lifetime
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}, check: func(t *testing.T, f *serviceFixture, _ Reconciliation) {
			view, err := f.service.GrantStatus(context.Background(), GrantLookup{GrantID: scopedGoldenID(0x300)})
			if err != nil || view.Current || view.Reason != "lifetime_not_current" {
				t.Fatalf("future lifetime granted authority: %+v %v", view, err)
			}
		}},
		{name: "unregistered-audience-is-inert-frozen-history", at: 11, valid: true, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeTaskChanged, d[2].Payload)
			p.Audience = []uuid.UUID{privateID(999)}
			d[2].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[3].Payload)
			a := r.Submission.Arguments.(ledger.TaskCreateArguments)
			a.Audience = p.Audience
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}, check: func(t *testing.T, f *serviceFixture, _ Reconciliation) {
			view, err := f.service.TaskStatus(context.Background(), TaskLookup{TaskID: scopedGoldenID(0x200)})
			if err != nil || !reflect.DeepEqual(view.Task.Audience, []uuid.UUID{privateID(999)}) || view.Reason != "unassigned" {
				t.Fatalf("producer registration guard tightened frozen replay: %+v %v", view, err)
			}
		}},
		{name: "unfamiliar-ending-holds", at: 23, valid: true, mutate: func(t *testing.T, _ *serviceFixture, d []ledger.Draft) []ledger.Draft {
			p := scopedDecode(t, ledger.DecodeRuntimeWorkEnded, d[1].Payload)
			p.Outcome = "future_observation"
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			a := r.Submission.Arguments.(ledger.WorkEndArguments)
			a.Outcome = p.Outcome
			r.Submission.Arguments = a
			scopedRewriteReceipt(t, d, r)
			return d
		}, check: func(t *testing.T, f *serviceFixture, report Reconciliation) {
			book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Held != (Exposure{TurnSlots: 1, LocalSlots: 1, BufferBytes: 1024}) || book.Used.LocalUnits != 5 || len(report.PendingWork) != 2 || report.PendingWork[1].Outcome != "future_observation" {
				t.Fatalf("unknown outcome released or disappeared: %+v %+v %v", book, report, err)
			}
		}},
		{name: "parent-may-end-after-unknown-child", at: 25, prefix: new(24), valid: true, check: func(t *testing.T, f *serviceFixture, report Reconciliation) {
			book, err := f.service.CapacityStatus(context.Background(), CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
			if err != nil || book.Held != (Exposure{LocalSlots: 1, BufferBytes: 1024}) || book.Used.LocalUnits != 5 || len(report.PendingWork) != 1 || report.PendingWork[0].Admission.WorkID != scopedGoldenID(0x501) {
				t.Fatalf("parent ending inferred child release or required a proven child outcome: %+v %+v %v", book, report, err)
			}
		}},
	}
	for _, index := range []int{6, 10, 11, 14, 16, 17, 18, 20, 21, 22, 23, 24, 25, 30, 31, 33, 36, 37} {
		cases = append(cases, ruleCase{name: fmt.Sprintf("valid-frozen-group-%02d", index), at: index, valid: true})
	}
	for _, revoke := range []struct {
		name    string
		target  int
		context string
		valid   bool
	}{
		{"issuer-may-revoke", 0x302, "0199c000-0000-7000-8000-0000000000d1", true},
		{"another-context-is-not-the-issuer", 0x302, "0199c000-0000-7000-8000-0000000000d3", false},
		{"agent-cannot-revoke-owner-root", 0x301, "0199c000-0000-7000-8000-0000000000d1", false},
	} {
		cases = append(cases, ruleCase{name: revoke.name, at: 31, valid: revoke.valid, prepare: func(t *testing.T, f *serviceFixture) {
			ctx := context.Background()
			target, err := f.service.GrantStatus(ctx, GrantLookup{GrantID: scopedGoldenID(revoke.target)})
			if err != nil {
				t.Fatal(err)
			}
			owner := Binding{uuid.MustParse("0199c000-0000-7000-8000-0000000000a1"), uuid.MustParse("0199c000-0000-7000-8000-0000000000d1")}
			recipients := make([]Binding, len(target.Grant.Recipients))
			for i, recipient := range target.Grant.Recipients {
				recipients[i] = Binding(recipient)
			}
			spec := GrantSpec{GrantID: privateID(941), Grantee: Binding{owner.AgentID, uuid.MustParse(revoke.context)}, Operations: []string{"grant.revoke"}, Resources: target.Grant.Resources, DataClass: target.Grant.DataClass, Recipients: recipients, Lifetime: "until_revoked"}
			scopedRoot(t, f.service, owner, 4, 940, spec)
		}, mutate: func(t *testing.T, f *serviceFixture, d []ledger.Draft) []ledger.Draft {
			target, err := f.service.GrantStatus(context.Background(), GrantLookup{GrantID: scopedGoldenID(revoke.target)})
			if err != nil {
				t.Fatal(err)
			}
			p := scopedDecode(t, ledger.DecodeRuntimeGrantRevoked, d[1].Payload)
			p.GrantID, p.ExpectedAuthorityVersion, p.AuthorityVersion = target.Grant.GrantID, 5, 6
			d[1].Payload = testPayload(t, p)
			r := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, d[2].Payload)
			r.Version, r.Submission.Actor, r.Submission.GrantID = 6, "agent", privateID(941)
			r.Submission.Origin.ContextID = uuid.MustParse(revoke.context)
			r.Submission.Target, r.Submission.Arguments = target.Grant.Resources[0], ledger.GrantRevokeArguments{GrantID: target.Grant.GrantID}
			r.Submission.Expected.AuthorityVersion = new(int64(5))
			for i := range d {
				d[i].Actor, d[i].ContextID = ledger.ActorAgent, r.Submission.Origin.ContextID
			}
			scopedRewriteReceipt(t, d, r)
			return d
		}})
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			prefix := row.at
			if row.prefix != nil {
				prefix = *row.prefix
			}
			f, commits := scopedGoldenFixture(t, "11", prefix)
			var err error
			f.service, _, err = Open(ctx, f.store, f.root, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if row.prepare != nil {
				row.prepare(t, f)
			}
			drafts := scopedGoldenDrafts(commits[row.at])
			intent := scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, drafts[len(drafts)-1].Payload)
			if row.mutate != nil {
				drafts = row.mutate(t, f, drafts)
			}
			before := f.store.Durable()
			_, err = scopedPreflightFixture(t, f, drafts, intent)
			if (err == nil) != row.valid || f.store.Durable() != before {
				t.Fatalf("preflight acceptance=%t want %t: %v", err == nil, row.valid, err)
			}
			if err := f.service.Close(); err != nil {
				t.Fatal(err)
			}
			f.service = nil
			if _, err := f.store.Submit(ctx, drafts[0], drafts[1:]); err != nil {
				t.Fatalf("real-store record fixture: %v", err)
			}
			before = f.store.Durable()
			var report Reconciliation
			f.service, report, err = Open(ctx, f.store, f.root, DefaultLimits())
			if row.valid {
				if err != nil || len(report.Issues) != 0 || f.store.Durable() != before {
					t.Fatalf("Open refused valid frozen group or appended: %+v %v", report, err)
				}
				if row.check != nil {
					row.check(t, f, report)
				}
			} else if !errors.Is(err, ErrInconsistent) || f.service != nil || f.store.Durable() != before {
				t.Fatalf("Open accepted invalid group or exposed a partial service: %v", err)
			}
		})
	}
}

func FuzzScopedGroupReplay(f *testing.F) {
	f.Add(byte(0), uint64(12), []byte(nil))
	f.Add(byte(0), uint64(13), []byte(nil))
	f.Add(byte(0), uint64(0), []byte(nil))
	f.Add(byte(1), uint64(0), []byte(`{}`))
	f.Add(byte(2), uint64(0), []byte(`{}`))
	f.Add(byte(3), uint64(0xd1), []byte(nil))
	f.Add(byte(3), uint64(0xd2), []byte(nil))
	f.Add(byte(4), uint64(0), []byte(nil))
	f.Add(byte(4), uint64(1), []byte(nil))
	ctx := context.Background()
	fixture, commits := scopedGoldenFixture(f, "11", 16)
	if err := fixture.store.Close(); err != nil {
		f.Fatal(err)
	}
	prepared := testRoot(f)
	if err := os.CopyFS(prepared, os.DirFS(fixture.root)); err != nil {
		f.Fatal(err)
	}
	var err error
	fixture.store, _, err = ledgerstore.Open(fixture.root, ledger.Defined())
	if err != nil {
		f.Fatal(err)
	}
	fixture.service, _, err = Open(ctx, fixture.store, fixture.root, DefaultLimits())
	if err != nil {
		f.Fatal(err)
	}
	baseline, report, err := Open(ctx, fixture.store, fixture.root, DefaultLimits())
	if err != nil || len(report.Issues) != 0 {
		f.Fatalf("fuzz baseline: %+v %v", report, err)
	}
	f.Cleanup(func() { _ = baseline.Close() })
	before := fixture.store.Durable()
	canonical := scopedGoldenDrafts(commits[16])
	intent := scopedDecode(f, ledger.DecodeRuntimeScopedReceipt, canonical[3].Payload)
	f.Fuzz(func(t *testing.T, mode byte, value uint64, input []byte) {
		t.Parallel()
		if len(input) > 64<<10 {
			return
		}
		drafts := scopedGoldenDrafts(commits[16])
		knownValid := false
		switch mode % 5 {
		case 0:
			usage := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, drafts[2].Payload)
			usage.Bytes = int64(value & ((1 << 20) - 1))
			knownValid = usage.Bytes == 12
			drafts[2].Payload = testPayload(t, usage)
		case 1:
			drafts[1].Payload = append([]byte(nil), input...)
		case 2:
			drafts[2].Payload = append([]byte(nil), input...)
		case 3:
			usage := scopedDecode(t, ledger.DecodeRuntimeLocalUsed, drafts[2].Payload)
			usage.Scopes[2].ID[15] = byte(value)
			knownValid = byte(value) == 0xd2
			drafts[2].Payload = testPayload(t, usage)
		case 4:
			revision := scopedDecode(t, ledger.DecodePrivateScopedRevision, drafts[1].Payload)
			revision.ExpectedVersion = 1 + int64(value%3)
			revision.Revision = revision.ExpectedVersion + 1
			knownValid = revision.ExpectedVersion == 1
			drafts[1].Payload = testPayload(t, revision)
		}
		_, err := scopedPreflightAgainst(t, fixture.service, baseline, drafts, intent)
		if fixture.store.Durable() != before {
			t.Fatal("preflight appended a fuzzed group")
		}
		if err != nil {
			if knownValid {
				t.Fatalf("valid bounded transition refused: %v", err)
			}
			return
		}
		// The unchanged command fixes one particular revision and one exact
		// charge. Compare reconstructed meaning, not incidental JSON spacing.
		drafts[0].Payload = testPayload(t, scopedDecode(t, ledger.DecodeCommand, drafts[0].Payload))
		drafts[1].Payload = testPayload(t, scopedDecode(t, ledger.DecodePrivateScopedRevision, drafts[1].Payload))
		drafts[2].Payload = testPayload(t, scopedDecode(t, ledger.DecodeRuntimeLocalUsed, drafts[2].Payload))
		drafts[3].Payload = testPayload(t, scopedDecode(t, ledger.DecodeRuntimeScopedReceipt, drafts[3].Payload))
		if !bytes.Equal(drafts[1].Payload, canonical[1].Payload) || !bytes.Equal(drafts[2].Payload, canonical[2].Payload) {
			t.Fatal("accepted a group inconsistent with the fixed submission and pre-transition state")
		}
		root := testRoot(t)
		if err := os.CopyFS(root, os.DirFS(prepared)); err != nil {
			t.Fatal(err)
		}
		store, _, err := ledgerstore.Open(root, ledger.Defined())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.Submit(ctx, drafts[0], drafts[1:]); err != nil {
			t.Fatal(err)
		}
		committed := store.Durable()
		service, report, err := Open(ctx, store, root, DefaultLimits())
		if err != nil || len(report.Issues) != 0 || len(report.PendingWork) != 0 {
			t.Fatalf("canonical accepted group failed replay: %+v %v", report, err)
		}
		t.Cleanup(func() { _ = service.Close() })
		page, err := service.ReadPrivate(ctx, PrivateRead{ReadBinding: ReadBinding{Binding: Binding{drafts[1].AgentID, drafts[1].ContextID}, Scope: "context"}, Revision: 2, Slot: "memory", MaxBytes: 1024})
		if err != nil || string(page.Bytes) != "work memory\n" {
			t.Fatalf("accepted transition lost its shared private history: %+v %v", page, err)
		}
		book, err := service.CapacityStatus(ctx, CapacityLookup{Scope: ledger.ScopeKey{Kind: "colony"}})
		if err != nil || book.Used.LocalUnits != 2 || book.Held != (Exposure{}) || store.Durable() != committed {
			t.Fatalf("accepted transition lost its exact charge or replay appended: %+v %v", book, err)
		}
	})
}
