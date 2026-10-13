# 108: Generate the complete original default fortress art pack

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F default tileset and sprite content (B16 as amended by D103; D90–D91)
Blocked by: 107

## What to build

Extend the Go art generator to produce the full original default pack (dwarf parts, terrain, rooms and fortress props with bounded motion frames) and its manifest, so the fortress runs without stand-in art.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- The Go art generator, with its pixel maps, original-art rules and review rounds, from ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md) ([D104](../../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites)); the manifest and budgets from ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md).

## Acceptance criteria

- [ ] The generator produces separate layers for every dwarf slot with several compatible variants (body, hair and beard, clothing, shoes, accessories, colors), Grand crests, role overlays and worker specialization emblems.
- [ ] Every 64-pixel part exists in both views (D108). The three-quarter view has the idle loop and a role action loop for every role, and a default set of Corps emblems and colours stays distinguishable from the role colours, including in the high-contrast palettes.
- [ ] Terrain and props cover rock, floor, walls, doors, stairs, corridors, workshops and stations, stockpiles, rails, board hubs and the furniture types D93 names (officer desks; Corps-appropriate worker stations, desks and labs; statues, cabinets, tables, chairs and similar furnishings), each with a declared footprint, plus dogs, horses with wheelbarrows and workshop, traffic and runner frames within declared frame bounds.
- [ ] Every role stays distinguishable by silhouette or trim and a non-color badge in default and high-contrast palettes at 1x and integer zoom.
- [ ] The pack loads through ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md)'s loader within its budgets with complete licence and provenance records; switching to it changes no logical layout or saved ID.
- [ ] The owner reviews the full pack's map and character contact sheets as in ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md). The generator changes are approved, and the pack becomes the default, only after he approves both sheets, and his approval is recorded under Comments.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 142, 143, 153.
- Implementation decisions: 1, 2, 3, 4, 52, 58, 63.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG01, FG09.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §2 role colors and art, §6
- Decision log B16, D90, D91, D103, D104

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — The owner chose agent-scripted art (ready-for-agent) over human-authored art for this ticket and ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md).

2026-10-08 — D103 rework: the full pack now comes from the Go art generator of ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md) instead of Aseprite ([D103](../../../docs/DECISIONS.md#d103-default-fortress-art-a-go-generator-replaces-aseprite)); the owner's contact-sheet review still gates the default pack. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-08 — D104 rework: the full pack keeps ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md)'s pixel maps and review rounds, which may learn from open-source projects without copying their art, and its generator changes are approved only after the owner approves both the map and the character contact sheets ([D104](../../../docs/DECISIONS.md#d104-default-fortress-art-pixel-maps-and-owner-review-of-generated-sprites)). Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-08 — Consolidation cross-link: Ticket [137](137-improve-the-generated-map-and-character-sprites-before-the-native-gui-exists.md) improves the map and character sprites before the native GUI exists. This ticket starts from the generator as last approved there and keeps the same review gate ([D105](../../../docs/DECISIONS.md#d105-default-fortress-art-go-modules-for-sprite-generation-and-art-rounds-before-the-gui)). These extensions add no blocker or criterion to this ticket.

2026-10-09 — D108 rework: the full pack draws every 64-pixel part in both views, with the idle and role action loops and a default set of Corps emblems and colours ([D108](../../../docs/DECISIONS.md#d108-character-sprites-a-still-front-view-an-animated-three-quarter-view-and-corps-emblems-and-colours)). Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-09 — Cross-link from ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md)'s closing ([B19](../../../docs/DECISIONS.md#b19-art-generator-round-2-character-views-page-loops-and-corps-pennants)): every part variant, the pennant and every emblem need the straight-on still plus every three-quarter frame, about 24 maps per layer with seven three-frame actions. Under [D109](../../../docs/DECISIONS.md#d109-character-sprites-carry-no-role-marker), this ticket's role overlays and Grand crests, and its rule that roles are told apart by silhouette or trim and a non-colour badge, concern the 16-pixel map sprites; on the 64-pixel sprites the action loop shows the role. Two owner questions come before those sprites are drawn: whether Grand roles share their base role's action, and whether Grand officers carry a Corps marker. A stowed front accessory assumes the pack: the pick's action frames lie across the pack and draw the pack's strap over its handle, while an outfit may carry a pick without a pack and ticket [117](117-randomize-once-and-customize-persisted-dwarf-appearance.md) will let users choose one. Give every stowed front accessory a form that stands alone, such as its own strap or belt loop, or propose a declared dependency between parts for ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md)'s manifest, and ask the owner if the answer changes approved pixels. Worker specialization emblems must stay distinct from Corps emblems, and non-colour emblems must stay readable in the high-contrast palettes, where the Corps colour guards also apply. This adds no blocker or criterion to this ticket.

2026-10-13 — The owner answered the three questions this ticket carried ([D120](../../../docs/DECISIONS.md#d120-answers-to-the-art-questions-deferred-to-tickets-108-and-137)): each Grand role gets its own action loop; Grand officers carry a colony banner instead of a Corps marker, whose design this ticket proposes on its character sheet; and an outfit with the pick but no pack shows no pick in its action frames. The colony banner joins this ticket's pack. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.
