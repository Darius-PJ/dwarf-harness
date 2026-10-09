# 131: Keep a Corps working while its officer is in service loss

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 outage Corps operation on C6 messaging (D97–D102)
Blocked by: 28, 29, 90, 91

## What to build

While an officer is in service loss, keep its Corps working. Core queues messages to the officer and tells senders it is unavailable, counts covered members' further questions against their allotment and pauses a member at its allotment through the existing eligibility hold. On restoration it sends the Service Restoration message and releases members in step as the officer answers them.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C6 messaging and C8 outage families. Actual role and Corps-authority records from C6 (tickets [28](28-keep-shutdown-and-revocation-admissible-under-saturated-social-traffic.md) and [29](29-retire-and-overturn-checks-through-real-owning-corps-authority.md)), the eligibility hold from ticket [90](90-hold-first-actions-and-ordinary-work-behind-generic-core-eligibility-guards.md) and the service-loss state from ticket [91](91-record-service-loss-and-restoration-for-every-agent.md).
- Verifiable through Core's command/event boundary and the terminal; the shared view projections follow in ticket [135](135-show-outages-pauses-and-stand-in-actions-in-every-view.md).

## Acceptance criteria

- [ ] For a lost officer, the covered members are those of its own Corps and, for a Grand officer, the officers it directly leads. They and the officer's superior find an outage notice at their next naturally scheduled turn; no notice starts a turn.
- [ ] Messages to a lost officer are accepted and queued within existing limits, and the sender is told at once that it is unavailable. A covered member's further messages to it are reserved questions counted against that member's question allotment, three by default and configurable per colony. Other senders' messages queue without counting, and messages to a lost worker simply queue.
- [ ] At its allotment a member pauses all ordinary work through the existing eligibility hold: running calls finish, nothing is cancelled or retried, held spending stays held, and it still receives messages and can be inspected. There is no separate notice when members begin pausing.
- [ ] A paused officer still answers its own Corps and the officers it leads and makes the decisions they await, so an outage does not spread down the hierarchy. No agent, superior or deputy decides in a lost officer's place.
- [ ] Core's Corps status, shown in the terminal (the colony status for a Grand officer), shows how many members are paused and how many, and which, reserved questions are waiting, filtered for the inspecting principal.
- [ ] A reserved question stops counting toward its sender's allotment once the officer has processed it.
- [ ] On restoration, Core sends a Service Restoration message in the officer's name, marked as from Core, to everyone told of the outage, at no model cost. A paused member resumes once the restored officer has processed every reserved question it sent, provided its remaining reserved questions to still-lost officers are below the allotment.
- [ ] Queued work is scheduled as usual after restoration, and restart or duplicate observations repeat no notice, message, release or spending.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 166, 167.
- Implementation decisions: 1, 2, 3, 4, 10, 15, 16, 18, 64.
- Testing decisions: 1, 2, 8, 20, 23.
- Fortress acceptance: FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C8 ordinary service-loss deliverable and "Officer recovery and retirement" prerequisite
- Core design §13.11
- Decision log D97, D98, D100, D101

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
