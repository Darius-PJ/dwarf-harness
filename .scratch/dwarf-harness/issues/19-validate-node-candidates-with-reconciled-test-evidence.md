# 19: Validate Node candidates with reconciled test evidence

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4
Blocked by: 17

## What to build

Run real pinned TestsStream/JUnit-wrapper checks through the same gate, reconciling inventory, terminal results, canary and exit status.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until Release 1's exit test passes (D182); any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Real pinned TestsStream or JUnit/wrapper execution reconciles inventory, outcomes, exit and same-process canary.
- [ ] Partial/empty/skip-only output, unbound reports and interruption cannot pass.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 34, 35.
- Implementation decisions: 1, 2, 3, 4, 13.
- Testing decisions: 1, 2, 9, 17, 20.

Canonical sections: Core roadmap C4; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-27 — Release tier rework (D182, D184, D186): Release 2. It would have joined Release 1 only if the first repository were Node, and the first is Python (D184). Slimmed like ticket [18](18-validate-python-candidates-with-reconciled-test-evidence.md): criteria 3 and 4 move with ticket 17's fixtures to tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md) and [46](46-qualify-the-real-windows-runner-profile-for-a2.md), both On trigger. The transparency criterion is added. This grants no implementation, contract-freeze or release approval.
