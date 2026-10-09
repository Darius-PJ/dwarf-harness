# 120: Announce terminal-created topology and resume manual building across restarts

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F reconciliation and persistent Build TODO (D89, D92–D93)
Blocked by: 111

## What to build

On attach, live change or gap recovery, compare authorized topology with the reviewed baseline, prompt Autocomplete build, Build manually or Later through a durable change group, and keep manual progress across restarts.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Topology snapshots from ticket [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md); change groups, items and baseline persist through ticket [96](96-save-current-fortress-presentation-atomically-with-bounded-retries.md).

## Acceptance criteria

- [ ] After authenticated attach the GUI compares the authorized topology snapshot and revision with the last reviewed baseline by stable IDs and revisions (never names, timestamps or the event cursor) and detects additions made while it was closed, including a new Corps with its leaders and workers.
- [ ] One durable change group is created or reconciled before choices appear, offering the change summary with Autocomplete build, Build manually and Later even when manual planning is off; first launch places the existing colony the same way.
- [ ] Build manually persists the choice and adds items for the Corps common area and board, officer offices, each workshop sideroom, adjacent stockpiles, each dwarf's placement, configured helper-dog positions and authorized rails; colony-scoped diplomatic seats and Grand offices are not duplicated per Corps, and dogs and rails derive only from actual associations.
- [ ] Each item has a stable identity (change group, subject ID, requirement kind), source revision, targets, dependencies, draft geometry and state (pending, placed, blocked with explanation, superseded), and each placement saves its own progress.
- [ ] Later persists the deferral with a visible pending-build badge and resumable TODO; unplaced agents keep working and stay accessible, and dismissing a dialog completes nothing.
- [ ] Closing halfway and restarting resumes the same items and placements; the reviewed baseline advances independently of pending items and of through_seq, and later unseen changes are announced without repeating completed work.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 147, 148.
- Implementation decisions: 1, 2, 3, 4, 60, 62.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG11.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §8 startup and live reconciliation, checklist contents and transitions
- GUI roadmap GUI-F restart drill

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
