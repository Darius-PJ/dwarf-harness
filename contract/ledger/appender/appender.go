// Package appender is the ledger-appender contract (D50): the restricted way
// a producer adds records to one colony's ledger. Core's writer is its only
// implementation. A caller supplies ledger.Draft values, which carry no
// sequence, hash, time, colony or commit field; the writer assigns all of
// them, so a caller can neither write ledger files nor choose sequences.
//
// An Append is acknowledged only after its commit is completely written and
// durably flushed. Otherwise it fails with an error that says what became of
// the commit:
//
//   - ErrRefused: the request is invalid or not allowed. Nothing was written,
//     and the same request fails again. A refusal for size also matches
//     ledger.ErrTooLarge.
//   - ErrPoisoned: an earlier failure left the writer needing recovery.
//     Nothing was written.
//   - ErrUncertain: the commit may or may not survive, and the writer is now
//     poisoned. The commit is not acknowledged. In-process recovery removes
//     it; after a crash before that recovery, recovery at the next start keeps
//     it if it is complete and valid. A caller that needs certainty looks the
//     commit up after recovery.
//   - ErrClosed: the writer is closed. Nothing was written.
//   - the error of ctx: ctx ended while the call waited for the writer.
//     Nothing was written. ctx bounds only that wait: once writing starts,
//     the commit completes or fails whatever ctx does.
//
// A draft's BodyRefs may name only bodies already published to the colony's
// body store. The writer refuses, with ErrRefused, a draft that names an
// unpublished body or breaks a rule of its record type, including the F0
// types that only the writer itself produces (ledger.command and
// ledger.bodies_collected).
//
// The writer serializes commits and promises no fairness among waiting
// callers. Priority for control commits (AC06) belongs to Core's concrete
// writer, as a separate method or interface, so that this contract does not
// change for it.
//
// The owner froze this contract at version 1 (D40, D111).
package appender

import (
	"context"
	"errors"

	"github.com/Darius-PJ/dwarf-harness/contract/ledger"
)

// Appender appends commits to one colony's ledger.
type Appender interface {
	// Append commits drafts, in order, as one commit. It returns only after
	// the commit is completely written and durably flushed.
	Append(ctx context.Context, drafts []ledger.Draft) (Committed, error)
}

// Committed is the sequence range of an acknowledged commit.
type Committed struct {
	First, Last uint64
}

var (
	// ErrRefused marks an Append that was not allowed. Nothing was written,
	// and the same request fails again.
	ErrRefused = errors.New("appender: append refused")
	// ErrPoisoned marks an Append refused because the writer needs recovery.
	// Nothing was written.
	ErrPoisoned = errors.New("appender: writer poisoned; recovery required")
	// ErrUncertain marks an Append whose commit may or may not survive. It is
	// not acknowledged, and the writer is now poisoned.
	ErrUncertain = errors.New("appender: commit outcome uncertain")
	// ErrClosed marks an Append to a closed writer. Nothing was written.
	ErrClosed = errors.New("appender: writer closed")
)
