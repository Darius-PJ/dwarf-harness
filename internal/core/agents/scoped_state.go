package agents

import (
	"context"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

type grantState struct {
	definition ledger.RuntimeGrantIssued
	issuer     string
	origin     ledger.Origin
	revoked    bool
	sequence   uint64
}

type scopedTaskState struct {
	record              ledger.RuntimeTaskChanged
	sequence            uint64
	descriptionSequence uint64
}
type scopedWorkState struct {
	admission                   ledger.RuntimeWorkAdmitted
	origin                      ledger.Origin
	turn                        uuid.UUID
	version                     int64
	dispatched, ended, released bool
	outcome                     string
	sequence                    uint64
	receipt                     ScopedReceipt
}

type scopedPlan struct {
	authority    int64
	grant        *grantState
	task         *scopedTaskState
	dependencies []scopedTaskState
	work         *scopedWorkState
	budget       map[ledger.ScopeKey]budgetAccount
	private      map[scopeKey]*scopeState
}

func (g grantState) rules() grantRules {
	p := g.definition
	recipients := make([]Binding, len(p.Recipients))
	for i, binding := range p.Recipients {
		recipients[i] = Binding(binding)
	}
	rules := grantRules{id: p.GrantID, parent: p.ParentGrantID, grantee: Binding(p.Grantee), operations: p.Operations, resources: p.Resources, dataClass: p.DataClass, recipients: recipients, limits: p.Limits, depth: p.DelegationDepth, lifetime: p.Lifetime, revoked: g.revoked}
	if p.TaskBinding != nil {
		rules.task = *p.TaskBinding
	}
	return rules
}

func originOf(ids submissionIDs) ledger.Origin {
	return ledger.Origin{AgentID: ids.agent, SessionID: ids.session, ContextID: ids.context}
}
func bindingOf(origin ledger.Origin) Binding { return Binding{origin.AgentID, origin.ContextID} }

func (p *projection) registeredOrigin(origin ledger.Origin) bool {
	agent, ok := p.agents[origin.AgentID]
	return ok && agent.session == origin.SessionID && p.contexts[origin.ContextID] == origin.AgentID
}

// Live maps and valid above-maximum proofs serve the current accepted prefix.
// Other misses consult history, including pruned terminal identities.
func (s *Service) loadGrant(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (grantState, bool, error) {
	if grant, ok := s.p.grants[id]; ok && through == s.p.through {
		return grant, true, nil
	}
	if s.p.identityAbsent(id, s.p.identities.grant, through) {
		return grantState{}, false, nil
	}
	if grant, found, hit := s.history.grant(id, through); hit {
		return grant, found, nil
	}
	value, err := readHistory(ctx, reads, 0, through, grantHistoryKey{id}, func(ctx context.Context) (historyGrantValue, error) {
		var grant grantState
		found := false
		err := s.scan(ctx, 0, through, func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				switch record.Type {
				case ledger.TypeRuntimeGrantIssued:
					p, err := ledger.DecodeRuntimeGrantIssued(record.Payload)
					if err != nil {
						return err
					}
					if p.GrantID == id {
						grant = grantState{definition: p, issuer: string(record.Actor), origin: ledger.Origin{AgentID: record.AgentID, SessionID: record.ExecutionSessionID, ContextID: record.ContextID}, sequence: record.Seq}
						found = true
					}
				case ledger.TypeRuntimeGrantRevoked:
					p, err := ledger.DecodeRuntimeGrantRevoked(record.Payload)
					if err != nil {
						return err
					}
					if p.GrantID == id {
						grant.revoked = true
					}
				}
			}
			return nil
		})
		if err != nil || !found {
			return historyGrantValue{}, err
		}
		payload, err := grant.definition.Encode()
		return historyGrantValue{payload, grant.issuer, grant.origin, grant.revoked, grant.sequence}, err
	})
	if err != nil || value.Sequence == 0 {
		if err == nil {
			s.history.putGrant(id, through, grantState{}, false)
		}
		return grantState{}, false, err
	}
	definition, err := ledger.DecodeRuntimeGrantIssued(value.Definition)
	if err != nil {
		return grantState{}, false, err
	}
	grant := grantState{definition: definition, issuer: value.Issuer, origin: value.Origin, revoked: value.Revoked, sequence: value.Sequence}
	s.history.putGrant(id, through, grant, true)
	return grant, true, nil
}

