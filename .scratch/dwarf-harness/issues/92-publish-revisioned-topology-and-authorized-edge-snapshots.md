# 92: Publish revisioned topology and authorized-edge snapshots

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C6 stable topology handoff with C7 authorized projection (D89–D96)
Blocked by: 27, 31

## What to build

Give terminal and later GUI views one authorized snapshot of colony, Corps, roles, agents, contexts and permitted communication edges, with stable IDs and a topology revision they can reconcile against.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C6 membership, role and relationship families and the corresponding C7 projection addition; C7 base is unchanged.
- No C8, C9 or GUI dependency.

## Acceptance criteria

- [ ] An authenticated view receives a principal-filtered snapshot of colony, Corps, role, agent, execution-session and context identities, membership epochs and a topology revision with its high-water sequence, then later changes after that sequence.
- [ ] Stable IDs survive rename, reassignment, retirement and restart; a rename changes only the label, and the revision advances on every authoritative topology transition.
- [ ] The snapshot lists every currently authorized direct channel and board membership derived from the actual role matrix: worker to parent Orchestrator, worker to own Diplomat, assigned parent/child links, Diplomat lateral cross-Corps channels and Corps-board participation. Worker-peer and Griot-worker direct edges never appear.
- [ ] Revocation or retirement removes the edge and the identity's sensitive fields from later snapshots; an identity the principal can no longer read appears only as unavailable, without its new name, membership or contents.
- [ ] A stream gap is recovered from a fresh authorized snapshot plus later events; the topology revision stays distinct from the event cursor, and snapshots are bounded and paginated.
- [ ] The terminal can print the same snapshot as plain text, and producing it makes no model call.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 131, 132, 133, 147, 150.
- Implementation decisions: 1, 2, 3, 4, 15, 16, 17, 57, 62.
- Testing decisions: 1, 2, 7, 8, 20, 21.
- Fortress acceptance: FG01, FG03, FG12.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 "D89–D95 social handoff" and C7 "Authorized topology/presentation snapshots"
- Core design §§7.1–7.2, 13.6–13.7
- Fortress GUI spec §2 spatial vocabulary and §8 startup and live reconciliation

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
