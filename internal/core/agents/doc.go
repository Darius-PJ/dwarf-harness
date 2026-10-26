// Package agents owns Core's identities, private state, scoped authority, tasks
// and work projections. The ledger selects state; directory contents
// and mutable Markdown never select a revision or confer authority.
//
// # Host trust and lifecycle
//
// Service methods are trusted owner or host entrances. BindAgent fixes a
// registered agent, its session and context for an Agent entrance; possession
// of either handle is not authentication. Agent operations cannot replace
// their origin or actor, and check current authority rather than inheriting
// owner access. Unknown or mismatched bindings return ErrNotBound. Owner
// commands refuse a supplied authority GrantID before identity or state lookup.
// Admission, dispatch and ending methods record decisions only: this package
// runs no file tool, process, provider, scheduler, promotion or repair command.
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
// Open consumes complete committed groups, including intervening workspace
// records, and reconstructs identities, private heads, scratch state, grants,
// tasks, capacity, usage, occupied work and review request slots. F3-10, F3-11
// and F3-12 are decoded as distinct families before applying private changes
// to the same scope head.
// Open verifies ledger-selected objects and inspects materializations. It reads
// no clock, appends no record and dispatches no work. Reconciliation.PendingWork
// reports unreleased admissions as recovery_pending without inferring an
// outcome or releasing their holds. The reserved core/state.json path is
// neither read nor written, and directory contents confer no identity.
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
// per object and 4 MiB per operation. Lower runtime limits do not invalidate
// history. The separate hard object-storage support ceiling is 1 MiB in both
// private families; history above that ceiling remains unsupported.
// Limits' amount ceilings are positive. MaxDelegationDepth and
// MaxTaskDependencies alone may be zero: shape ceilings forbidding delegation
// or dependencies. MaxScopedBytes defaults to 4 MiB and MaxSelectedObjects to
// 64; these guard new scoped operations, not matched historical requests.
//
// Objects are written to an owning-directory temporary file, flushed, closed
// and durably renamed before the single ledger commit. An existing addressed
// object must match its size and digest and is never overwritten. Successfully
// flushed directory identities are remembered only by the Service instance;
// failed flushes are not remembered. Owner registration, context creation and
// private writes submit six-, four- and three-record groups respectively.
// Agent private writes submit a four-record scoped group, with agent command
// and receipt provenance and a core private revision on the same shared head.
// Markdown completion and its whole availability projection precede a
// successful acknowledgement from either entrance.
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
// private receipt. Old receipt lookups and historical reads scan ledger history.
// A matched private-write retry waits for the same scope's in-flight completion,
// without holding the service lock, reserving bytes, charging, inspecting or
// materializing again. Cancellation ends that wait, not the committed fact;
// receipt-only status remains available. A failed completion does not erase
// its historical receipt.
// A new private write scans the whole ledger for preflight integrity and again
// for committed materialization. After replay, Open scans the whole history
// per scope to validate objects, with another scan for each scope it completes.
// These costs grow with ledger history and, for Open, the number of scopes.
// No constant-latency or product-capacity claim follows from storage-only
// measurements.
//
// # Scoped authority, tasks and accounting
//
// A scoped mutation uses one selected owner-rooted grant and its complete
// current chain, never a union of grants. Delegation attenuates operations,
// resources, recipients, ceilings, lifetime and task-generation binding.
// Hierarchy gives no implicit authority. Revocation and reassignment fence old
// authority; an unfamiliar lifetime grants nothing.
//
// Task mutations compare the task version and, after assignment, its assignment
// ID and generation. Reassignment requires a fresh work context and clears the
// previous assignment's transient state. Definitions and dependency IDs are
// immutable. Producers require registered audience agents; frozen historical
// audience lists are not retroactively subjected to that producer guard.
// Subject references must be known artifacts with an exact source body
// reference. Agent subjects additionally require current artifact-read
// authority, or the description's audience and current task-inspect authority,
// before existence-sensitive source checks. No bytes are disclosed or charged
// by that reference check.
//
// TaskStatus and ListTasks disclose only tasks in the caller's audience and
// current task.inspect region. Authorized views include stored subject-reference
// metadata, not body bytes or permission to read them. Invisible and nonexistent
// tasks have the same refusal; invisible dependencies remain opaque. Readiness
// follows dependencies, current task.start and turn.start chains, and capacity
// enrolment. Colony and agent enrolment are required; context enrolment is
// optional. A free-slot shortage yields ready_queued, not permission denial.
// Ready describes the assignee's own start authority. The owner's task-start
// entrance requires only the assignee's current turn-admission chain.
//
// ConfigureCapacity changes ceilings without resetting usage or releasing
// occupied work. One checked book applies charges and holds to colony, agent,
// context and every grant in the chain. A dispatch consumes its reserved unit
// once. Unknown work endings retain holds; proven endings release only their
// applicable occupancy, never consumed units. Lowering capacity below occupancy,
// revocation and restart do not erase it. Capacity fields are nonnegative and
// may be zero; max_operation_bytes is independent of local_slots. Producer
// Limits bound new requests, not historical replay.
// A grant's max_operation_bytes excess is ErrDenied for new local admissions
// and private writes through authority checking, but BusyError for private and
// artifact reads through the preparation book. Retry reads skip that new-work
// ceiling, not the physical reservation.
//
// StartTurn is Agent-only. It requires current private integrity, an exact
// active assignment when task-bound, and current turn.start authority. One
// active turn per context is independent of configurable agent and colony
// ceilings; the agent turn-slot Limits default is three. Busy refusals consume
// neither a command nor a work ID. A turn slot grants no model-call authority.
// AdmitLocal holds a local slot, bytes and one unit for a concrete file effect.
// BeginLocal rechecks current authority, assignment and dedicated scratch at
// dispatch and consumes that held unit once. External classes have no producer.
// BeginLocal, EndLocal and EndTurn have no production caller yet; ticket 15's
// executor will supply dispatch and proven effect endings.
//
// Host endings bind the original work, not a replacement origin or target.
// Agent CancelTurn selects its own current turn.cancel chain, which may differ
// from the admission grant; the owner derives that grant and accepts no supplied
// selection. Reassignment fences the old Agent entrance but does not prevent
// owner or host endings. A turn may end once every child has an ending record:
// unknown is an ending, but retains its existing local and unit accounting.
// Unknown may also end undispatched local work; it releases nothing.
// TurnStatus distinguishes dispatched and ended work and exposes no executor.
// The owner's TurnStatus refuses a supplied GrantID before looking up work.
// ReconcileAdmissions explicitly resolves one pending work item: proven-unsent
// local work is released, dispatched work without an ending becomes unknown,
// and an unmatched turn with no unended child becomes interrupted. It never
// starts work or guesses a known outcome for an already unknown-ended child.
//
// Owner capacity views show aggregate accounting. An Agent's own grant and
// capacity views show its fixed-grantee leaf and own originating usage and
// holds, even when that grant is no longer current. Own usage and Agent
// TurnStatus are session-exact: agent, session and context must all match.
// They disclose neither shared free capacity nor another origin's occupancy.
// Denial remains distinct from exhaustion, and no status result authorizes a
// subsequent operation.
//
// Scoped receipts use the frozen schema-2 semantic submission and contain no
// task definition or subject. A same-principal, same-digest retry returns its
// original receipt before CAS, runtime ceilings and durable capacity checks,
// but only after current authorization. Cold command lookup and a second tail
// scan avoid treating a cache or prefix miss as proof of absence. Before
// retaining task-description or private-write bytes, preparation reserves
// finite exposure; the retained copy is rehashed before publication. Commit
// preflight repeats authority and state checks, then validates the whole group
// with the same reducer used by Open. Publication, verified reads and private
// materialization do not hold the service lock.
//
// # Review and failure decisions
//
// RequestReview, RequestFailure, ResolveReview and ResolveFailure record a
// semantic judgment of bound inputs through the F3-12 family. They execute no
// check: CheckSHA256 and ConfigSHA256 identify the checklist and configuration
// judged against and are verified against nothing. An approve completes the
// task and creates no promotion or gate state. Subjects and evidence are
// artifact or private_revision references, verified by exact ledger source
// identity and metadata only; every other kind is ErrDenied, and no body or
// Markdown is read or copied. RequiredEvidence is nonempty and bounded by
// Limits.MaxReviewEvidence (16 by default; ErrTooLarge). The producer orders a
// copy of it by compact encoding, and a duplicate reference is ErrInvalid.
//
// A live task holds at most one request slot. A request names
// ReplacesRequestID exactly when a pending or rejected slot exists, and then
// names that slot; anything else is ErrReviewStale. A request bumps no task
// version. It binds the task's version, assignment, generation, assignee,
// subject and acceptance digest, the reviewer principal, the colony authority
// version, which must be current, and a deadline. A review request needs a
// submitted task; a failure request may name an assigned task in any
// nonterminal state, with a submitted task's own subject. A reject keeps the
// slot; an approve, a fail, a replacement, a cancellation, a reassignment or
// a task version change ends it. While the current version and assignment
// hold a pending or rejected slot, a direct TransitionTask complete or fail
// returns ErrReviewRequired, for the owner too, live and at replay. The
// owner's escapes are a replacement naming himself as reviewer followed by his
// own result, a cancellation or a reassignment. A direct fail citing a
// private_revision subject is still ErrDenied by the task-subject rule, before
// that guard.
//
// A requester proves its current task.complete chain (review) or task.fail
// chain (failure) naming the reviewer's binding as a recipient; a failure
// requester must be the assignee. The reviewer proves its own chain for the
// same operation on the task. Both participants need current read rights on
// the subject and every evidence reference: an artifact through an
// artifact.read chain or, for a task description, the task's audience and
// task.inspect; a private_revision only within the participant's own agent,
// and its own context for context scope, through private.read. The owner
// needs no chain. A result rechecks the reviewer's selected chain, the
// requester's recorded grant, the task's assignment and generation and both
// participants' read rights. It does not require the colony authority version
// unchanged, so an unrelated grant issue stales nothing, while a revocation in
// either chain is ErrDenied. An agent command charges one local unit to the
// colony, agent, context and grant scopes of its own chain; an owner command
// charges nothing.
//
// A new command is judged in two passes, in this order: the caller's current
// chain, under the service lock before any lookup; a same-ID match, which
// answers at once; the reducer's head (the request and its slot, the named
// reviewer, the task's version as RevisionError, its assignment and
// generation as ErrReviewStale, its state, acceptance and submission, both
// chains); trusted time on the first clock sample; the evidence and scope
// bounds; the process slot and an agent's reserved unit (BusyError); the
// source witnesses, prepared without the lock in one batch; every guard again
// under the lock at the refreshed prefix with the second sample; the whole
// reducer, which judges both participants' read rights and the sources, then
// time, then the kernel transition and its charge; and one Submit. A caller
// already past the head that lacks subject rights and is late therefore gets
// ErrReviewExpired; the deadline is a member of its own binding, so that
// discloses nothing.
//
// A result naming a replaced or terminal request is ErrReviewStale for its
// named reviewer. Any other caller gets ErrDenied for replaced, terminal and
// unused request IDs alike. ReviewStatus shows an Agent only a request it made
// or was named to review, with current task.inspect and read rights;
// ReviewCommandStatus shows an Agent only its own receipts, with the decision
// authority. Hidden and nonexistent requests and receipts are the same
// ErrDenied; the owner's ReviewCommandStatus returns ErrUnavailable for an
// absent receipt.
//
// The review clock is the service's own UTC wall clock, never a caller's or a
// record's time. It is sampled after the lookup and again under the lock
// before Submit; the second sample is the record's observed_utc_ns. A request
// needs a sample before its deadline, a result a sample at or before it;
// otherwise ErrReviewExpired. A failed, nonpositive or regressed sample is
// ErrReviewClock. The floor is the highest of the replayed envelope times,
// the prior F3-12 observations and every sample this incarnation has read
// successfully, including one read by a call a later guard refused. Neither
// refusal consumes or poisons anything. Open, ReviewStatus,
// ReviewCommandStatus and a matched retry read no clock. The store holds an
// envelope time when its clock goes back, so a held envelope never extends a
// deadline, and a backwards clock refuses new review decisions until it
// passes the floor. A deadline bounds the final decision sample, not the
// disk flush.
//
// A same-ID, same-digest retry returns the original receipt before the slot
// and request-ID checks, the deadline and the limits, and without a clock,
// but after the caller's current chain and read rights on every reference it
// names; a changed digest is ErrCommandMismatch. ReviewReceipt is the same
// value from the producer, a retry and ReviewCommandStatus: it names the
// receipt's own sequence and its effect's, never a read prefix. A request
// group has three records; an approve or fail has four (command, result, the
// task description's body unpin, receipt) and a reject three. The
// projection, the book and the receipt are installed before the
// acknowledgement. A commit that lands at the preflighted prefix installs the
// reducer's plan with only its envelope times read back; a duplicate or a
// non-adjacent commit refreshes and looks up the command as F3-11 does. An
// uncertain write poisons the service, and cancellation after Submit cannot
// undo the commit.
//
// Open replays F3-12 groups through the same reducer and refuses an
// inconsistent one, including a receipt whose outcome disagrees with its
// effect. A pending request stays pending with its bound deadline: nothing
// expires, resumes, dispatches or appends at Open. A crash before Submit
// consumes no command ID and leaves no reservation; a committed result whose
// acknowledgement was lost stays one transition, one unpin and one charge,
// and its retry returns the original receipt. The injected-crash tests prove
// this bookkeeping; they are not power-loss or isolation proofs.
//
// Task facts have one shared reading. An approve or fail review_result is a
// task transition that advances the task's version and state exactly as a
// task_changed complete or fail would, without a task_changed record. Task
// loaders, history, TaskStatus and dependent readiness consume both families
// in ledger order; a consumer of F3-11 alone cannot reconstruct the tasks of
// a log holding F3-12. Private transcript, memory or handoff text, artifact
// bodies, Markdown and a core/state.json file never decide a task or request:
// the state file is ignored, and foreign Markdown fences its scope with
// ErrIntegrity.
//
// Limits recorded with the ruling: (1) after a reject, F3-11's frozen task
// table has no submitted to active transition, so the ways on are a
// replacement and then an approve or fail, a cancellation or a reassignment;
// revise and resubmit needs a later type version. (2) A pending failure
// request on an active task goes stale when the assignee submits or waits,
// since its version moves; the requester requests again. (3) A requester must
// hold the decision authority it delegates, so an assignee without
// task.complete cannot request its own review; the completing authority or
// the owner does. (4) The guard binds the owner, whose escapes are those
// above. (5) A backwards system clock refuses new review decisions until it
// passes the floor.
//
// Costs, as built. Live state is one slot per live task and the request-ID
// and command-ID watermarks; there is no all-request index or snapshot. A
// fresh command or request ID above its watermark needs no history scan; one
// at or below it scans history. A new command reads its witnesses once, in
// one batch from the start of the segment holding the earliest witness to the
// commit holding the latest. With 64 MiB segments that start is in practice
// the log's, so the batch costs the witness's position, where the replaced
// form read the whole prefix. After Submit one commit's envelope times are
// read back. A cold receipt or request lookup scans the whole history; a
// result on a replaced or terminal request loads that request from history
// twice. Every new decision and every replayed F3-12 group rechecks both
// participants' read rights. For an Agent's artifact reference the
// artifact-read authority check takes the reference's source from the
// witness batch, checks the current chain of each of the Agent's grants, and
// loads only the task that the source names. These checks read the live
// projection; a task one of them needs that is terminal, and so evicted from
// it, is read by scanning the whole history. On the ordinary path the source
// names the task under review, which is live, so a decision costs about the
// participants' grants plus its witness batch, and Open adds that for each
// F3-12 group. An Agent's retry rechecks its own read rights under the lock
// the same way, except that a retry matched before the witness batch is read
// has none: each artifact reference then reads its source, from the start of
// the source's segment to its commit, and after an approve or a fail the
// reviewed task is terminal, so a reference naming it also costs at least one
// whole-history scan. Ticket 12 measured these costs with a throwaway driver
// on one Windows host; the figures are in its evidence. They are bookkeeping
// figures, not an SLA, a capacity or a population limit.
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
// never private content. An authorized Agent Snapshot shows its own scope's
// details; a context view reduces an inherited agent-scope failure to one fixed,
// hash-free agent_integrity fence. Agent private-integrity refusals are plain
// ErrIntegrity, with no hashes. Owner diagnostics are unchanged. No authorized
// foreign-file or missing-object repair path is provided here.
//
// # Reads and scratch projection
//
// Snapshot returns the selected revision, heads and availability/issues, not
// private bytes. Agent Snapshot requires context.inspect on the scope's entire
// declared slot set; it never reports unauthorized slots as absent. Agent
// ReadPrivate and WritePrivate require current exact-scope authority.
// ReadPrivate requires an explicit committed revision and byte budget.
// It reads verified immutable objects, never Markdown. Transcript pages
// contain whole occurrences; NextCursor is the last returned occurrence's ledger
// sequence when more remain, or zero at the end. If the first remaining chunk
// cannot fit, ErrTooLarge is returned rather than an empty advancing page.
//
// Agent read retries re-read and verify the originally selected objects or
// artifact, and recheck authority immediately before disclosure. They consume
// no second unit and ignore new-work Limits and max_operation_bytes, but must
// still acquire the shared preparation slot and transient local/buffer exposure.
// Private reads reserve selected backing allocations, including each object's
// one-byte growth check. Artifact reads reserve ledgerstore.MaxBodyBytes plus
// its growth-check byte, never just the caller's declared maximum. A physical
// capacity refusal is busy and writes nothing. ScopedCommandStatus needs no
// byte reservation or private-object/body I/O.
// Every accounting scope must have that buffer headroom: colony, agent, context
// if configured, and the leaf and each ancestor grant, including their own
// Limits.BufferBytes. Even a tiny artifact is busy when any such ceiling or
// remaining capacity cannot reserve ledgerstore.MaxBodyBytes+1.
//
// ReadArtifact requires an exact recorded source body reference and verifies
// both digest and declared size. An Agent needs an exact owner-rooted
// artifact.read chain, or a task description's audience and current
// task.inspect authority. Authorization precedes existence-sensitive errors.
// Missing, corrupt and collected bodies remain distinct authorized errors;
// no failure returns a partial body. ErrBodyCorrupt also reports a reference
// whose declared size disagrees with the digest-verified body's length; that
// case is an inconsistent reference, not evidence of damaged stored bytes.
// The owner's passive ReadArtifact requires no enrolment, consumes no unit and
// writes no group, but still shares the process preparation bound and uses the
// store's real maximum allocation. Its MaxBytes is bounded by
// Limits.MaxScopedBytes (4 MiB by default), so it cannot read a larger body.
//
// ScratchBinding is a read-only projection of frozen workspace scratch-created
// and scratch-closed records, keyed by workspace and colony-unique context. It
// reports absent, present, closed or mismatch to the owner, and distinguishes
// dedicated from temporary scratches. The Agent entrance requires file.read
// on its own workspace root (empty prefix), not merely a sub-prefix; a foreign
// or mismatched binding is denied. Neither entrance creates a scratch or
// selects another context.
//
// # Paid history and passive views
//
// Open consumes F3-13 without dispatching, sampling a clock, reading credential
// sources, or opening paid inputs, outputs or observations. The shared budget
// book retains exact current-period settlements, full unresolved holds across
// periods, and per-attempt violation fences. Late settlements and corrections
// remain in the admission period and use the admission profile. A cap change
// cannot release a hold or clear a violation. Acknowledgement retires the pinned
// profile for the colony's lifetime without refunding actual cost.
//
// Every paid child needs an ending before its turn can end. An unknown result
// ends that child but keeps its hold. Reconciliation lists held attempts and all
// credential versions still authorized without in-memory bytes, including their
// nonsecret source locators. An unfamiliar financial interpretation preserves
// exposure instead of inventing a zero charge or making historical Open fail.
//
// PaidAttemptStatus, PaidCommandStatus and PaidBudgetStatus return owned
// snapshots through a declared ledger sequence, never a permit to send. Agent
// views require current turn.inspect and disclose only the exact originating
// agent, session and context; shared budget terms remain opaque. Historical
// lookups use a cold ledger scan, not an indefinitely retained attempt index.
// VerifyInput explicitly checks the recorded body and request digest with the
// private object read bound; InputIntegrity reports availability independently
// of money. A missing input cannot erase a hold, settlement or fence. This
// integrity-only probe returns no private bytes and deliberately uses the same
// exact-origin turn.inspect authority as the snapshot, not private.read.
//
// # Paid preparation and admission
//
// OpenPaid explicitly enables live paid producers with bounded PaidLimits.
// ConfigurePaidRoute installs a complete, current supported profile using CAS;
// ConfigureSpend changes terms without resetting usage. The first colony terms,
// even disabled terms, fix the period kind. An agent may DelegateSpend only on
// its own issued child, within every ancestor's terms, for one local unit; later
// it may disable that child but never expand or re-enable its spend authority.
//
// BindCredential reads one environment or Windows Credential Manager source
// into bounded private memory and records only provider, scope, version, source
// kind and visible-ASCII locator. No value, digest or size enters the ledger.
// RevokeCredential and rotation revoke the old authorization durably before
// releasing its bytes outside the service lock. Release waits for all concurrent
// in-flight borrows (at most DispatchTimeout for paid exchanges). After every
// Core start the owner must bind a fresh version per scope; replay never
// rehydrates a source. Repeated rotations in one start are allowed.
//
// PrepareAnthropic takes an explicit route, credential version, parent turn and
// task binding. It reserves physical buffers before serializing, then seals and
// quotes the owned request once. PreparedPaidCall is opaque and belongs to that
// Service incarnation; Close releases unused preparation. AdmitPaid rechecks
// current model authority, task generation, every enabled spend ancestor and
// all finite ceilings. It publishes the raw sealed body as a private object,
// commits one schema-4 command/effect/receipt group and the full atomic hold,
// then returns an owned receipt and immutable BoundCall. Refusal consumes no
// command or financial exposure; the preparation can be retried until closed.
//
// Paid and review decisions share reviewClock and the same effective floor:
// committed envelope/observation times plus the incarnation's highest successful
// sample, including samples from refused calls. Historical replay uses only its
// prefix's committed floor and never consults a later live sample.
//
// # Paid dispatch and factual settlement
//
// DispatchPaid is a host-only one-shot entrance. It reserves an active exchange
// and 96 MiB of physical response/capture buffers, in addition to the retained
// preparation, and borrows the pinned credential before its final transition.
// Current authority, task generation, parent cancellation, route/profile,
// credential version, seal, quote and occupied spend are rechecked. The existing
// money hold is counted once. Only the invocation that newly commits the durable
// dispatch intent may send, outside the lock and inside Secret.WithBytes.
// The dedicated providers transport permits HTTP/1.1 only: no proxy, redirects,
// retry, replayable request body, idempotency header or transparent compression.
// Private serviceDeps can supply loopback roots and a raw TCP observer through
// providers.NewTransportWithRoots; PaidOptions cannot select a transport.
//
// DispatchPaid returns the intent receipt and an opaque ObservedPaidCall, not a
// settlement. Matching retries return the recorded receipt and any result view,
// never a send permit or fresh observation. RecordPaidObservation requires a new
// caller command ID and the handle, or an explicit host-attested TrustedEvidence
// object reference. It verifies object identity and derives usage/no_charge/
// unknown through the admission profile; callers cannot supply a price.
// Completed exclusive counters replace cumulative snapshots and price once.
// Missing, inconsistent or overflowing usage keeps the full hold. A documented
// no-charge refusal requires the pinned rule, explicit permitted category, no
// output witness and no content block ever begun. Haiku refusals stay unknown.
//
// CorrectPaidSettlement binds the predecessor result sequence and evidence,
// preserves known provider identities and the monotone output witness, and
// replaces one charge in the original period. Repeated evidence has no financial
// effect and does not consume another command ID. Factual settlement/correction
// use no clock, present-day credential or live authority, including after plain
// Open. AcknowledgePaidOverrun is owner-only, retains actual money, retires that
// profile digest across all matching routes without changing route CAS versions,
// and clears only the named fence. Other fences continue blocking exposure.
//
// ReconcilePaid works even with plain Open. It releases only a reserved,
// never-sent attempt; an intended attempt ends as unknown with its full hold.
// An active exchange cannot be reconciled. Close cancels exchanges and waits
// for their I/O and capture to drain before releasing slots, buffers and secrets.
//
// # Paid object encodings and disclosure
//
// Input remains the exact raw sealed UTF-8 request body, not a paid_input
// wrapper. The literal pinned secret is rejected in decoded input before its
// hash or publication. Output and observation objects live in the originating
// context's core/objects, published before a result selects their digest/size.
// They use compact encoding/json, decimal integers, lowercase digest strings,
// origin {agent_id,session_id,context_id}, and no trailing newline. These object
// encodings are not frozen ledger payloads.
//
// Output field order is schema:1, kind:"paid_output", attempt_id, origin,
// parent_turn_id, block_index, chunk_index, block. Text blocks are {kind:"text",
// text}; local tools are {kind:"local_tool_call",id,name,input}; opaque blocks are
// {kind:"opaque_provider",provider,model,artifact_kind,payload}. Thinking joins
// thinking/signature exactly; redacted_thinking retains the complete raw JSON
// block, including extensions. Opaque signatures are neither verified nor used
// as continuation authority. Text chunks split only at UTF-8 boundaries; every
// object is at most 1 MiB and the output sequence at most 8 MiB. A malformed tool
// may retain complete billable usage but never canonical output.
//
// Observation order is schema:1, kind:"paid_observation", attempt_id, origin,
// parent_turn_id, request_sha256, provider_request_id?, provider_message_id?,
// model?, http_status, headers, stop_reason?, stop_sequence?, stop_details?,
// raw_usage_start?, raw_usage_final?, raw_usage_merged?, output_started, complete,
// message_objects, error?, unknown_event_types, unknown_event_type_overflow,
// content_blocks, content_error, profile_violation, error_class?. Question marks
// mark omitted empty fields. Raw usage preserves null/absence and merged map
// keys sort lexically. Headers are sorted lowercase {name,values} from the
// provider allowlist only; errors are {type,message,details?,request_id?}.
// complete describes message completion, not certainty about billing.
// Unknown/incomplete/refusal/content-error captures select no output objects.
// The bounded capture rejects literal pinned authentication material in decoded
// provider fields, including opaque extensions, before any artifact is hashed.
// Observation estimates and encoded bytes are capped by the private store's
// 1 MiB object limit. Capture failure produces a fixed, nonsecret unknown
// observation, never successful truncation. No request object, authentication
// header, arbitrary response header, raw SSE or partial/refused text is persisted.
//
// ReadPaidObject selects only the recorded input or result objects of an attempt,
// including prior corrected evidence. Owner reads are passive; Agent reads need
// the exact originating agent/session/context and current private.read covering
// all four context slots (continuation, handoff, memory, transcript) as one target.
// A memory-only grant cannot read a whole exchange. Missing, unrelated and
// other-origin objects have the same denial. MaxBytes is at most 1 MiB; physical
// read reservations bound allocation. Authority is rechecked after I/O, returned
// bytes are owned, and no read samples a clock, charges or appends a record.
package agents
