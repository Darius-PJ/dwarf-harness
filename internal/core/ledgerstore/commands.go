package ledgerstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"time"
	"uuid"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
	"github.com/Darius-PJ/dwarf-harness/contract/ledger/appender"
)

const (
	// hotCommands bounds the command IDs the hot index holds. It is Core
	// policy, which the owner may change without a format change.
	hotCommands = 1 << 16
	// commandLead is how long after its commit's event time a command ID
	// may be dated. It is Core policy, not a format rule. It may only grow:
	// looking an ID up without reading the log is sound only if every
	// ledger.command record was accepted under a lead at least as large as
	// the current one.
	commandLead = 5 * time.Minute
)

var (
	// ErrCommandMismatch reports a Submit of a command ID that the log
	// already holds with another submission digest. It always comes with
	// appender.ErrRefused.
	ErrCommandMismatch = errors.New("ledgerstore: command ID already used for a different submission")
	// ErrCommandTime reports a Submit of a command ID dated more than
	// commandLead after the event time the commit would be stamped with. It
	// always comes with appender.ErrRefused. Nothing was written, and it is
	// the one refusal that depends on the clock: the same request may
	// succeed later.
	ErrCommandTime = errors.New("ledgerstore: command ID is dated after the commit time allows")
)

// Receipt is Submit's result.
type Receipt struct {
	// Committed is the command's commit: this call's, or, for a duplicate,
	// the original's.
	appender.Committed
	// Duplicate reports that an earlier commit holds the command ID with the
	// same submission digest, so that nothing was written.
	Duplicate bool
}

// entry is what the hot index holds of one ledger.command record: the
// submission digest and the commit that holds it.
type entry struct {
	submission  ledger.Digest
	first, last uint64 // the commit's sequence range; first is the record's own
	timeNS      int64  // the commit's event time
}

// segInfo is the segment catalogue's entry for one segment file. The segment
// is authoritative; the entry only says whether it can hold a command ID.
type segInfo struct {
	first            uint64 // the sequence of the segment's first record
	idMinMS, idMaxMS int64  // the range of the UUIDv7 timestamps of its command IDs; empty if idMinMS > idMaxMS
}

// newSegInfo returns the entry of the segment that starts at first, holding
// no command.
func newSegInfo(first uint64) segInfo {
	return segInfo{first: first, idMinMS: math.MaxInt64, idMaxMS: -1}
}

// widen extends the range to hold the timestamp ms.
func (g *segInfo) widen(ms int64) {
	g.idMinMS, g.idMaxMS = min(g.idMinMS, ms), max(g.idMaxMS, ms)
}

// mayHold reports whether a command ID with timestamp ms can be in the segment.
func (g segInfo) mayHold(ms int64) bool { return g.idMinMS <= ms && ms <= g.idMaxMS }

// idQueue is a FIFO of command IDs.
type idQueue struct {
	ids  []uuid.UUID
	head int // ids[head:] are queued
}

func (q *idQueue) push(id uuid.UUID) { q.ids = append(q.ids, id) }

// pop removes and returns the oldest ID; the queue must not be empty.
func (q *idQueue) pop() uuid.UUID {
	id := q.ids[q.head]
	q.head++
	if q.head*2 >= len(q.ids) {
		q.ids = slices.Delete(q.ids, 0, q.head)
		q.head = 0
	}
	return id
}

// idMS returns the 48-bit Unix millisecond timestamp of a UUIDv7.
func idMS(id uuid.UUID) int64 {
	return int64(id[0])<<40 | int64(id[1])<<32 | int64(id[2])<<24 | int64(id[3])<<16 | int64(id[4])<<8 | int64(id[5])
}

// issueLimitMS returns the latest timestamp, in milliseconds, that a command
// ID may carry when its commit's event time is ns: (ns + lead) / 1 ms rounded
// down, computed so that it cannot overflow.
func issueLimitMS(ns int64, lead time.Duration) int64 {
	const ms = int64(time.Millisecond)
	return ns/ms + int64(lead)/ms + (ns%ms+int64(lead)%ms)/ms
}

// segmentCatalogue returns an empty entry for each segment file of the log
// in logDir, in sequence order. Open has verified the log, so names that are
// not segment names are the dot names the format ignores.
func segmentCatalogue(logDir string) ([]segInfo, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return nil, err
	}
	var segs []segInfo
	for _, e := range entries {
		if first, ok := ledger.ParseSegmentName(e.Name()); ok {
			segs = append(segs, newSegInfo(first))
		}
	}
	return segs, nil
}

