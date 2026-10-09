# 73: Transform typed resource observations without owning money or sources

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U5 typed-transform capability
Blocked by: 04, 41, 62

## What to build

Normalize typed resource observations without pooling scopes or releasing financial holds.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen R1 band/status and Core exposure/usage measurement contracts with real scope/unit identities.

May finish before U1.E engine capability (ticket [60](60-propagate-sources-with-bounded-replay-safe-best-arrivals.md)) and C4; this is not whole U5 or source-engine integration.

## Acceptance criteria

- [ ] Every R1 band maps through RESOURCE-VALUES with unknown/expiry withdrawal; no raw header, reconstructed provider ratio, threshold redefinition or refresh probe enters field behavior.
- [ ] Budget/use/runner ratios reject invalid units/scopes/windows/denominators and use exact finite half-up/clamp rules, distinct from per-hop flooring.
- [ ] Stable budget_limit_ref replacement across period/config changes preserves separate daily/monthly and distinct-scope limits, current-period settlements and all cross-period holds once/attempt; retiring a projection cannot settle/release money.
- [ ] Typed ProviderStatus creates its separate single scoped binary perturbation/15-minute lease, not pressure or per-consumer clones; expiry cannot clear Router availability or cause a call/retry.
- [ ] Transforms consume U3-registered nodes/usage links and Core facts without creating graph records, a duplicate source store, wallet, accounting reconstruction or global scarcity scalar.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### RESOURCE-VALUES

Field ordinal lookup: empty=1,000,000; critical=900,000; low=750,000; constrained=500,000; ample=0. Unknown/expired withdraws. R1 owns the upstream band/expiry/coalescing scheme, including <=15-minute freshness and provisional 5-minute renewal rules. Typed non-available provider state creates one 1,000,000-unit perturbation level/true scope with 15-minute field lease. Same-unit finite ratios use half-up million-unit normalization, clamp at one million, positive rounded zero becomes one before explicit reach cutoff; hop propagation still floors with no minimum. Stable budget-limit series replace across period/configuration changes, retain all outstanding cross-period holds exactly once/attempt, and never release money from projection expiry.

Source: Undercurrent design §15.1; Parent specification implementation 41.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 88, 101, 102.
- Implementation decisions: 1, 2, 3, 4, 31, 33, 41.
- Testing decisions: 1, 2, 5, 11, 12, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U5 typed-transform stop point
- Undercurrent design §§9,13.4,15.1
- Router R1/§5.5
- Decision log B10/B12

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
