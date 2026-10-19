// Package graph implements U3's artifact/module analysis, strict replay and
// explicitly driven durable worker. It reuses F-U3-61's frozen ledger values and
// depends on contracts, never Core. Callers supply the real appender and an
// immutable artifact reader; graphfoundation is the Core wiring boundary.
// The standalone analyzers remain record-free and cannot grant durable work.
//
// Artifact identity is [birth_seq,path], never content. Module identity is
// [language,"workspace",native_name]; its version hashes native-confirmed
// members. Edges point from dependency module to consuming artifact, with the
// exact artifact digest, literal reference and analyzer configuration as proof.
// Hyperlinks, directories and contexts create no edges. State.Canonical uses
// ledger's JSON escaping/order, UTF-8 byte ordering and integer-only fields.
// Empty state hashes to ledger.EmptyGraphDigest. Content and evidence changes
// do not alter an otherwise unchanged key or ledger.EdgeID.
//
// AnalyzeOpaque proves immutable presence, not dependency completeness. Go
// remains adapter "go", dependency_unapproved, pending OG-GO; checks,
// declarations, resources and usage remain upstream_unfrozen. AnalyzeSQL is a
// bounded in-process recognizer for CREATE TABLE and SELECT ... FROM, not a
// database parser or executor. Joins, aliases, nesting, dynamic SQL and unclosed
// quotes/comments are unsupported_syntax; absent or ambiguous tables are
// unresolved_reference. Unquoted identifiers fold ASCII case; quoted ones keep
// their unescaped bytes. Partial parses never produce partial causal success.
//
// Analyzer owns at most one Python/Node child. Python uses -I -S -B, ast and
// PathFinder without importing target packages, including their initializers.
// TypeScript uses only the verified absolute lib/typescript.js, compiler AST
// and native resolution over corpus-only virtual files. Literal JSON config
// extends/references stay in that corpus; plugins and target configuration code
// never execute. Missing tools are adapter_unavailable, never an installation
// or regex/parser-only fallback. SQL runs no child and no query.
//
// The private graph-helper/v1 protocol is one uint32 big-endian length plus
// strict UTF-8 JSON in each direction. Unlike additive ledger records, every
// member is required and unknown, null, duplicate, unbound, unsorted, repeated,
// trailing, non-integer or out-of-corpus output is refused. Frozen field caps
// apply: keys/references 4096 bytes, scopes 8192, open tokens 64, weight_ppm
// 1..1000000. Profile identities are ledger.LimitsProfileV1 (engine-limits/1)
// and ledger.HelperProfileV1 (graph-helper-limits/1), not the framing name.
//
// The first-party helper profile is 30 seconds wall time, 2 seconds WaitDelay,
// 8 MiB framed JSON and decoded corpus, 1 MiB per source blob, 64 KiB retained
// stderr, and at most 10000 work/output items. Framed stdout overflow cancels and
// waits; stderr is truncated without cancelling. Failed invocations discard
// partial causal output. Scripts are embedded, copied as
// files to an owned neutral temporary cwd and passed by path, never command-line
// source. The separate corpus root and entire cwd are removed only after Wait.
// If an OS wait cannot establish direct-child exit, the Analyzer stays fenced
// and retains that directory; it admits no replacement child.
// The environment contains only SystemRoot/WINDIR when present, owned TEMP/TMP,
// LANG=C and LC_ALL=C. No inherited PATH, loader, Python, Node or Git overrides.
// This is a direct-child no-target-execution design, not A2 isolation, hostile
// same-user tamper resistance, or an OS CPU/RSS guarantee.
//
// Tools must first pass NewToolVerification/Step, with explicit quanta of at
// most 10000 units and one unit per 256 bytes. An incomplete digest is never
// verified; replacement/metadata change fails the verifier. A verifier is
// process-local: after restart reverify and retain all prior durable charges.
// Every helper launch checks the verified file identity again. Configuration
// identity includes the fixed script digests, Python root, SQL grammar and
// TypeScript options, and tools in node/python/typescript order (no SQL tool).
// Service owns cumulative limits and conservative retained-state charges:
// 512 bytes per node/key/scope/coverage entry, 256 per edge/evidence/adjacency
// entry, plus owned storage and encoded copies. Standalone calls grant no credit.
// The parent framed decoder also reserves 64 MiB conservatively for encoded
// and decoded copies and per-token allocations before constructing output.
//
// Service consumes only v1 workspace.registered, workspace.promotion_admitted,
// workspace.promotion_accepted, graph.analysis_started, graph.analysis_progress,
// graph.delta_chunk and graph.version_committed. One bounded AllTypes reader
// decodes only those payloads; unrelated records contribute only sequence and
// hash. Through and ChainHash always name the last fully applied commit.
// Open replays that prefix without a child, Git, parser, timer, body read or
// append. A nonzero Options.Through fixes a historical, read-only view.
//
// Replay enforces graph envelopes and commit shapes, recomputes the analysis
// key, binds exact registered/subject/frontier records and checks the base.
// It derives the structural counter, checks revisions and reservations, forms
// consecutive chunks without remove/put overlap or dangling evidence, and
// verifies both the payload-stream digest and State.Canonical graph digest.
// Only an eligible final publishes: its base, workspace subject and colony-wide
// acceptance frontier must still be current. Otherwise it closes as superseded
// without changing the image or counter. A nonempty final's actual sequence is
// the version; a zero-net final confirms its base version and digest. The
// F-U3-61 golden fixes encodings, not a service history, and Open refuses it.
//
// Step starts an attempt and returns, or reserves one 10000-unit quantum and
// runs one bounded unit, with at most two commits in that call. The 2000000-unit
// total is durable and never refunded. Walk, corpus and opaque units may make
// consecutive calls of their own kind, charging actual WorkUsed and stopping
// when complete, stalled or below 512 remaining units. Only calls with the full
// allowance count toward the two-call no-progress limit. Births come from the
// predecessor-tree and changed-path walk, never the incremental image. Opaque
// windows are pre-sized at 2+ceil(bytes/256) per artifact. A helper or SQL call
// covers its complete bounded corpus once per quantum; complete or scope-unknown,
// that adapter is finished. Tool-file verification is separate, resumable,
// charged work before invocation. Empty corpora skip verification and invocation.
// Immutable object bytes are memoized by Git OID within one attempt, never
// across attempts or recovery; acceptance events are never coalesced.
//
// The private candidate is capped at 64 MiB conservatively. Diff publishes only
// net fact mutations: remove edges, remove nodes, put nodes, put edges, then
// replace analyzed workspace coverage in key order. Whole-element chunks are at
// most 512 KiB, checkpointed in the same commit; no chunk publishes topology.
// Current state is bounded to 50000 nodes, 200000 edges and 512 MiB including
// indexes and retained history.
// Candidate overflow ends unknown without publishing. If valid replay instead
// reaches the retained-state cap, it stops at the last complete commit, exposes
// unknown/state_cap with zero version/digest and no image, and Step/Resume append
// nothing; every Page returns ErrRevision. A new limits profile is not defined here.
//
// Every acknowledged append hydrates the complete prefix through Committed.Last,
// including racing Core records, before publication. Uncertain or poisoned
// outcomes fence the service until a new Open discovers durable reality.
// A refused, closed or context-failed append discards private work, not its
// durable reservation. The next Step reconstructs from the durable prefix as
// Resume does; every recomputed unit gets a fresh reservation and charge.
// Open attempts are recovery_pending: only Resume of the exact ID can continue.
// It restarts interrupted invocations, charges them again, and byte-compares
// regenerated chunks with the durable prefix; disagreement is recovery_unknown.
// VerifiedCursor is recovery bookkeeping, not a helper input cursor.
// Exhaustion is sticky for source, scope, epoch, configuration and both profiles.
// A rebuild marks only the scope active at exhaustion, not completed or unreached
// scopes. A scope transition makes the next scope active before its first read;
// the last scope stays active through aggregate merge, diff and publication.
// Replay derives that scope's tuple from terminal coverage and the frontier.
// Reopen, a fresh UUID or an unrelated acceptance grants no new allowance.
// Only a marked-scope acceptance or new epoch/profile unblocks that marker;
// unmarked workspaces can still advance incrementally while a rebuild is blocked.
//
// With an available image, ready means no open attempt, runnable candidate,
// pending workspace or due rebuild. Declared gaps remain in Reasons, not State.
// Runnable or open work is pending even with a retained terminal reason.
// Unknown means due work has no runnable candidate; retained-cap fencing also
// exposes unknown without an image.
//
// Observe and Page use one immutable image while work is busy. Page requests
// bind version, digest, checked cursor and chain hash; stale headers get
// ErrRevision. No image mutex is held across I/O, a callback or child execution.
// ErrHistory refuses malformed durable input without publishing it, ordinary
// source/adapter failures produce explicit unknown coverage, and ErrRecovery
// fences uncertain persistence. Close cancels and joins work but owns no store.
// Every subsequent call, including Close, returns ErrClosed.
//
// Rebuild precedence is analyzer_epoch, digest_mismatch, structural_100, load.
// The projection check compares its recorded version/digest with the reducer's
// effective identity in constant time, never by an unreserved full hash.
// One rebuild walks every accepted workspace afresh from genesis, in UUID order,
// using each latest accepted tree as of its input frontier. Its source belongs
// to the frontier acceptance's workspace. It never seeds births from the image
// or incremental walk cache. Both candidate and durable-base canonicalization
// run within reserved units. Equal digests confirm the base without chunks;
// otherwise only net mutations publish. Cadence runs before the 100th subject's
// incremental work and publishes a new version, as does an analyzer epoch change.
// One eligible rebuild resets the structural counter and clears all due flags;
// unknown or superseded attempts do neither. Staleness is checked before every
// reservation, and a later Step selects again rather than retrying autonomously.
// This foundation is not accepted-promotion integration (ticket 63), U5 readiness
// or ticket 61's close.
//
// Native tests use absolute DWARF_GRAPH_PYTHON, DWARF_GRAPH_NODE and
// DWARF_GRAPH_TYPESCRIPT paths. The last names lib/typescript.js itself, not a
// directory or target-selected module. Unset variables skip only installed-tool
// rows with an explicit not-exercised reason; deterministic missing-tool,
// framing, overflow, lost-handshake and cancellation tests always run using the
// test executable as a real child. Smoke-profile versions are Python 3.13.15,
// Node v24.18.0 and TypeScript 6.0.3. Tests never install tools or run fuzz loops;
// FuzzSQLSubset, FuzzGraphHelperResponse and FuzzGraphReplayPrefix supply bounded
// seed corpora for the PM's exact-commit fuzz gate.
package graph
