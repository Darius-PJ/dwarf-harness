package agents

import (
	"context"
	"errors"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func reviewOperation(kind string) string {
	if kind == "failure" {
		return "task.fail"
	}
	return "task.complete"
}

func (s *Service) reviewParticipant(ctx context.Context, principal ledger.ReviewPrincipal, b ledger.ReviewBinding, kind string, requester bool, task ledger.RuntimeTaskChanged, through uint64) ([]grantRules, error) {
	if !s.p.registeredOrigin(principal.Origin) {
		return nil, ErrDenied
	}
	var chain []grantRules
	if principal.Actor == "agent" {
		if !slices.Contains(task.Audience, principal.Origin.AgentID) || requester && kind == "failure" && ledger.Binding(bindingOf(principal.Origin)) != b.Assignee {
			return nil, ErrDenied
		}
		var recipients []Binding
		if requester {
			recipients = []Binding{bindingOf(b.Reviewer.Origin)}
		}
		var err error
		chain, err = s.authorize(ctx, principal.Origin, principal.GrantID, reviewOperation(kind), []ledger.Resource{{Kind: "task", TaskID: b.TaskID}}, recipients, 0, through)
		if err != nil {
			return nil, err
		}
	} else if principal.Actor != "owner" || principal.GrantID != (uuid.UUID{}) {
		return nil, ErrInvalid
	}
	return chain, nil
}

func (s *Service) reviewReadRights(ctx context.Context, principal ledger.ReviewPrincipal, b ledger.ReviewBinding, through uint64, sources map[int64]ledger.Record) error {
	refs := append([]ledger.SubjectRef{b.Subject}, b.RequiredEvidence...)
	for _, subject := range refs {
		if _, err := subject.MarshalJSON(); err != nil {
			return ErrInvalid
		}
		switch subject.Kind {
		case "artifact":
			if principal.Actor != "owner" {
				if _, err := s.artifactAuthority(ctx, principal.Origin, subject.Artifact, uuid.UUID{}, through, sources); err != nil {
					return err
				}
			}
		case "private_revision":
			ref := subject.PrivateRevision
			if principal.Actor == "owner" {
				continue
			}
			caller := ledger.Binding(bindingOf(principal.Origin))
			if caller.AgentID != ref.Binding.AgentID || ref.Scope == "context" && caller != ref.Binding {
				return ErrDenied
			}
			allowed := false
			for _, id := range s.ownGrantIDs(principal.Origin) {
				_, err := s.authorize(ctx, principal.Origin, id, "private.read", []ledger.Resource{{Kind: "context", Binding: caller, Scope: ref.Scope, Slots: []string{ref.Slot}}}, nil, ref.Bytes, through)
				if err == nil {
					allowed = true
					break
				}
				if !errors.Is(err, ErrDenied) {
					return err
				}
			}
			if !allowed {
				return ErrDenied
			}
		default:
			return ErrDenied
		}
	}
	return nil
}

// Metadata witnesses are bounded by the distinct references in this operation;
// neither body bytes nor materialized Markdown are consulted.
func (s *Service) reviewWitnesses(ctx context.Context, b ledger.ReviewBinding, through uint64) (map[int64]ledger.Record, error) {
	refs := append([]ledger.SubjectRef{b.Subject}, b.RequiredEvidence...)
	sequences := make([]int64, 0, len(refs))
	for _, subject := range refs {
		if _, err := subject.MarshalJSON(); err != nil {
			return nil, ErrInvalid
		}
		switch subject.Kind {
		case "artifact":
			sequences = append(sequences, subject.Artifact.SourceSequence)
		case "private_revision":
			sequences = append(sequences, subject.PrivateRevision.SourceSequence)
		default:
			return nil, ErrDenied
		}
	}
	return s.sourceRecords(ctx, sequences, through)
}

func (s *Service) reviewSources(b ledger.ReviewBinding, sources map[int64]ledger.Record) error {
	refs := append([]ledger.SubjectRef{b.Subject}, b.RequiredEvidence...)
	for _, subject := range refs {
		sequence := subject.Artifact.SourceSequence
		if subject.Kind == "private_revision" {
			sequence = subject.PrivateRevision.SourceSequence
		}
		source := sources[sequence]
		if subject.Kind == "artifact" {
			if !slices.Contains(source.BodyRefs, subject.Artifact.SHA256) {
				return ErrUnavailable
			}
			// Task descriptions additionally carry their authoritative byte size.
			if source.Type == ledger.TypeRuntimeTaskChanged {
				p, err := ledger.DecodeRuntimeTaskChanged(source.Payload)
				if err != nil {
					return err
				}
				if p.Description.SHA256 == subject.Artifact.SHA256 && p.Description.Bytes != subject.Artifact.Bytes {
					return ErrInvalid
				}
			}
			continue
		}
		ref := subject.PrivateRevision
		if ref == nil || s.p.contexts[ref.Binding.ContextID] != ref.Binding.AgentID || source.AgentID != ref.Binding.AgentID || source.ContextID != ref.Binding.ContextID || s.p.agents[ref.Binding.AgentID].session != source.ExecutionSessionID {
			return ErrInvalid
		}
		var revision ledger.PrivateRevision
		var err error
		switch source.Type {
		case ledger.TypePrivateRevision:
			revision, err = ledger.DecodePrivateRevision(source.Payload)
		case ledger.TypePrivateScopedRevision:
			var scoped ledger.PrivateScopedRevision
			scoped, err = ledger.DecodePrivateScopedRevision(source.Payload)
			revision = ledger.PrivateRevision(scoped)
		default:
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if revision.Scope != ref.Scope || revision.Revision != ref.Revision || !slices.ContainsFunc(revision.Changes, func(c ledger.PrivateChange) bool {
			return c.Slot == ref.Slot && (c.Mode == "append" || c.Mode == "replace") && c.SHA256 == ref.SHA256 && c.Bytes == ref.Bytes
		}) {
			return ErrInvalid
		}
	}
	return nil
}

func reviewDenied(err error) error {
	if errors.Is(err, ErrDenied) || errors.Is(err, ErrNotBound) || errors.Is(err, ErrUnavailable) {
		return ErrDenied
	}
	return err
}