func (s *Service) loadTask(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (scopedTaskState, bool, error) {
	if task, ok := s.p.tasks[id]; ok && through == s.p.through {
		return task, true, nil
	}
	if s.p.identityAbsent(id, s.p.identities.task, through) {
		return scopedTaskState{}, false, nil
	}
	if task, found, hit := s.history.task(id, through); hit {
		return task, found, nil
	}
	value, err := readHistory(ctx, reads, 0, through, taskHistoryKey{id}, func(ctx context.Context) (historyTaskValue, error) {
		var task scopedTaskState
		found := false
		err := s.scan(ctx, 0, through, func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				switch record.Type {
				case ledger.TypeRuntimeTaskChanged:
					p, err := ledger.DecodeRuntimeTaskChanged(record.Payload)
					if err != nil {
						return err
					}
					if p.TaskID == id {
						if p.Action == "create" {
							task.descriptionSequence = record.Seq
						}
						task.record, task.sequence, found = p, record.Seq, true
					}
				case ledger.TypeRuntimeReviewResult:
					p, err := ledger.DecodeRuntimeReviewResult(record.Payload)
					if err != nil {
						return err
					}
					if p.Binding.TaskID == id && p.Verdict != "reject" {
						task = taskAfterReview(task, p, record.Seq)
					}
				}
			}
			return nil
		})
		if err != nil || !found {
			return historyTaskValue{}, err
		}
		payload, err := task.record.Encode()
		return historyTaskValue{payload, task.sequence, task.descriptionSequence}, err
	})
	if err != nil || value.Sequence == 0 {
		if err == nil {
			s.history.putTask(id, through, scopedTaskState{}, false)
		}
		return scopedTaskState{}, false, err
	}
	record, err := ledger.DecodeRuntimeTaskChanged(value.Record)
	if err != nil {
		return scopedTaskState{}, false, err
	}
	task := scopedTaskState{record: record, sequence: value.Sequence, descriptionSequence: value.DescriptionSequence}
	s.history.putTask(id, through, task, true)
	return task, true, nil
}

func (s *Service) loadWork(ctx context.Context, reads *historyReadSet, id uuid.UUID, through uint64) (scopedWorkState, bool, error) {
	if work, ok := s.p.work[id]; ok && through == s.p.through {
		return work, true, nil
	}
	if s.p.identityAbsent(id, s.p.identities.work, through) {
		return scopedWorkState{}, false, nil
	}
	if work, found, hit := s.history.work(id, through); hit {
		return work, found, nil
	}
	value, err := readHistory(ctx, reads, 0, through, workHistoryKey{id}, func(ctx context.Context) (*historyWorkValue, error) {
		var work scopedWorkState
		found := false
		err := s.scan(ctx, 0, through, func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				switch record.Type {
				case ledger.TypeRuntimeWorkAdmitted:
					p, err := ledger.DecodeRuntimeWorkAdmitted(record.Payload)
					if err != nil {
						return err
					}
					if p.WorkID == id {
						work = scopedWorkState{admission: p, origin: ledger.Origin{AgentID: record.AgentID, SessionID: record.ExecutionSessionID, ContextID: record.ContextID}, turn: record.TurnID, version: p.Version, sequence: record.Seq}
						found = true
					}
				case ledger.TypeRuntimeWorkDispatched:
					p, err := ledger.DecodeRuntimeWorkDispatched(record.Payload)
					if err != nil {
						return err
					}
					if p.WorkID == id {
						work.dispatched, work.version = true, p.Version
					}
				case ledger.TypeRuntimeWorkEnded:
					p, err := ledger.DecodeRuntimeWorkEnded(record.Payload)
					if err != nil {
						return err
					}
					if p.WorkID == id {
						work.ended, work.version, work.outcome = true, p.Version, p.Outcome
						work.released = releasesWork(work.admission.Kind, p.Outcome)
					}
				case ledger.TypeRuntimeScopedReceipt:
					p, err := ledger.DecodeRuntimeScopedReceipt(record.Payload)
					if err != nil {
						return err
					}
					if p.WorkID == id {
						work.receipt = scopedReceiptAt(p, record.Seq)
					}
				}
			}
			return nil
		})
		if err != nil || !found {
			return nil, err
		}
		payload, err := work.admission.Encode()
		return &historyWorkValue{payload, work.origin, work.turn, work.version, work.dispatched, work.ended, work.released, work.outcome, work.sequence, historyScopedReceipt{work.receipt, historyResource(work.receipt.Target)}}, err
	})
	if err != nil || value == nil {
		if err == nil {
			s.history.putWork(id, through, scopedWorkState{}, false)
		}
		return scopedWorkState{}, false, err
	}
	admission, err := ledger.DecodeRuntimeWorkAdmitted(value.Admission)
	if err != nil {
		return scopedWorkState{}, false, err
	}
	work := scopedWorkState{admission: admission, origin: value.Origin, turn: value.Turn, version: value.Version, dispatched: value.Dispatched, ended: value.Ended, released: value.Released, outcome: value.Outcome, sequence: value.Sequence, receipt: value.Receipt.receipt()}
	s.history.putWork(id, through, work, true)
	return work, true, nil
}

