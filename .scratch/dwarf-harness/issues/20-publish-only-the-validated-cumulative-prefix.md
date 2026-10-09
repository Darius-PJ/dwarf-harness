# 20: Publish only the validated cumulative prefix

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4
Blocked by: 17

## What to build

Overlap real cumulative validations, keep foreign outputs sealed, rebuild invalidated generations, and publish exact predecessor-relative deltas in durable order with honest cancellation/no-change/recovery.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Actually overlap cumulative real validations within declared P/window limits and publish only successful request-order prefix.
- [ ] Bind all evidence to exact generation/predecessor and seal every foreign diagnostic/artifact path until current authorization and exact accepted-predecessor witness permit disclosure.
- [ ] Local failure suffix vs foreign rebuild and speculative redundancy vs actual no-change remain distinct; every rebuilt non-no-op requires fresh full checks.
- [ ] Observe real Git and durable acceptance at each interruption/cancellation boundary; preserve precise incremental attribution and later scratch edits.
- [ ] Results stay original-context/passive; malformed/unavailable/nonpassing gates never advance canonical.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 8, 9, 10, 12, 13, 14, 15, 16, 17, 18, 36, 37.
- Implementation decisions: 1, 2, 3, 4, 7, 8, 13.
- Testing decisions: 1, 2, 9, 17, 18, 20.

Canonical sections: Core roadmap C4; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
