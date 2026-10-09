# 112: Rearrange, decorate and reclaim rooms without touching Core facts

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F layout editing (D89, D93)
Blocked by: 111

## What to build

Keep Edit Layout available in both planning modes for moving, resizing, recoloring, furnishing and reclaiming fortress spaces, all as reversible presentation changes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.

## Acceptance criteria

- [ ] Users move or resize rooms and whole Corps districts (internal placements preserved), change corridors and stairs, move stations and stockpiles, bend rails, recolor walls, floors and rooms and place, rotate or remove furniture and statues, with per-run undo and redo.
- [ ] A moved workshop keeps its explicit Corps binding; adjacency or containment problems become Build TODO items, never membership edits; dragging a rail bend never adds an edge and deleting a drawn path never revokes one.
- [ ] Decorations cannot swallow a room's hit target or obstruct inspector access; Core-produced names and membership stay distinguishable from cosmetic labels, and appearance grants no buffs, authority or priority.
- [ ] Removing a room returns its placement to staging without deleting or retiring the agent; an emptied worker room is preserved for explicit reuse, relocation or reclamation, and underground reclamation fills it with dirt, never silent bulldozing.
- [ ] Before-and-after checks of every edit show identical Core membership, relationships, permissions, work and message delivery, and every edit has a keyboard path.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 145, 146, 150.
- Implementation decisions: 1, 2, 3, 4, 57, 60.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG03, FG10, FG14.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §7 cosmetic building and completion, §2 rails without new messaging rights

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
