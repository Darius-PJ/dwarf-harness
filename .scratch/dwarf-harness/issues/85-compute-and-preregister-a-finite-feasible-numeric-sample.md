# 85: Compute and preregister a finite feasible numeric sample

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U10 planning/power capability
Blocked by: 84

## What to build

Compute and preregister a feasible sample using the fixed bounded four-endpoint power procedure.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-approved numeric maximum clusters/arm, task/enrollment horizon, CPU/wall-time and money before power simulation; separate D78 capacity/budget acceptance before randomization.

Conditional empirical-baseline planning, not proof of general interval coverage, real-world completion probability or usefulness.

## Acceptance criteria

- [ ] Implement the exact task-weighted cluster-ratio estimator, delta variance, Welch intervals and zero-risk/missing/nonfinite handling; unequal-cluster regression cases reject an unweighted cluster average.
- [ ] STUDY-POWER baseline bootstraps, loss bound, formula, stratum allocation and minima produce a recorded numeric starting count; any required count above manifest maximum stops before power simulation or enrollment, never clamps.
- [ ] Run the finite 10,000-pair per-count conditional-power procedure with fixed PRNG/seeds and baseline/code digests, prespecified alternatives, effective loss-adjusted counts and invalid replicates counted as non-rejections.
- [ ] Every endpoint must meet the 95% Clopper-Pearson lower power bound >=0.80; failed sizes double with a single maximum-size evaluation, then stop inconclusive. No endpoint omission, seed shopping, unbounded retry or model call.
- [ ] Record all size attempts and freeze resulting numeric cluster/task sample per arm plus analysis inputs; compare expected provider/validation costs separately with approved money and capacity limits before permitting the trial.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### STUDY-POWER

Task-weighted ratio estimator and cluster delta variance from §14.5, not equal-weight cluster means. Familywise one-sided alpha .05, endpoint alpha .0125; target .80 conditional power at twice minimum effect. 10,000 within-stratum whole-cluster baseline bootstraps; nearest-rank 90th percentile n*v, invalid replicate=+infinity; 90% one-sided Clopper-Pearson loss upper bound r. Initial per-arm formula exactly §14.5, maximum across endpoints, >=20/arm and >=2/stratum; equal stratum allocation with largest remainders/stable-ID ties. Owner manifest must pre-bind maximum clusters/arm, horizon, CPU/wall and money. Formula above maximum stops before simulation, never clamps. At each permitted size use 10,000 whole-cluster trial pairs, .8 reduction alternative and binary zero-to-one probability .10/(1-p), floor(n*(1-r)) analyzable clusters, invalid statistics/nonviable strata=non-rejection. Each endpoint's 95% one-sided Clopper-Pearson lower rejection bound must be >=.80. Double failed counts capped at maximum, test maximum once, then stop. Freeze PRNG/seeds/baseline/code digests; no seed shopping/model calls.

Source: Undercurrent design §14.5; Parent specification implementation 51; testing 19.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 93, 115, 116.
- Implementation decisions: 1, 2, 3, 4, 51.
- Testing decisions: 1, 2, 19, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U10
- Undercurrent design §14.5
- Parent specification incorporated study-arithmetic evidence and its explicit limits

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
