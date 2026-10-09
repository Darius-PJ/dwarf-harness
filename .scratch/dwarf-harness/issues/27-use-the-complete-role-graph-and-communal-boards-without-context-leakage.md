# 27: Use the complete role graph and communal boards without context leakage

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C6 role/board capability and routing §13 social drill
Blocked by: 26

## What to build

Enforce actual Corps/Grand/Diplomat routes, communal boards and counterpart-bound private memory.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen role, Corps-membership, assigned-parent, board and operational-notification families.
- No R0/R1, C5, C8 or whole-C4 dependency; exercise available router artifacts without making router completion a prerequisite.

## Acceptance criteria

- [ ] Actual membership and assigned parent links enforce the complete role matrix, including corresponding Grand chains, with default deny for unlisted pairs/operations and no blanket descendant inspection or cross-colony route.
- [ ] A permitted cross-Corps exchange follows real explicit Diplomat forwarding with provenance. The broker cannot fabricate a relay, impersonate an intermediary, or turn a workflow edge into permission.
- [ ] Workers discuss on one genuinely shared Corps board, stored once with reader-specific cursors. Mentions/replies create no private subchannel; worker-to-worker and Griot-worker DMs fail, and board reading grants no extra posting/reply rights.
- [ ] A worker switches Orchestrator → Diplomat → board and back through new bound turns. Short follow-ups, fallback, late results and approvals cannot carry prior private history, plans, response IDs, cache state or grants into another context; view focus cannot retarget a running turn.
- [ ] Membership retirement/reassignment closes future posting/delivery and preserves only delivered history with sequence holes, including authorization checks on counts, existence, attachments and queued reads.
- [ ] Assigned-superior and Grand-to-owner operational notifications are durable without creating ordinary action permission. Orchestrator instructions require an exact scoped interruption, evidence, impact/deadline and alternatives; current-authority cancellation targets that work, never its submitted promotions or unrelated private contexts.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 42, 43, 44, 47, 48, 55.
- Implementation decisions: 1, 2, 3, 4, 11, 15, 16, 18, 19.
- Testing decisions: 1, 2, 8, 14, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6
- Core design §§7.1–7.5,13.6
- Routing design §13

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Tickets [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md), [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md), [94](94-classify-a-about-b-relationships-in-the-nine-region-grid-from-eligible-evidence.md), [95](95-refresh-relationship-labels-automatically-within-coalesced-bounded-spend.md) and [104](104-hand-off-recipient-scoped-attenuated-succession-evidence.md) extend the C6 role graph, relationships and memory delivered here with topology and edge snapshots, administrator inspection, perspective-bound labels and succession-handoff evidence; ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) uses its role records for officer recovery. These extensions add no blocker or criterion to this ticket.

2026-10-08 — Consolidation cross-link: Ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) no longer uses this ticket's role records and no longer waits for it ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)); officer consequences of service loss now build on C6 Corps facts in tickets [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md)–[134](134-hand-over-an-office-after-an-outage.md). These extensions add no blocker or criterion to this ticket.
