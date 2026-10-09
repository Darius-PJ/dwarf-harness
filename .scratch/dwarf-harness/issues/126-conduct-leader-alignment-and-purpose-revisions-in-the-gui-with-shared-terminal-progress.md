# 126: Conduct leader alignment and Purpose revisions in the GUI with shared terminal progress

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F shared alignment (D91–D93)
Blocked by: 101, 125

## What to build

Present Orchestrator-then-Griot onboarding interviews, human acceptance, staged Purpose revisions and optional re-alignment in the GUI, sharing one durable progress state with the terminal.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Alignment, Purpose and re-alignment producers from tickets [98](98-keep-one-shared-corps-or-colony-purpose-with-staged-human-cutover.md), [100](100-align-every-new-orchestrator-and-griot-before-ordinary-work.md) and [101](101-version-the-corp-creed-and-target-optional-re-alignment.md).

## Acceptance criteria

- [ ] Team onboarding presents the Orchestrator's expected-work interview, then the Griot's values interview, from the actual pinned runs; waiting questions are answerable from either launcher and resume the same progress.
- [ ] The UI explains that interviews can continue endlessly and recommends stopping when questions no longer feel consequential; only the human accepts, even with questions remaining.
- [ ] Agent and party sheets show alignment and activation state; a new Corps shows ordinary work held until both acceptances, after which Create stays inactive and Create and activate proceeds when gates permit.
- [ ] Staged Purpose revisions show the approved version still governing work until cutover; work or values changes offer targeted re-alignment that the human may skip, shorten or disable, while initial alignment has no skip control.
- [ ] Two views racing the same answer or approval execute once; reconnect neither repeats a paid first turn nor activates a Create-only team, and stale answers are refused.
- [ ] Interview text renders inert, and the whole flow is keyboard and screen-reader usable.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 155, 156, 157, 158.
- Implementation decisions: 1, 2, 3, 4, 18, 57.
- Testing decisions: 1, 2, 15, 20, 21, 22.
- Fortress acceptance: FG17, FG18.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §7 Purpose alignment, activation and changes
- GUI roadmap shared creation, Purpose and succession
- Core roadmap C7 "Shared interview/approval state"

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
