# 18: Validate Python candidates with reconciled test evidence

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4
Blocked by: 17

## What to build

Run real pinned pytest/JUnit with trusted collection and terminal reconciliation through the same required-check gate; reject empty, partial or forged-success outcomes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Real pytest/JUnit execution uses trusted collection, run binding and full terminal reconciliation under the same exact-subject gate.
- [ ] Collection/usage/internal errors, empty or skip-only suites, truncation, mismatched exits and wrong canaries cannot pass.
- [ ] Pinned adversarial/control fixtures work through the real adapter; no new authority or alternate runner.
- [ ] Provide runnable pinned per-profile finite V6 fixtures through the real runner; qualification/pass remains a separate real gate, reusable by C10 without waiting for whole C4.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 34, 35.
- Implementation decisions: 1, 2, 3, 4, 13.
- Testing decisions: 1, 2, 9, 17, 20.

Canonical sections: Core roadmap C4; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
