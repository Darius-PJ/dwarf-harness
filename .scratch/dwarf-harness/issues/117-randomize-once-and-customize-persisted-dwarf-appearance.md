# 117: Randomize once and customize persisted dwarf appearance

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F appearance (B16, D90)
Blocked by: 113

## What to build

Give each dwarf a seeded random outfit on first appearance, then let users customize, lock and reroll slots, persisting exact choices through Core across restarts, role changes and pack switches.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Presentation persistence from ticket [96](96-save-current-fortress-presentation-atomically-with-bounded-retries.md); asset pipeline from ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md).

## Acceptance criteria

- [ ] On first appearance a compatible outfit is chosen locally and both its seed and exact part and color IDs are saved; restart, role change, resizing, reconnect and pack upgrades never reroll.
- [ ] The Appearance tab offers body, hair, beard, clothing, shoes, accessories and colors with live preview, Apply and Cancel, and explicit reroll with lockable slots; the RPG sprite shows the full outfit and the overworld dwarf keeps its mandatory class color and badge.
- [ ] Switching tilesets changes artwork only: logical coordinates, bindings, completed progress and exact appearance choices persist; a missing part shows a marked substitute, and restoring the pack restores the look.
- [ ] Clothing, paint or tileset never erase the role marker, and appearance grants no buffs, authority, memory or performance change.
- [ ] A clone gets a new appearance unless the user explicitly copies cosmetic choices, which copies no memory or agent property.
- [ ] Every control is keyboard reachable with announced state.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 142, 143.
- Implementation decisions: 1, 2, 3, 4, 58, 60.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG09.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §6 steps 4–6
- Decision log B16

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
