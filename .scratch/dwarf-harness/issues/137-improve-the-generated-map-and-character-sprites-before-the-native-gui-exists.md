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

2026-10-09 — Round 1 plan. A designer drafted it from a survey of openly licensed technique sources, and the build advisor approved it with changes: four art-data changes with no generator code, module or format change. The owner selected all four: the worker's hammer blow, legs that turn with the body, seamless wall tops and the dog side-on. The round's sources, changed cells, digests and his review of the result will be recorded here when the round ends.

2026-10-09 — Round 1 sources (criterion 4). Each taught technique only; no art was copied or traced and no code was reused. The Liberated Pixel Cup Style Guide (OpenGameArt.org; documentation and assets CC BY-SA 3.0 and GPL 3.0 or later): a repeated tile keeps its centre one colour or a very subtle pattern and carries detail at its edges, and creatures need their own palette to stand out from the background. The Battle for Wesnoth wiki's art tutorials (GNU Free Documentation License, version not stated): exaggerate the wind-up, move each part in proportion to its weight, run an attack as raise, weight forward, impact, and redraw a part after a turn instead of sliding it. Wikipedia's articles on the twelve basic principles of animation and on 2.5D (CC BY-SA 4.0): timing as the frames and holds an action gets, and a three-quarter view showing a turned figure's side as well as its front.

2026-10-09 — Owner review, round 1. Reviewed sample at commit ae249f7 (`contentSha256` 70cdd2ce8d07ac0751529dc07a175fbd3fa117953b58421f526a264b573f94dc; generator `sourceSha256` 82ba059fc79b3c027309663184af6c75ac75b37715b8cee68352386991ccfebd, unchanged because the round changed no code; toolchain go1.27.2; command `GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/artgen`): map-1x 854f01ac1812852d41279ec12791ffdb96baa64e51bbbe68a775ef45419c4e33, map-4x ec3af6bfab70dea0cece49b09779c4a59ba6ab90222ca08452e1a859d9e9fe3e, character-1x ab5935f75a763294157716b643fa560c0ef66d8eb87ac4140261561d5230f2b8, character-4x a29dd78f58fdf367cbafbd4f58cee3e1b5ab10594e207ef4e52e260d721f25ca; previews page-carter-act_diplomat 4ca4ddceba258b4c1593e35fea4ec2c31a3d8f19e9d2b46dece267e1f582bb63, page-carter-idle 4ea4217d5a4c542c0445d920f56bb2d5f753e82268b17ad84b28141c402646bd, page-miner-act_worker eb4ae1363a1adc29ac0951a4bbb871687ee2bc6ee4059fd65aa6534ec529ed23, page-miner-idle f7ebe26a86fb65888d0533ebdd7f96942a59f4b04f19d16dae80a0b26ccfd551, page-scribe-act_diplomat 9241935b1aad34c1caa09d6b252f0ea1c99f2b30672a163f5119b058a527d25f, page-scribe-idle 79c53a7616f9bcf94632b2cb575090d2cad173908710966dcfdae6bbcbf4c0f8, page-smith-act_worker 235fd3c795216a4c18b8067846fc1d3d972c951a29cffcc86a73a1b8ef272cf1, page-smith-idle 7679ca6afc8f2eee3a33d7e31ed4171e698a5d862e6c27d9d64b164932d8c24b. The round changed 64 cells: all 18 agent-page cells, 40 three-quarter layer cells (the legs in every frame, the strike frame's riding layers and the worker's tool layer with its new anvil), the four dog cells, the wall-top tile and the room. The map's dwarves, roles and badges and the character sheet's front stills, rosters and Corps markers are byte-identical to the approved sample. The review message disclosed that the maps were authored with throwaway scripts outside the repository; every committed frame is explicit text. The owner approved all four changes of the plan, the map sheet and the character sheet.

2026-10-09 — The build advisor approved round 1 at commit ae249f7 after one review fix that evened the sitting dog's chest edge. With the owner's approval of both sheets, the round's maps count as approved under [D104](../../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites). Its rules and interpretations are recorded in [B20](../../../docs/DECISIONS.md#b20-art-round-1-of-ticket-137-the-hammer-blow-turned-legs-a-seamless-wall-top-and-a-side-on-dog). Left for later rounds: the vest covers almost all of the legwear; the pennant cannot fold or wave in its space and frame count; the overworld pick's head reads as a club; the 64-pixel ramps have only three steps, and a fourth needs generator code; and the dog's fur is close to the floor in brightness.
