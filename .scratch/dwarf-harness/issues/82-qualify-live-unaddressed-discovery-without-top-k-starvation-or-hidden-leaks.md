# 82: Qualify live unaddressed discovery without top-k starvation or hidden leaks

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U9 ambient channel
Blocked by: 78

## What to build

Demonstrate live unfamiliar-region discovery without starvation or hidden-candidate leakage.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Exact approved live-discovery configuration and authorized workloads; new revisions re-enter the existing per-kind release gate.

Genuinely distinct live integration/fairness qualification, not a generic tests-only ticket or a duplicate renderer implementation.

## Acceptance criteria

- [ ] An authorized unfamiliar region outside the context's known dependency footprint can reach its real bounded turn through the reserved exploration slot without a sender choosing that context.
- [ ] Under local dominance and catalogue churn, use the already-implemented selector to verify fitting unfamiliar regions are not starved by oversized rows; unseen/cursor state survives retries and restart without falsely marking skipped rows seen.
- [ ] Measure coverage and p95 unseen age using authorized eligible renderable context–region pairs only; separately retain unrenderable counts/durations and restart the coverage clock when a pair becomes renderable.
- [ ] Denied/revoked additions and removals cannot perturb visible membership/order/counts/timing/metadata, and weighted-draw rejection/overflow/counter exhaustion stays fail closed.
- [ ] Re-run the exact applicable SIMULATION/disclosure/actual token-cost/capacity gates for the live configuration, without claiming simulation establishes live-agent benefit or adding a second ambient mechanism.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### SIMULATION

Exact manifest before runs; 20 fixed seeds, 1,000 decision epochs, 30-day explicit ledger horizon, 10 regions, populations 12/20/40 active agent-context pairs. Every applicable invariant passes every seed. Balanced final-200-epoch max occupancy <=0.30 each epoch. Whole-seed immediate reversal <=0.10 with nonzero denominator. Paired field-on/hidden completions >=0.90 with feasible completions. After a 20-epoch hotspot: occupancy plus rolling most-recent-20-decision reversal <=0.10 for 10 consecutive epochs within 100; zero transition denominator invalid. Each context covers >=9/10 authorized eligible renderable regions by 1,000 epochs; renderable-pair p95 unseen age <=100 epochs; separately report unrenderable count/duration. Zero unauthorized/duplicate notices; sensing/render caps hold. Worst seed must pass. Asymmetric workloads use preregistered feasible allocations, not the balanced occupancy ceiling. Disabled mechanism must reproduce the challenged herding/oscillation. Synthetic cost units are not real provider prices or live-agent evidence.

Source: Undercurrent design §§14.3–14.4; Undercurrent roadmap U2; Parent specification implementation 37.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 79, 89, 90, 105, 108.
- Implementation decisions: 1, 2, 3, 4, 42, 43, 44, 47.
- Testing decisions: 1, 2, 6, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U9
- Undercurrent design §§10.3,11.1,14.2–14.4

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
