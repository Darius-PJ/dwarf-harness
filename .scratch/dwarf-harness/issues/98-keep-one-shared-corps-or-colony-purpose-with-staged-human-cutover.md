# 98: Keep one shared Corps or Colony Purpose with staged human cutover

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 shared Corps and Colony Purpose (D92–D93)
Blocked by: 28, 29

## What to build

Record one mutable Purpose per Corps with role mandates, and one Colony Purpose for Grand leadership, whose revisions are staged while the approved version keeps governing work until the human decides.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C6 Done and owner-frozen C8 Purpose/mandate families.
- Specify how in-flight work crosses a Purpose cutover before the family is frozen.

## Acceptance criteria

- [ ] Each Corps has exactly one versioned Purpose with Orchestrator, Griot, Diplomat and worker mandates, and Grand leadership shares one Colony Purpose; no agent-specific Purpose can be created or edited.
- [ ] A proposed revision is staged with its author and provenance while the approved revision keeps governing current work and alignment; cutover happens only after the required human decision and records version and approval provenance.
- [ ] A correction can be cut over without any interview; stale or competing revision decisions are refused by expected version.
- [ ] Affected agents read the current approved Purpose through their actual grants; it never becomes an independent agent objective or a cosmetic room designation.
- [ ] Revision history is append-only and replays correctly through restart and two terminal views.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 158.
- Implementation decisions: 1, 2, 3, 4, 18.
- Testing decisions: 1, 2, 8, 20, 22.
- Fortress acceptance: FG18.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C8 Delivers and "Alignment and Purpose"
- Core design §§7.1, 12.4
- Decision log D92, D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
