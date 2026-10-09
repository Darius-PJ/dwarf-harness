# 125: Draft and approve a new Corps or additions on one RPG creation screen

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F shared creation (D90–D92)
Blocked by: 100, 117, 121

## What to build

Provide one RPG-style creation entry that walks space, Purpose, leadership triad, workers and placements as reversible drafts, then submits the exact batch for approval with Create or Create and activate.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- C8 drafts, batch approval and alignment from tickets [99](99-finalize-an-exactly-reviewed-scratch-or-clone-creation-batch.md) and [100](100-align-every-new-orchestrator-and-griot-before-ordinary-work.md); presentation drafts through ticket [96](96-save-current-fortress-presentation-atomically-with-bounded-retries.md).

## Acceptance criteria

- [ ] One starting screen handles new Corps and additions to existing Corps, from scratch or as batches of one or more clones of a selected agent.
- [ ] The space-first journey is discrete and transparent (define space and room uses, create and configure characters, plan placements); a new Corps establishes Purpose and completes its triad before workers, and a clone can never silently fill an occupied leadership slot.
- [ ] Drafts and draft placements save through Core and survive restart without births, topology or spending; extending a cosmetic Corps boundary needs no birth approval.
- [ ] Final review enumerates the exact Corps and agents, role destinations, clone inheritance, cosmetic-copy choice, grants and budgets; changing reviewed facts invalidates approval, and partial failures and retries stay visible without duplicating births.
- [ ] Create and Create and activate both warn that the first alignment turn starts immediately and incurs model usage, and refused or pending admission is shown.
- [ ] With manual planning off, approved creation applies a deterministic template; with it on, unbuilt rooms join the Build TODO; every step is keyboard operable.
- [ ] Creating a Corps also sets its colour and emblem ([D108](../../../docs/DECISIONS.md#d108-character-sprites-a-still-front-view-an-animated-three-quarter-view-and-corps-emblems-and-colours), [D110](../../../docs/DECISIONS.md#d110-corps-colour-and-emblem-are-chosen-at-corps-creation)), with an unused pair suggested; the pair is saved as presentation data, survives restarts and never enters Core state, prompts or routing, and a Corps created outside the GUI shows a fixed default pair.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 154, 157.
- Implementation decisions: 1, 2, 3, 4, 18, 57, 60.
- Testing decisions: 1, 2, 15, 20, 21, 22.
- Fortress acceptance: FG16, FG17.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §7 unified creation drafts and Purpose alignment
- GUI roadmap shared creation, Purpose and succession

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-09 — D110 rework: the owner placed the choice of a new Corps's colour and emblem in this ticket, at creation ([D110](../../../docs/DECISIONS.md#d110-corps-colour-and-emblem-are-chosen-at-corps-creation)); one criterion is added. Changing an existing Corps's pair later needs a later ticket. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.
