package agents

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

func (s *Service) TaskStatus(ctx context.Context, request TaskLookup) (TaskView, error) {
	return s.taskStatus(ctx, nil, request)
}
func (a *Agent) TaskStatus(ctx context.Context, request TaskLookup) (TaskView, error) {
	if a == nil || a.service == nil {
		return TaskView{}, ErrNotBound
	}
	return a.service.taskStatus(ctx, &a.ids, request)
}
func (s *Service) taskStatus(ctx context.Context, bound *submissionIDs, request TaskLookup) (TaskView, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return TaskView{}, err
	}
	defer done()
	if !validID(request.TaskID) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return TaskView{}, ErrInvalid
	}
	if bound != nil {
		if _, err := s.authorize(ctx, originOf(*bound), request.GrantID, "task.inspect", []ledger.Resource{{Kind: "task", TaskID: request.TaskID}}, nil, 0, s.p.through); err != nil {
			return TaskView{}, err
		}
	}
	task, found, err := s.loadTask(ctx, request.TaskID, s.p.through)
	if err != nil {
		return TaskView{}, err
	}
	if bound != nil && (!found || !slices.Contains(task.record.Audience, bound.agent)) {
		return TaskView{}, ErrDenied
	}
	if !found {
		return TaskView{}, ErrUnavailable
	}
	return s.taskView(ctx, task, bound)
}

func (s *Service) ListTasks(ctx context.Context, request TaskList) (TaskPage, error) {
	return s.listTasks(ctx, nil, request)
}
func (a *Agent) ListTasks(ctx context.Context, request TaskList) (TaskPage, error) {
	if a == nil || a.service == nil {
		return TaskPage{}, ErrNotBound
	}
	return a.service.listTasks(ctx, &a.ids, request)
}
func (s *Service) listTasks(ctx context.Context, bound *submissionIDs, request TaskList) (TaskPage, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return TaskPage{}, err
	}
	defer done()
	if request.Limit <= 0 || request.AfterTaskID != (uuid.UUID{}) && !validID(request.AfterTaskID) || bound == nil && request.GrantID != (uuid.UUID{}) {
		return TaskPage{}, ErrInvalid
	}
	if request.Limit > s.limits.MaxLiveTasks {
		return TaskPage{}, ErrTooLarge
	}
	if bound != nil {
		chain, err := s.currentChain(ctx, originOf(*bound), request.GrantID, s.p.through)
		if err != nil {
			return TaskPage{}, err
		}
		allowed := false
		for _, resource := range chain[0].resources {
			if resource.Kind != "task" || !validID(resource.TaskID) {
				continue
			}
			_, err := s.authorize(ctx, originOf(*bound), request.GrantID, "task.inspect", []ledger.Resource{resource}, nil, 0, s.p.through)
			if err == nil {
				allowed = true
				break
			}
			if !errors.Is(err, ErrDenied) {
				return TaskPage{}, err
			}
		}
		if !allowed {
			return TaskPage{}, ErrDenied
		}
	}
	// Terminal dependency facts may remain hot, but never enter the live list.
	// Keep only the next page and one authorized look-ahead, not a skipped-ID
	// cursor or a second unbounded task index.
	ids := []uuid.UUID{}
	for id, task := range s.p.tasks {
		if terminalTask(task.record.State) || bytes.Compare(id[:], request.AfterTaskID[:]) <= 0 {
			continue
		}
		if bound != nil {
			_, err := s.authorize(ctx, originOf(*bound), request.GrantID, "task.inspect", []ledger.Resource{{Kind: "task", TaskID: id}}, nil, 0, s.p.through)
			if errors.Is(err, ErrDenied) {
				continue
			}
			if err != nil {
				return TaskPage{}, err
			}
			if !slices.Contains(task.record.Audience, bound.agent) {
				continue
			}
		}
		at, _ := slices.BinarySearchFunc(ids, id, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		if at > request.Limit {
			continue
		}
		ids = slices.Insert(ids, at, id)
		if len(ids)-1 > request.Limit {
			ids = ids[:request.Limit+1]
		}
	}
	page := TaskPage{Tasks: make([]TaskView, 0, min(len(ids), request.Limit)), ThroughSequence: s.p.through}
	if len(ids) > request.Limit {
		ids = ids[:request.Limit]
		page.AfterTaskID = ids[len(ids)-1]
	}
	for _, id := range ids {
		view, err := s.taskView(ctx, s.p.tasks[id], bound)
		if err != nil {
			return TaskPage{}, err
		}
		page.Tasks = append(page.Tasks, view)
	}
	return page, nil
}

