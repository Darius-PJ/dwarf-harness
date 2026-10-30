# 14: Add OpenAI Responses to the same safe static paid-attempt path

Status: ready-for-agent
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

- [ ] An explicitly selected supported OpenAI text model executes and returns bound results through the existing Core hold/intent/settlement path; selecting another context or receiving a late stream cannot retarget transcript/artifacts or credential authority. Unsupported destination, model, feature or credential version refuses before send.
- [ ] Independent inclusive input/output maxima use the pinned endpoint profile; applicable long-context, cache-write and region rates contribute their exact maximum. Text plus reasoning share one output cap, and cached/write components are not added again to inclusive totals. Final usage and served-tier data settle at the tariff pinned to that attempt.
- [ ] Controlled Responses fixtures cover partial/missing usage, incomplete reasoning-only output, streamed cumulative snapshots, classified rate/quota/credential errors and disconnects; they produce the same honest held/settled states as the Anthropic path, not success inferred from EOF.
- [ ] Physical send instrumentation and crash windows demonstrate one POST per attempt for both real adapters; changed semantic envelopes cannot pass admission-to-intent revalidation. Recovery sends no provider requests, and an explicit owner retry receives a new attempt that must fit beside unresolved holds.
- [ ] Concurrent attempts across both providers and sibling grants respect shared colony/agent/ancestor ceilings, rollover and duplicate/correction semantics; provider-local configuration never creates a global invoice cap. Actual over-bound usage remains visible and admission-blocking.
- [ ] Expose safe static-provider baseline (ticket [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md)) to Main's TURNS without any R0/R1/full-CORE-RUNTIME dependency. Recorded-response and local SSE evidence is reported as such; live-provider claims require authorized live calls. Completing this ticket does not complete the C3 runtime or allow real colony delivery.

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
