# 41: Qualify the complete R1 path against its static baseline

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: Whole R1 acceptance; final complete R1 capability (ticket [41](41-qualify-the-complete-r1-path-against-its-static-baseline.md)) anchor
Blocked by: 39, 40

## What to build

Prove complete R1 behavior and measured cost against the unchanged static baseline.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- R1 owns standard-library-only context-module plus golden samples. Owner freeze precedes runtime consumer cutover and U6 consumption; C3's fixed D25 note remains until the one-time cutover.
- Frozen C1 ledger-format, ledger-reader and ledger-appender, including the actual R1 provider/headroom/budget-exposure/usage family. No law-format/U1 dependency.
- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization. Specific family/scope: for authorized live experiments
- Replay consent and declared comparison workload
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- This is a genuinely distinct cross-feature empirical/integration qualification, not a generic tests-after-implementation ticket. Keep each behavior's deterministic regressions in its owning ticket.
- Use the actual runtime, Core stores and real adapters; preserve controlled fixtures where needed for inaccessible failures and label their evidential limits.
- No C6 role drill, U1 law or C4 completion dependency is introduced.

## Acceptance criteria

- [ ] On declared internal test jobs, compare R1 with R0's static baseline and show no success regression plus measurable net savings. Include classifier/helper/social/summary/compaction/retry costs when exercised; report success, cost per successful task, latency and escalation rather than only reduced main-agent token count.
- [ ] Exercise the combined turn→module render→seal/quote→Core hold→intent→provider→settlement/R11 path and provider handover. Mutation, mismatch, restart and explicit retry scenarios preserve one-shot sends, immutable quotes, unknown holds, original admission-period settlement and unclamped overrun blocking.
- [ ] The two-adapter integration includes the independent-inclusive Anthropic overflow case, input-rate-above-output-rate tariffs, nonduplicated tool/cache/reasoning usage, quoted semantic-header/endpoint/credential changes and exact rounding. These are real admission-boundary checks, not a replacement budget mock.
- [ ] Combined module cases prove sum/allowance and 64-KiB enforcement, complete blocks, explicit omission, valid zero output and required-versus-optional handling. R11 appears once at correct scope and cannot leak exact private facts through the authorized projection boundary.
- [ ] Measure local guard/accounting overhead and whole-process CPU/RAM/disk/latency on stated modest hardware and declared concurrency. Keep current capability-source dates, owner freezes and live-versus-fixture evidence explicit; document a failed gate as failure rather than shrinking the workload or claiming historical arithmetic as measurement.
- [ ] Only passing all R1 exit criteria establishes complete R1 capability (ticket [41](41-qualify-the-complete-r1-path-against-its-static-baseline.md)) and unblocks R2/U5/U6/U7 as their own dependencies permit. The C6 relationship/board drill remains with C6 role-route capability (ticket [27](27-use-the-complete-role-graph-and-communal-boards-without-context-leakage.md)), and pre-C4 results remain testing-only.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 32, 33, 65, 66, 67, 68, 69, 70, 71, 72, 76.
- Implementation decisions: 1, 2, 3, 4, 12, 22, 23, 24, 25, 28.
- Testing decisions: 1, 2, 6, 11, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R1 exit criterion and overhead gate
- Routing design §§13–14
- Roadmap index R1/U5/U6/U7 edges
- Decision log D52,D79

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
