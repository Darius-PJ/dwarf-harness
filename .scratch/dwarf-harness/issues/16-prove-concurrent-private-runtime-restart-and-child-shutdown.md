# 16: Prove concurrent private-runtime restart and child shutdown

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: 12, 15

## What to build

Exercise concurrent writers, temporary merge-back, passive results, crash interruption and positive child shutdown on test workloads; complete required cross-OS CI and runtime capacity evidence.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Owner-provided private remote and Windows/Linux/macOS CI; live provider credentials only when a live test is attempted.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Real concurrent test turns exercise expected-revision memory writes, nonconflicting/conflicting temporary scratch merge-back and independent private continuations.
- [ ] Crash mid-turn records interruption without repeating tool effects; normal/abnormal Windows shutdown positively proves descendant exit before reuse.
- [ ] Passive results return to original context on its natural turn with no completion-only model call.
- [ ] Complete required Windows/Linux/macOS CI and declared runtime resource profiles; private remote, credentials and approvals remain explicit prerequisites.
- [ ] The continuation owner persists each bound turn's originating context, operation/generation, expected state, pending result identities, wait reason and applicable counters and deadline through Core transitions, separately from provider continuation artifacts; restart reconstructs waits and interrupted outcomes without automatically resuming execution.
- [ ] Cancellation/result races resolve against current durable state: duplicate or stale callbacks cannot revive terminal work, already-intended effects are reconciled rather than reported as prevented, waiting never releases unresolved financial holds or reuses unconfirmed child resources, and no context ever runs two live turns.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 18, 23, 24, 25, 26, 63.
- Implementation decisions: 1, 2, 3, 4, 8, 10, 11, 12.
- Testing decisions: 1, 2, 8, 9, 14, 16, 18, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [90](90-hold-first-actions-and-ordinary-work-behind-generic-core-eligibility-guards.md) adds C3's generic first-action and ordinary-work eligibility guards on this runtime, and ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) adds the ordinary officer recovery procedure once ticket [87](87-specify-the-ordinary-officer-connection-loss-and-service-restoration-policy.md) specifies it. These extensions add no blocker or criterion to this ticket.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover persisted continuation-owner state and race precedence across restart. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-08 — Consolidation cross-link: Ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) now records service loss for every agent, including execution failures from this ticket's restart and child-shutdown proofs, and is blocked by this ticket under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing). These extensions add no blocker or criterion to this ticket.

2026-10-24 — Consolidation cross-link: Ticket [140](140-bound-the-private-state-services-replay-and-lookup-cost.md) bounds the cost of opening the private-state service after a restart and of its turn start and local admission, which this ticket's restart proof and declared runtime resource profiles exercise. It reaches this ticket through ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md), which it now blocks ([D142](../../../docs/DECISIONS.md#d142-tickets-139-and-140-published-with-three-blocker-changes)). These extensions add no blocker or criterion to this ticket.
