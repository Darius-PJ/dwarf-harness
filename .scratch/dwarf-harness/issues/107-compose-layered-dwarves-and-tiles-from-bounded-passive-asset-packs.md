# 107: Compose layered dwarves and tiles from bounded passive asset packs

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F asset pipeline (B16 as amended by D103; fortress spec §6)
Blocked by: 54, 88

## What to build

Load bundled or explicitly imported local asset packs through a strict versioned manifest and composite layered dwarves and tiles in the native client, rejecting anything unbounded, remote or executable.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Freeze the importer only after exercising the real generated sample recorded by ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md); no runtime editor-document parser, editor or generator executable, script or plug-in is loaded.
- No new runtime or graphics dependency without a D41 record.

## Acceptance criteria

- [ ] A versioned manifest declares stable pack and part IDs, content version and hash, licence and provenance, world and RPG variants, layer slot and order, anchors, frame rectangles, palette masks, compatibility and occlusion constraints, and semantic tile categories (rock, floor, door, station, rail, decor); choices are stored as IDs, never inferred from filenames or atlas offsets.
- [ ] The loader accepts only PNG images and a strict passive manifest from a pack root, keeps every path inside it, enforces image-dimension and decoded-byte budgets (initially 64 MiB of source art per active pack) and rejects traversal, executable content, scripts and remote URLs; it never uses Fyne's network-capable URI loading.
- [ ] The compositor assembles parts in the baseline layer order with declared occlusion (a hat over hair), palette masks and the reserved role overlay, caching composites within a bounded budget (initially 32 MiB) for 16-pixel world and 64-pixel RPG variants with nearest-neighbour integer scaling.
- [ ] A missing part renders a clearly marked bundled substitute without changing the saved part ID; an invalid pack is rejected as a whole and leaves the previous pack and layout intact.
- [ ] A native preview of ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md)'s real generated sample shows correctly registered layered dwarves and tiles, and malformed, oversized, path-traversing and missing-asset packs are exercised.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 142, 143.
- Implementation decisions: 1, 2, 3, 4, 52, 58, 63.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG09, FG14.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §6 and §10 resource gates
- GUI roadmap fortress and RPG layer
- Decision log B16, D103

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D103 rework: ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md) now produces its sample with the Go art generator instead of Aseprite ([D103](../../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite)), so the importer freeze and native preview use that generated sample. Imported replacement packs stay supported. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-08 — Consolidation cross-link: Ticket [137](137-improve-the-generated-map-and-character-sprites-before-the-native-gui-exists.md) may improve the generated sprites while this ticket waits. It keeps the output format proved in ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md), and once this ticket freezes the importer any format change goes through that freeze ([D105](../../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui)). These extensions add no blocker or criterion to this ticket.

2026-10-09 — Consolidation cross-link: under [D108](../../../docs/DECISIONS.md#d108-character-sprites-a-still-front-view-an-animated-three-quarter-view-and-corps-emblems-and-colours), ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md)'s second sample round adds the two character views, idle and role action frames and Corps emblem parts, so the manifest this ticket freezes must be able to declare views, animation states and Corps markers. These extensions add no blocker or criterion to this ticket.
