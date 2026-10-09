# 79: Coalesce idle changes into bounded ordinary paid sensing turns

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U8 sensing capability
Blocked by: 27, 78

## What to build

Coalesce changes into authorized, quota- and money-bounded paid sensing opportunities.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-configured finite sensing money ceiling and current scoped grants; approved applicable kind/revision.

Bounded paid sensing, not independent permission to start an intervention or bypass the released-kind gate.

## Acceptance criteria

- [ ] Relevant event changes coalesce by context/kind/region, persistent conditions and cooldown/salience transitions retain due-time reevaluation, and bounded deterministic jitter/fairness avoids one call per event or whole-colony polling.
- [ ] Sampling itself invokes no model and creates no field source; due-but-denied candidates retain stale/unknown coverage and fairness age rather than healthy silence.
- [ ] All SENSING-MOOD rate/money ceilings apply independently, with missing money authorization denying sensing and no Orchestrator enlargement of owner ceilings.
- [ ] Each admitted wakeup uses ordinary Core scheduling, per-agent/context concurrency and full reservation/dispatch/settlement rules for every call/helper/retry.
- [ ] Restart, clock rollback, repeated hints, revocation, agent disablement and field-off transitions preserve dues/quotas and cancel/recheck queued work before dispatch; measure actual fairness/resource/call costs.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### SENSING-MOOD

Paid sensing at most 12/colony/rolling hour, 4/agent/rolling 24 hours, and 5% configured monthly sensing allocation, subordinate to owner ceilings. Missing money ceiling denies. Sampling no model call. Independent 30-minute agent-wide and 24-hour host-condition-family cooldowns begin at durable close using nondecreasing Core UTC; pause does not close; crash closes once. Rewording never creates a new family; material host revision cannot waive cooldowns, quotas or grants.

Source: Undercurrent design §§11.2–11.3; Field philosophy §9; Parent specification implementation 48.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 91, 109.
- Implementation decisions: 1, 2, 3, 4, 42, 47, 48.
- Testing decisions: 1, 2, 11, 12, 13, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U8
- Undercurrent design §11.2
- Field philosophy §9
- Decision log D22/D80/B13

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