func releasesWork(kind, outcome string) bool {
	if kind == "turn" {
		return outcome == "released" || outcome == "cancelled" || outcome == "interrupted"
	}
	return outcome == "released_unsent" || outcome == "succeeded" || outcome == "failed" || outcome == "interrupted"
}

func (s *Service) assignmentUnused(ctx context.Context, reads *historyReadSet, assignment ledger.AssignmentInput, through uint64) (idUnused, contextUnused bool, err error) {
	if s.p.identityAbsent(assignment.AssignmentID, s.p.identities.assignment, through) {
		key := scopeKey{assignment.Assignee.AgentID, assignment.Assignee.ContextID}
		if task, known := s.p.contextAssociation(key, through); known {
			return true, task == (uuid.UUID{}), nil
		}
	}
	key := historyKey{kind: historyAssignment, id: assignment.AssignmentID, context: assignment.Assignee.ContextID}
	if e, hit := s.history.get(key, 0, through); hit {
		if e.found && len(e.data) == 2 && e.data[0] <= 1 && e.data[1] <= 1 {
			return e.data[0] == 1, e.data[1] == 1, nil
		}
		s.history.discard(key)
	}
	answer, err := readHistory(ctx, reads, 0, through, assignmentHistoryKey{assignment}, func(ctx context.Context) ([2]bool, error) {
		idUnused, contextUnused := true, true
		err := s.scan(ctx, 0, through, func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				if record.Type != ledger.TypeRuntimeTaskChanged {
					continue
				}
				p, err := ledger.DecodeRuntimeTaskChanged(record.Payload)
				if err != nil {
					return err
				}
				if p.Assignment != nil {
					idUnused = idUnused && p.Assignment.AssignmentID != assignment.AssignmentID
					contextUnused = contextUnused && p.Assignment.Assignee.ContextID != assignment.Assignee.ContextID
				}
			}
			return nil
		})
		return [2]bool{idUnused, contextUnused}, err
	})
	idUnused, contextUnused = answer[0], answer[1]
	if err == nil {
		data := []byte{0, 0}
		if idUnused {
			data[0] = 1
		}
		if contextUnused {
			data[1] = 1
		}
		s.history.put(historyEntry{key: key, from: through, found: true, data: data})
	}
	return idUnused, contextUnused, err
}

