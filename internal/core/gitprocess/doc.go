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
// to be gone or signaled. All other ambiguity retains the fence and refuses
// admission. Missing-name recovery has no exit code or process totals and
// infers neither success, effect absence nor refund. The kernel initiated every
// member's termination before releasing the name; the root is waited, but
// descendants are not individually waitable. Already-issued kernel I/O may
// complete after termination; no Git logic runs after that point.
// The flat runs/<uuid>.json layout publishes launch_pending and running only.
// Live closure is proved natively; after the port, process and Job handles close,
// the fence is removed without a directory flush. A crash between proof and
// removal leaves running, which recovery closes by name_absent or retained_job,
// with ExitCode -1 and no success inferred. A fence that cannot be removed keeps
// its Owner retained. Reconcile alone publishes closed before removal; existing
// closed fences, including closed/live, remain decodable and recoverable.
// Open removes only a lone <uuid>.json.pending that strictly decodes as
// launch_pending for this root and run. Before the first launch it can finish or
// replace an interrupted identity.json.pending; Report.Debris counts these
// shapes. A stale .json.pending beside an authoritative .json is removed only
// after positive reconciliation. Every runs entry must be a regular file with
// a canonical UUIDv7 name and one of those two suffixes. Unknown entries,
// directories and unsafe pending-only states fail closed without a sweep.
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
