# 88: Generate a minimal original layered art sample with the Go art generator

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F asset-pipeline input (B16 as amended by D103–D105; fortress spec §6)
Blocked by: 01

## What to build

Write the Go art generator, a development program kept in the repository, and use it to produce a small original layered sample with its frame metadata, proving registration, anchors, provenance and reproducible output before the importer is built. The owner reviews the generated map and character sprites before the generator code is approved.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- The generator builds with the pinned toolchain and check command from ticket [01](01-prove-the-portable-toolchain-and-integration-check.md). It uses the Go standard library and, once each has its D29 record, any Go module from outside it that assists sprite generation ([D105](../../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui)). It lives with the project's development tools, outside Core, the terminal and the GUI module, and needs no image editor or editor licence ([D103](../../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite)).
- It is a development tool: it never ships in the GUI, never runs on a user's computer and makes no model call.
- All art comes from version-controlled code, pixel maps, palettes and seeds that agents write; no image-generation model, pre-existing character kit, copied Dwarf Fortress asset or third-party sprite is used, and neither the owner nor users draw or edit pixels.
- Review rounds may study open-source pixel-art and sprite-generation projects for technique and style ([D104](../../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites)). None of their art is copied, and reused code follows the dependency rules (D29) and its source licence.

## Acceptance criteria

- [ ] The generator deterministically produces a small dwarf part set as separate transparent layers covering every baseline slot (back accessory, body, shoes, legwear, torso clothing, hair/beard, headwear, front accessory), including a hat that occludes hair, skin/hair/clothing palette masks and the reserved overworld role overlay, on 16-pixel overworld and 64-pixel RPG grids.
- [ ] Every part, variant and frame takes its shape from a pixel map kept in the repository: a text grid in which each character is one pixel. Code rules supply only colors, palette masks, combinations of variants and small seeded texture in rock and floor tiles, so a review comment changes only the parts it names.
- [ ] The sample also contains a few rock, floor, wall and door tiles, one station, one stockpile, one rail segment, a dog, and a horse with wheelbarrow, each with a bounded set of motion frames.
- [ ] The generator writes transparent untrimmed per-layer PNG frames, or an atlas with explicit source rectangles and offsets, plus JSON metadata giving each frame's rectangle, anchor and layer slot; re-running it with the same inputs, pinned toolchain and module versions reproduces the same files or a documented, explained difference.
- [ ] The output's registration, anchors and offsets are inspected and recorded, together with the toolchain version and generator command, as evidence for the importer freeze.
- [ ] Licence and provenance records identify the art as original project work produced by the recorded generator version, module versions, inputs and seeds.
- [ ] The generator writes two contact sheets, each at 1x and at an integer zoom. The map sheet shows the 16-pixel tiles, props, animals and overworld dwarves in every role, plus a small sample room built from the tiles; the character sheet shows every 64-pixel part, variant and frame, plus assembled example dwarves.
- [ ] The owner reviews both contact sheets, and agents turn his comments into changes to pixel maps or rules before they run the generator again. The generator code is approved only after he approves both sheets, and his approval is recorded under Comments.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 142, 143.
- Implementation decisions: 1, 2, 3, 4, 5, 58.
- Testing decisions: 1, 2, 3, 15, 20, 21.
- Fortress acceptance: FG09.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §6 steps 1–3 and §12
- GUI roadmap fortress and RPG layer
- Decision log B16, D29, D45, D90, D91, D103, D104, D105

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — The owner chose agent-scripted art (ready-for-agent) over human-authored art for this ticket and ticket [108](108-generate-the-complete-original-default-fortress-art-pack.md).

2026-10-08 — D103 rework: the owner approved a Go art generator in place of Aseprite ([D103](../../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite)) and kept replacement packs. Retitled from “Generate a minimal original layered art sample through a pinned Aseprite CLI”; the criteria now name the generator instead of Aseprite's batch CLI and source documents. The ticket now waits for ticket [01](01-prove-the-portable-toolchain-and-integration-check.md), because the generator is project Go code that needs the pinned toolchain and the check command. This grants no separate implementation, contract-freeze or release approval.

2026-10-08 — D104 rework: the owner adopted the two proposed controls and widened the review ([D104](../../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites)). Shapes now come from pixel maps, the generator writes contact sheets of the map and the character sprites, and the generator code is approved only after he approves both. Review rounds may learn from open-source projects without copying their art. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-08 — D105 rework: the generator may now use Go modules from outside the standard library that assist sprite generation, each with its D29 record and a pinned version ([D105](../../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui)); reproducibility and provenance now include module versions. Ticket [137](137-improve-the-generated-map-and-character-sprites-before-the-native-gui-exists.md) continues the review rounds after this ticket, before the native GUI exists. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.
