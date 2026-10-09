# 08: Reconcile finalization and cancellation without replaying effects

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C2
Blocked by: 06

## What to build

Implement ordered intent/ref/durable-result mechanics, conditional crash reconciliation and cancellation cutoff; product promotion remains unavailable until the real validator exists.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Serialize durable intent, guarded Git mutation and its durability, accepted ledger result, then slot release; ref movement alone is not accepted work.
- [ ] Only submitter/owner request cancellation wins before intent; turn completion/cancellation or view closure is not promotion cancellation.
- [ ] Under exact evidence and fenced-writer prerequisites, old tip interrupts, intended tip completes missing bookkeeping, uncertainty blocks; never retry a Git effect blindly.
- [ ] No-change compares only the actual accepted predecessor, preserves independent IDs and creates no acceptance/field effect.
- [ ] C2 product remains unavailable without C4; mechanical primitive proofs in disposable repositories are not accepted promotions.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 14, 15, 16, 17, 18.
- Implementation decisions: 1, 2, 3, 4, 8.
- Testing decisions: 1, 2, 9, 20.

Canonical sections: Core roadmap C2; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
