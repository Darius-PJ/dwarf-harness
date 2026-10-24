package agents

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reviewRuleBinding() ledger.ReviewBinding {
	task, _ := taskRuleFixture()
	artifact := ledger.SubjectRef{Kind: "artifact", Artifact: ledger.ArtifactRef{SourceSequence: 4, SHA256: taskArtifact().Artifact.SHA256, Bytes: 8}}
	private := ledger.SubjectRef{Kind: "private_revision", PrivateRevision: &ledger.PrivateRevisionRef{Binding: ledger.Binding(task.assignment.binding), Scope: "context", Slot: "transcript", Revision: 2, SourceSequence: 9, SHA256: artifact.Artifact.SHA256, Bytes: 8}}
	return ledger.ReviewBinding{
		TaskID: task.id, TaskVersion: int64(task.version), AssignmentID: task.assignment.id, Generation: task.assignment.generation, Assignee: ledger.Binding(task.assignment.binding),
		Reviewer: ledger.ReviewPrincipal{Actor: "agent", Origin: ledger.Origin{AgentID: privateID(4), SessionID: privateID(6), ContextID: privateID(5)}, GrantID: privateID(70)},
		Subject:  private, AcceptanceSHA256: artifact.Artifact.SHA256, CheckSHA256: artifact.Artifact.SHA256, ConfigSHA256: artifact.Artifact.SHA256,
		RequiredEvidence: []ledger.SubjectRef{artifact, private}, AuthorizationRevision: 7, DeadlineUTCNS: 100,
	}
}

func TestReviewBindingRejectsSingleMemberSubstitution(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*ledger.ReviewBinding)
	}{
		{"task_identity", func(b *ledger.ReviewBinding) { b.TaskID = privateID(99) }},
		{"task_version", func(b *ledger.ReviewBinding) { b.TaskVersion++ }},
		{"assignment_identity", func(b *ledger.ReviewBinding) { b.AssignmentID = privateID(99) }},
		{"assignment_generation", func(b *ledger.ReviewBinding) { b.Generation++ }},
		{"assignee_agent", func(b *ledger.ReviewBinding) { b.Assignee.AgentID = privateID(99) }},
		{"assignee_context", func(b *ledger.ReviewBinding) { b.Assignee.ContextID = privateID(99) }},
		{"reviewer_principal", func(b *ledger.ReviewBinding) { b.Reviewer.Actor = "owner" }},
		{"reviewer_agent", func(b *ledger.ReviewBinding) { b.Reviewer.Origin.AgentID = privateID(99) }},
		{"reviewer_session", func(b *ledger.ReviewBinding) { b.Reviewer.Origin.SessionID = privateID(99) }},
		{"reviewer_context", func(b *ledger.ReviewBinding) { b.Reviewer.Origin.ContextID = privateID(99) }},
		{"reviewer_grant", func(b *ledger.ReviewBinding) { b.Reviewer.GrantID = privateID(99) }},
		{"acceptance_digest", func(b *ledger.ReviewBinding) { b.AcceptanceSHA256[0]++ }},
		{"check_digest", func(b *ledger.ReviewBinding) { b.CheckSHA256[0]++ }},
		{"config_digest", func(b *ledger.ReviewBinding) { b.ConfigSHA256[0]++ }},
		{"authorization_revision", func(b *ledger.ReviewBinding) { b.AuthorizationRevision++ }},
		{"deadline", func(b *ledger.ReviewBinding) { b.DeadlineUTCNS++ }},
		{"evidence_removed", func(b *ledger.ReviewBinding) { b.RequiredEvidence = b.RequiredEvidence[:1] }},
		{"evidence_added", func(b *ledger.ReviewBinding) { b.RequiredEvidence = append(b.RequiredEvidence, b.Subject) }},
		{"evidence_order", func(b *ledger.ReviewBinding) {
			b.RequiredEvidence[0], b.RequiredEvidence[1] = b.RequiredEvidence[1], b.RequiredEvidence[0]
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			want, got := reviewRuleBinding(), reviewRuleBinding()
			row.change(&got)
			if err := matchReviewBinding(want, got); !errors.Is(err, ErrInvalid) {
				t.Fatalf("substituted binding accepted: %v", err)
			}
		})
	}
}

func TestReviewBindingPreservesSubjectOccurrences(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"subject", "private_evidence"} {
		for _, row := range []struct {
			name   string
			change func(*ledger.PrivateRevisionRef)
		}{
			{"source_sequence", func(s *ledger.PrivateRevisionRef) { s.SourceSequence++ }},
			{"revision", func(s *ledger.PrivateRevisionRef) { s.Revision++ }},
			{"agent", func(s *ledger.PrivateRevisionRef) { s.Binding.AgentID = privateID(99) }},
			{"context", func(s *ledger.PrivateRevisionRef) { s.Binding.ContextID = privateID(99) }},
			{"scope", func(s *ledger.PrivateRevisionRef) { s.Scope = "agent" }},
			{"slot", func(s *ledger.PrivateRevisionRef) { s.Slot = "memory" }},
			{"digest", func(s *ledger.PrivateRevisionRef) { s.SHA256[0]++ }},
			{"bytes", func(s *ledger.PrivateRevisionRef) { s.Bytes++ }},
		} {
			t.Run(target+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				want, got := reviewRuleBinding(), reviewRuleBinding()
				subject := &got.Subject
				if target == "private_evidence" {
					subject = &got.RequiredEvidence[1]
				}
				row.change(subject.PrivateRevision)
				if err := matchReviewBinding(want, got); !errors.Is(err, ErrInvalid) {
					t.Fatalf("substituted occurrence accepted: %v", err)
				}
			})
		}
	}
	for _, row := range []struct {
		name   string
		change func(*ledger.SubjectRef)
	}{
		{"kind", func(s *ledger.SubjectRef) { s.Kind = "future" }},
		{"source_sequence", func(s *ledger.SubjectRef) { s.Artifact.SourceSequence++ }},
		{"digest", func(s *ledger.SubjectRef) { s.Artifact.SHA256[0]++ }},
		{"bytes", func(s *ledger.SubjectRef) { s.Artifact.Bytes++ }},
	} {
		t.Run("artifact_evidence/"+row.name, func(t *testing.T) {
			t.Parallel()
			want, got := reviewRuleBinding(), reviewRuleBinding()
			row.change(&got.RequiredEvidence[0])
			if err := matchReviewBinding(want, got); !errors.Is(err, ErrInvalid) {
				t.Fatalf("substituted artifact accepted: %v", err)
			}
		})
	}
}

func TestReviewBindingMatchesOwnedEquivalentValues(t *testing.T) {
	t.Parallel()
	want, got, before := reviewRuleBinding(), reviewRuleBinding(), reviewRuleBinding()
	if err := matchReviewBinding(want, got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, got) {
		t.Fatal("binding comparison changed the callback")
	}
}
