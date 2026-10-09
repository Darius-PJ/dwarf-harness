# 24: Govern pinned checks, confirmed repair and quarantine

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C5
Blocked by: 22, 23

## What to build

Keep check identities/lineages and failures replayable; demand reproduced repair and fresh evidence, and block required quarantined checks until authorized qualifying release or replacement.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Replay pinned check digests, lineage, open failure and active/quarantined/retired state separately from required status.
- [ ] Repair requires same-condition fail-before/pass-after and law-required fresh confirmations/maintained checks, not promotion alone.
- [ ] Required quarantine means unavailable until explicit qualifying owner release or governed replacement; never timeout, skip or success fallback.
- [ ] Agent-authored reproduction is evidence until separately governed adoption; bounded attribution uses actual runner evidence.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 38, 39.
- Implementation decisions: 1, 2, 3, 4, 14.
- Testing decisions: 1, 2, 9, 12, 20.

Canonical sections: Core roadmap C5; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
