package agents

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"uuid"
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
