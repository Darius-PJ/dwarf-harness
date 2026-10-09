# 124: Attach Open CLI to the selected context's existing session

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F with C7 existing-session terminal attach (D92)
Blocked by: 113, 123

## What to build

From an agent sheet, attach the qualified embedded terminal or the user's external terminal to that agent's existing Core session and an explicitly chosen context.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Terminal route decided by ticket [123](123-qualify-a-reusable-native-terminal-within-the-effort-allowance-or-choose-the-external-fallback.md); owner-frozen C7 attach binding and syntax.

## Acceptance criteria

- [ ] Open CLI is offered after the user chooses an authorized context, or keeps a still-valid current context; C7 supplies an authenticated binding to the installed dwarf-harness entry point by colony, agent, session and context IDs.
- [ ] The launch uses an argument vector, never shell text built from names, and no provider secret appears in arguments or environment.
- [ ] Embedded or external attachment reaches the same execution session with no duplicate model loop, transcript or provider continuation; closing the terminal detaches unless the user explicitly cancels or shuts down through existing commands.
- [ ] An attach or spawn failure is reported with safe manual instructions using the frozen attach syntax, never invented flags or a fake console.
- [ ] Alignment and approval progress resume identically from the GUI and either terminal.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 138.
- Implementation decisions: 1, 2, 3, 4, 17, 61.
- Testing decisions: 1, 2, 7, 15, 20, 21.
- Fortress acceptance: FG05.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §3 clicks, navigation and agent CLI
- Core roadmap C7 "Real terminal attach"

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
