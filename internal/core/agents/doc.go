// Package agents owns Core's owner-addressed private-state service: agent and
// context identities, immutable private objects, revisions, receipts and their
// Markdown materializations. The ledger selects state; directory contents and
// mutable Markdown never select a revision.
//
// # Host trust and lifecycle
//
// All exported operations are owner-only entry points for the trusted host.
// Possession of a Service is not authentication or a grant. Callers supply an
// agent/context Binding, not an actor; Core derives the agent's one session and
// writes owner command/identity/receipt records and core revision records.
// Unknown or mismatched bindings return ErrNotBound. This package admits no
// colony work, launches no child or provider, and implements no agent entry,
// grants, tasks, turns, promotion or repair commands.
//
// The host opens one Service for the colony store and keeps that store alive
// until Service.Close has drained active calls. Close fences new calls with
// ErrClosed and waits for in-flight work, including committed materialization.
// It does not close the shared ledgerstore.Store. After an uncertain store write,
// the incarnation is poisoned: subsequent operations return ErrPoisoned. Close
// it, recover or reopen the store, and open a new Service; do not reuse a Service
// across store recovery. Multiple live Services for the same colony are not a
// supported coordination mechanism.
//
// Open consumes complete committed groups, including intervening non-private
// records, and reconstructs identity, heads and scratch state. It verifies
// ledger-selected objects and inspects materializations. It reads no clock,
// appends no record, dispatches no work and infers no identity from a directory.
// The reserved core/state.json path is neither read nor written: even a stray
// invalid file there has no authority. There is no pending private operation or
// interrupted-operation reconciliation protocol.
//
// # Transitions and receipts
//
// RegisterAgent creates one agent/session and its general context. CreateContext
// creates a colony-unique work context without importing another context's
// private state. Agent directories use the registered basename; context paths
// use full UUIDs. Owned path components must be real directories or regular
// files, not links. These checks protect against unsafe entries, not hostile
// same-user races outside the trusted host boundary.
//
// WritePrivate changes an agent scope (instructions and memory) or a context
// scope (memory, handoff, transcript and continuation). Bytes are opaque and
// exact. Transcript append adds one nonempty occurrence; equal chunks in two
// revisions remain two occurrences. Continuation may be replaced or cleared.
// Each scope has its own revision. ExpectedVersion is checked under the service
// lock immediately before Submit; RevisionError reports expected and current.
// Concurrent preparation is bounded by Limits.MaxConcurrentWrites; BusyError
// is a refusal, not a durable pending state. Default producer bounds are 1 MiB
// per object and 4 MiB per operation. Lower limits do not invalidate history.
//
// Objects are written to an owning-directory temporary file, flushed, closed
// and durably renamed before the single ledger commit. An existing addressed
// object must match its size and digest and is never overwritten. Successfully
// flushed directory identities are remembered only by the Service instance;
// failed flushes are not remembered. Registration, creation and private writes
// submit six-, four- and three-record groups respectively. Markdown completion
// and its in-memory availability projection precede a successful acknowledgement.
// After Submit succeeds, caller cancellation cannot undo the commit or abandon
// its completion. Refusals before Submit consume no command ID; published but
// unreferenced objects remain unselected.
//
// Receipt is the immutable applied result, not a claim of current availability.
// The schema-1 canonical submission form and F3-10 records are frozen producer
// rules. Same-ID, same-digest retries return the original receipt, including
// after restart, before CAS, integrity and capacity checks; a different digest
// returns ErrCommandMismatch. CommandStatus requires the receipt's own binding,
// command ID and submission digest. It returns ErrUnavailable for an absent
// private receipt. Old receipt lookups and historical reads scan ledger history;
// latency grows with that history. No constant-latency or product-capacity claim
// follows from storage-only measurements.
//
// # Materialization and integrity
//
// A managed Markdown slot is current when it equals its committed head, stale
// when absent or equal to an earlier committed object of that same slot, and
// foreign otherwise (including non-regular entries). Open completes stale
// slots from verified committed bytes and reports each completion in
// Reconciliation.Completed, without appending. It never overwrites foreign
// materializations. Failed completion remains stale and is reported.
//
// A live completion failure returns MaterializationError with the committed
// revision and the underlying cause; Snapshot exposes stale availability. The
// receipt still replays. A later successful write completes its whole selected
// head, or a subsequent Open completes stale files after the cause is removed.
// Neither is a retry loop or a repair command.
//
// Foreign materializations and missing or corrupt selected heads fence the
// affected scope. Agent-wide integrity failure fences that agent's contexts;
// a context failure does not fence a sibling. Open also verifies historical
// objects and reports their failures; a corrupt replaced object is not a reason
// to fence an intact current head, but reading that historical object fails.
// Transcript history remains selected and its corruption fences the scope.
// IntegrityIssue carries binding, slot, revision and expected/observed digests,
// never private content. No authorized foreign-file or missing-object repair
// path is provided here.
//
// # Reads and scratch projection
//
// Snapshot returns the selected revision, heads and availability/issues, not
// private bytes. ReadPrivate requires an explicit committed revision and byte
// budget. It reads verified immutable objects, never Markdown. Transcript pages
// contain whole occurrences; NextCursor is the last returned occurrence's ledger
// sequence when more remain, or zero at the end. If the first remaining chunk
// cannot fit, ErrTooLarge is returned rather than an empty advancing page.
//
// ScratchBinding is a read-only projection of frozen workspace scratch-created
// and scratch-closed records, keyed by workspace and colony-unique context. It
// reports absent, present, closed or mismatch, and distinguishes dedicated from
// temporary scratches. It never creates a scratch or selects another context.
package agents
