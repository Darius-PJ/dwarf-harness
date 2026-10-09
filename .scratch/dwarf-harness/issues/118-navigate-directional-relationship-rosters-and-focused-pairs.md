# 118: Navigate directional relationship rosters and focused pairs

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F relationship inspection (D90–D92)
Blocked by: 95, 113

## What to build

Add the Relationships tab: each agent's roster with directional labels, a focused A/B pair view that keeps both perspectives separate, and navigation between rosters with rechecked authorization.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Labels and refresh state from tickets [94](94-classify-a-about-b-relationships-in-the-nine-region-grid-from-eligible-evidence.md) and [95](95-refresh-relationship-labels-automatically-within-coalesced-bounded-spend.md); scoped memories and administrator projection from ticket [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md).

## Acceptance criteria

- [ ] A's roster shows counterpart sprites with A-about-B labels: grid word, available axes, unknown or partial evidence and outdated/updating markers, never a midpoint or an outdated label presented as current.
- [ ] Clicking B opens the focused A/B pair with A about B and B about A as separate perspectives and separately scoped memories; selecting either agent opens that agent's own roster, and breadcrumbs restore pair and viewpoint on Back.
- [ ] The pair detail shows named directional evidence and separately identified exchanged history, never merging private memories.
- [ ] Every navigation step rechecks authorization: the administrator sees both perspectives, other principals see only their grants, and a retired agent's history keeps its holes.
- [ ] A relationship memory without DM rights shows no rail or message permission, and labels never imply permission, performance or compatibility.
- [ ] Viewing, hovering and navigating start no classification; pages are bounded and keyboard and screen-reader usable.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 139, 140.
- Implementation decisions: 1, 2, 3, 4, 54, 59.
- Testing decisions: 1, 2, 15, 20, 21.
- Fortress acceptance: FG06, FG07.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §3 navigation table, §5
- GUI roadmap fortress and RPG layer

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