// Submit commits command, a ledger.command v1 draft, and drafts, the draft
// records of the command's effect, as one commit with the command record
// first, unless the log already holds the command's ID. It is the only way to
// write a ledger.command record: Append refuses one.
//
// The command ID is the caller's immutable UUIDv7, and the record keeps a
// digest of the caller's canonical submission bytes, which must cover
// everything that binds the request: its content, candidate and context.
// Submit looks the ID up before it validates any draft. With the same digest
// it returns the original commit's range with Duplicate set, and writes
// nothing, even if the drafts would now be refused, for example because a
// body they name has since been collected. With another digest it refuses
// with appender.ErrRefused and ErrCommandMismatch. An ID resolves for the
// life of the log: the hot index answers for the newest, and a lookup that it
// cannot answer reads the log.
//
// Before it waits for the writer turn, Submit refuses with appender.ErrRefused
// a command that is not a ledger.command version 1 draft, has body
// references, has a payload that ledger.DecodeCommand rejects or is not in
// the store's catalogue. Under the turn, after the lookup, it refuses the
// drafts that Append would refuse, and, with ErrCommandTime, an ID dated more
// than commandLead after the event time that the commit is stamped with. That
// is the only refusal that depends on the clock. A refusal writes nothing and
// uses no sequence. The other errors are Append's.
//
// ctx bounds the wait for the writer turn and the reading of the log for an
// old ID, and ends either with ctx's error and nothing written. Once the
// commit is being written it completes whatever ctx does. Any other error
// from reading the log also means that nothing was written.
func (s *Store) Submit(ctx context.Context, command ledger.Draft, drafts []ledger.Draft) (Receipt, error) {
	cmd, err := s.checkCommand(command)
	if err != nil {
		return Receipt{}, err
	}
	// select chooses at random between a free turn and an ended ctx, so an
	// ended ctx is checked first and never takes the turn.
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	select {
	case s.turn <- struct{}{}:
	case <-ctx.Done():
		return Receipt{}, ctx.Err()
	}
	defer s.release()
	switch {
	case s.closed:
		return Receipt{}, appender.ErrClosed
	case s.poison != nil:
		return Receipt{}, fmt.Errorf("%w: %w", appender.ErrPoisoned, s.poison)
	}

	prior, found, err := s.lookup(ctx, cmd.CommandID)
	switch {
	case err != nil:
		return Receipt{}, err
	case found && prior.submission != cmd.SubmissionSHA256:
		return Receipt{}, fmt.Errorf("%w: %w: %s", appender.ErrRefused, ErrCommandMismatch, cmd.CommandID)
	case found:
		return Receipt{Committed: appender.Committed{First: prior.first, Last: prior.last}, Duplicate: true}, nil
	}

	if err := s.checkCatalogue(drafts); err != nil {
		return Receipt{}, err
	}
	plan, err := s.pins.plan(drafts, s.deps.maxPinned)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: %w", appender.ErrRefused, err)
	}
	if err := s.checkReferences(drafts); err != nil {
		return Receipt{}, err
	}
	ns, held := s.eventTime()
	if t, limit := idMS(cmd.CommandID), issueLimitMS(ns, commandLead); t > limit {
		return Receipt{}, fmt.Errorf("%w: %w: %s is dated at %d ms, after the limit of %d ms", appender.ErrRefused, ErrCommandTime, cmd.CommandID, t, limit)
	}
	c, err := s.commitAt(ns, held, append([]ledger.Draft{command}, drafts...))
	if err != nil {
		return Receipt{}, err
	}
	// The commit is durable, and the current segment is the catalogue's
	// last entry.
	s.pins.apply(plan)
	s.segs[len(s.segs)-1].widen(idMS(cmd.CommandID))
	s.addHot(cmd.CommandID, entry{submission: cmd.SubmissionSHA256, first: c.First, last: c.Last, timeNS: ns})
	return Receipt{Committed: c}, nil
}

