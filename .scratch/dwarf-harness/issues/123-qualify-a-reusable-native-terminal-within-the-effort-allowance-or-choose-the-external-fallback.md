# 123: Qualify a reusable native terminal within the effort allowance or choose the external fallback

Status: ready-for-agent
Type: prototype
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F terminal integration; C7 existing-session attach (D90–D92)
Blocked by: 54

## What to build

Prove or reject embedding a complete off-the-shelf native terminal for the real launcher on supported platforms within the owner's approximately 5–10% overall-project allowance, otherwise select the user-selected external terminal.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Any new module needs a D41 dependency record and owner approval; the historical [terminal research](../../fortress-gui/terminal-research.md) is candidate evidence only.
- No bespoke emulator, renderer or history project, browser renderer or shell-text launch.

## Acceptance criteria

- [ ] A pinned candidate revision (first candidate: the fyne-io/terminal library widget, not its application) runs the installed dwarf-harness terminal view inside Fyne v2.8.1 by resolved executable and argument vector through ConPTY on Windows and a PTY on Linux and macOS, never through a constructed shell command.
- [ ] Scrollback, resize, input and paste, Unicode and quoting, environment and working directory, process wait, exit and cleanup, and basic accessibility are exercised on each supported platform, with results and versions recorded.
- [ ] The bounded integration and adaptation work is estimated against the approximately 5–10% overall-project allowance and compared with the external-terminal fallback, and the licences of the candidate and its dependencies are recorded.
- [ ] The outcome (embed the qualified candidate, or use the user-selected default external terminal) is recorded as a decision with its originator and evidence; failing any blocking item selects the fallback, never a bespoke build.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 138.
- Implementation decisions: 1, 2, 3, 4, 61.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG05.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §3 CLI paragraph and §12
- Fortress terminal research
- GUI roadmap fortress and RPG layer
- Decision log D90–D92

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-20 — For the first release, this ticket needs only its Windows parts; its Linux and macOS parts follow the release ([D138](../../../docs/DECISIONS.md#d138-a-windows-first-release-with-portable-code)). The code stays portable and is tested on Linux and macOS in CI. Its number, blockers and criteria are unchanged.
