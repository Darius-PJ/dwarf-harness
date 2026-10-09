# 89: Report waiting-ready, waiting-blocked and active task facts

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C3 actual task projections (D90–D92)
Blocked by: 12

## What to build

Let authorized callers query each agent's self-assigned and delegated tasks as waiting-ready, waiting-blocked with reasons, or active, straight from committed Core task records.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C3 task and assignment families; no C6, C7, C8 or GUI dependency.
- Consumed later by the fortress stockpiles (ticket [114](114-show-waiting-task-stockpiles-workshop-activity-and-authorized-rails.md)); C3 completion does not wait for any GUI work.

## Acceptance criteria

- [ ] A typed Core query returns, for an authorized principal, each agent's waiting self-assigned and delegated tasks split into ready-but-queued and blocked with the blocked reason, plus active tasks separately, using actual task identities, versions and states.
- [ ] Results derive only from committed task transitions; no stall-age threshold, staffing or competence judgement, or message backlog enters the projection.
- [ ] Claims, assignments, completions, failures and revocations move a task between states exactly once in sequence order, including across restart; stale or duplicate callbacks cannot move it.
- [ ] Authorization filters task identities, titles, counts and blocked reasons per principal and context before results are built; a forbidden task contributes neither a row nor a count.
- [ ] Results are bounded and paginated with a revision or high-water marker so consumers can recover without rescanning history; no view, animation or cosmetic state can delay or alter a fact.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 132, 135.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 57.
- Testing decisions: 1, 2, 8, 20.
- Fortress acceptance: FG21.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 "Actual task projections (D90–D92)" and its exit criteria
- Fortress GUI spec §2 work representation and §9 C3 handoff
- Decision log D90–D92

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
