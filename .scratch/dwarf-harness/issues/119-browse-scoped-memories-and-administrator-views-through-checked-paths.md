# 119: Browse scoped memories and administrator views through checked paths

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F memory browser; GUI-C administrator inspection (D90)
Blocked by: 93, 113

## What to build

Add the Memory tab and administrator inspection across fortress and existing operational panels, rendering every scoped memory as inert text and filtering every alternate output path.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Administrator and agent projections from ticket [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md).

## Acceptance criteria

- [ ] Separate selectors cover general, relationship, task and board memory, each showing owner agent/context, counterpart or task, version, provenance and freshness, with permitted revision history; general memory is never a union of private memories.
- [ ] Memory renders as inert text; selecting it starts no turn, injects nothing into a prompt and copies nothing elsewhere, and edits or shares use their existing authorized Core commands.
- [ ] The authenticated administrator reads all colony records, including both relationship perspectives, without impersonation and without provider credentials; the same administrator view is verified on the existing operational panels (tickets [55](55-inspect-authorized-operations-and-mutate-a-task-through-the-real-core.md) and [56](56-read-conversations-and-griot-history-without-reopening-retired-access.md)).
- [ ] Other principals see only granted material and retired agents keep their D83 holes; changed authorization reaches thumbnails, snippets, search, counts, accessibility announcements, clipboard and exports as well as the body.
- [ ] Transient buffers are cleared when authority cannot be re-established, and lists are bounded and keyboard and screen-reader usable.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 124, 141, 164.
- Implementation decisions: 1, 2, 3, 4, 52, 54, 57.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG08.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §4 memory browser, §10 privacy gates
- GUI roadmap GUI-C administrator inspection

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
