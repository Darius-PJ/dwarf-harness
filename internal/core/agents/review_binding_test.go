package agents

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reviewRuleBinding() reviewBinding {
	task, _ := taskRuleFixture()
	artifact := reviewSubject{kind: "artifact", sourceSequence: 4, sha256: taskArtifact().Artifact.SHA256, bytes: 8}
	private := reviewSubject{kind: "private_revision", binding: task.assignment.binding, scope: "context", slot: "transcript", revision: 2, sourceSequence: 9, sha256: artifact.sha256, bytes: 8}
	return reviewBinding{
		task: task.id, version: task.version, assignment: task.assignment,
		reviewer: reviewPrincipal{actor: "agent", origin: ledger.Origin{AgentID: privateID(4), SessionID: privateID(6), ContextID: privateID(5)}, grant: privateID(70)},
		subject:  private, acceptance: artifact.sha256, check: artifact.sha256, config: artifact.sha256,
		evidence: []reviewSubject{artifact, private}, authorizationRevision: 7, deadline: 100,
	}
}

func TestReviewBindingRejectsSingleMemberSubstitution(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name   string
		change func(*reviewBinding)
	}{
		{"task_identity", func(b *reviewBinding) { b.task = privateID(99) }},
		{"task_version", func(b *reviewBinding) { b.version++ }},
		{"assignment_identity", func(b *reviewBinding) { b.assignment.id = privateID(99) }},
		{"assignment_generation", func(b *reviewBinding) { b.assignment.generation++ }},
		{"assignee_agent", func(b *reviewBinding) { b.assignment.binding.AgentID = privateID(99) }},
		{"assignee_context", func(b *reviewBinding) { b.assignment.binding.ContextID = privateID(99) }},
		{"reviewer_principal", func(b *reviewBinding) { b.reviewer.actor = "owner" }},
		{"reviewer_agent", func(b *reviewBinding) { b.reviewer.origin.AgentID = privateID(99) }},
		{"reviewer_session", func(b *reviewBinding) { b.reviewer.origin.SessionID = privateID(99) }},
		{"reviewer_context", func(b *reviewBinding) { b.reviewer.origin.ContextID = privateID(99) }},
		{"reviewer_grant", func(b *reviewBinding) { b.reviewer.grant = privateID(99) }},
		{"acceptance_digest", func(b *reviewBinding) { b.acceptance[0]++ }},
		{"check_digest", func(b *reviewBinding) { b.check[0]++ }},
		{"config_digest", func(b *reviewBinding) { b.config[0]++ }},
		{"authorization_revision", func(b *reviewBinding) { b.authorizationRevision++ }},
		{"deadline", func(b *reviewBinding) { b.deadline++ }},
		{"evidence_removed", func(b *reviewBinding) { b.evidence = b.evidence[:1] }},
		{"evidence_added", func(b *reviewBinding) { b.evidence = append(b.evidence, b.subject) }},
		{"evidence_order", func(b *reviewBinding) { b.evidence[0], b.evidence[1] = b.evidence[1], b.evidence[0] }},
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
	for _, target := range []string{"subject", "artifact_evidence", "private_evidence"} {
		for _, row := range []struct {
			name   string
			change func(*reviewSubject)
		}{
			{"kind", func(s *reviewSubject) { s.kind = "future" }},
			{"source_sequence", func(s *reviewSubject) { s.sourceSequence++ }},
			{"revision", func(s *reviewSubject) { s.revision++ }},
			{"agent", func(s *reviewSubject) { s.binding.AgentID = privateID(99) }},
			{"context", func(s *reviewSubject) { s.binding.ContextID = privateID(99) }},
			{"scope", func(s *reviewSubject) { s.scope = "agent" }},
			{"slot", func(s *reviewSubject) { s.slot = "memory" }},
			{"digest", func(s *reviewSubject) { s.sha256[0]++ }},
			{"bytes", func(s *reviewSubject) { s.bytes++ }},
		} {
			t.Run(target+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				want, got := reviewRuleBinding(), reviewRuleBinding()
				subject := &got.subject
				if target == "artifact_evidence" {
					subject = &got.evidence[0]
				} else if target == "private_evidence" {
					subject = &got.evidence[1]
				}
				row.change(subject)
				if err := matchReviewBinding(want, got); !errors.Is(err, ErrInvalid) {
					t.Fatalf("substituted occurrence accepted: %v", err)
				}
			})
		}
	}
}

func TestReviewBindingMatchesOwnedEquivalentValues(t *testing.T) {
	t.Parallel()
	want, got := reviewRuleBinding(), reviewRuleBinding()
	before := got
	before.evidence = slices.Clone(got.evidence)
	if err := matchReviewBinding(want, got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, got) {
		t.Fatal("binding comparison changed the callback")
	}
}