func (s *Service) sourceRecord(ctx context.Context, reads *historyReadSet, sequence int64, through uint64) (ledger.Record, bool, error) {
	if sequence <= 0 || uint64(sequence) > through {
		return ledger.Record{}, false, nil
	}
	sources, err := s.sourceRecords(ctx, reads, []int64{sequence}, through)
	source, found := sources[sequence]
	return source, found, err
}

func (s *Service) verifyArtifactSource(ctx context.Context, reads *historyReadSet, artifact ledger.ArtifactRef, through uint64) error {
	source, found, err := s.sourceRecord(ctx, reads, artifact.SourceSequence, through)
	if err != nil {
		return err
	}
	if !found || !slices.Contains(source.BodyRefs, artifact.SHA256) {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) registeredResource(ctx context.Context, reads *historyReadSet, resource ledger.Resource, through uint64) (bool, error) {
	switch resource.Kind {
	case "context":
		return s.p.contexts[resource.Binding.ContextID] == resource.Binding.AgentID, nil
	case "task":
		if resource.TaskID == (uuid.UUID{}) {
			return true, nil
		}
		_, found, err := s.loadTask(ctx, reads, resource.TaskID, through)
		return found, err
	case "artifact":
		err := s.verifyArtifactSource(ctx, reads, resource.Artifact, through)
		return err == nil, err
	case "workspace":
		if s.p.contexts[resource.Binding.ContextID] != resource.Binding.AgentID {
			return false, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		key := historyKey{kind: historyWorkspace, id: resource.WorkspaceID}
		if e, hit := s.history.get(key, 0, through); hit {
			if len(e.data) == 0 && e.first == 0 && e.last == 0 && e.task == (uuid.UUID{}) {
				return e.found, nil
			}
			s.history.discard(key)
		}
		workspace := resource.WorkspaceID
		answer, err := readHistory(ctx, reads, 0, through, workspaceHistoryKey{workspace}, func(ctx context.Context) (bool, error) {
			found := false
			err := s.scan(ctx, 0, through, func(commit ledger.Commit) error {
				for _, record := range commit.Records {
					if record.Type != ledger.TypeWorkspaceRegistered {
						continue
					}
					p, err := ledger.DecodeWorkspaceRegistered(record.Payload)
					if err != nil {
						return err
					}
					found = found || p.WorkspaceID == workspace
				}
				return nil
			})
			return found, err
		})
		if err == nil {
			s.history.put(historyEntry{key: key, from: through, found: answer})
		}
		return answer, err
	case "external":
		return true, nil
	default:
		return false, nil
	}
}

func (s *Service) contextTask(ctx context.Context, reads *historyReadSet, origin ledger.Origin, through uint64) (scopedTaskState, bool, error) {
	if s.p.agents[origin.AgentID].general == origin.ContextID {
		return scopedTaskState{}, false, nil
	}
	if id, known := s.p.contextAssociation(scopeKey{origin.AgentID, origin.ContextID}, through); known {
		if id == (uuid.UUID{}) {
			return scopedTaskState{}, false, nil
		}
		return s.loadTask(ctx, reads, id, through)
	}
	if through == s.p.through {
		for _, task := range s.p.tasks {
			if task.record.Assignment != nil && task.record.Assignment.Assignee.ContextID == origin.ContextID {
				return task, true, nil
			}
		}
	}
	contextID := origin.ContextID
	id, err := readHistory(ctx, reads, 0, through, contextTaskHistoryKey{contextID}, func(ctx context.Context) (uuid.UUID, error) {
		var id uuid.UUID
		err := s.scan(ctx, 0, through, func(commit ledger.Commit) error {
			for _, record := range commit.Records {
				if record.Type != ledger.TypeRuntimeTaskChanged {
					continue
				}
				task, err := ledger.DecodeRuntimeTaskChanged(record.Payload)
				if err != nil {
					return err
				}
				if task.Assignment != nil && task.Assignment.Assignee.ContextID == contextID {
					id = task.TaskID
				}
			}
			return nil
		})
		return id, err
	})
	if err != nil || id == (uuid.UUID{}) {
		return scopedTaskState{}, false, err
	}
	return s.loadTask(ctx, reads, id, through)
}
