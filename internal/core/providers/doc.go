// Package providers implements record-free Anthropic and OpenAI profiles, sealed requests,
// exact pricing, one-shot HTTPS transport and bounded response interpretation.
// It reads no clock, owns no authority or credential source, writes no ledger,
// and does not discover capabilities or tariffs. The trusted Core installer
// supplies source-verified profiles; loopback profiles prove mechanics only.
//
// CompileAnthropic and CompileOpenAI validate their respective supported
// text/local-tool/cache subsets and serialize once. Seal copies the bytes and
// nonsecret credential identity. Bound keeps the full inclusive input cap
// independently of requested output; Maximum and PriceUsage use exact rationals
// and one final ceiling.
// SHA-256 helpers are producer encodings, not a frozen ledger contract.
// Installation requires complete source topics and supported current semantics;
// historical profile/pricing entrances preserve the admission-time interpretation
// without applying current expiry or producer-size limits to valid history.
// NormalizeSealedUsage derives request-impossible classes from the sealed quote
// when a host later supplies complete evidence without the preparation buffers.
//
// Transport uses a dedicated verified-TLS HTTP/1 pool, a positive nonrewindable
// POST body and no idempotency headers, redirects, proxy or retry layer. Core
// must commit durable intent and consume its one-use send permit before Do.
// A stale keep-alive failure is unknown, not evidence of a free request. Limits
// are provisional host policy; the defaults suit a small drill, not a maximal
// generation. CloseIdleConnections does not replace Core's active-call drain.
// NewTransportWithRoots is a loopback-test seam: it changes trust roots and can
// wrap raw TCP connections, but keeps hostname verification, TLS handshake and
// HTTP/1-only selection inside the same constructor. It is not an alternate
// response or RoundTripper. Keep-alive is retained; GetBody remains nil and
// positive ContentLength prevents Go's zero-write replay path.
//
// The Anthropic SSE parser distinguishes complete message state from available
// cumulative billing evidence. Null invalidates a counter, omission retains it,
// cache aggregates are checked against exclusive children, and thinking stays
// inside output. Unknown or incomplete evidence is never a zero settlement.
// Canonical blocks escape only after message_stop. Argument-only failures and
// undeclared valid local names return an untrusted local_tool_request candidate
// with arguments_text, not executable JSON, without erasing final usage.
// Names outside the 64-byte ASCII model-tool grammar remain content failures.
// Thinking deltas and signatures concatenate exactly; the complete assembled
// assistant array is encoded once as StreamResult.Replay. Opaque extensions and
// redacted data remain uninterpreted. An unrenderable argument has no replay.
// ContentBlocks and OutputStarted retain evidence of begun output even when a
// refusal clears returned blocks. No-charge interpretation belongs to Core and
// requires the admission profile's dated rule and the complete refusal facts.
//
// CompileAnthropic accepts a sole assistant anthropic_replay Block with Replay
// holding the stored assembled array. Structured tool history stays Haiku-only;
// Sonnet/Opus require replay. CompileOpenAI accepts a sole assistant openai_replay
// Block holding the complete stored output array, preserving each item byte for
// byte, including phase and encrypted reasoning. Both require exactly one
// immediately following result per call, in call order, before any user text.
// Duplicate, unmatched and half-matched batches fail before sealing. Core alone
// verifies the source context/provider/model and frozen system/tools/history
// prefix; possession of replay bytes is not authority. Provider acceptance of
// byte-exact replay is not established by loopback tests.
//
// OpenAI tool_result blocks contain one text value, encoded as a
// function_call_output string, at most 64 KiB. Media, hosted tools, cache markers
// and provider-side continuation state stay unsupported. The compiler pins
// default/global service, store:false and implicit 30m caching with no prewarm.
// The Responses reader takes output and usage only from the first validated
// completed/incomplete/failed terminal; earlier snapshots never add counters or
// fill missing terminal fields. Cached reads/writes partition inclusive input,
// and reasoning is already inside output. Complete usage may settle an incomplete
// or failed answer; content validity is independent of financial availability.
// Served tier must be default. Model mismatch, unknown billing dimensions and
// absent/null required counters retain the hold. Unknown event, item or part
// types also retain the hold and mark a profile violation. Unknown
// null/plain-zero usage extensions and zero-only tool-usage objects do not
// introduce charges.
// Streaming response entrances emit decoded text deltas before any credential
// check; callers supply that gate. Observer errors disable presentation and
// content publication while independent terminal usage still drains.
//
// OpenAI's eight classes split cached, uncached, write and generated units into
// short/long bands. The versioned kguard schema selects short through 272000 and
// long above 278528 inclusive input tokens. The intervening interval returns
// ErrTariffAmbiguous with no bound violation and has no release path under this
// schema. Admission covers both bands independently of requested output. The
// shared 1 MiB SSE event/line limit applies to the whole terminal response,
// including encrypted reasoning: larger terminals are held, never truncated.
// Entity/output/event/item limits remain 16 MiB/8 MiB/262144/256. Reasoning output
// is opaque responses_reasoning JSON; neither it nor a response ID grants authority.
// Core's shared PaidLimits.MaxRequestBytes additionally caps every sealed body
// at 512 KiB by default and at most, below this package's 1 MiB RequestLimit.
// Requested/served default is distinct from the default_short/default_long
// pricing tiers. Short and long classes each contain input.cached,
// input.uncached, input.write and output.generated; reasoning is not an
// additional billable class. No OpenAI error or refusal implies no charge.
//
// openai-responses-text-30m-kguard/v2 additionally recognizes the optional
// cache_write_tokens_details split: absent/null means no split; an object must
// have plain non-negative ttl_30m_tokens and ttl_12h_tokens counters (absent/null
// counts as zero), with zero 12h writes and a sum equal to cache_write_tokens.
// The aggregate remains mandatory and is the only source of write units.
// Unknown usage members may be null, plain zero or object trees of those values
// at any depth; arrays and other values retain the hold with a profile violation.
// Tool usage keeps its separate zero-only rule. v1 retains its shipped
// scalar-only extension tolerance, so the split object still holds under v1.
//
// Core's caller sequence is explicit: install a sourced profile, bind a fresh
// credential version and configure all spend scopes, start an authorized parent
// turn, then PrepareAnthropic/PrepareOpenAI, AdmitPaid, DispatchPaid and
// RecordPaidObservation. Read status and context-owned artifacts separately.
// Neither a quote, a BoundCall nor a recovered receipt authorizes another POST.
// Plain Core Open performs no network or source read. Both adapters use the same
// one-shot transport, exchange pool and budget book; see package agents for the
// complete operating sequence and recovery/settlement rules.
//
// ResponseEvidence is untrusted provider data. Before publishing any artifact,
// its Core caller must apply the pinned-secret capture policy and the context's
// artifact encoding limits. This package performs no artifact publication,
// settlement, retry, tool execution or network call beyond an explicit Do.
//
// The bound turn loop has driven both adapters only against loopback TLS/SSE
// with synthetic profiles; its multi-call tool loops have made no live
// provider request. Loopback usage counters are fixture values, not provider
// tokenization, and loopback latency is not provider latency.
package providers
