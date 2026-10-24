# 12: Accept only exact-subject review and failure results

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: 11

## What to build

Bind callbacks to task/assignment version, subject, evidence, authority and deadline; duplicate results are idempotent and stale results cannot approve new work.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Owner-provided private remote and Windows/Linux/macOS CI; live provider credentials only when a live test is attempted.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Typed review/failure requests and results bind exact assignment/version/subject, acceptance/check/config and evidence plus authorization revision and deadline.
- [ ] Duplicate callbacks are idempotent; stale or cross-context callbacks cannot approve different work or mutate current state.
- [ ] Chat, boards and narratives never replace authoritative task/gate state.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 29.
- Implementation decisions: 1, 2, 3, 4, 11.
- Testing decisions: 1, 2, 8, 17, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [89](89-report-waiting-ready-waiting-blocked-and-active-task-facts.md) adds C3's waiting-ready, waiting-blocked and active task-fact query over these task records, which the fortress stockpiles consume. These extensions add no blocker or criterion to this ticket.

2026-10-24 — Ticket 11, this ticket's only blocker, is resolved: C3's scoped grants, tasks and non-paid capacity are in `internal/core/agents` on `main` ([B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11)). Its `runtime.task_changed` records carry a submission, failure evidence or awaited artifact as a `SubjectRef` whose `kind` is an open token, which version 1 writes as `artifact`, so this ticket adds subject kinds by addition ([D140](../../../docs/DECISIONS.md#d140-f3-11-scoped-authority-task-and-non-paid-work-records-frozen)). A direct `complete` or `fail` is an explicit authorized decision that asserts no review, and ticket 11's [ruling](../c3-evidence/ticket-11-design-ruling.md), P7, has this ticket add its guards to the same transition. S2's review carried one change here: before the first producer of an unfamiliar kind, `SubjectRef.MarshalJSON` canonicalizes unfamiliar members with `json.Marshal(json.RawMessage(raw))`. The other start gates above stand.
