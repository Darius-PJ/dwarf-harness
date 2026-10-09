# 128: Prepare and complete officer succession from the GUI

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F succession (D93–D95)
Blocked by: 106, 118, 126

## What to build

Show and drive Aspirant preparation, handoff review, cutover and predecessor retirement through Core succession, including the ordinary recovery state, with no GUI bypass.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Succession producers from tickets [104](104-hand-off-recipient-scoped-attenuated-succession-evidence.md), [105](105-prepare-and-align-an-aspirant-outside-the-occupied-officer-slot.md) and [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md).

## Acceptance criteria

- [ ] Party sheets show an Aspirant separately from the occupied leadership slot, with its alignment and readiness progress; the Incumbent remains the shown holder until cutover.
- [ ] The handoff review shows processes, lessons and the attenuated relationship roster as second-hand evidence with provenance and unknowns, never the Incumbent's exact values or private memories.
- [ ] Cutover and predecessor retirement appear only from Core's recorded outcome, and office bindings are flagged for explicit reuse review rather than handing the old identity to the successor.
- [ ] During Incumbent service loss the GUI shows the ordinary recovery state and offers no emergency promotion; detaching the GUI does not stop Core succession.
- [ ] Every succession control is keyboard operable, with confirmation for one-way steps.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 160, 161, 162.
- Implementation decisions: 1, 2, 3, 4, 18, 57.
- Testing decisions: 1, 2, 15, 20, 21, 22.
- Fortress acceptance: FG20.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §7 prepared officer succession
- GUI roadmap Creed, mythology and succession stage

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Succession now follows Core design §13.11: ticket [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md) pauses it during Incumbent or Aspirant service loss, and ticket [136](136-show-outages-and-stand-in-for-an-office-from-the-gui.md) adds the unrecoverable declaration and the restoration-before-cutover choice to the GUI succession view ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)). These extensions add no blocker or criterion to this ticket.
