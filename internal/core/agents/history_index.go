package agents

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"sync"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// These are discardable proofs about an accepted prefix, not authority. All
// accesses share the projection's Service.mu (or its exclusive Open replay).
// Checksums detect accidental damage; they are not same-process security seals.
type identityMaxima struct {
	task, work, grant, assignment uuid.UUID
	through                       uint64
	known                         bool
	checksum                      uint32
}

func newIdentityMaxima() identityMaxima {
	m := identityMaxima{known: true}
	m.checksum = m.sum()
	return m
}

func (m identityMaxima) sum() uint32 {
	var data [72]byte
	copy(data[0:16], m.task[:])
	copy(data[16:32], m.work[:])
	copy(data[32:48], m.grant[:])
	copy(data[48:64], m.assignment[:])
	binary.LittleEndian.PutUint64(data[64:], m.through)
	return crc32.ChecksumIEEE(data[:])
}

func (m identityMaxima) valid(through uint64) bool {
	return m.known && m.through == through && m.checksum == m.sum()
}

// A zero task with a valid accepted stamp means known never assigned. The zero
// value of the whole hint is unknown. Reassignment keeps the old context's task.
type contextAssociation struct {
	task     uuid.UUID
	accepted uint64
	checksum uint32
}

func (h contextAssociation) sum(key scopeKey) uint32 {
	var data [56]byte
	copy(data[0:16], key.agent[:])
	copy(data[16:32], key.context[:])
	copy(data[32:48], h.task[:])
	binary.LittleEndian.PutUint64(data[48:], h.accepted)
	return crc32.ChecksumIEEE(data[:])
}

func (p *projection) contextAssociation(key scopeKey, through uint64) (uuid.UUID, bool) {
	// The coverage check rules out historical/future queries and incomplete
	// replay. Unchanged contexts keep their own stamps across later commits.
	if through != p.through || !p.identities.valid(through) {
		return uuid.UUID{}, false
	}
	state := p.scopes[key]
	if state == nil {
		return uuid.UUID{}, false
	}
	h := state.association
	if h.accepted == 0 || h.accepted > through || h.checksum != h.sum(key) {
		return uuid.UUID{}, false
	}
	return h.task, true
}

func (p *projection) setContextAssociation(key scopeKey, task uuid.UUID, through uint64) {
	if state := p.scopes[key]; state != nil {
		h := contextAssociation{task: task, accepted: through}
		h.checksum = h.sum(key)
		state.association = h
	}
}

func (p *projection) identityAbsent(id, maximum uuid.UUID, through uint64) bool {
	return through == p.through && p.identities.valid(through) && bytes.Compare(id[:], maximum[:]) > 0
}

func widenIdentity(maximum *uuid.UUID, id uuid.UUID) {
	if bytes.Compare(id[:], maximum[:]) > 0 {
		*maximum = id
	}
}

// publishHistory runs only after the entire commit is accepted, immediately
// before publishing p.through. It also covers the preflighted live-review path,
// which introduces none of these identities but still advances coverage.
func (p *projection) publishHistory(g *privateGroup, scoped *scopedGroup, through uint64) {
	m := p.identities
	if m.valid(p.through) {
		if scoped != nil {
			if scoped.task != nil {
				widenIdentity(&m.task, scoped.task.TaskID)
				if scoped.task.Assignment != nil {
					widenIdentity(&m.assignment, scoped.task.Assignment.AssignmentID)
				}
			}
			if scoped.issue != nil {
				widenIdentity(&m.grant, scoped.issue.GrantID)
			}
			if scoped.admission != nil {
				widenIdentity(&m.work, scoped.admission.WorkID)
			}
		}
		m.through = through
		m.checksum = m.sum()
		p.identities = m
	} else {
		// A suffix cannot recover a lost complete-history maximum. A new
		// projection's from-zero replay can; until then use original scans.
		p.identities = identityMaxima{}
	}
	if g != nil && g.kind != "" {
		p.setContextAssociation(scopeKey{g.ids.agent, g.ids.context}, uuid.UUID{}, through)
	}
	if scoped != nil && scoped.task != nil && scoped.task.Assignment != nil {
		binding := scoped.task.Assignment.Assignee
		p.setContextAssociation(scopeKey{binding.AgentID, binding.ContextID}, scoped.task.TaskID, through)
	}
}

