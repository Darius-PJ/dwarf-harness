# 14: Add OpenAI Responses to the same safe static paid-attempt path

Status: resolved
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 paid-attempt baseline; final safe static-provider baseline (ticket [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md)) anchor
Blocked by: 13

## What to build

Add a complete OpenAI Responses path using the same durable wallet and dispatch guarantees.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Explicit D52 approval of the integrated Core boundary; C2 workspace capability (tickets [07](07-merge-a-second-writer-through-temporary-scratch.md), [09](09-control-exact-one-off-policy-activation.md)) complete; relevant C1 ledger-format/reader/appender and producer-ready C3 families owner-frozen; owner-provided private remote with Windows/Linux/macOS CI. All are prerequisites, not approvals granted by these tickets.
- Each ticket owns the specification of its real producer payloads and golden examples; C1 records the additive family freeze, authorized callers, consumers, privacy and replay-essential fields. Pause before consuming/emitting an unfrozen family. No separate schema-only ticket or placeholder payload. Specific family/scope: for any genuinely additional versioned payload
- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization.
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- Implement handwritten standard OpenAI Responses text streaming, usage/error interpretation and opaque context-owned artifacts over the same Core dispatcher.
- Complete the reusable C3 adapter surface: canonical-context inputs, raw cache usage and served tier, rate-limit headers, classified errors and provider continuation artifacts. R0 wraps these adapters; R1 does not replace them.
- Core OS credential sources remain Core-owned; later C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))/GUI only consume authorized typed operations. Default store:false; no implicit provider/model, stored-state dependency, Flex/Batch/media/hosted tools or new wallet.

## Acceptance criteria

- [x] An explicitly selected supported OpenAI text model executes and returns bound results through the existing Core hold/intent/settlement path; selecting another context or receiving a late stream cannot retarget transcript/artifacts or credential authority. Unsupported destination, model, feature or credential version refuses before send. Tests: `TestOpenAIPaidBoundResults`, `TestOpenAIPaidRefusalBeforeSend`, `TestOpenAIPaidCredentialPinning`, `TestOpenAIProviderIsolation`, `TestOpenAICompilerRefusesUnsupportedInput`, `TestOpenAIPaidNewCaptureSecretSurfaces`.
- [x] Independent inclusive input/output maxima use the pinned endpoint profile; applicable long-context, cache-write and region rates contribute their exact maximum. Text plus reasoning share one output cap, and cached/write components are not added again to inclusive totals. Final usage and served-tier data settle at the tariff pinned to that attempt. Tests: `TestOpenAIBoundAndSettlement` in both providers and agents, `TestOpenAIHistoricalBandValidation`, `TestOpenAIPaidHistoricalEvidence`, `TestOpenAIUsageV2Split`, `TestOpenAIUsageV2SettlementAndReplay`. Unsupported regional endpoints refuse rather than use an unpriced uplift.
- [x] Controlled Responses fixtures cover partial/missing usage, incomplete reasoning-only output, streamed cumulative snapshots, classified rate/quota/credential errors and disconnects; they produce the same honest held/settled states as the Anthropic path, not success inferred from EOF. Tests: `TestOpenAIResponsesEvidence`, `TestOpenAIStreamFramingIdentityAndFinality`, `TestOpenAIStreamTierToolAndContentPolicies`, `TestOpenAIResponseClassificationAndHeaders`, `TestOpenAIResponseProtocolAndErrorEvents`, `TestOpenAIPaidEvidenceStates`, `TestOpenAILiveUsageSchema`, `TestOpenAIUsageUnknownAndNull`, `TestOpenAIUsageV2UnknownTrees`.
- [x] Physical send instrumentation and crash windows demonstrate one POST per attempt for both real adapters; changed semantic envelopes cannot pass admission-to-intent revalidation. Recovery sends no provider requests, and an explicit owner retry receives a new attempt that must fit beside unresolved holds. Tests: `TestPaidHTTP1OneShot`, `TestTransportUsesOnlySealedHeaderSetAndBody`, `TestTransportALPNHeadersAndStatusesNeverRepeat`, `TestTransportRedirectDestinationReceivesNothing`, `TestPaidIntentBeforeBytes`, `TestPaidInjectedCrashRecovery` (35 Anthropic and 33 applicable OpenAI windows), `TestPaidCloseDrainsAndReconcileHolds`, `TestPaidExplicitNewAttemptAfterUnknown`.
- [x] Concurrent attempts across both providers and sibling grants respect shared colony/agent/ancestor ceilings, rollover and duplicate/correction semantics; provider-local configuration never creates a global invoice cap. Actual over-bound usage remains visible and admission-blocking. Tests: `TestPaidMixedProviderScopes`, `TestOpenAIBoundAndSettlement`, `TestOpenAIPaidHistoricalEvidence`, `TestPaidGroupRulesAndFrozenReplay`, `TestGoldenOpenAIStatic`; agents package race and twentyfold affected-row race runs.
- [x] Expose safe static-provider baseline (ticket [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md)) to Main's TURNS without any R0/R1/full-CORE-RUNTIME dependency. Recorded-response and local SSE evidence is reported as such; live-provider claims require authorized live calls. Completing this ticket does not complete the C3 runtime or allow real colony delivery. Proof: S3's throwaway typed-Core smoke exercised both real adapters over local TLS/SSE, bound object reads and plain reopen without extra requests; the three separately authorized live attempts are distinguished in the Answer.

