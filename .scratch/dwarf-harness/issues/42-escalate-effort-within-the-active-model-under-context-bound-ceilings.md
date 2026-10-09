# 42: Escalate effort within the active model under context-bound ceilings

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R2 complete
Blocked by: 41

## What to build

Exhaust supported effort before tier escalation while enforcing every applicable ceiling.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization.
- Provider facts reverified; betas remain individually off unless explicitly enabled
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- Implement the provider-neutral ordered tier/effort ladder, balanced profile and context/turn-local escalation history; use cache-safe effort controls where actually supported.
- This phase activates effort controls, not R4's priced automatic model transitions. Unsupported required changes remain blocked rather than silently changing model/provider.
- Profile examples, p95 reasoning reserves and escalation counts are calibration values, not hard endpoint bounds.

## Acceptance criteria

- [ ] Load rejects unavailable models/effort levels for every enabled provider/model unless an explicit logged clamp is configured; effort:default omits the parameter. The selected balanced profile has no implicit provider/model or spend authorization.
- [ ] Choose within the highest matched floor and lowest ceiling, exhausting supported active-model effort before a later tier increase. A floor/ceiling conflict never lowers requirements, raises spend or rewrites the task.
- [ ] Apply required upgrades immediately with an explicitly supported cache-safe control where possible; unsupported betas stay off. Optional cache-breaking downgrades do not acquire R3's unbuilt optimization behavior or bypass existing safeguards.
- [ ] Final truncation may raise output one configured step; repeated schema/argument/tool failures may climb one supported rung. Respect per-turn escalation ceilings, context-local correction floors and scoped overrides; tool continuations/escalations never reclassify or borrow another context's failure history.
- [ ] Each escalation/output retry is a new sealed, bounded Core-admitted attempt. Context overflow triggers no blind resend/hidden summary, and missing usage from the earlier attempt retains its hold. At the applicable ceiling, return a scoped blocked outcome.
- [ ] Replay demonstrates net savings after the full separately admitted escalation cost and no meaningful success loss; report latency and resource overhead, not just a cheaper first call.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 27, 31, 32, 66, 73.
- Implementation decisions: 1, 2, 3, 4, 11, 12, 23, 24, 26.
- Testing decisions: 1, 2, 11, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R2
- Routing design §§8.1–8.4,10,13–14; Appendix C

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
