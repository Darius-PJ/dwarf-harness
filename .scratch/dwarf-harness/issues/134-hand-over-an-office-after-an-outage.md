# 134: Hand over an office after an outage

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 unrecoverable officers and office handover (D98–D101)
Blocked by: 106, 133

## What to build

Let the owner declare an officer in service loss unrecoverable, so that a prepared, aligned Aspirant takes over through the normal reviewed cutover with a handoff Core assembles from the officer's records, and let the owner decide what happens if the officer is restored before cutover.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C8 succession, declaration and handover families; reviewed cutover and office handover from ticket [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md), and the owner stand-in from ticket [133](133-stand-in-for-a-lost-office-as-the-owner.md).

## Acceptance criteria

- [ ] The owner may declare an officer in service loss unrecoverable; a declaration for an officer not in service loss is refused. The officer stays the holder until a prepared, aligned Aspirant takes over through the normal reviewed cutover without the live handoff, then retires with its records kept. There is never a vacancy, second holder or timer.
- [ ] Core assembles that handoff from the officer's records only: its open tasks, the assignments it made, its pending decisions and its relationship roster attenuated as in an ordinary handoff, with no lessons and nothing from private memory.
- [ ] If the officer is restored before cutover, Core pauses the succession and asks the owner to cancel it, switch to an ordinary succession with a live handoff, or proceed as declared, while the officer works as the holder.
- [ ] The cutover passes unanswered messages, pending decisions, task proposals, the owner's decisions for the office and open tasks as every succession does (ticket [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md)).
- [ ] After the handover, covered members receive a handover notice instead of a Service Restoration message, and paused members are released in step as the new holder processes their reserved questions.
- [ ] Declarations and choices are typed, revision-checked and replay-safe: restart or a duplicate command neither repeats a cutover nor leaves the office without exactly one holder.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 162, 168.
- Implementation decisions: 1, 2, 3, 4, 16, 18, 64.
- Testing decisions: 1, 2, 8, 20, 22, 23.
- Fortress acceptance: FG20, FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core design §13.11
- Core roadmap C8 "Officer recovery and retirement" and exit criterion
- Decision log D94, D98, D99, D100

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