## Answer

The providers adapter compiles stateless OpenAI Responses text requests and interprets bounded terminal streaming evidence. Core's `PrepareOpenAI` and provider switches use the existing book, holds, sealed intent, one-shot transport, context-owned artifacts, settlement and corrections, not a second wallet. F3-13 gains additive OpenAI producer values and the chapter-14 golden; earlier goldens and encodings are unchanged. Usage schemas `openai-responses-text-30m-kguard/v1` and `/v2` remain accepted for installation and replay: v2 recognizes the cache-write TTL split and recursive null/zero extension objects without reinterpreting v1 history or inferring mandatory counters. Both-adapter crash and physical-send proofs establish the local safety boundary. The [agents](../../../internal/core/agents/doc.go), [providers](../../../internal/core/providers/doc.go) and [ledger](../../../contract/ledger/doc.go) package documentation are the operating and producer references.

Live evidence is three separately authorized attempts, not an invoice or a server-side request-count measurement. Each uses `gpt-6-luna`, the full USD 0.233572 maximum, four USD 1.00 lifetime ceilings without warnings, one read-only pre-flight GET and a fresh throwaway colony. Attempt 1 returned HTTP 200 but refused in-stream for lack of API credits; Core classified it `exhausted`, retained the full 233,572-micro hold and settled zero. Attempt 2 completed at the provider (served model `gpt-6-luna`, tier `default`, 9 input and 5 output tokens, zero cached/write tokens, one text block), but Core held the full maximum under v1 for the undocumented nested cache-write split: a profile violation, zero settled, not a successful Core settlement. Attempt 3: with usage schema v2 the same request completed (served `gpt-6-luna`, tier `default`, 9 input and 5 output tokens) and settled at 4 micros, releasing the 233,572-micro hold; a plain reopen left money and the frontier unchanged. This is the first live OpenAI settlement through Core; attempt 2's retained throwaway colony keeps its profile-violation fence until acknowledged. No attempt was automatically retried.

Limits: both schemas hold inclusive input 272,001–278,528 as `tariff_ambiguous`, with no guessed band, no fence and no release path. Holds from unknown event/item/part types, missing served tier, served-model mismatch, or missing/null/inconsistent/overflowing required counters likewise have no public late-evidence release path. Profile violations fence the attempt's scopes; these include unknown event/item/part types, non-default tier, nonzero tool usage and inconsistent required counters (overlapping or overflowing inclusive sums). Missing tier, model mismatch, missing, null or overflowing required counters and the tariff-ambiguity interval hold without a fence. A profile-violation hold is classed `protocol_error`. The supported subset excludes regional endpoints, hosted/media tools, stored-state dependence, Flex/Batch and automatic retries. A billable non-refusal Anthropic terminal with zero output blocks still fails result recording and retains its hold; ticket [141](141-settle-billable-anthropic-terminals-with-no-output-blocks.md) owns that pre-existing defect without changing F3-13.

