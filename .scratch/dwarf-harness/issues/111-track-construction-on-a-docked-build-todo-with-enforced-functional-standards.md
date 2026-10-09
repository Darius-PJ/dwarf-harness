# 111: Track construction on a docked Build TODO with enforced functional standards

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F construction standards (D92–D93)
Blocked by: 108, 110

## What to build

Derive construction completion from saved objects against meaningful standards, and show what remains in a docked Build TODO grouped by Corps and agent.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Furniture types and footprints come from the default pack's manifest (ticket [108](108-generate-the-complete-original-default-fortress-art-pack.md)); initial room-size presets, furniture type/count thresholds and footprint-qualified templates are recorded engineering choices, not measured or frozen values.

## Acceptance criteria

- [ ] Completion requires usable floors, correct designation, non-overlap, required stations and nearby stockpiles, and corridor or stair connectivity; a disconnected room stays on the TODO until connected.
- [ ] Functional furniture (officer desks and Corps-appropriate worker stations, desks or labs) can never be waived; room-size and officer decorative type/count thresholds apply unless the user disables them, and disabling stops only those from blocking completion.
- [ ] Every room-size preset fits its required furniture, and autocompleting a room's furnishing is offered at the user's discretion.
- [ ] The docked TODO groups items by Corps and agent; selecting one focuses its region or ghost plan and offers the right tool; items complete only from committed presentation objects, and clearing a required object reopens its item.
- [ ] No TODO state, standard or completion ever gates agent work, membership, messaging or eligibility, and the TODO and its announcements are keyboard and screen-reader usable without stealing focus.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 145.
- Implementation decisions: 1, 2, 3, 4, 57, 60.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG10.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §7 cosmetic building and completion, §8 checklist contents
- Decision log D92, D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
