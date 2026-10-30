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

// ListTaskFacts lists the owner's committed task facts for one assignee.
func (s *Service) ListTaskFacts(ctx context.Context, q TaskFactsQuery) (TaskFactsPage, error) {
	return s.listTaskFacts(ctx, nil, q)
}

// ListTaskFacts lists committed task facts visible through the selected inspect
// chain and this agent's audience membership. It grants no execution authority.
func (a *Agent) ListTaskFacts(ctx context.Context, q TaskFactsQuery) (TaskFactsPage, error) {
	if a == nil || a.service == nil {
		return TaskFactsPage{}, ErrNotBound
	}
	return a.service.listTaskFacts(ctx, &a.ids, q)
}

func (s *Service) listTaskFacts(ctx context.Context, bound *submissionIDs, q TaskFactsQuery) (TaskFactsPage, error) {
	done, err := s.scopedQuery(ctx, bound)
	if err != nil {
		return TaskFactsPage{}, err
	}
	defer done()
	if !validID(q.AgentID) || q.Limit <= 0 || bound == nil && q.GrantID != (uuid.UUID{}) {
		return TaskFactsPage{}, ErrInvalid
	}
	if q.Limit > s.limits.MaxLiveTasks {
		return TaskFactsPage{}, ErrTooLarge
	}
	if err := s.taskListingPrecheck(ctx, bound, q.GrantID); err != nil {
		return TaskFactsPage{}, err
	}
	coordinate := TaskFactsCursor{
		ColonyID: s.store.Colony(), Actor: "owner", AgentID: q.AgentID,
		GrantID: q.GrantID, ThroughSequence: s.p.through,
	}
	if bound != nil {
		coordinate.Actor, coordinate.Origin = "agent", originOf(*bound)
	}
	after := uuid.UUID{}
	if cursor := q.Cursor; cursor != nil {
		if cursor.ColonyID != coordinate.ColonyID || cursor.Actor != coordinate.Actor ||
			cursor.Origin != coordinate.Origin || cursor.AgentID != q.AgentID || cursor.GrantID != q.GrantID ||
			cursor.ThroughSequence == 0 || !validID(cursor.AfterTaskID) {
			return TaskFactsPage{}, ErrInvalid
		}
		if cursor.ThroughSequence != s.p.through {
			return TaskFactsPage{}, ErrTaskFactsStale
		}
		after = cursor.AfterTaskID
	}

	// Count only authorized candidates, retaining the smallest Limit+1 IDs.
	// No caller-sized allocation or second retained task index is needed.
	ids := []uuid.UUID{}
	counts := TaskFactCounts{}
	for id, task := range s.p.tasks {
		if err := ctx.Err(); err != nil {
			return TaskFactsPage{}, err
		}
		if task.record.Assignment == nil || task.record.Assignment.Assignee.AgentID != q.AgentID ||
			task.record.State != "waiting" && task.record.State != "active" {
			continue
		}
		if bound != nil {
			_, err := s.authorize(ctx, originOf(*bound), q.GrantID, "task.inspect", []ledger.Resource{{Kind: "task", TaskID: id}}, nil, 0, s.p.through)
			if errors.Is(err, ErrDenied) {
				continue
			}
			if err != nil {
				return TaskFactsPage{}, err
			}
			if !slices.Contains(task.record.Audience, bound.agent) {
				continue
			}
		}
		readiness, _, err := s.taskReadiness(ctx, task.record, bound, false)
		if err != nil {
			return TaskFactsPage{}, err
		}
		switch readiness {
		case "ready", "ready_queued":
			counts.WaitingReady++
		case "waiting_blocked":
			counts.WaitingBlocked++
		case "active":
			counts.Active++
		}
		if bytes.Compare(id[:], after[:]) <= 0 {
			continue
		}
		at, _ := slices.BinarySearchFunc(ids, id, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
		if at > q.Limit {
			continue
		}
		ids = slices.Insert(ids, at, id)
		if len(ids)-1 > q.Limit {
			ids = ids[:q.Limit+1]
		}
	}
	if err := ctx.Err(); err != nil {
		return TaskFactsPage{}, err
	}
	page := TaskFactsPage{
		AgentID: q.AgentID, WaitingReady: []TaskFact{}, WaitingBlocked: []TaskFact{}, Active: []TaskFact{},
		Counts: counts, ThroughSequence: s.p.through,
	}
	if len(ids) > q.Limit {
		ids = ids[:q.Limit]
		coordinate.AfterTaskID = ids[len(ids)-1]
		page.Next = &coordinate
	}
	for _, id := range ids {
		task := s.p.tasks[id]
		if task.descriptionSequence == 0 || task.descriptionSequence > math.MaxInt64 {
			return TaskFactsPage{}, ErrUnavailable
		}
		readiness, reason, err := s.taskReadiness(ctx, task.record, bound, false)
		if err != nil {
			return TaskFactsPage{}, err
		}
		fact := TaskFact{
			TaskID: id, Version: task.record.Version, State: task.record.State,
			Assignment:     *task.record.Assignment,
			DescriptionRef: ledger.ArtifactRef{SourceSequence: int64(task.descriptionSequence), SHA256: task.record.Description.SHA256, Bytes: task.record.Description.Bytes},
			Sequence:       task.sequence, Readiness: readiness, Reason: reason,
		}
		switch readiness {
		case "ready", "ready_queued":
			page.WaitingReady = append(page.WaitingReady, fact)
		case "waiting_blocked":
			page.WaitingBlocked = append(page.WaitingBlocked, fact)
		case "active":
			page.Active = append(page.Active, fact)
		}
	}
	return page, nil
}
