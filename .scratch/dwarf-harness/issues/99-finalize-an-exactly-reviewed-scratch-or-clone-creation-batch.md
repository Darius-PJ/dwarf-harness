# 99: Finalize an exactly reviewed scratch or clone creation batch

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 reviewed creation drafts, batch births and recorded intent (D90–D92)
Blocked by: 32, 98

## What to build

Turn a reversible creation draft (a new Corps with Purpose, triad and workers, or additions to an existing Corps, from scratch or as clone batches) into exactly the reviewed births once, with recorded Create or Create-and-activate intent.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C8 BirthProposal, batch and intent families; specify the batch commit and partial-failure encoding with C7 before freeze.
- Agent-initiated births keep ticket [32](32-create-one-independently-owned-child-from-an-exactly-approved-birth-manifest.md)'s current-superior exact-manifest approval and Grand owner approval.

## Acceptance criteria

- [ ] A creation draft is a reversible Core record that survives restart and creates no agent, membership, grant or spending; inspecting or saving it makes no model call.
- [ ] A new-Corps draft requires its shared Purpose, then exactly one Orchestrator, Griot and Diplomat, before workers; additions may add one or more agents to an existing Corps; clone batches name each new identity and its explicitly shareable inheritance.
- [ ] Final review enumerates the exact Corps, agents, roles and destinations, clone inheritance, grants and budgets and creation intent; approval binds that content hash and any change invalidates it.
- [ ] Final creation rechecks current authority and role occupancy; a second holder of an occupied leadership role, including through a leader clone, is refused.
- [ ] Batch commit is idempotent: retries and crashes never duplicate a birth, partial failures are visible per item, and clones receive fresh identities and private contexts with no memory, continuation or grants by default.
- [ ] The terminal exercises scratch, clone, new-Corps and addition batches, including agent-initiated births under their existing superior approval.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 53, 54, 154.
- Implementation decisions: 1, 2, 3, 4, 11, 18.
- Testing decisions: 1, 2, 8, 20, 22.
- Fortress acceptance: FG16.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C8 Delivers and "Reviewed creation and intent"
- Core design §§7.1, 7.6, 13.6
- Decision log D84, D85, D90–D92

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
