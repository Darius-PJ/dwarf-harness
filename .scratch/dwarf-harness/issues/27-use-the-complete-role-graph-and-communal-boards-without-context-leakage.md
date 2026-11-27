# 27: Use the complete role graph and communal boards without context leakage

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C6 owner-set message routes and one shared board (D182, D186)
Blocked by: 26

## What to build

Let the owner set which agents may message each other, and give the colony's agents one shared board, while each relationship and board context keeps counterpart-bound private memory and nothing leaks between contexts. The Corps, Grand and Diplomat role matrix belongs to ticket [153](153-enforce-the-corps-grand-and-diplomat-role-matrix-without-context-leakage.md).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen message-route and board families. The role, Corps-membership, assigned-parent and operational-notification families belong to ticket [153](153-enforce-the-corps-grand-and-diplomat-role-matrix-without-context-leakage.md).
- No R0/R1, C5, C8 or whole-C4 dependency; exercise available router artifacts without making router completion a prerequisite.

## Acceptance criteria

- [ ] From the terminal, the owner sets which pairs of agents may exchange private messages and which agents read and post on the shared board. Every unlisted pair and operation is denied, and there is no cross-colony route. Core checks the current routes at send, delivery and retrieval, so a change applies to the next of each.
- [ ] The agents discuss on one genuinely shared board, stored once with reader-specific cursors. Mentions/replies create no private subchannel, and board reading grants no extra posting/reply rights.
- [ ] An agent switches between a private conversation, the board and its task context and back through new bound turns. Short follow-ups, fallback, late results and approvals cannot carry prior private history, plans, response IDs, cache state or grants into another context; view focus cannot retarget a running turn.
- [ ] Removing a route or an agent's board membership closes future posting/delivery and preserves only delivered history with sequence holes, including authorization checks on counts, existence, attachments and queued reads.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 42, 44, 47, 48.
- Implementation decisions: 1, 2, 3, 4, 11, 15, 16.
- Testing decisions: 1, 2, 8, 14, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6
- Core design §§7.3–7.5,13.6

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Tickets [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md), [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md), [94](94-classify-a-about-b-relationships-in-the-nine-region-grid-from-eligible-evidence.md), [95](95-refresh-relationship-labels-automatically-within-coalesced-bounded-spend.md) and [104](104-hand-off-recipient-scoped-attenuated-succession-evidence.md) extend the C6 role graph, relationships and memory delivered here with topology and edge snapshots, administrator inspection, perspective-bound labels and succession-handoff evidence; ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) uses its role records for officer recovery. These extensions add no blocker or criterion to this ticket.

2026-10-08 — Consolidation cross-link: Ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) no longer uses this ticket's role records and no longer waits for it ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)); officer consequences of service loss now build on C6 Corps facts in tickets [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md)–[134](134-hand-over-an-office-after-an-outage.md). These extensions add no blocker or criterion to this ticket.

2026-11-27 — Release tier rework (D182, D186): Release 2. By the owner's decision the ticket is split. It keeps owner-set message routes and one shared board, with counterpart-bound private memory and no context leakage. The Corps, Grand and Diplomat role matrix, Corps membership and parent links, Diplomat forwarding, the worker-board rule, the routing §13 role drill and the superior and Grand notifications move to ticket [153](153-enforce-the-corps-grand-and-diplomat-role-matrix-without-context-leakage.md), in Release 3's teams layer. Its number and title are unchanged, and the title now covers owner-set routes, one shared board and no context leakage; ticket 153 completes the role graph it names. Its roadmap capability, What to build, gates, criteria and specification coverage are rewritten to match, with the transparency criterion added. Tickets [55](55-inspect-authorized-operations-and-mutate-a-task-through-the-real-core.md) and [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md) no longer wait on this ticket. Tickets [29](29-retire-and-overturn-checks-through-real-owning-corps-authority.md) and [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md) wait on ticket 153 instead, and tickets [35](35-execute-a-pinned-branching-workflow-with-nonblocking-joins-and-bounded-review-escape.md) and [80](80-start-notify-close-and-recover-scoped-moods-without-another-approval-hop.md) also wait on it. Its blocker, ticket [26](26-exchange-private-relationship-messages-and-recover-a-two-agent-crash.md), is unchanged. This grants no implementation, contract-freeze or release approval.
