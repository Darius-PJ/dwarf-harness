# 137: Improve the generated map and character sprites before the native GUI exists

Status: claimed
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F asset-pipeline input before the native GUI (B16 as amended by D103–D105; fortress spec §6)
Blocked by: 88

## What to build

Continue improving the Go art generator's 16-pixel map sprites and 64-pixel character sprites in owner review rounds, learning from open-source pixel-art and sprite-generation projects, so that the default art matures before the native GUI and its asset loader exist.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- The generator, pixel maps, contact sheets and review gate from ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md), as the owner approved them there. No GUI ticket is a prerequisite; the asset loader (ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md)) and the full default pack (ticket [108](108-generate-the-complete-original-default-fortress-art-pack.md)) follow separately.
- The generator may use Go modules from outside the standard library that assist sprite generation ([D105](../../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui)). Each needs its D29 record before use, and an x-tier module needs a decision-log entry. No module ships in a program, and none adds an image editor, an image-generation model or a model call.
- Open-source projects inform technique and style only: none of their art is copied into the default pack, and reused code follows D29 and its source licence.

## Scope boundaries

- Rounds improve the quality, consistency and coverage of the map and character sprites in the generator's own output and contact sheets. Loading through the asset loader, its budgets, role readability in the native client and the switch of the default pack remain ticket [108](108-generate-the-complete-original-default-fortress-art-pack.md)'s work.
- Rounds keep the output format proved in ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md). Once ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md) freezes the importer, a format change goes through that freeze.
- Every shape stays a pixel map (D104). A technique that generates shapes by rule, or any other change to that rule, needs the owner's decision first.

## Acceptance criteria

- [ ] Each review round changes pixel maps, palettes or rules, regenerates both contact sheets at 1x and at an integer zoom, and records the owner's comments and decision under Comments. Generator code from a round is approved only after he approves both sheets.
- [ ] Rounds may refine existing sprites and add parts, variants, tiles, props, animals or frames from the fortress art list, while every role stays distinguishable in the contact sheets by silhouette or trim and a non-color badge.
- [ ] Each adopted module has its D29 record before use and a pinned version. Re-running the generator with the same inputs, toolchain and module versions reproduces the same files or a documented, explained difference.
- [ ] Each round that draws on an open-source project records the project, its licence, what was learned and whether any code was reused.
- [ ] Licence and provenance records keep identifying the art as original project work produced by the recorded generator version, module versions, inputs and seeds.
- [ ] The owner records under Comments that the map and character sprites are ready to serve as the base for the full pack in ticket [108](108-generate-the-complete-original-default-fortress-art-pack.md).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 142, 143.
- Implementation decisions: 1, 2, 3, 4, 5, 58.
- Testing decisions: 1, 2, 3, 20, 21.
- Fortress acceptance: FG09.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §6 owner review and steps 1–3
- Core design §8.2 dependency policy
- Decision log B16, D29, D90, D91, D103, D104, D105

## Comments

2026-10-08 — Published under [D105](../../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui) after the owner asked for a ticket to improve the art before the GUI is finished. This adopts the ticket for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-09 — Cross-link from ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md)'s closing: the sample format of its second round, with two character views, animation loops and Corps pennants ([B19](../../../docs/DECISIONS.md#b19-art-generator-round-2-character-views-page-loops-and-corps-pennants)), is the format later rounds keep. This ticket's criterion that every role stays distinguishable by silhouette or trim and a non-colour badge concerns the map sheet, because the 64-pixel sprites carry no role marker ([D109](../../../docs/DECISIONS.md#d109-character-sprites-carry-no-role-marker)). With ticket 88 resolved, this ticket starts from the generator as approved there. This adds no blocker or criterion to this ticket.

2026-10-09 — Claimed for the staged build. The owner chose that the first round begins with a team proposal. A designer studies open-source pixel-art technique and proposes specific improvements to the current sprites; the owner approves or changes the plan before any pixel changes, and the work then runs on its own branch and worktree (D41).
