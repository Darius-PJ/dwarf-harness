# 13: Dispatch and settle a sealed static Anthropic text attempt through Core

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 paid-attempt baseline, before operational R0/R1
Blocked by: 11

## What to build

Send one sealed, exactly priced attempt through Core; preserve ambiguous holds and settle without duplicate billing.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Explicit D52 approval of the integrated Core boundary; C2 workspace capability (tickets [07](07-merge-a-second-writer-through-temporary-scratch.md), [09](09-control-exact-one-off-policy-activation.md)) complete; relevant C1 ledger-format/reader/appender and producer-ready C3 families owner-frozen; owner-provided private remote with Windows/Linux/macOS CI. All are prerequisites, not approvals granted by these tickets.
- Each ticket owns the specification of its real producer payloads and golden examples; C1 records the additive family freeze, authorized callers, consumers, privacy and replay-essential fields. Pause before consuming/emitting an unfrozen family. No separate schema-only ticket or placeholder payload. Specific family/scope: for C3 reservations/dispatch/settlement/corrections
- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization.
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- Implement the first reusable Core BudgetBook paid path and canonical routing §§5.3/9.4 seal/bound behavior together with the handwritten Anthropic streaming adapter. This is not a second Router wallet or a temporary weaker dispatcher.
- Core resolves explicitly configured environment/native OS-store credentials into pinned non-secret scope/version references. Secrets remain solely under Core control; native launchers never hold them. No C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))/GUI prerequisite.
- Limit the executable path to supported standard text and included local-tool token data; no general turn/tool loop, dynamic routing, hosted tools, media, hidden retries or R11.

## Acceptance criteria

- [ ] Absent/revoked spend or model-call authority, missing credentials, unsupported/unpriced features and stale capability data send zero requests. Finite caps parse exactly to at most six decimal places; explicit unlimited still requires a finite CostBound, durable hold and settlement. Permission denial remains distinct from capacity exhaustion.
- [ ] Seal the exact HTTPS authority/path/method, model/region/tier, validated sorted non-secret semantic headers, pinned credential scope/version and exact UTF-8 body. Implement §5.3 length-framed request/quote hashes and checked exact rational pricing with one final upward rounding. Reject post-quote changes before intent; never log/hash authentication bytes or resolve a mutable current key at dispatch.
- [ ] Reserve the full independent inclusive input maximum and requested output cap, including reasoning, without subtracting output from input or double-counting tool/system input. Anthropic uncached/read/5-minute-write/1-hour-write classes are exclusive. Exercise accepted input-plus-max_tokens greater than context, input rates above output rates and overflow/overlap rejection.
- [ ] One atomic durable Core admission holds against sorted de-duplicated colony, agent, credential/account-within-colony, task/grant and every attenuated ancestor scope. Concurrent child calls cannot reuse parent allowance; grants do not pre-reserve money. Warnings apply only at the configured finite threshold, and rollups count each attempt once.
- [ ] The unchanged request and current authority are checked before durable dispatch_intent/SENT, then exactly one physical paid POST is possible. Establish the selected HTTP/1.1 profile: no SDK/middleware retry, redirects, rewindable body, idempotency-key header or HTTP/2 retry path. Pre-intent recovery may release only an unsent hold; cancellation, timeout, disconnect or SENT without complete usage retains the full unknown hold and never retries.
- [ ] Complete final cumulative usage replaces prior snapshots and settles once at the admission tariff; duplicate evidence neither charges nor releases twice. Unknown holds cross periods; late usage charges the original admission period. Record actual overruns without clamping and block new admissions until resolved; documented final no-charge evidence alone permits zero settlement. Controlled SSE/crash scenarios exercise real Core stores and transport, exposing raw cache/tier/header/error data and context-owned artifacts for later consumers.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 26, 27, 31, 32, 33, 67, 68, 69, 70.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 12, 23, 24.
- Testing decisions: 1, 2, 4, 8, 11, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3
- Core design §§3.2,8.3,13.5
- Routing design §§5.3,6.5,9.2,9.4,10–11; Appendix B
- Decision log D22,D33,D40,D50,D82,B7,B9–B10

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-24 — Ticket 11, this ticket's only blocker, is resolved: C3's scoped grants, tasks and non-paid capacity are in `internal/core/agents` on `main` ([B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11)). Its one budget book holds the owner's finite non-paid limits, consumed units and holds, with no money, tariff or paid field. Ticket 11's [ruling](../c3-evidence/ticket-11-design-ruling.md), P9, has this ticket add its paid hold and settlement records and microdollar ceilings to that book and its reducer, rather than a second wallet. Those records carry their own scope vocabulary: F3-11's `ScopeKey` is not required byte for byte, and the book's in-memory keys are not frozen. A turn admission executes nothing, and dispatch and endings enter only through host-only methods with no production caller (P10, P11). The other start gates above stand.
