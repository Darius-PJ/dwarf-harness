// Package graph implements the record-free artifact/module portion of U3.
// It reuses F-U3-61's frozen ledger values and owns no records, appender, service,
// replay, accepted-source listener or Core dependency. Callers derive artifact
// birth sequences from frozen history, obtain immutable regular blobs through
// Core's workspace reader, reserve work before invocation and publish later.
// These helpers are not a substitute for the durable worker of ticket 61 S3.
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
// The caller owns cumulative limits and conservative retained-state charges
// (512 bytes per node/key/scope/coverage, 256 per edge/evidence/adjacency plus
// actual storage); no S1 call grants or refunds durable allowance.
// The parent framed decoder also reserves 64 MiB conservatively for encoded
// and decoded copies and per-token allocations before constructing output.
//
// Native tests use absolute DWARF_GRAPH_PYTHON, DWARF_GRAPH_NODE and
// DWARF_GRAPH_TYPESCRIPT paths. The last names lib/typescript.js itself, not a
// directory or target-selected module. Unset variables skip only installed-tool
// rows with an explicit not-exercised reason; deterministic missing-tool,
// framing, overflow, lost-handshake and cancellation tests always run using the
// test executable as a real child. Smoke-profile versions are Python 3.13.15,
// Node v24.18.0 and TypeScript 6.0.3. Tests never install tools or run fuzz loops;
// FuzzSQLSubset and FuzzGraphHelperResponse supply bounded seed corpora for the
// PM's exact-commit fuzz gate.
package graph
