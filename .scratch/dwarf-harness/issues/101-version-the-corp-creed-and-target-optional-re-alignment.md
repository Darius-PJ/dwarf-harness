# 101: Version the Corp Creed and target optional re-alignment

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 Corp Creed and optional re-alignment (D93)
Blocked by: 93, 100

## What to build

Produce a versioned Corp Creed from the Orchestrator's alignment, keep raw interviews and project exports distinct, and route meaningful Purpose changes to optional re-alignment of the affected leader.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C8 Creed and re-alignment families; C6 recipient-scoped publication for Creed delivery.

## Acceptance criteria

- [ ] Accepted Orchestrator alignment yields a versioned Corp Creed containing Purpose, role mandates and resolved vocabulary and decisions, delivered to the affected agents through recipient-scoped C6 publication with version provenance.
- [ ] Raw interview transcripts stay private to their participants and administrator-inspectable through ticket [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md); they are never published with the Creed or placed in other agents' contexts.
- [ ] Project-document exports need their own human review and never write a working repository or create a second decision store automatically.
- [ ] A meaningful work change targets Orchestrator re-alignment and a values change targets the Griot; corrections cut over with no interview.
- [ ] The human may skip, shorten or disable re-alignment, recorded with provenance; no control waives a new leader's initial alignment, grants, budgets or validation gates, and no model can exempt itself.
- [ ] Creed versions and re-alignment progress replay correctly through restart and two terminal views.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 158, 159.
- Implementation decisions: 1, 2, 3, 4, 18, 19.
- Testing decisions: 1, 2, 8, 20, 22.
- Fortress acceptance: FG18.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C8 "Creed and Griot" and "Alignment and Purpose"
- Core design §12.4
- Decision log D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
