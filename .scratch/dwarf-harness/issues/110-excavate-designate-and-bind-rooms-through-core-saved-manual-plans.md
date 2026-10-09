# 110: Excavate, designate and bind rooms through Core-saved manual plans

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F building with C7 presentation persistence (D89–D92)
Blocked by: 96, 109

## What to build

Let users dig, designate and bind Corps halls, workshops, offices, Grand offices, the diplomatic hall and stockpiles in an explicit Build mode, saving each applied plan through Core with per-run undo.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Owner-frozen presentation commands from ticket [96](96-save-current-fortress-presentation-atomically-with-bounded-retries.md).

## Acceptance criteria

- [ ] Inspect and Build are explicit modes; Build is a reversible editing state, never drag-to-reassign, and Enter or Open Inspector still reaches the agent.
- [ ] With the per-colony manual digging and planning setting (default on), the user chooses an area, designates excavation, assigns room use and an explicit agent or Corps binding, places stations and stockpiles and applies the plan; excavation is immediate, with no material economy, labor delay or model turn, and toggling the setting never completes, discards or bypasses existing plans or approvals.
- [ ] Room uses cover Corps common areas with a board hub, worker workshop siderooms, one personally bound office for each Corps Orchestrator, Griot and Diplomat, larger Grand offices in a Grand precinct, the shared diplomatic gathering hall (each Diplomat shown once there while its office keeps a nameplate) and nearby task stockpiles.
- [ ] Each applied placement saves through Core and shows Saved only after the receipt; disconnected, failed or conflicting saves stay visibly unsaved with retry, remain-open or explicit discard, and a revision conflict reloads and reconciles before the edit is reapplied.
- [ ] Undo and redo apply new version-checked cosmetic transitions, are bounded per GUI run and are discarded on close; restart restores current layout and unfinished blueprints but no undo history.
- [ ] Undug rock, unfinished rooms, distance or a closed GUI never pause turns, block messages or change membership, relationships, permissions, work or delivery, and every build action has a keyboard path.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 132, 133, 144, 146, 151.
- Implementation decisions: 1, 2, 3, 4, 52, 57, 60.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG01, FG02, FG10, FG13.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §§2, 3, 7 cosmetic building and completion, 8 persistence
- GUI roadmap fortress and RPG layer

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