const (
	historyCommand byte = iota + 1
	historyGrant
	historyTask
	historyWork
	historyReview
	historyAssignment
	historySource
	historyWorkspace
	historyAckRead

	historyEntryLimit = 256
	historyByteLimit  = 8 << 20
)

type historyKey struct {
	kind        byte
	id, context uuid.UUID
	sequence    uint64
}

// Encoded metadata and one copied opaque Reader checkpoint are retained. A
// selected record never keeps its reader's entire commit backing buffer alive.
type historyEntry struct {
	key                      historyKey
	from, after, first, last uint64
	task                     uuid.UUID
	found                    bool
	data                     []byte
	checkpoint               *ledger.ReadCheckpoint
	checksum                 uint32
}

func (e historyEntry) retainedCharge() int {
	charge := len(e.data) + 128
	if e.checkpoint != nil {
		charge += 256
	}
	return charge
}

func (e historyEntry) validCheckpointWrapper() bool {
	if e.key.kind != historyAckRead {
		return e.checkpoint == nil
	}
	return e.key == (historyKey{kind: historyAckRead}) && e.checkpoint != nil &&
		len(e.data) == 0 && e.found && e.after == 0 && e.first == 0 &&
		e.last == 0 && e.task == (uuid.UUID{})
}

func (e historyEntry) sum() uint32 {
	var metadata [91]byte
	metadata[0] = e.key.kind
	copy(metadata[1:17], e.key.id[:])
	copy(metadata[17:33], e.key.context[:])
	binary.LittleEndian.PutUint64(metadata[33:41], e.key.sequence)
	binary.LittleEndian.PutUint64(metadata[41:49], e.from)
	binary.LittleEndian.PutUint64(metadata[49:57], e.after)
	binary.LittleEndian.PutUint64(metadata[57:65], e.first)
	binary.LittleEndian.PutUint64(metadata[65:73], e.last)
	copy(metadata[73:89], e.task[:])
	if e.found {
		metadata[89] = 1
	}
	if e.checkpoint != nil {
		metadata[90] = 1
	}
	return crc32.Update(crc32.ChecksumIEEE(metadata[:]), crc32.IEEETable, e.data)
}

func historyCoverageSum(through uint64) uint32 {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], through)
	return crc32.ChecksumIEEE(data[:])
}

// This is the one instance-private historical LRU. Its mutex never spans I/O
// or acquisition of Service.mu. Projection callers take Service.mu first;
// unlocked command/source readers touch only this mutex.
type historyIndex struct {
	mu       sync.Mutex
	through  uint64
	checksum uint32
	entries  map[historyKey]*list.Element
	lru      list.List
	bytes    int
	disabled bool
}

func newHistoryIndex() *historyIndex {
	return &historyIndex{checksum: historyCoverageSum(0), entries: make(map[historyKey]*list.Element)}
}

func (h *historyIndex) clearLocked() {
	clear(h.entries)
	h.lru.Init()
	h.bytes = 0
}

func (h *historyIndex) removeLocked(key historyKey) {
	if node := h.entries[key]; node != nil {
		h.bytes -= node.Value.(*historyEntry).retainedCharge()
		delete(h.entries, key)
		h.lru.Remove(node)
	}
}

func (h *historyIndex) discard(key historyKey) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.removeLocked(key)
	h.mu.Unlock()
}

func (h *historyIndex) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.clearLocked()
	h.disabled = true
	h.mu.Unlock()
}

