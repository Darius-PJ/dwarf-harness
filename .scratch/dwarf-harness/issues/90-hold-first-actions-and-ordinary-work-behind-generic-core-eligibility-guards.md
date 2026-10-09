# 90: Hold first actions and ordinary work behind generic Core eligibility guards

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C3 staged lifecycle eligibility (D91–D93)
Blocked by: 16

## What to build

Give the Core scheduler a generic eligibility state that holds an agent's ordinary work, or requires a declared first action, until an authorized transition releases it, without C3 knowing C8's lifecycle.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C3 runtime and eligibility families; no C6, C8, C9 or GUI dependency.
- Tests set generic eligibility states through authorized Core transitions, never a fake alignment, skill or approval. C8 binds its leadership lifecycle in ticket [100](100-align-every-new-orchestrator-and-griot-before-ordinary-work.md).

## Acceptance criteria

- [ ] An agent can be marked first-action-required or ordinary-work-ineligible through a typed, versioned Core transition; release needs a later authorized transition recorded with its operation identity.
- [ ] The single continuation owner checks eligibility at every scheduling and dispatch boundary: explicit starts, scheduled continuation, task assignment, retries and restart recovery cannot dispatch held ordinary work, and refusal is a visible typed state rather than a hidden queue.
- [ ] Only the declared first action may proceed while ordinary work is held, under ordinary grants and BudgetBook admission; refused admission stays visible and unlocks nothing.
- [ ] Waiting, inspection and restart start no paid work; restart reconstructs held and eligible state and any admitted first turn's identity without dispatching it again.
- [ ] While work is held, authorized inspection and the permitted first action stay available, and recording received work never makes the agent eligible.
- [ ] The guard sits on the scheduling boundary that later message delivery (C6) and view commands (C7) use, so those producers inherit it instead of adding their own checks.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 155, 157.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 18.
- Testing decisions: 1, 2, 8, 20, 22.
- Fortress acceptance: FG17.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 "Staged lifecycle eligibility (D91–D93)" and its exit criteria
- Core design §§12.4, 13.5
- Decision log D91–D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md) pauses a Corps member at its question allotment through this ticket's eligibility hold while its officer is in service loss ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)). These extensions add no blocker or criterion to this ticket.
