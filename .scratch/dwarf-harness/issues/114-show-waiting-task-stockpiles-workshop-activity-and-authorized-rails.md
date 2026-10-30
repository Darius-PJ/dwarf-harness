# 114: Show waiting-task stockpiles, workshop activity and authorized rails

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F work and communication representation (D89–D92)
Blocked by: 89, 110

## What to build

Show real waiting and active task facts as stockpiles and workshop activity, and authorized communication edges as rails with board hubs and receipt-based traffic.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Task facts from ticket [89](89-report-waiting-ready-waiting-blocked-and-active-task-facts.md); authorized edges from ticket [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md).

## Acceptance criteria

- [ ] Stockpiles beside each station show the agent's waiting self-assigned and delegated tasks, distinguishing ready from blocked work by shape or icon and text; active work appears at workshops; pile size reflects waiting work only, with no understaffing, competence or stall-age claim.
- [ ] Inspecting a pile lists the underlying tasks and blocked reasons the principal may read; self-assignment and delegation remain Core actions reached through existing commands.
- [ ] Rails join station and stockpile ports for every currently authorized direct channel; worker-peer discussion and Griot story routes end at the shared board hub, and no worker-peer or Griot-worker direct rail appears.
- [ ] Rails distinguish an available link from actual traffic using labelled receipt state; idle links remain; selecting a rail shows its permitted endpoints and channel, not message bodies by default.
- [ ] An edge retirement or revocation stops rendering that connectivity from the new projection; overview aggregation and selected-agent highlighting reduce clutter while a rail list keeps every authorized edge.
- [ ] Rerouting, moving or removing rail geometry leaves membership, relationships, permissions, work and delivery unchanged.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 132, 135.
- Implementation decisions: 1, 2, 3, 4, 52, 57.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG03, FG21.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §2 spatial vocabulary, work and representative motion, rails without new messaging rights

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-30 — Ticket [89](89-report-waiting-ready-waiting-blocked-and-active-task-facts.md) is resolved. It supplies authorized per-agent waiting-ready, waiting-blocked and active task facts, complete visible counts and bounded same-head pages with stale-cursor recovery. Count-only consumers need one call per agent at Limit 1; no GUI or launcher transport was added. Ticket [110](110-excavate-designate-and-bind-rooms-through-core-saved-manual-plans.md), this ticket's other blocker, remains unresolved; the other start and authority gates above stand.
