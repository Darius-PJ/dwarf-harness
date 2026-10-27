# 140: Bound the private-state service's replay and lookup cost

Status: ready-for-agent
Type: task
Published: 2026-10-24
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 private-state service replay and lookup cost (B3, B32)
Blocked by: 11

## What to build

Keep Core's private-state service quick to open and to use as its history grows. Ticket 11's labelled profile ([evidence](../c3-evidence/ticket-11-evidence.md#the-p16-smoke-and-the-s3b-7-profile)) found that opening the service took 1.42 s at 87 commits and 75.8 s at 874, while the ledger store's own opening took 19 ms and 166 ms, and that a turn start, a local admission, a local dispatch, a task creation and a task cancel each took 4.5 to 9.4 times as long for ten times the history. The cost is whole-ledger scans: a task lookup that misses on a work context no task has bound, a task derived twice per phase, and the integrity scans at every turn start. [B31](../../../docs/DECISIONS.md#b31-c3-the-private-state-service-ticket-10)'s Limits record the same service's earlier scans, a private write's two whole-ledger scans and Open's two per clean scope, with a candidate fix; the design says whether the fix covers them. [B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11)'s Limits record these figures as [B3](../../../docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage)'s measured trigger for a rebuildable index or snapshot. Remove the scans before ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md) makes turn start and local work production paths, without changing anything the service does.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Builds on the private-state service as ticket [11](11-enforce-scoped-grants-and-atomic-task-ownership.md) left it. Until ticket 15 gives turn start and local work a production caller, the cost is latent.
- Adds no record type, because B3's indexes and snapshots are rebuildable and discardable, never ledger authority, and changes no frozen record, rule or golden ([D139](../../../docs/DECISIONS.md#d139-f3-10-private-state-records-frozen), [D140](../../../docs/DECISIONS.md#d140-f3-11-scoped-authority-task-and-non-paid-work-records-frozen)). A new dependency would wait for the owner (D29).
- A ruled design chooses the fix before code (D117), and ticket 11's measurement driver, rerun, is its evidence.
- Each slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); no provider credentials are needed.

## Scope boundaries

- A cost fix inside the service: no new behaviour, command, record or contract.
- The rerun is labelled admission and storage only, as ticket 11's was. C3's runtime capacity evidence stays with ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md), and C4's workload gate with ticket [23](23-measure-every-change-delivery-under-steady-and-burst-load.md).

## Acceptance criteria

- [ ] A ruled design chooses the fix before code, from the candidates ticket 11's S4 review gave: a bounded index from work context to task and the turn-start integrity check narrowed to the current heads behind the projection fence; B3's fused startup pass or a snapshot only if those fail.
- [ ] Every index or snapshot the fix adds is rebuilt from the ledger alone and is discardable: deleting it, or finding it stale or corrupt, loses no committed state, appends nothing and dispatches nothing.
- [ ] Behaviour is unchanged: grants, busy and denied answers, task transitions, reads and restart recovery give the same results, both frozen goldens replay through the service, and ticket 11's injected crash rows pass unchanged; no record type, frozen rule or golden changes.
- [ ] Ticket 11's measurement driver, rerun unchanged on the same traffic at the same two log lengths, shows opening the service growing no faster than the ledger's length, as the store's own opening does, and turn start, local admission, local dispatch, task creation and task cancel each within the bound the design states. The output keeps its label, admission and storage only, and claims no capacity.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 26.
- Implementation decisions: 1, 2, 4, 10.
- Testing decisions: 1, 2, 4, 17, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3
- Decision log B3/B32/D139/D140

## Comments

2026-10-24 — Published as ticket 140 at the owner's word, which also had the tickets' dependencies and order audited and updated, and with his approval of the breakdown ([D142](../../../docs/DECISIONS.md#d142-tickets-139-and-140-published-with-three-blocker-changes)). This adopts the ticket for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-24 — Ticket [12](12-accept-only-exact-subject-review-and-failure-results.md) is resolved ([B33](../../../docs/DECISIONS.md#b33-c3-exact-subject-review-and-failure-results-ticket-12)). Its [ruling](../c3-evidence/ticket-12-design-ruling.md), P13, as its S4 review restated it, names what this ticket inherits from it:
- the F3-12 groups in the index, since task state needs both families ([D144](../../../docs/DECISIONS.md#d144-f3-12-review-and-failure-decision-records-frozen));
- the cold request and receipt lookups as index sites;
- the witness batch, which in a one-segment log reads up to each witness's position.

`sourceRecord`'s from-0 scan is no longer among them: ticket 12 moved it to a segment-aligned cursor. Ticket 12's S4 measured the cost of ticket 11's `artifactAuthority`, whose check order loaded every task a participant's grants named before knowing the artifact's own, and its S5 corrected that order. Before S5, on a quiet machine, `agents.Open` took 43.9 s at 1,047 records and 330.6 s at 2,163, and one review decision 1.16 s and 6.0 s. After S5, quiet and under the profiler, the driver rerun on d37ca15 put the read-rights path at 0.000 of every profile: at 2× (the project manager's rerun) `agents.Open` took 14.7 s at 1,047 records and 50.7 s at 2,163 (exponent 1.70; the coder's run 13.2 s → 47.8 s, 1.78) and one approve 84 ms → 140 ms (the coder's 85 → 160 ms); the coder's run on the tree whose hashes equal d37ca15's blobs reached a measured 10.04× (H 10,515, N 868, G 328): `agents.Open` 15.6 s → 1,191.1 s (exponent 1.88) and one approve 94 → 671 ms (0.85). What remains of Open's growth is ticket 10's and 11's per-scope history validation (about 45 % of its CPU) and scoped replay (23–29 %), with the review path's witness batch at 9–11 %, the ruling's O(N·(G + segment)) in a one-segment log; the driver's 10× label was changed by the project manager's answer, hashes recorded, and the 10× log's unmeasured-10× line is false for that run ([evidence](../c3-evidence/ticket-12-evidence.md#s5-the-artifact-read-check-order-2026-10-24)). Ticket 12's [evidence](../c3-evidence/ticket-12-evidence.md#s4s-labelled-measurement) records its driver's setup, so this ticket's rerun can include review traffic. This adds no blocker to this ticket and changes none of its criteria.
