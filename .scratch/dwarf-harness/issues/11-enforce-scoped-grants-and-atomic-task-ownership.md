# 11: Enforce scoped grants and atomic task ownership

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: 10

## What to build

Use current attenuated authority for bound local-tool/turn admission, commands, effects and reads; race claims/reassignments safely and keep permission denial distinct from exhausted capacity.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Owner-provided private remote and Windows/Linux/macOS CI; live provider credentials only when a live test is attempted.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Default-deny actor/context/operation/target checks apply at admission, dispatch and disclosure; owner ceilings only attenuate via explicitly authorized task/context grants.
- [ ] In-scope local tools require no paid consent; shell/process/network/connector/model effects need separate current grants.
- [ ] Atomic expected-version task claims/assignments/transitions refuse races and stale reassignment; revocation is checked before new effects and reads.
- [ ] Bound non-paid StartTurn admission and artifact/current-context access work before provider integration; permission denial and resource exhaustion stay distinct.
- [ ] Core alone owns durable non-paid capacity admission; paid provider work extends the same BudgetBook rather than creating a second wallet or inferred allowance.
- [ ] Task state, claims, assignments, grants and pending work change only through producer-owned typed Core transitions that bind actor, context, operation and target, check expected version and current authority, publish required private bytes and a bounded replay-complete event group through the existing sequencer, then update rebuildable authorized projections before acknowledging. Model, Git and check effects stay outside the short transition and return through typed, identity-bound results; an intent or command receipt is never proof that the external effect happened.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 5, 27, 28, 30, 31.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 12.
- Testing decisions: 1, 2, 8, 17, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover compulsory typed Core transitions for task, grant and pending-work state. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.
