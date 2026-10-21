# 129: Qualify the complete fortress on supported native platforms

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F release evidence; reapplies GUI-D/GUI-E with the fortress enabled
Blocked by: 58, 108, 115, 116, 119, 122, 124, 127, 128

## What to build

Run every FG01–FG21 scenario and the native accessibility, packaging and declared-workload gates against the real Core, terminal and native client with the full default pack.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Actual supported Windows, Linux and macOS machines, screen readers and accessibility inspectors; native SDK, signing and notarization prerequisites.
- Declare workload and resource bounds before measuring; a failure stays blocked, and the declared workload is never shrunk to pass.
- GUI-E parity (ticket [58](58-qualify-native-accessibility-packaging-and-full-c9-parity-on-supported-platforms.md)) stays independently deliverable; this ticket adds the fortress release gate.

## Acceptance criteria

- [ ] Every FG01–FG21 scenario runs against the actual Core, terminal and native client with the complete original pack, with observations recorded; document checks, mocks and art previews do not count.
- [ ] The GUI roadmap restart drill passes: close the GUI, create a Corps in the terminal, reopen and build half of it manually, restart, resume the same TODO and interview state, then preview autocomplete with explicit acceptance; races cannot create a second dwarf, room, item, birth or paid first turn.
- [ ] Keyboard, screen-reader, focus, non-color, scaling, high-contrast and reduced-motion checks pass with animated views on each declared platform, recording OS, Fyne/runtime and assistive-technology versions.
- [ ] On the declared 8-GiB integrated-graphics laptop, the 20-agent/three-context and 200-agent navigation and layout cases, with 50/s steady and 500/s ten-second burst synthetic projected updates, measure cold start, idle CPU/RAM, whole-process peak memory, pan/zoom/input latency, backlog and recovery, animation and save latency alongside active Core work.
- [ ] GUI-E packaging and resource checks are rerun with the fortress enabled; Core and terminal stay cgo-free, and the pack and its licence notices ship with the GUI.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 131, 137, 143, 146, 153.
- Implementation decisions: 1, 2, 3, 4, 5, 55, 63.
- Testing decisions: 1, 2, 3, 15, 18, 20, 21, 22.
- Fortress acceptance: FG01–FG21.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §§10–11
- GUI roadmap GUI-F, GUI-D and GUI-E
- Decision log B16, D89–D96

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [136](136-show-outages-and-stand-in-for-an-office-from-the-gui.md) runs the new FG22 officer-outage scenario on the supported platforms after this ticket's qualification; this ticket's FG01–FG21 run is unchanged ([D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)). These extensions add no blocker or criterion to this ticket.

2026-10-20 — For the first release, this ticket needs only its Windows parts; its Linux and macOS parts follow the release ([D138](../../../docs/DECISIONS.md#d138-a-windows-first-release-with-portable-code)). The code stays portable and is tested on Linux and macOS in CI. Its number, blockers and criteria are unchanged.