// checkCommand returns the payload of command if Submit may write it as a
// ledger.command record. It refuses with appender.ErrRefused any other
// draft.
func (s *Store) checkCommand(command ledger.Draft) (ledger.Command, error) {
	switch {
	case command.Type != ledger.TypeCommand || command.TypeVersion != 1:
		return ledger.Command{}, fmt.Errorf("%w: command is %s version %d, not %s version 1", appender.ErrRefused, command.Type, command.TypeVersion, ledger.TypeCommand)
	case !s.cat.Contains(ledger.TypeVersion{Type: ledger.TypeCommand, Version: 1}):
		return ledger.Command{}, fmt.Errorf("%w: %s version 1 is not in the catalogue", appender.ErrRefused, ledger.TypeCommand)
	case len(command.BodyRefs) != 0:
		return ledger.Command{}, fmt.Errorf("%w: command lists bodies", appender.ErrRefused)
	}
	cmd, err := ledger.DecodeCommand(command.Payload)
	if err != nil {
		return ledger.Command{}, fmt.Errorf("%w: command: %w", appender.ErrRefused, err)
	}
	return cmd, nil
}

// addHot adds the command id, held by the commit that e describes, to the
// hot index, the newest of its commands. If that passes the bound, it
// evicts the oldest commands: everything the log holds up to the evicted
// commit's end is then cold, found only by reading it. The writer turn
// guards the hot index; Open builds it before the store is shared.
func (s *Store) addHot(id uuid.UUID, e entry) {
	s.hot[id] = e
	s.hotOrder.push(id)
	for len(s.hot) > max(s.deps.hotCommands, 0) {
		old := s.hotOrder.pop()
		s.coldThrough, s.coldTimeNS = s.hot[old].last, s.hot[old].timeNS
		delete(s.hot, old)
	}
}

// lookup returns the entry of the ledger.command record of command ID id, if
// the log holds one. The writer turn guards it.
//
// The hot index holds every command above coldThrough, the end of the newest
// evicted commit. For an ID it does not hold, the log decides, and reading
// it can be avoided when the ID cannot be in the part of the log that the
// index has evicted:
//
//   - If nothing was evicted, the index holds every command.
//   - An ID is dated at most commandLead after the event time of the commit
//     that records it, and event times never decrease, so no ID dated after
//     the newest evicted commit's time plus commandLead is in the evicted
//     part. A freshly minted ID is therefore never looked up on disk while
//     the index holds at least commandLead of history.
//   - Otherwise the segments that can hold the ID are read, oldest first,
//     up to coldThrough: those whose range of command ID timestamps holds
//     the ID's. The catalogue's ranges come from the records themselves, so
//     this is correct whatever the issuance rule; the rule only keeps them
//     tight.
//
// An entry found by reading is not added to the hot index, so that the index
// keeps the order of the log.
func (s *Store) lookup(ctx context.Context, id uuid.UUID) (entry, bool, error) {
	if e, ok := s.hot[id]; ok {
		return e, true, nil
	}
	if s.coldThrough == 0 {
		return entry{}, false, nil
	}
	ms := idMS(id)
	if ms > issueLimitMS(s.coldTimeNS, commandLead) {
		return entry{}, false, nil
	}
	for i, seg := range s.segs {
		if seg.first > s.coldThrough {
			break
		}
		if !seg.mayHold(ms) {
			continue
		}
		// A segment ends where the next begins, or at the log's end.
		last := s.chain.Seq
		if i+1 < len(s.segs) {
			last = s.segs[i+1].first - 1
		}
		e, found, err := s.scanSegment(ctx, seg.first, min(last, s.coldThrough), id)
		if err != nil || found {
			return e, found, err
		}
	}
	return entry{}, false, nil
}

// scanSegment reads the commits of the segment that starts at first, through
// the commit end through, for the ledger.command record of id. It checks ctx
// between commits.
func (s *Store) scanSegment(ctx context.Context, first, through uint64, id uuid.UUID) (entry, bool, error) {
	s.coldScans.Add(1)
	r, err := ledger.OpenReader(s.logDir, ledger.ReadOptions{
		Colony: s.colony, After: first - 1, Through: through,
		Consume: ledger.Consumption{ledger.TypeCommand: {1}},
	})
	if err != nil {
		return entry{}, false, err
	}
	defer r.Close()
	for {
		if err := ctx.Err(); err != nil {
			return entry{}, false, err
		}
		c, err := r.Next()
		if errors.Is(err, io.EOF) {
			return entry{}, false, nil
		}
		if err != nil {
			return entry{}, false, err
		}
		for _, rec := range c.Records {
			cmd, err := ledger.DecodeCommand(rec.Payload)
			if err != nil {
				return entry{}, false, inconsistent(rec.Seq, "%v", err)
			}
			if cmd.CommandID == id {
				return entry{submission: cmd.SubmissionSHA256, first: c.First, last: c.Last, timeNS: rec.EventTimeNS}, true, nil
			}
		}
	}
}
