package agents

import (
	"errors"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// ErrTaskFactsStale means a continuation no longer names the committed head.
// Discard the incomplete enumeration and start again with a nil cursor.
var ErrTaskFactsStale = errors.New("agents: task facts revision is stale")

// TaskFactsQuery selects one current assignee and one combined page limit.
// The owner uses a zero GrantID; an Agent selects one current inspect chain.
type TaskFactsQuery struct {
	AgentID uuid.UUID
	GrantID uuid.UUID
	Limit   int
	Cursor  *TaskFactsCursor
}

// TaskFactsCursor is a same-head traversal coordinate, not an authority token.
// Limit may change between pages; every other query scope must match.
type TaskFactsCursor struct {
	ColonyID        uuid.UUID
	Actor           string
	Origin          ledger.Origin
	AgentID         uuid.UUID
	GrantID         uuid.UUID
	ThroughSequence uint64
	AfterTaskID     uuid.UUID
}

// TaskFact is owned, fixed metadata for an assigned waiting or active task.
// DescriptionRef identifies the original description, not permission to read it.
// Sequence is the last task effect; readiness is qualified by the page's head.
type TaskFact struct {
	TaskID         uuid.UUID
	Version        int64
	State          string
	Assignment     ledger.Assignment
	DescriptionRef ledger.ArtifactRef
	Sequence       uint64
	Readiness      string
	Reason         string
}

// TaskFactCounts counts all visible eligible tasks, not just this page's rows.
type TaskFactCounts struct {
	WaitingReady   int
	WaitingBlocked int
	Active         int
}

// TaskFactsPage contains at most Limit rows across its three non-nil buckets.
// Each bucket is ordered by raw TaskID bytes. ThroughSequence is the committed
// head for the rows and complete Counts. Next is nil when enumeration is done.
type TaskFactsPage struct {
	AgentID         uuid.UUID
	WaitingReady    []TaskFact
	WaitingBlocked  []TaskFact
	Active          []TaskFact
	Counts          TaskFactCounts
	ThroughSequence uint64
	Next            *TaskFactsCursor
}
