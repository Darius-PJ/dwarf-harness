// Package gitprocess owns workspace Git children, not an agent execution
// capability or a containment qualification. Windows launches use a named,
// non-breakaway Job with KILL_ON_JOB_CLOSE and a finite active-process limit.
// JOB_LIST assigns the suspended root atomically at creation; verified limits,
// membership and a durable running fence precede resume. Only three standard
// handles are inherited, never the Job handle.
// A completion port is associated while the Job is inactive and drained after
// closure. ACTIVE_PROCESS_LIMIT produces ErrLimit regardless of root exit code;
// it is not an expected Git exit. Microsoft does not guarantee that notification's
// delivery: a lost message leaves a refused spawn to Git's own exit status.
//
// Run streams with fixed buffers. On root exit it immediately terminates any
// remaining descendants, including detached Git maintenance, and preserves all
// bytes already in stdout/stderr while proving Job accounting zero, an empty
// PID list and joined pumps within one StopGrace. Cancellation and Close use
// the same proof. Git's atomic pack/ref writes tolerate termination; closure
// does not imply success or absence of an effect. An uninterruptible caller
// reader/writer retains the Owner lock and returns ErrUnconfirmed, not a false
// joined result. Callers must make their streams finite or interruptible.
// Run waits on the root's process handle and polls Job accounting and the PID
// list only during StopGrace after stopping.
// The Job-zero proof can precede completion of terminated members' kernel
// rundown, so an observer holding a descendant handle waits for its signal
// rather than assuming it is signaled at the instant of closure.
// An unconfirmed Run or Close retains its Owner and native resources in a
// package-private, append-only process-lifetime registry. Dropping the caller's
// reference cannot release the root lock through a finalizer. The same process
// cannot reopen that root; the next process reconciles its durable fences.
//
// Open holds an exclusive durable root lock named "lock", following the colony's
// durable-lock convention. Windows fences use canonical compact JSON plus LF,
// bounded to 4096 bytes, UUIDv7 root/run identities and
// never-reused Local Job names. Recovery never launches Git. A retained Job
// must have the exact limits before termination and positive zero proof. Only
// ERROR_FILE_NOT_FOUND in the recorded Windows session permits name_absent;
// a running fence additionally requires the recorded root PID/creation identity
// to be gone or signaled. Gone means opening the recorded PID reports no such
// process (ERROR_INVALID_PARAMETER), the opened process has another creation
// time or is signaled within StopGrace, or, after any other open failure, one
// accepted system process list has no entry with both the recorded PID and
// creation time; a listed identity that cannot be opened refuses without
// waiting. All other ambiguity retains the fence and refuses
// admission. Missing-name recovery has no exit code or process totals and
// infers neither success, effect absence nor refund. The kernel initiated every
// member's termination before releasing the name; the root is waited, but
// descendants are not individually waitable. Already-issued kernel I/O may
// complete after termination; no Git logic runs after that point.
//
// Each runs/<batch>.slots container reserves 32 sorted, never-reused UUIDv7
// names. Its exact 20,992 bytes are a 4096-byte canonical JSON header with zero
// padding, 32 write-once 512-byte root frames, and a 512-byte footer (32 hint
// bytes followed by zeros). The initial all-zero frames/footer, file length and
// name are published through <batch>.slots.pending before any Job is created:
// one file FlushFileBuffers and one write-through rename with a directory
// FlushFileBuffers per batch. One cached writable handle is held per container.
// Each root writes its exact PID/creation identity with one WriteAt and requires
// its own successful File.Sync before resume; a sibling's Sync never substitutes.
// Frame alignment is a layout property, not an atomicity guarantee.
//
// After native closure and all port/process/Job handles close, the closed
// observation precedes a cached retirement byte. The root frame never changes.
// The last completing member of an exhausted batch instead leaves its hint zero,
// closes the writer and deletes the container; successful Close does the same
// for a partial batch after joining all calls. There are no exit-path flushes or
// renames. Any write, close or removal failure retains the Owner and its lock.
// Metadata seams are per Owner and nil in production; they may run under
// reservationMu but cannot call Owner APIs or wait for another Run. Lifecycle
// observation callbacks never hold reservationMu, nor does a root's File.Sync.
//
// Open validates the entire layout before any sweep and reports loaded containers
// in Report.Reservations, even when Pending is empty. Reopened containers never
// allocate. Reconcile checks every reserved name: an all-zero frame takes the
// launch_pending proof; a valid running frame takes the running/root-identity
// proof even when its retirement hint is one. Hints suppress duplicate reports,
// not native proof. A nonzero invalid or torn frame refuses pending manual
// investigation, rather than being guessed unused. An unopenable live root of
// the package's own, a failed or inconsistent process list, or a failed
// self-check refuses; the list's creation-time member is reserved in
// Microsoft's layout and is checked against the current process's own entry;
// that check guards the layout, not the list's completeness.
// A container resurrected by a lost
// unflushed deletion is re-proved beside any newer container, never reused.
//
// Reconcile publishes a full canonical closed <run>.json before removing it for
// every root-bearing candidate or found Job. Only a zero frame with exact name
// absence elides that publication and produces no Closed result. A linked
// closed-only .json.pending is not proof: re-prove the candidate. On zero-frame
// name absence, remove that validated temp and sync the directory before retiring
// the container. Authoritative linked closed records consume durable proof.
// Standalone v1 fences (including closed/live) retain their original recovery.
// Open sweeps only valid matching initial .slots.pending or standalone
// launch_pending .json.pending files, with Report.Debris counting files, not
// names. Stale container temps survive until positive reconciliation. Before
// the first launch Open can finish or replace identity.json.pending.
// Unknown entries, nonregular files, wrong lengths, duplicate membership,
// invalid headers/frames/footer bytes and unsafe cross-links refuse without
// sweeping neighboring valid debris.
//
// Elsewhere the Owner keeps the root lock, cancellable admission and root/I/O
// joins, but writes no fences and never claims tree closure. RequireTree refuses
// before filesystem or process effects on those platforms.
//
// Spec.Started is an optional process-start observer, normally nil. It is called
// synchronously at most once after successful resume (and the Windows running
// fence), before Run waits. It must return promptly and must not retain or
// signal the process. An observer needing later process evidence opens its own
// handle by PID. A failed launch never invokes it. The notification is not a
// closure proof or an authority to launch another process.
package gitprocess
