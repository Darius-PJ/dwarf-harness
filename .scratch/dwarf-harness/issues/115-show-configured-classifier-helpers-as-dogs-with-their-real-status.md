# 115: Show configured classifier helpers as dogs with their real status

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: Router/helper association projection (R5) with GUI-F dogs (D89)
Blocked by: 53, 110

## What to build

Project actual configured classifier-helper associations and status through Core, and show each one as a dog in the assisted agent's office or workshop.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- First-party classifiers from ticket [53](53-enable-optional-local-and-paid-classifiers-only-after-measured-net-value.md); an external helper appears only after its R5/C10 qualification (ticket [52](52-run-an-external-routing-helper-only-inside-a-qualified-finite-compartment.md)), never because a dog was drawn.
- The name jev identifies no installable package; nothing is installed by this ticket.

## Acceptance criteria

- [ ] The router/helper owner publishes authorized helper associations (helper identity and assisted agents) and runtime status (idle, running, unavailable, unknown) through an owner-frozen C7 projection.
- [ ] Each association shows a dog in the assisted agent's office, or workshop for a worker; a shared helper has a marker in each assisted room without implying another process, and several helpers are individually inspectable through a bounded list.
- [ ] Unknown status is labelled unknown, not busy; disabling or removing an association removes its dog, and decoration, model names or room content never create one.
- [ ] Helper usage stays under Core admission and accounting and is attributed once in agent and party statistics.
- [ ] Dog motion follows reported state within bounded, suspendable animation with textual equivalents.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 134.
- Implementation decisions: 1, 2, 3, 4, 29, 57.
- Testing decisions: 1, 2, 11, 15, 20, 21.
- Fortress acceptance: FG02.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §2 helper dogs
- Router roadmap R5
- GUI roadmap fortress and RPG layer

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
