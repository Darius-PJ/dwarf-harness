# 121: Autocomplete remaining construction from an accepted rearrangement preview

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F autocomplete and templates (D93)
Blocked by: 112, 120

## What to build

Fill remaining Build TODO work from deterministic templates after an explicit preview, including any proposed rearrangement of completed rooms or redundant-furniture replacement the user accepts.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.

## Acceptance criteria

- [ ] Deterministic default templates place stockpiles beside stations and workshops off the common hall, and also serve manual-off application of approved GUI topology changes.
- [ ] Autocomplete previews the missing construction plus any proposed rearrangement of completed rooms; nothing changes until the user accepts, and placements, blueprints, outfits, furniture and pins are preserved except for reviewed changes.
- [ ] When a filled room lacks required furniture types, the preview warns before replacing redundant furniture with the necessary pieces; every preset fits its required furniture and plans stay within z-15..z+2.
- [ ] No valid fit, no space or a reached limit leaves explained pending items and the last saved fortress intact, with no silent demolition, lost customization or false completion.
- [ ] Only accepted bounded presentation transactions are applied; larger plans commit complete bounded items, an interrupted autocomplete resumes from its remaining TODOs, and no agent is created or modified.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 149.
- Implementation decisions: 1, 2, 3, 4, 60.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG11, FG13, FG14.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §8 autocomplete, §7 cosmetic building and completion
- Decision log D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
