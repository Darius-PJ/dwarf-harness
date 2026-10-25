// Package providers implements record-free static Anthropic request foundations.
// It reads no clock, owns no authority or credential source, writes no ledger,
// and does not discover capabilities or tariffs. The trusted Core installer
// supplies source-verified profiles; loopback profiles prove mechanics only.
//
// CompileAnthropic validates the supported text/local-tool/cache subset and
// serializes it once. Seal copies those bytes and the nonsecret credential
// identity. Bound keeps the full inclusive input cap independently of requested
// output; Maximum and PriceUsage use exact rationals and one final ceiling.
// SHA-256 helpers are producer encodings, not a frozen ledger contract.
//
// Transport uses a dedicated verified-TLS HTTP/1 pool, a positive nonrewindable
// POST body and no idempotency headers, redirects, proxy or retry layer. Core
// must commit durable intent and consume its one-use send permit before Do.
// A stale keep-alive failure is unknown, not evidence of a free request. Limits
// are provisional host policy; the defaults suit a small drill, not a maximal
// generation. CloseIdleConnections does not replace Core's active-call drain.
//
// The bounded SSE parser distinguishes complete message state from available
// cumulative billing evidence. Null invalidates a counter, omission retains it,
// cache aggregates are checked against exclusive children, and thinking stays
// inside output. Unknown or incomplete evidence is never a zero settlement.
// Canonical blocks escape only after message_stop; malformed local-tool JSON
// discards output while preserving independent final usage. Opaque thinking
// signatures and redacted provider payloads remain uninterpreted.
//
// ResponseEvidence is untrusted provider data. Before publishing any artifact,
// its Core caller must apply the pinned-secret capture policy and the context's
// artifact encoding limits. This package performs no artifact publication,
// settlement, retry, tool execution or network call beyond an explicit Do.
package providers
