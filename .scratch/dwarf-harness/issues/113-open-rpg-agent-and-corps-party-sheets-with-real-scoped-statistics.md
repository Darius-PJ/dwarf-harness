# 113: Open RPG agent and Corps party sheets with real scoped statistics

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F identity inspection with C7 statistics projection (D89–D90)
Blocked by: 55, 56, 57, 110

## What to build

Open the right RPG agent, party or diplomatic roster screen from fortress clicks or the keyboard, showing real scoped Core statistics and linking to the existing operational panels.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Adds an owner-frozen C7 statistics projection over existing C3, BudgetBook and router facts; absent producers show unavailable, never zero.

## Scope boundaries

- Relationships, Memory and Appearance tabs arrive with tickets [118](118-navigate-directional-relationship-rosters-and-focused-pairs.md), [119](119-browse-scoped-memories-and-administrator-views-through-checked-paths.md) and [117](117-randomize-once-and-customize-persisted-dwarf-appearance.md); Open CLI with ticket [124](124-attach-open-cli-to-the-selected-contexts-existing-session.md); Purpose, alignment and succession status with tickets [126](126-conduct-leader-alignment-and-purpose-revisions-in-the-gui-with-shared-terminal-progress.md) and [128](128-prepare-and-complete-officer-succession-from-the-gui.md).

## Acceptance criteria

- [ ] In Inspect mode picking follows dwarf or helper or station, then owned room, then common-area background; an office interior opens its owner even when that dwarf is elsewhere, and keyboard room and agent lists reach the same screens.
- [ ] The agent sheet shows the full assembled sprite, name, role, Corps and status with Stats and Contexts tabs; statistics come from actual Core facts (identity, role and Corps, configured provider/model and capabilities, execution state, active contexts against the concurrency limit, assigned work and blocked reasons, queue and receipt state, measured token and cache use, charged and reserved cost, freshness), each with scope, unit and period or revision.
- [ ] Unavailable is not zero, estimated is not settled, helper usage is attributed once, and no HP, level, competence or affinity is derived from clothes, rank, room size or tokens; task status, accepted promotion and narrative stay distinct.
- [ ] The Corps party sheet lists every authorized member (exactly one Orchestrator, Griot and Diplomat plus workers, including unplaced members) with paging and filtering but no clipping, Core-supplied aggregates with the same scope and period as the rows and no double counting, and drill-down to the existing task, grant, resource, promotion, workflow and story panels.
- [ ] The diplomatic-hall background opens a gathering roster that keeps each Diplomat's real Corps label and is not a new Corps; Back restores camera, selection and list position, and an unreadable identity opens an unavailable view.
- [ ] Statistics refresh within bounded work, stay keyboard and screen-reader usable with non-color status, and viewing spends no model calls.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 136, 137.
- Implementation decisions: 1, 2, 3, 4, 52, 54, 57, 63.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG04.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §3 clicks and navigation, §4 agent character sheet and Corps party sheet
- GUI roadmap fortress and RPG layer, GUI-C

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [136](136-show-outages-and-stand-in-for-an-office-from-the-gui.md) adds service loss and its cause to agent sheets, and paused members and waiting reserved questions to the Corps party sheet ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)). These extensions add no blocker or criterion to this ticket.

2026-10-09 — Consolidation cross-link: under [D108](../../../docs/DECISIONS.md#d108-character-sprites-a-still-front-view-an-animated-three-quarter-view-and-corps-emblems-and-colours), the agent sheet shows the three-quarter sprite with its idle or role action loop chosen from Core's actual work status, and the party sheet shows straight-on still sprites; both views carry the Corps emblem and colour. These extensions add no blocker or criterion to this ticket.

2026-10-09 — Cross-link from ticket [88](88-generate-a-minimal-original-layered-art-sample-with-the-go-art-generator.md)'s closing: the status keeps its own icon and text, and the animation loop is never the status indicator. Mapping Core's statuses onto the idle and working loops, and what plays for statuses the owner has not named, such as service loss, paused and unknown, are decisions for this ticket. With reduced motion the sheet shows a still frame and keeps the status text. This adds no blocker or criterion to this ticket.