func (h *historyIndex) get(key historyKey, after, through uint64) (historyEntry, bool) {
	if h == nil {
		return historyEntry{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.disabled || h.checksum != historyCoverageSum(h.through) {
		return historyEntry{}, false
	}
	node := h.entries[key]
	if node == nil {
		return historyEntry{}, false
	}
	e := node.Value.(*historyEntry)
	if e.key != key || e.checksum != e.sum() || !e.validCheckpointWrapper() || e.from > h.through || !e.found && len(e.data) != 0 {
		h.removeLocked(key)
		return historyEntry{}, false
	}
	if key.kind == historyAckRead {
		if e.from != through || through != h.through {
			return historyEntry{}, false
		}
	} else if key.kind == historySource {
		if !e.found || e.first == 0 || e.first > key.sequence || key.sequence > e.last || e.last > through || through > h.through {
			return historyEntry{}, false
		}
	} else {
		// Only the cold search's exact boundary and the current accepted
		// boundary are known complete; an arbitrary intermediate integer is
		// not a reader cursor. A tail answer also retains its own lower bound.
		if e.from > through || through > h.through || through != e.from && through != h.through || e.after != after {
			return historyEntry{}, false
		}
		if key.kind == historyCommand && (after > through || e.found && (e.first <= after || e.first > e.last || e.last > e.from)) {
			h.removeLocked(key)
			return historyEntry{}, false
		}
	}
	out := *e
	out.data = bytes.Clone(e.data)
	if e.checkpoint != nil {
		checkpoint := *e.checkpoint
		out.checkpoint = &checkpoint
	}
	h.lru.MoveToFront(node)
	return out, true
}

func (h *historyIndex) put(e historyEntry) {
	if h == nil || !e.validCheckpointWrapper() || e.retainedCharge() > historyByteLimit {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.disabled || h.checksum != historyCoverageSum(h.through) {
		return
	}
	if e.key.kind == historySource {
		if !e.found || e.first == 0 || e.last < e.first || e.last > e.from || e.from > h.through {
			return
		}
	} else if e.from != h.through {
		// A late mutable, negative or command search must not undo a
		// winner's invalidation. Its caller still performs the tail recheck.
		return
	}
	if h.entries == nil {
		h.entries = make(map[historyKey]*list.Element)
	}
	h.removeLocked(e.key)
	for len(h.entries) >= historyEntryLimit || h.bytes+e.retainedCharge() > historyByteLimit {
		h.removeLocked(h.lru.Back().Value.(*historyEntry).key)
	}
	data := make([]byte, len(e.data))
	copy(data, e.data)
	e.data = data
	if e.checkpoint != nil {
		checkpoint := *e.checkpoint
		e.checkpoint = &checkpoint
	}
	e.checksum = e.sum()
	h.entries[e.key] = h.lru.PushFront(&e)
	h.bytes += e.retainedCharge()
}

func (h *historyIndex) ackCheckpoint(after uint64) *ledger.ReadCheckpoint {
	e, ok := h.get(historyKey{kind: historyAckRead}, 0, after)
	if !ok {
		return nil
	}
	return e.checkpoint
}

func (h *historyIndex) putAckCheckpoint(after uint64, checkpoint *ledger.ReadCheckpoint) {
	if checkpoint == nil {
		return
	}
	h.put(historyEntry{key: historyKey{kind: historyAckRead}, from: after, found: true, checkpoint: checkpoint})
}

// Accepted effects invalidate answers; they do not reduce historical state a
// second time. A bounded review/assignment scan replaces any reverse index.
// Call before publishing the projection cursor on BOTH publication paths.
func (h *historyIndex) advance(after, through uint64, drafts []ledger.Draft) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.checksum != historyCoverageSum(h.through) || h.through != after {
		h.clearLocked()
	}
	h.removeLocked(historyKey{kind: historyAckRead})
	invalidateTask := func(id uuid.UUID) {
		h.removeLocked(historyKey{kind: historyTask, id: id})
		for key, node := range h.entries {
			if key.kind == historyReview && node.Value.(*historyEntry).task == id {
				h.removeLocked(key)
			}
		}
	}
	for _, draft := range drafts {
		var err error
		switch draft.Type {
		case ledger.TypeCommand:
			var p ledger.Command
			p, err = ledger.DecodeCommand(draft.Payload)
			h.removeLocked(historyKey{kind: historyCommand, id: p.CommandID})
		case ledger.TypeWorkspaceRegistered:
			var p ledger.WorkspaceRegistered
			p, err = ledger.DecodeWorkspaceRegistered(draft.Payload)
			h.removeLocked(historyKey{kind: historyWorkspace, id: p.WorkspaceID})
		case ledger.TypeRuntimeGrantIssued:
			var p ledger.RuntimeGrantIssued
			p, err = ledger.DecodeRuntimeGrantIssued(draft.Payload)
			h.removeLocked(historyKey{kind: historyGrant, id: p.GrantID})
		case ledger.TypeRuntimeGrantRevoked:
			var p ledger.RuntimeGrantRevoked
			p, err = ledger.DecodeRuntimeGrantRevoked(draft.Payload)
			h.removeLocked(historyKey{kind: historyGrant, id: p.GrantID})
		case ledger.TypeRuntimeTaskChanged:
			var p ledger.RuntimeTaskChanged
			p, err = ledger.DecodeRuntimeTaskChanged(draft.Payload)
			invalidateTask(p.TaskID)
			if p.Assignment != nil {
				for key := range h.entries {
					if key.kind == historyAssignment && (key.id == p.Assignment.AssignmentID || key.context == p.Assignment.Assignee.ContextID) {
						h.removeLocked(key)
					}
				}
			}
		case ledger.TypeRuntimeWorkAdmitted:
			var p ledger.RuntimeWorkAdmitted
			p, err = ledger.DecodeRuntimeWorkAdmitted(draft.Payload)
			h.removeLocked(historyKey{kind: historyWork, id: p.WorkID})
		case ledger.TypeRuntimeWorkDispatched:
			var p ledger.RuntimeWorkDispatched
			p, err = ledger.DecodeRuntimeWorkDispatched(draft.Payload)
			h.removeLocked(historyKey{kind: historyWork, id: p.WorkID})
		case ledger.TypeRuntimeWorkEnded:
			var p ledger.RuntimeWorkEnded
			p, err = ledger.DecodeRuntimeWorkEnded(draft.Payload)
			h.removeLocked(historyKey{kind: historyWork, id: p.WorkID})
		case ledger.TypeRuntimeReviewRequested:
			var p ledger.RuntimeReviewRequested
			p, err = ledger.DecodeRuntimeReviewRequested(draft.Payload)
			h.removeLocked(historyKey{kind: historyReview, id: p.RequestID})
			h.removeLocked(historyKey{kind: historyReview, id: p.ReplacesRequestID})
		case ledger.TypeRuntimeReviewResult:
			var p ledger.RuntimeReviewResult
			p, err = ledger.DecodeRuntimeReviewResult(draft.Payload)
			h.removeLocked(historyKey{kind: historyReview, id: p.RequestID})
			if p.Verdict != "reject" {
				invalidateTask(p.Binding.TaskID)
			}
		}
		if err != nil {
			// Acceleration cannot add a new refusal to the authoritative
			// reducer. Losing the whole LRU is the conservative fallback.
			h.clearLocked()
		}
	}
	h.through, h.checksum = through, historyCoverageSum(through)
}

// These wrappers add only state/sequence scalars to existing payload codecs.
// No task, grant, work or review payload schema is duplicated here.
type historyGrantValue struct {
	Definition json.RawMessage
	Issuer     string
	Origin     ledger.Origin
	Revoked    bool
	Sequence   uint64
}
type historyTaskValue struct {
	Record                        json.RawMessage
	Sequence, DescriptionSequence uint64
}
type historyWorkValue struct {
	Admission                   json.RawMessage
	Origin                      ledger.Origin
	Turn                        uuid.UUID
	Version                     int64
	Dispatched, Ended, Released bool
	Outcome                     string
	Sequence                    uint64
	Receipt                     ScopedReceipt
}
type historyReviewValue struct {
	Request, Result          json.RawMessage
	State                    string
	SupersededBy             uuid.UUID
	Sequence, ResultSequence uint64
}

func (h *historyIndex) grant(id uuid.UUID, through uint64) (grantState, bool, bool) {
	key := historyKey{kind: historyGrant, id: id}
	e, hit := h.get(key, 0, through)
	if !hit {
		return grantState{}, false, false
	}
	if !e.found && len(e.data) == 0 {
		return grantState{}, false, true
	}
	var v historyGrantValue
	if json.Unmarshal(e.data, &v) == nil && v.Sequence > 0 && v.Sequence <= e.from {
		p, err := ledger.DecodeRuntimeGrantIssued(v.Definition)
		if err == nil && p.GrantID == id && (v.Issuer == "owner" || v.Issuer == "agent") && validID(v.Origin.AgentID) && validID(v.Origin.SessionID) && validID(v.Origin.ContextID) {
			return grantState{definition: p, issuer: v.Issuer, origin: v.Origin, revoked: v.Revoked, sequence: v.Sequence}, true, true
		}
	}
	h.discard(key)
	return grantState{}, false, false
}

func (h *historyIndex) putGrant(id uuid.UUID, through uint64, state grantState, found bool) {
	e := historyEntry{key: historyKey{kind: historyGrant, id: id}, from: through, found: found}
	if found {
		payload, err := state.definition.Encode()
		if err != nil {
			return
		}
		e.data, err = json.Marshal(historyGrantValue{payload, state.issuer, state.origin, state.revoked, state.sequence})
		if err != nil {
			return
		}
	}
	h.put(e)
}

func (h *historyIndex) task(id uuid.UUID, through uint64) (scopedTaskState, bool, bool) {
	key := historyKey{kind: historyTask, id: id}
	e, hit := h.get(key, 0, through)
	if !hit {
		return scopedTaskState{}, false, false
	}
	if !e.found && len(e.data) == 0 {
		return scopedTaskState{}, false, true
	}
	var v historyTaskValue
	if json.Unmarshal(e.data, &v) == nil && v.DescriptionSequence > 0 && v.DescriptionSequence <= v.Sequence && v.Sequence <= e.from {
		p, err := ledger.DecodeRuntimeTaskChanged(v.Record)
		if err == nil && p.TaskID == id {
			return scopedTaskState{record: p, sequence: v.Sequence, descriptionSequence: v.DescriptionSequence}, true, true
		}
	}
	h.discard(key)
	return scopedTaskState{}, false, false
}

func (h *historyIndex) putTask(id uuid.UUID, through uint64, state scopedTaskState, found bool) {
	e := historyEntry{key: historyKey{kind: historyTask, id: id}, from: through, found: found}
	if found {
		payload, err := state.record.Encode()
		if err != nil {
			return
		}
		e.data, err = json.Marshal(historyTaskValue{payload, state.sequence, state.descriptionSequence})
		if err != nil {
			return
		}
	}
	h.put(e)
}

func (h *historyIndex) work(id uuid.UUID, through uint64) (scopedWorkState, bool, bool) {
	key := historyKey{kind: historyWork, id: id}
	e, hit := h.get(key, 0, through)
	if !hit {
		return scopedWorkState{}, false, false
	}
	if !e.found && len(e.data) == 0 {
		return scopedWorkState{}, false, true
	}
	var v historyWorkValue
	if json.Unmarshal(e.data, &v) == nil && v.Sequence > 0 && v.Sequence <= e.from && v.Version > 0 && v.Receipt.Sequence > 0 && v.Receipt.Sequence <= e.from && v.Receipt.WorkID == id {
		p, err := ledger.DecodeRuntimeWorkAdmitted(v.Admission)
		if err == nil && p.WorkID == id && v.Version >= p.Version && validID(v.Origin.AgentID) && validID(v.Origin.SessionID) && validID(v.Origin.ContextID) {
			return scopedWorkState{admission: p, origin: v.Origin, turn: v.Turn, version: v.Version, dispatched: v.Dispatched, ended: v.Ended, released: v.Released, outcome: v.Outcome, sequence: v.Sequence, receipt: v.Receipt}, true, true
		}
	}
	h.discard(key)
	return scopedWorkState{}, false, false
}

func (h *historyIndex) putWork(id uuid.UUID, through uint64, state scopedWorkState, found bool) {
	e := historyEntry{key: historyKey{kind: historyWork, id: id}, from: through, found: found}
	if found {
		payload, err := state.admission.Encode()
		if err != nil {
			return
		}
		e.data, err = json.Marshal(historyWorkValue{payload, state.origin, state.turn, state.version, state.dispatched, state.ended, state.released, state.outcome, state.sequence, state.receipt})
		if err != nil {
			return
		}
	}
	h.put(e)
}

func (h *historyIndex) review(id uuid.UUID, through uint64) (reviewSlot, bool, bool) {
	key := historyKey{kind: historyReview, id: id}
	e, hit := h.get(key, 0, through)
	if !hit {
		return reviewSlot{}, false, false
	}
	if !e.found && len(e.data) == 0 {
		return reviewSlot{}, false, true
	}
	var v historyReviewValue
	if json.Unmarshal(e.data, &v) == nil && v.Sequence > 0 && v.Sequence <= e.from {
		q, err := ledger.DecodeRuntimeReviewRequested(v.Request)
		if err == nil && q.RequestID == id && q.Binding.TaskID == e.task {
			slot := reviewSlot{id: id, binding: q.Binding, kind: q.Kind, state: v.State, supersededBy: v.SupersededBy, request: q, sequence: v.Sequence, resultSequence: v.ResultSequence}
			if len(v.Result) != 0 {
				result, err := ledger.DecodeRuntimeReviewResult(v.Result)
				if err == nil && result.RequestID == id && result.RequestSequence == int64(v.Sequence) && v.ResultSequence > v.Sequence && v.ResultSequence <= e.from && matchReviewBinding(result.Binding, q.Binding) == nil && v.State == reviewOutcome(result.Verdict) {
					slot.result = &result
					return slot, true, true
				}
			} else if v.ResultSequence == 0 && (v.State == "pending" && v.SupersededBy == (uuid.UUID{}) || v.State == "stale" && v.SupersededBy == (uuid.UUID{}) || v.State == "superseded" && validID(v.SupersededBy)) {
				return slot, true, true
			}
		}
	}
	h.discard(key)
	return reviewSlot{}, false, false
}

func (h *historyIndex) putReview(id uuid.UUID, through uint64, slot reviewSlot, found bool) {
	e := historyEntry{key: historyKey{kind: historyReview, id: id}, from: through, found: found}
	if found {
		request, err := slot.request.Encode()
		if err != nil {
			return
		}
		v := historyReviewValue{Request: request, State: slot.state, SupersededBy: slot.supersededBy, Sequence: slot.sequence, ResultSequence: slot.resultSequence}
		if slot.result != nil {
			v.Result, err = slot.result.Encode()
			if err != nil {
				return
			}
		}
		e.data, err = json.Marshal(v)
		if err != nil {
			return
		}
		e.task = slot.binding.TaskID
	}
	h.put(e)
}

type historyCommandValue struct {
	Command json.RawMessage
	Drafts  []ledger.Draft
}

func (h *historyIndex) command(id uuid.UUID, after, through uint64) (commandMatch, bool) {
	key := historyKey{kind: historyCommand, id: id}
	e, hit := h.get(key, after, through)
	if !hit {
		return commandMatch{}, false
	}
	if !e.found {
		return commandMatch{}, true
	}
	var v historyCommandValue
	if json.Unmarshal(e.data, &v) == nil {
		command, err := ledger.DecodeCommand(v.Command)
		if err == nil && command.CommandID == id {
			if len(v.Drafts) == 0 {
				return commandMatch{found: true, command: command}, true
			}
			if uint64(len(v.Drafts)) == e.last-e.first+1 {
				g, scoped, review, paid, _, loop, err := decodeFamily(v.Drafts, e.first)
				occurrences := 0
				for _, draft := range v.Drafts {
					if draft.Type == ledger.TypeCommand {
						c, decodeErr := ledger.DecodeCommand(draft.Payload)
						if decodeErr != nil {
							err = decodeErr
						} else if c.CommandID == id && c == command {
							occurrences++
						}
					}
				}
				if err == nil && occurrences == 1 && (g != nil || scoped != nil || review != nil || paid != nil || loop != nil) {
					return commandMatch{found: true, command: command, group: g, scoped: scoped, review: review, paid: paid, loop: loop}, true
				}
			}
		}
	}
	h.discard(key)
	return commandMatch{}, false
}

func (h *historyIndex) putCommand(id uuid.UUID, after, through uint64, match commandMatch, commit ledger.Commit) {
	if match.registration != nil {
		return
	}
	e := historyEntry{key: historyKey{kind: historyCommand, id: id}, from: through, after: after, found: match.found}
	if match.found {
		command, err := match.command.Encode()
		if err != nil {
			return
		}
		v := historyCommandValue{Command: command}
		// Unconsumed families need only the F0 identity, not arbitrary
		// payloads that this service neither interprets nor retains.
		if match.group != nil || match.scoped != nil || match.review != nil || match.paid != nil || match.loop != nil {
			v.Drafts = draftsOf(commit)
		}
		e.first, e.last = commit.First, commit.Last
		e.data, err = json.Marshal(v)
		if err != nil {
			return
		}
	}
	h.put(e)
}

type historySourceValue struct {
	Record   ledger.Record
	Payload  []byte
	BodyRefs []string
}

func (h *historyIndex) source(sequence int64, through uint64, colony uuid.UUID) (ledger.Record, bool) {
	key := historyKey{kind: historySource, sequence: uint64(sequence)}
	e, hit := h.get(key, 0, through)
	if !hit {
		return ledger.Record{}, false
	}
	var value historySourceValue
	err := json.Unmarshal(e.data, &value)
	record := value.Record
	record.Payload = value.Payload
	if err == nil && value.BodyRefs != nil {
		record.BodyRefs = make([]ledger.Digest, len(value.BodyRefs))
		for i, text := range value.BodyRefs {
			record.BodyRefs[i], err = ledger.ParseDigest(text)
			if err != nil {
				break
			}
		}
	}
	if err == nil && record.Seq == uint64(sequence) && record.ColonyID == colony && record.CommitCount > 0 && record.CommitIndex >= 0 && record.CommitIndex < record.CommitCount && uint64(record.CommitCount) == e.last-e.first+1 && uint64(record.CommitIndex) == record.Seq-e.first && validID(record.CommitID) && json.Valid(record.Payload) && json.Valid(record.JSON) {
		sum := ledger.Digest(sha256.Sum256(record.JSON))
		var err error
		switch record.Type {
		case ledger.TypePrivateRevision:
			_, err = ledger.DecodePrivateRevision(record.Payload)
		case ledger.TypePrivateScopedRevision:
			_, err = ledger.DecodePrivateScopedRevision(record.Payload)
		case ledger.TypePrivateTurnRevision:
			_, err = ledger.DecodePrivateTurnRevision(record.Payload)
		case ledger.TypeRuntimeTaskChanged:
			_, err = ledger.DecodeRuntimeTaskChanged(record.Payload)
		}
		if err == nil && record.Hash == sum.String() {
			return record, true
		}
	}
	h.discard(key)
	return ledger.Record{}, false
}

func (h *historyIndex) putSource(record ledger.Record, first, last, through uint64) {
	// Only interpreted ledger metadata is eligible, never arbitrary unknown
	// payloads or any object/body bytes. The authoritative reader still
	// supplies all other source types without caching them.
	if _, known := privateConsumption()[record.Type]; !known {
		return
	}
	// Preserve the original payload spelling rather than remarshal a
	// RawMessage (which may rewrite escaping). Both byte copies are charged.
	payload := record.Payload
	record.Payload = nil
	// Digests marshal as text but need explicit parsing on decode.
	var bodyRefs []string
	if record.BodyRefs != nil {
		bodyRefs = make([]string, len(record.BodyRefs))
		for i, digest := range record.BodyRefs {
			bodyRefs[i] = digest.String()
		}
	}
	record.BodyRefs = nil
	data, err := json.Marshal(historySourceValue{Record: record, Payload: payload, BodyRefs: bodyRefs})
	if err == nil {
		h.put(historyEntry{key: historyKey{kind: historySource, sequence: record.Seq}, from: through, first: first, last: last, found: true, data: data})
	}
}

// warmSources runs after acceptance and coverage publication, with no history
// mutex held. putSource copies only eligible metadata into the same bounded LRU.
func (h *historyIndex) warmSources(commit ledger.Commit) {
	if h == nil {
		return
	}
	for _, record := range commit.Records {
		if record.Type == ledger.TypePrivateRevision || record.Type == ledger.TypePrivateScopedRevision || record.Type == ledger.TypePrivateTurnRevision || len(record.BodyRefs) != 0 {
			h.putSource(record, commit.First, commit.Last, commit.Last)
		}
	}
}
