# 127: Review Corp Creeds and approve initial founding stories in the GUI

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: GUI-F Creed and mythology (D93–D94)
Blocked by: 102, 126

## What to build

Show Creed versions, keep raw interviews and exports separate, and let the human review and approve initial Grand and Corps founding stories with their lineage, clearly labelled as fiction.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Native client from ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md): Fyne v2.8.1 in the isolated gui/ module, authenticated C7 attach and typed commands only; no browser, game engine, GUI database or direct colony-file access.
- Creed and mythology producers from tickets [101](101-version-the-corp-creed-and-target-optional-re-alignment.md) and [102](102-approve-initial-founding-stories-from-grand-prehistory-and-corps-reinterpretations.md).

## Acceptance criteria

- [ ] Corp Creed versions show Purpose, mandates and resolved vocabulary and decisions with version provenance to authorized readers.
- [ ] Raw interviews appear only to their participants' authorized views and the administrator; project-document exports are separate review actions that write no repository automatically.
- [ ] Grand prehistory and Corps founding-story drafts show their source and version lineage; the human approves each initial publication explicitly, and an unpublished story never blocks alignment or activation.
- [ ] Narrative is visually and textually distinguished from factual history, completed work and statistics, and Corps mentioned in stories never appear as real Corps.
- [ ] Story text renders inert and bounded, and the flow is keyboard and screen-reader usable.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 159, 163.
- Implementation decisions: 1, 2, 3, 4, 19, 57.
- Testing decisions: 1, 2, 15, 20, 21, 22.
- Fortress acceptance: FG18, FG19.

Canonical sections (resolved through the parent specification’s reading map):
- Fortress GUI spec §7 Corp Creed and founding mythology

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
