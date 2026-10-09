# 136: Show outages and stand in for an office from the GUI

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F officer outages (D97–D102)
Blocked by: 129, 135

## What to build

Show outages, service-loss states, paused Corps members and waiting questions in the fortress; let the owner answer grouped prompts, stand in for a lost office and decide unrecoverable successions from the GUI; and run FG22 on the supported platforms.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated GUI module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Outage projections and commands from ticket [135](135-show-outages-pauses-and-stand-in-actions-in-every-view.md); the qualified fortress, supported platforms and assistive technology from ticket [129](129-qualify-the-complete-fortress-on-supported-native-platforms.md).

## Acceptance criteria

- [ ] Agent sheets and dwarves show service loss and its cause with a non-color status, and outage begin and end notices list every affected agent without stealing focus.
- [ ] The Corps party sheet shows how many members are paused and how many, and which, reserved questions are waiting, filtered for the inspecting principal; a Grand officer's outage shows on the colony status.
- [ ] The owner answers one wait-or-stop prompt per cause, with per-turn overrides and per-turn switch or compact choices, resolved once with the terminal.
- [ ] Stand-in controls label every act “the owner, acting for” the office, and the succession view offers the unrecoverable declaration and the restoration-before-cutover choice, with confirmation for one-way steps. Every control is keyboard operable.
- [ ] Viewing never restores an agent or starts paid work, and detaching the GUI changes no outage, pause or succession state.
- [ ] FG22 runs on each supported platform against the actual Core, terminal and native client, with keyboard, screen-reader and non-color checks and recorded observations; document checks, mocks and art previews do not count.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 166, 167, 168.
- Implementation decisions: 1, 2, 3, 4, 52, 54, 55, 57, 63, 64.
- Testing decisions: 1, 2, 15, 20, 21, 23.
- Fortress acceptance: FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §4 Corps party sheet, §7 officer outages and §11 FG22
- GUI roadmap GUI-F officer-outages stage
- Decision log D97–D102

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
