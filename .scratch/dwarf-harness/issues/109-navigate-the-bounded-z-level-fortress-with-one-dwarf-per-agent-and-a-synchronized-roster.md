# 109: Navigate the bounded z-level fortress with one dwarf per agent and a synchronized roster

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F identity and navigation (D89–D92)
Blocked by: 92, 107

## What to build

Render the authorized colony as a chunked top-down tile fortress spanning z-15 through z+2, with exactly one dwarf per visible agent, unplaced agents in a staging roster, and a synchronized textual tree.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Authorized topology from ticket [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md); layout persistence arrives with ticket [110](110-excavate-designate-and-bind-rooms-through-core-saved-manual-plans.md).

## Acceptance criteria

- [ ] A viewport/chunk renderer (never one widget per tile) draws levels z-15 through z+2 with z0 as the surface; navigation and edits above z+2 or below z-15 are refused and higher levels stay inaccessible.
- [ ] Each visible agent appears exactly once whatever its contexts or turns; agents without a placement appear in a staging roster and stay reachable, and sprites elsewhere are portraits of the same identity.
- [ ] Every dwarf shows its reserved role silhouette or trim, a non-color role badge and, for Grand roles, a crest from the default palette (Worker green, Orchestrator amber, Griot violet, Diplomat cyan; Grand gold, magenta, blue); status is a separate icon and text, and worker specialization adds only an emblem.
- [ ] Pan, integer pixel zoom, level switching and search address agents and rooms by stable IDs, never position; a synchronized native tree/list and keyboard cell and room selection reach every subject.
- [ ] Topology changes and gap recovery come from authorized snapshots; an identity that becomes unreadable shows as unavailable without its new name, membership or content.
- [ ] Rendering stays bounded in the 200-agent navigation case, and high contrast and text scaling keep classes distinguishable without color.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 131, 153.
- Implementation decisions: 1, 2, 3, 4, 52, 54, 57, 63.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG01, FG04.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §§1, 2 (spatial vocabulary, role colors and art), 10
- GUI roadmap fortress and RPG layer

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