No turn/tool loop, R0/R1 integration, global invoice cap, production-delivery approval or capacity claim is included. C3 status remains solely in its [Status line](../../../docs/ROADMAP-CORE.md#c3-agent-runtime) and [board row](../../../docs/ROADMAPS.md#status-board); contract versions and status remain in the [register](../../../docs/CONTRACTS.md).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 26, 27, 31, 32, 33, 66, 67, 68, 69, 70.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 12, 23, 24.
- Testing decisions: 1, 2, 4, 8, 11, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 adapter and sealed-dispatch deliverables
- Routing design §§5.3,6.5,8.5,9.4,10–11; Appendix B
- Decision log B9–B10

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-30 — Ticket [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md), this ticket's only blocker, is resolved ([B36](../../../docs/DECISIONS.md#b36-c3-sealed-static-anthropic-paid-attempts-ticket-13)). It supplies the shared Core book, pinned credential sources, sealed request/quote, atomic hold, one-shot HTTP/1.1 dispatch, evidence-backed settlement, corrections, unknown-hold recovery and context-owned object reads. This ticket adds OpenAI Responses on that path, not a second wallet or an automatic retry. The owner authorized an OpenAI live drill and placed it here ([D150](../../../docs/DECISIONS.md#d150-the-authorized-live-drill-moves-to-ticket-14-against-openai)); plan it in this ticket's design, using his privately provisioned environment credential, and run it only after the local evidence and the drill's model-fact, spend and authorization gates are satisfied. Ticket 13 made no live Anthropic call and verified no live profile. The other start and authority gates above stand.

2026-10-30 — Claimed after ticket 13's close, with the shared paid path as the baseline. DesignT14 supplied the brief from ReaderT14OpenAI's dated facts; fresh Fable advisor RuleT14 approved with seven amendments. Closed provider switches, exclusive tariff classes, complete terminal evidence and an additive F3-13 extension govern the work.

2026-10-30 — The owner selected USD 1.00 lifetime ceilings in each of the drill colony, agent, credential and root model-call grant scopes, with no warning threshold, and authorized one `gpt-6-luna` attempt at the full USD 0.233572 maximum after S1–S3 local evidence, the additive producer record and pre-flight checks. He kept the read-only model GET. No breaking-contract gate was triggered.

2026-10-30 — S1: CoderT14S1 built the record-free Responses adapter, sealed provider isolation, exact accounting and both-envelope physical-send proofs at 6e689bb, rebased as fda20d6. ReviewT14S1 approved with notes; D/F/G rule breaks failed their named tests and restored green, and five 120-second fuzz campaigns passed. Check 18 (run 41081773914) succeeded on 6e689bb; subsequent integration uses the final combined exact-SHA check.

2026-10-30 — S1 follow-up: e32e638, rebased as 4d70acf, refuses asynchronous/nonlocal function routing, tolerates zero-only tool usage in both placements, shares the request-size convention and removes a dead check. The nested-tool money break failed then restored green, and both changed OpenAI fuzz targets passed fresh 120-second campaigns. ReviewT14S2 approved the follow-up with S2.

2026-10-30 — S2: CoderT14S2 added Core's shared-book OpenAI preparation, dispatch, capture, settlement and replay, plus F3-13's chapter-14 golden, at b967263 and 077ad26, rebased as c23cfd5 and afcae02. ReviewT14S2 approved with notes; A–F/H breaks failed then restored green, mixed-provider race proofs passed, and the replay fuzz campaign passed. [D163](../../../docs/DECISIONS.md#d163-openai-producer-values-added-to-f3-13) records the additive vocabulary. The outgoing scan refused a synthetic credential-like test declaration; its name was corrected in the producer commit before publication, with no scan override.

2026-10-30 — S3: CoderT14S3 added both-adapter crash/recovery and explicit-new-attempt proofs, the typed-Core local smoke and operating documentation at dffa8a5, rebased as 52453e5. ReviewT14S3 approved the slice, driver and live profile. Thirty-five Anthropic and 33 applicable OpenAI crash windows passed; C/G breaks failed then restored green. The PM added the review's limit sentences at 947de7e and d9e50a6 for close review.

2026-10-30 — Live attempt 1 passed the pre-flight GET, then returned HTTP 200 with an in-stream lack-of-API-credits refusal. Core recorded `exhausted` with the full 233,572-micro unknown hold and zero settled; no live settlement or retry occurred. The owner added API credits and separately authorized one further attempt with the same request, maximum and four ceilings in a fresh throwaway colony.

2026-10-30 — Live attempt 2 completed at the provider with 9 input and 5 output tokens, but v1 held the undocumented nested cache-write split as a profile violation at the full 233,572 micros, zero settled. The owner chose a fourth slice to recognize the real usage shape, followed by exactly one more attempt with unchanged request, maximum and ceilings in another fresh throwaway colony.

2026-10-30 — S4: a fresh Fable ruling approved the PM's follow-up brief with five amendments. CoderT14S4 implemented usage schema v2 beside unchanged v1 at 1e7d2be; [D164](../../../docs/DECISIONS.md#d164-openai-usage-schema-v2-for-the-cache-write-split) records the additive token. The split/no-inference and historical-version money breaks (a)–(d) failed then restored green; the local live-shaped fixture settled four micros and replayed unchanged, without a golden change. Both changed-code fuzz targets passed 120-second campaigns on 5e744f6. The PM's ledger/register sentences are 5e744f6. S4 review: approved by a fresh Fable advisor; its text corrections are f9fa809 and 5037284, and D164 took its four precision edits.

2026-10-30 — Live attempt 3: with usage schema v2 the same request completed (served `gpt-6-luna`, tier `default`, 9 input and 5 output tokens) and settled at 4 micros, releasing the 233,572-micro hold; a plain reopen left money and the frontier unchanged. This is the first live OpenAI settlement through Core; attempt 2's retained throwaway colony keeps its profile-violation fence until acknowledged.

2026-10-30 — Closed with the six criteria's evidence above. Hosted check: check 25 (run 41112339962) succeeded on 5037284. Merge: `main` ce4a67a..5037284. Close review, including the PM's documentation sentences: approved by a fresh Fable advisor after two factual corrections. Ticket [141](141-settle-billable-anthropic-terminals-with-no-output-blocks.md) carries the Anthropic zero-output-block defect. Public tracker/paired-roadmap edits and private design/evidence records land separately (D145); no build-record entry is added (D161). The other start and authority gates above stand.

2026-10-30 — Ticket [141](141-settle-billable-anthropic-terminals-with-no-output-blocks.md) is resolved: a billable, non-refusal Anthropic terminal with no output block now settles on its usage and releases its hold, so the zero-output-block limit in the Answer above no longer applies.
