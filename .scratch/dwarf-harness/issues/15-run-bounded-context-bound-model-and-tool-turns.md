# 15: Run bounded context-bound model and tool turns

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: 14, 140

## What to build

Run real static-provider turns through the Core command/event boundary, fair context scheduling, scoped tools and correctly attributed terminal/live output without extra administrative model calls.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Owner-provided private remote and Windows/Linux/macOS CI; live provider credentials only when a live test is attempted.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Actual static Anthropic/OpenAI streaming tool loop uses hand-written adapters, selected context transcript/artifacts and the Core command/event boundary.
- [ ] One active turn/context and provisional three/agent are enforced by bounded fair scheduling; busy creates no hidden queued start.
- [ ] Active-context note names only counterpart/task titles; output attribution follows bound IDs, not terminal selection; live text differs from durable final message.
- [ ] Every tool/model operation receives current authority/admission and durable operation/result records; promotion remains unavailable before C4.
- [ ] Models see a small, stable tool surface: common scoped file tools and typed Core operations with stable names and versioned argument and result schemas, not one tool per internal method or a catch-all Bash/Eval gateway. Infrequent operations use the authorized local catalogue/meta-tool pair of routing §8.3; discovery is bounded and filtered by the bound context's current authority, is not a grant, and invocation resolves the concrete operation and target and rechecks permission.
- [ ] The model-tool entry point accepts only schema-declared, unambiguous equivalent argument forms and rejects duplicate keys, conflicting alternatives, unknown fields and undeclared coercions before dispatch. The normalized request binds to the existing command digest and receipt, so one command ID cannot change meaning through another spelling or schema revision; a malformed request returns a bounded local correction naming the field and allowed form, with no repair-model call and no disclosure of inaccessible targets. Law, ledger and launcher formats stay strict.
- [ ] Tool results return as typed facts to the existing Core scheduler/runtime, the single owner that decides whether the bound turn continues, waits, yields or finishes. Each dispatch boundary applies cancellation, shutdown or lost authority first, then an exhausted deadline or loop bound, then unresolved required results, and otherwise fair rescheduling with fresh authority and resource admission; a requested finish cannot skip pending required work.
- [ ] Tool-surface measurements record schema bytes and tokens, discovery round trips, invalid-argument corrections, latency and successful task completion under the declared workload.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 23, 24, 26, 27, 31.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 12.
- Testing decisions: 1, 2, 8, 11, 18, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover the small stable tool surface, model-only argument normalization and the single continuation owner. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-24 — [D142](../../../docs/DECISIONS.md#d142-tickets-139-and-140-published-with-three-blocker-changes) rework: the blockers were 14 and are now 14, 140. Ticket [140](140-bound-the-private-state-services-replay-and-lookup-cost.md) bounds the cost of opening the private-state service and of its turn start, local admission and local dispatch, which this ticket makes production paths; ticket 11's S4 review asked for that fix to land first ([B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11), Limits). The number and criteria are unchanged; this grants no separate implementation, contract-freeze or release approval.
