# 74: Maintain scoped resource and availability fields through real ledger changes

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U5 non-runner source integration
Blocked by: 60, 73

## What to build

Maintain expiring resource-pressure and separate availability sources through real ledger changes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Actual R1/Core non-runner producers; pre-C4 activity, if any, remains testing-only.

Complete non-runner U5 path; no whole U3 capability (ticket [63](63-update-graphs-from-real-durable-accepted-deltas-exactly-once.md)) or C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md)) dependency imposed on it.

## Acceptance criteria

- [ ] Real durable R1 headroom/status and Core budget/usage updates drive engine levels only along the existing authorized resource-to-consumer topology.
- [ ] A source replaces/withdraws exactly its former reach on band renewal, expiry, status replacement, period/configuration rollover, limit retirement and resource removal, without stale-level accumulation.
- [ ] Delayed ambiguous settlement retains the underlying maximum hold and unknown state until Core resolves it; late settlement remains attributed to its original period and duplicate facts never count an attempt twice.
- [ ] Nested scopes, provider/account/model/region and metric/currency dimensions remain distinct; a single agent's exhausted limit cannot appear as account-wide exhaustion.
- [ ] Replay, genuine no-update expiry and actual scoped-input workload measurements agree with RESOURCE-VALUES; observing or changing a field never calls the provider or changes admission/accounting.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### RESOURCE-VALUES

Field ordinal lookup: empty=1,000,000; critical=900,000; low=750,000; constrained=500,000; ample=0. Unknown/expired withdraws. R1 owns the upstream band/expiry/coalescing scheme, including <=15-minute freshness and provisional 5-minute renewal rules. Typed non-available provider state creates one 1,000,000-unit perturbation level/true scope with 15-minute field lease. Same-unit finite ratios use half-up million-unit normalization, clamp at one million, positive rounded zero becomes one before explicit reach cutoff; hop propagation still floors with no minimum. Stable budget-limit series replace across period/configuration changes, retain all outstanding cross-period holds exactly once/attempt, and never release money from projection expiry.

Source: Undercurrent design §15.1; Parent specification implementation 41.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 82, 88, 94, 101, 102.
- Implementation decisions: 1, 2, 3, 4, 32, 33, 41.
- Testing decisions: 1, 2, 11, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U5
- Undercurrent design §§9,15.1
- Decision log D52/B10/B12

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
