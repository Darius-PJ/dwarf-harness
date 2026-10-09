# 103: Select and test the deterministic succession attenuation

Status: ready-for-agent
Type: prototype
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C6/C8 succession attenuation (D94–D95)
Blocked by: 94

## What to build

Test candidate deterministic, normalized attenuation functions on the encoding calibrated in ticket [94](94-classify-a-about-b-relationships-in-the-nine-region-grid-from-eligible-evidence.md), and record the owner's selection of the exact function, coefficients and representation.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- The owner reserved the exact implementation pending testing (D95); the illustrative linear formula and retention factors are candidates only, never defaults.
- Uses the encoding and cutpoints specified in ticket [94](94-classify-a-about-b-relationships-in-the-nine-region-grid-from-eligible-evidence.md).

## Acceptance criteria

- [ ] Every candidate is deterministic, keeps outputs in the normalized range, moves every known trust and sentiment value toward zero, softens inherited distrust as well as trust, and keeps unknown as unknown.
- [ ] Property tests cover sign symmetry, monotonicity and stronger retention of trust than sentiment across the whole domain, including boundary values, and show how attenuated values move across the nine-region cutpoints.
- [ ] Scenario tests on realistic calibrated relationship rosters compare how much second-hand evidence each candidate preserves against the room it leaves for the Aspirant's own opinions.
- [ ] The owner selects the function, coefficients and numeric representation; a decision-log entry records the choice with its originator, and the Core design and roadmap reference it.
- [ ] The prototype survives only as the reference implementation and test vectors for ticket [104](104-hand-off-recipient-scoped-attenuated-succession-evidence.md), not as a parallel runtime.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 161.
- Implementation decisions: 1, 2, 3, 4, 18, 59.
- Testing decisions: 1, 2, 20, 22.
- Fortress acceptance: FG20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 "Scoped handoff evidence"
- Core design §13.10
- Decision log D94, D95

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
