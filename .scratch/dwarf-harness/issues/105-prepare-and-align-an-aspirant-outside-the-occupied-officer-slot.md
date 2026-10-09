# 105: Prepare and align an Aspirant outside the occupied officer slot

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 Aspirant preparation (D93–D95)
Blocked by: 100, 104

## What to build

Create and align a non-operational Aspirant for an occupied officer role, deliver the Incumbent's scoped handoff and define readiness, while the Incumbent stays the sole holder.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Specify Aspirant readiness and the reviewed handover record before the C8 succession family is frozen.

## Acceptance criteria

- [ ] An Aspirant is a new or existing agent recorded against an officer role without occupying it; the Incumbent remains the only holder and stays responsible.
- [ ] A new Orchestrator or Griot Aspirant completes its mandatory initial alignment and human acceptance (ticket [100](100-align-every-new-orchestrator-and-griot-before-ordinary-work.md)); no Aspirant control can waive it.
- [ ] The Incumbent's handoff (ticket [104](104-hand-off-recipient-scoped-attenuated-succession-evidence.md)) reaches only the Aspirant and is recorded with the readiness evidence.
- [ ] Readiness is a specified Core state with recorded evidence; a filled office, completed interview or animation alone never makes an Aspirant ready.
- [ ] Any attempt to vacate an occupied officer role without a ready Aspirant is refused.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 160, 161.
- Implementation decisions: 1, 2, 3, 4, 18.
- Testing decisions: 1, 2, 8, 20, 22.
- Fortress acceptance: FG20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C8 Aspirant delivery and "Role authority and cardinality"
- Core design §7.1
- Decision log D93–D95

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [134](134-hand-over-an-office-after-an-outage.md) lets an Aspirant prepared and aligned here take over from an officer the owner declares unrecoverable, and ticket [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md) now pauses succession while the Aspirant is in service loss ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)). These extensions add no blocker or criterion to this ticket.
