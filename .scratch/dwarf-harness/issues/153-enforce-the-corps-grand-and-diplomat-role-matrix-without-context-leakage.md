# 153: Enforce the Corps, Grand and Diplomat role matrix without context leakage

Status: ready-for-agent
Type: task
Published: 2026-11-27
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C6 role matrix, Corps membership and parent links, and routing §13's role drill, split from ticket 27 (D182, D186)
Blocked by: 27

## What to build

On top of ticket [27](27-use-the-complete-role-graph-and-communal-boards-without-context-leakage.md)'s owner-set message routes and shared board, enforce the actual Corps, Grand and Diplomat role matrix. Explicit Corps membership and assigned parent links decide who may message whom, Diplomats forward between Corps, workers talk to each other only on their Corps board, and operational notifications follow the chain. A worker moving among its Orchestrator, Diplomat and board contexts leaks nothing between them. This is the part of ticket 27 that the release tiers moved to Release 3's teams layer (D182).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen role, Corps-membership, assigned-parent and operational-notification families, beside ticket 27's route and board families.
- No R0/R1, C5, C8 or whole-C4 dependency; exercise available router artifacts without making router completion a prerequisite.
- Corps membership and parent-link records are C6's and do not wait for C8's births.
- Implementation starts after the owner's go for Release 3's teams layer (D182).
- A design brief and a ruling come before code (D117). The design states how the role matrix and ticket 27's owner-set routes apply together.

## Acceptance criteria

- [ ] Actual membership and assigned parent links enforce the complete role matrix, including corresponding Grand chains, with default deny for unlisted pairs/operations and no blanket descendant inspection or cross-colony route.
- [ ] A permitted cross-Corps exchange follows real explicit Diplomat forwarding with provenance. The broker cannot fabricate a relay, impersonate an intermediary, or turn a workflow edge into permission.
- [ ] Each Corps's workers discuss on their own Corps board, built on ticket 27's shared board. Worker-to-worker and Griot-worker direct messages fail before delivery, and board reading grants no extra posting/reply rights.
- [ ] A worker switches Orchestrator → Diplomat → board and back through new bound turns. Short follow-ups, fallback, late results and approvals cannot carry prior private history, plans, response IDs, cache state or grants into another context; view focus cannot retarget a running turn.
- [ ] Corps membership retirement/reassignment closes future posting/delivery and preserves only delivered history with sequence holes, including authorization checks on counts, existence, attachments and queued reads.
- [ ] Assigned-superior and Grand-to-owner operational notifications are durable without creating ordinary action permission. Orchestrator instructions require an exact scoped interruption, evidence, impact/deadline and alternatives; current-authority cancellation targets that work, never its submitted promotions or unrelated private contexts.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 42, 43, 47, 55.
- Implementation decisions: 1, 2, 3, 4, 11, 15, 16, 18, 19.
- Testing decisions: 1, 2, 8, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6
- Core design §§7.1–7.2,7.5,13.6
- Routing design §13

## Comments

2026-11-27 — Published by the owner's decision, split from ticket [27](27-use-the-complete-role-graph-and-communal-boards-without-context-leakage.md). The release tiers keep one shared board and owner-set message routes in Release 2 and move the Corps, Grand and Diplomat role matrix to Release 3's teams layer (D182), and he approved its publication (D186). Its criteria are ticket 27's role-matrix, Diplomat, Corps-board, role-drill, membership and notification criteria as published on 2026-10-06. Release tier: Release 3, teams layer. This grants no implementation, contract-freeze or release approval; the start and authority gates above stand.
