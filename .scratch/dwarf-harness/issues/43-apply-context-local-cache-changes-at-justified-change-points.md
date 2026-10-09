# 43: Apply context-local cache changes at justified change points

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R3 complete
Blocked by: 42

## What to build

Price context-local apply/defer decisions and run bounded background compaction; never delay revocation or prewarm.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization. Specific family/scope: for compaction
- Owner agreement where compaction is lossy/user-chosen handover work
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.
- No prewarm exception is authorized

## Scope boundaries

- Activate pending-change pricing, TTL/breakpoint selection and bounded selected-context background compaction (D88).
- Reuse bounded prediction metadata and D51 shared-prefix pricing without treating another context's history as warm.
- Preserve D24 no-prewarm/no-staggering despite surviving §9.7 wording; do not amend sources in this ticket proposal.

## Acceptance criteria

- [ ] Price only the selected context's own rendered history and actual target cache state, keeping shared tools/system pricing separate. Correct predictions from exclusive reported usage, never invent segment attribution, and treat evicted prediction entries as cold.
- [ ] Mandatory context switches, revoked access, required capabilities and user overrides take effect before the next call, using supported cache-safe controls if available. No cache saving permits stale disclosure or delays a required change; unavailable capability blocks.
- [ ] An optional cache-breaking change applies now only when cost(apply)×(1+downgrade_margin) is strictly less than cost(defer); equality defers. Pending changes are bounded and re-priced at real prefix-rebuilding change points; view focus, another context's activity and cache-neutral events are not change points.
- [ ] Honor supported adapter cache minima, at most four breakpoints and Anthropic lookback/TTL rules; use documented OpenAI retention/scope/key behavior. Accept simultaneous cold-prefix double payments; send no prewarm requests and never stagger turns to avoid them.
- [ ] Under an owner-agreed policy, background compaction summarizes an immutable snapshot of a bounded authorized prefix ending at a complete exchange, pinned to its source identity, summary/render base and context/authorization binding. It is a read-only internal job, never a second live turn or tool loop: at most one pending or in-flight job per context, finite input/output, wall-time and memory/work bounds, counted against existing concurrency and resource limits, with every paid call using its own ordinary Core reservation and one-shot dispatch and no free retry.
- [ ] Foreground work keeps appending while the job runs. A short Core compare-and-publish transition at a safe request-compilation boundary adopts the result only when the covered prefix and summary base still match, current authorization covers every retained input and the binding is compatible, keeping the newer suffix unchanged and in order. Output from a replaced prefix, superseded base, incompatible binding or revoked input is discarded without a model call; the summary changes future rendering only, never canonical transcript or event history, and when no bounded rendering fits the turn stops explicitly instead of dropping the suffix, summarizing full history on a target provider or cascading summaries.
- [ ] Compaction scheduling uses coalesced bounded opportunities from the existing scheduler and cache planner, calibrated from context growth, foreground delay, task success and total cache/compaction cost rather than a copied trigger percentage, and is compared with the ordinary uncompacted path under the same declared workload, counting paid calls, discarded work, foreground stalls, CPU/RAM and cost per successful task.
- [ ] Authorized observed/replay evidence shows hit rate at least the static baseline without success regression; include compaction/write/cold-rebuild costs and memory/latency overhead. Expected horizon and cache estimates never reduce endpoint reservations.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 32, 66, 74.
- Implementation decisions: 1, 2, 3, 4, 12, 23, 24, 27, 28.
- Testing decisions: 1, 2, 11, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R3
- Routing design §§6.5,8.3–8.5,9.1–9.7,13–14; Appendices A/C
- Decision log D24,D51
- spec.md Further Notes: Surviving source tension—prewarming

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover D88 bounded background compaction, replacing the earlier scheduled-compaction criterion. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.
