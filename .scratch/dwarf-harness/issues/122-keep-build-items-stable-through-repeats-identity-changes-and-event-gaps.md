# 122: Keep build items stable through repeats, identity changes and event gaps

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F reconciliation robustness (D89, D92–D93)
Blocked by: 120

## What to build

Keep reconciliation idempotent and identity-correct when changes repeat, overlap mid-build, rename, reassign or retire agents, or arrive across a stream gap.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.

## Acceptance criteria

- [ ] Repeated attach, restart, reconnect, deferral and duplicate observations reuse the same change group and items; no duplicate room, dwarf or item appears.
- [ ] Another Corps created mid-build merges into the remaining work while unaffected completed placements stay intact.
- [ ] Renaming updates the authorized label while preserving IDs and placement; a real role or Corps change flags affected bindings for explicit reuse or relocation review, never rebirthing an agent, rerolling appearance or transferring an office's old identity to a successor.
- [ ] Removal or retirement supersedes obsolete pending items, and emptied worker architecture is preserved for explicit reuse, movement or reclamation.
- [ ] Losing the inspecting principal's authorization removes sensitive labels, references and cached content; recovery after an event gap uses an authorized snapshot and discloses no unauthorized identity.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 147, 150.
- Implementation decisions: 1, 2, 3, 4, 60, 62.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG12.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §8 checklist contents and transitions

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
