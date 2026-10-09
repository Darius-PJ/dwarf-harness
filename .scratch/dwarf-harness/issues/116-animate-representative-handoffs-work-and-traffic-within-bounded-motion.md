# 116: Animate representative handoffs, work and traffic within bounded motion

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F representative motion (D90–D91)
Blocked by: 114

## What to build

Animate workshops, message traffic and horse-and-wheelbarrow task handoffs as batched, rate-limited representations of real facts, with static equivalents and suspension.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.

## Acceptance criteria

- [ ] Each office rests with one horse and wheelbarrow; on a represented handoff the resident departs, the office may be briefly empty, and a replacement fades in after a short cooldown independently of the travelling runner fading out on delivery.
- [ ] Same-route handoffs batch and departures are rate-gated so not every handoff makes a trip; runners are never shown as agents or validation runners.
- [ ] Assignment, stockpile counts and delivery never wait for travel, cooldown, a visible route or an open GUI; workshops animate from actual work state and rails from authorized receipts.
- [ ] Reduced motion and hidden-view suspension keep every fact and its textual equivalent; animation work and update rates are bounded with no idle wandering, and caps are recorded as calibration values.
- [ ] Keyboard and screen-reader users receive the same handoff and activity facts as text without losing focus.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 135, 153.
- Implementation decisions: 1, 2, 3, 4, 57, 63.
- Testing decisions: 1, 2, 15, 18, 20, 21.
- Fortress acceptance: FG21.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §2 work and representative motion, §10 accessibility and resource gates

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
