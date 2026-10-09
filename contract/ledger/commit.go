package ledger

import (
	"errors"
	"fmt"
	"math"
	"uuid"
)

// Chain is the position after the last committed record.
type Chain struct {
	Seq         uint64 // last sequence; 0 for an empty log
	Hash        string // last record's hash; ZeroHash for an empty log
	EventTimeNS int64  // last event time; 0 for an empty log
}

// Genesis returns the chain position of an empty log.
func Genesis() Chain { return Chain{Hash: ZeroHash} }

// valid reports whether c is a position a log can be at.
func (c Chain) valid() bool {
	if c.Seq == 0 {
		return c == Genesis()
	}
	return len(c.Hash) == len(ZeroHash) && isLowerHex([]byte(c.Hash)) && c.EventTimeNS > 0
}

// CommitHeader carries the fields the writer stamps once per commit.
type CommitHeader struct {
	ColonyID    uuid.UUID
	CommitID    uuid.UUID
	EventTimeNS int64
	ClockHeld   bool // the clock moved backwards and EventTimeNS is held at after's
}

// EncodeCommit frames drafts as one commit continuing after. It assigns seq,
// prev_hash and the commit fields, and returns the commit's lines and the
// chain position after them. It refuses, with an error wrapping ErrInvalid,
// any commit a reader would reject, and, with an error wrapping ErrTooLarge,
// a line over MaxRecordBytes or a commit over MaxCommitBytes. It is a pure
// function.
func EncodeCommit(after Chain, h CommitHeader, drafts []Draft) ([]byte, Chain, error) {
	if err := checkCommit(after, h, drafts); err != nil {
		return nil, Chain{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out []byte
	c := after
	for i := range drafts {
		e := Envelope{
			Format:      FormatVersion,
			Seq:         c.Seq + 1,
			CommitID:    h.CommitID,
			CommitIndex: i,
			CommitCount: len(drafts),
			PrevHash:    c.Hash,
			EventTimeNS: h.EventTimeNS,
			ClockHeld:   h.ClockHeld,
			ColonyID:    h.ColonyID,
			Draft:       drafts[i],
		}
		line, hash, err := encodeLine(&e)
		if err != nil {
			return nil, Chain{}, fmt.Errorf("%w: draft %d: %w", ErrInvalid, i, err)
		}
		if len(line) > MaxRecordBytes {
			return nil, Chain{}, fmt.Errorf("%w: draft %d encodes to %d bytes, cap %d", ErrTooLarge, i, len(line), MaxRecordBytes)
		}
		if len(out)+len(line) > MaxCommitBytes {
			return nil, Chain{}, fmt.Errorf("%w: commit exceeds %d bytes at draft %d", ErrTooLarge, MaxCommitBytes, i)
		}
		out = append(out, line...)
		c = Chain{Seq: e.Seq, Hash: hash, EventTimeNS: h.EventTimeNS}
	}
	return out, c, nil
}

// checkCommit applies every envelope rule a reader applies to the commit.
func checkCommit(after Chain, h CommitHeader, drafts []Draft) error {
	switch {
	case len(drafts) == 0:
		return errors.New("no drafts")
	case !after.valid():
		return errors.New("after is not a log position")
	case uint64(len(drafts)) > math.MaxUint64-after.Seq:
		return errors.New("sequence overflow")
	case !isV7(h.ColonyID):
		return fmt.Errorf("colony ID %s is not a UUIDv7", h.ColonyID)
	case !isV7(h.CommitID):
		return fmt.Errorf("commit ID %s is not a UUIDv7", h.CommitID)
	case h.EventTimeNS <= 0:
		return fmt.Errorf("event time %d is not positive", h.EventTimeNS)
	case h.EventTimeNS < after.EventTimeNS:
		return fmt.Errorf("event time %d is before the previous %d", h.EventTimeNS, after.EventTimeNS)
	case h.ClockHeld && after.Seq == 0:
		return errors.New("clock held on the first commit")
	case h.ClockHeld && h.EventTimeNS != after.EventTimeNS:
		return fmt.Errorf("clock held at %d, not the previous time %d", h.EventTimeNS, after.EventTimeNS)
	}
	for i := range drafts {
		if err := checkDraft(&drafts[i]); err != nil {
			return fmt.Errorf("draft %d: %w", i, err)
		}
	}
	return nil
}

func checkDraft(d *Draft) error {
	switch {
	case !validType(d.Type):
		return fmt.Errorf("type %q does not match the type grammar", d.Type)
	case d.TypeVersion < 1:
		return fmt.Errorf("type version %d is below 1", d.TypeVersion)
	case !d.Actor.valid():
		return fmt.Errorf("actor %q is not owner, agent, core or runner", d.Actor)
	case d.MeasurementPath != "" && !validPath(d.MeasurementPath):
		return fmt.Errorf("measurement path %q does not match its grammar", d.MeasurementPath)
	}
	for _, id := range [...]struct {
		name string
		id   uuid.UUID
	}{
		{"agent_id", d.AgentID},
		{"execution_session_id", d.ExecutionSessionID},
		{"context_id", d.ContextID},
		{"turn_id", d.TurnID},
		{"operation_id", d.OperationID},
	} {
		if id.id != (uuid.UUID{}) && !isV7(id.id) {
			return fmt.Errorf("%s %s is not a UUIDv7", id.name, id.id)
		}
	}
	return checkPayload(d.Payload)
}
