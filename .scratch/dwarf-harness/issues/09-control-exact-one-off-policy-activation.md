# 09: Control exact one-off policy activation

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C2
Blocked by: 08

## What to build

Retain owner-approved versions; revision-check immediate/scheduled activation, replacement and cancellation; unresolved finalization or an untrusted clock holds the colony-wide transition.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Bind owner authority to retained exact hashes and one pending immediate/not-before request; same-ID mismatch, stale CAS replacement/cancellation and past/ambiguous timestamp are refused.
- [ ] Hold new intents across affected workspaces, resolve old intents, then switch one colony-wide revision; unresolved finalization and clock uncertainty cannot be bypassed.
- [ ] Reconstruct pending/overdue controls without replaying execution or backdating effective time; cancellation removes only its own hold.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 19, 20, 21.
- Implementation decisions: 1, 2, 3, 4, 9.
- Testing decisions: 1, 2, 10, 20.

Canonical sections: Core roadmap C2; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
