// Package winexec runs one authorized Windows executable in a fresh regular
// AppContainer, with zero capabilities, three explicitly inherited standard
// handles, and an atomically assigned non-breakaway Job before initial resume.
// It is a Core-internal OS adapter, not a registered contract, scheduler, C3
// integration, C4 validator, publication channel, or A2 qualification claim.
//
// Each dedicated local root is one OS-locked physical slot. Run returns one
// host-private bounded receipt. A synchronized root exit plus native Job-zero,
// completed I/O ownership, durable closure and safe cleanup are all necessary
// for reuse; a successful kill request or root exit alone never suffices.
// Unknown resources leave a persistent unconfirmed fence. Explicit reopening
// may clean only validated closed fences; it never replays a request or clears
// quarantine. Open bounds recovery cleanup to five seconds or its caller's
// earlier deadline. Close forbids admission and releases the root lock last.
//
// Authorized input is copied into a sealed readable/executable tree. Work and
// the identity-bound native profile are private writable low-integrity trees.
// There is no scratch/profile hard disk quota and neither tree is exported.
// Duplicate and ASCII-case-colliding input paths are rejected before admission;
// native exclusive creation remains authoritative for other Windows aliases.
// Job memory limits native commitment, not physical RAM. Usage memory peaks are
// the reported native counters, not a claim of physical RAM or commitment.
// The time limit is aggregate user-mode CPU, separately from the hard CPU-rate
// cap and monotonic wall budget. Once stopping starts, StopGrace independently
// bounds proof, I/O completion and cleanup, including after a natural root exit;
// later cancellation or the wall deadline cannot consume that grace again.
// Approximately five seconds accommodates observed transient scanner references.
// A kernel API itself is not preempted by these between-operation deadlines.
//
// the child's environment reveals the host's LocalAppData path, hence the user name; it is required by AppContainer launch and is not a grant beyond the owned profile folder.
// the token holds ALL APPLICATION PACKAGES (`S-1-15-2-1`); any object that grants it is reachable; Core's protected objects must never grant it.
//
// Windows may rewrite LocalAppData and TEMP/TMP into the owned profile. The
// environment otherwise contains only native system paths and DWARF_WORK. No
// ambient credentials, PATH, home, console, or caller-selected handles are passed.
// LPAC was rejected on the qualified host because its zero-capability Winsock
// initialization failed with 10107 and broke the Go payload's ordinary file I/O.
// There is no runtime LPAC fallback, capability expansion, or network exemption.
//
// This is process-crash resource fencing on the exercised Windows filesystem,
// not a new power-loss guarantee. Unsupported platforms refuse without effects.
package winexec
