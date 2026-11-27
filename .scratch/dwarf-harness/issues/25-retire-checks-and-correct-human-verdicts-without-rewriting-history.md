# 25: Retire checks and correct human verdicts without rewriting history

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C5
Blocked by: 20

## What to build

Support owner-only retirement and exact scoped append-only verdict corrections; repairer-context/clone exclusion and quarantine replay are ticket [24](24-govern-pinned-checks-confirmed-repair-and-quarantine.md)'s, and Corps retirement waits for its real C6 gate.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until Release 1's exit test passes (D182); any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Before real C6 authority integration only owner retirement acts.
- [ ] Human correction appends against exact prior verdict/subject/criterion/scope/revision and never rewrites history.
- [ ] Retirement and correction replay identically; human verdicts never waive required C4 checks.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 40, 41.
- Implementation decisions: 1, 2, 3, 4, 14.
- Testing decisions: 1, 2, 8, 9, 20.

Canonical sections: Core roadmap C5; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-27 — Release tier rework (D182, D186): Release 2, owner-only retirement and verdict corrections. Its repairer exclusion and its replay of quarantine and release move to ticket [24](24-govern-pinned-checks-confirmed-repair-and-quarantine.md), On trigger, which builds the repair and quarantine they act on. Ticket 24 also holds the check registry's replay of digests, lineage and state, so this ticket builds the part of the registry that retirement needs: the check identity and its retired state. The transparency criterion is added. Its blockers were 24; they are now 20, because owner retirement and verdict corrections act on the checks and verdicts of ticket [20](20-publish-only-the-validated-cumulative-prefix.md)'s landing path, not on 24's repair or quarantine. This grants no implementation, contract-freeze or release approval.