func (s *Service) taskView(ctx context.Context, task scopedTaskState, viewer *submissionIDs) (TaskView, error) {
	encoded, err := task.record.Encode()
	if err != nil {
		return TaskView{}, err
	}
	copy, err := ledger.DecodeRuntimeTaskChanged(encoded)
	if err != nil {
		return TaskView{}, err
	}
	if task.descriptionSequence == 0 || task.descriptionSequence > math.MaxInt64 {
		return TaskView{}, ErrUnavailable
	}
	state, reason, err := s.taskReadiness(ctx, task.record, viewer)
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: copy, DescriptionRef: ledger.ArtifactRef{SourceSequence: int64(task.descriptionSequence), SHA256: copy.Description.SHA256, Bytes: copy.Description.Bytes}, Sequence: task.sequence, Readiness: state, Reason: reason, ThroughSequence: s.p.through}, nil
}

func (s *Service) taskReadiness(ctx context.Context, task ledger.RuntimeTaskChanged, viewer *submissionIDs) (string, string, error) {
	if task.State != "waiting" {
		return task.State, "", nil
	}
	if task.Assignment == nil {
		return "waiting_blocked", "unassigned", nil
	}
	incomplete := false
	for _, id := range task.Dependencies {
		if viewer != nil {
			visible, err := s.canInspectTask(ctx, originOf(*viewer), id, s.p.through)
			if err != nil {
				return "", "", err
			}
			if !visible {
				return "waiting_blocked", "dependency_unavailable", nil
			}
		}
		dependency, found, err := s.loadTask(ctx, id, s.p.through)
		if err != nil {
			return "", "", err
		}
		if !found {
			return "waiting_blocked", "dependency_unavailable", nil
		}
		incomplete = incomplete || dependency.record.State != "completed"
	}
	if incomplete {
		return "waiting_blocked", "dependency", nil
	}
	if task.WaitReason != "" {
		return "waiting_blocked", task.WaitReason, nil
	}
	binding := task.Assignment.Assignee
	origin := ledger.Origin{AgentID: binding.AgentID, SessionID: s.p.agents[binding.AgentID].session, ContextID: binding.ContextID}
	start, startReason, err := s.operationReadiness(ctx, origin, "task.start", ledger.Resource{Kind: "task", TaskID: task.TaskID})
	if err != nil {
		return "", "", err
	}
	turn, turnReason, err := s.operationReadiness(ctx, origin, "turn.start", ledger.Resource{Kind: "context", Binding: binding, Scope: "context", Slots: []string{}})
	if err != nil {
		return "", "", err
	}
	if start == "waiting_blocked" {
		return start, startReason, nil
	}
	if turn == "waiting_blocked" {
		return turn, turnReason, nil
	}
	if turn == "ready_queued" {
		return turn, "turn_capacity", nil
	}
	if start == "ready_queued" {
		return start, "local_capacity", nil
	}
	return "ready", "", nil
}

// Each candidate is a whole chain for one operation. In particular, a partial
// task grant and a partial turn grant cannot be combined into either authority.
func (s *Service) operationReadiness(ctx context.Context, origin ledger.Origin, operation string, target ledger.Resource) (string, string, error) {
	permission, enrolled, enabled, queued := false, false, false, false
	for _, id := range s.ownGrantIDs(origin) {
		chain, err := s.authorize(ctx, origin, id, operation, []ledger.Resource{target}, nil, 0, s.p.through)
		if errors.Is(err, ErrDenied) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		permission = true
		scopes, err := chainScopes(origin, chain)
		if err != nil {
			return "", "", err
		}
		configured, positive, units, slots := true, true, true, true
		for _, scope := range scopes {
			account := s.p.book.accounts[scope]
			if !account.configured && scope.Kind != "context" {
				configured = false
				continue
			}
			_, _, available, err := capacityExposures(account, s.p.book.preparing[scope], !account.configured)
			if err != nil {
				return "", "", err
			}
			if operation == "turn.start" {
				positive = positive && (!account.configured || account.limits.TurnSlots > 0)
				slots = slots && available.TurnSlots > 0
			} else {
				positive = positive && (!account.configured || account.limits.LocalSlots > 0 && account.limits.LocalUnits > 0)
				units = units && available.LocalUnits > 0
				slots = slots && available.LocalSlots > 0
			}
		}
		if !configured {
			continue
		}
		enrolled = true
		if !positive {
			continue
		}
		enabled = true
		if operation == "turn.start" {
			for _, work := range s.p.work {
				if work.admission.Kind == "turn" && !work.released && work.origin.ContextID == origin.ContextID {
					slots = false
				}
			}
		}
		if !units {
			continue
		}
		if slots {
			return "ready", "", nil
		}
		queued = true
	}
	if queued {
		return "ready_queued", "", nil
	}
	if !permission {
		return "waiting_blocked", operation + "_denied", nil
	}
	if !enrolled {
		return "waiting_blocked", "capacity_unconfigured", nil
	}
	if !enabled {
		return "waiting_blocked", "capacity_disabled", nil
	}
	return "waiting_blocked", "local_units", nil
}
