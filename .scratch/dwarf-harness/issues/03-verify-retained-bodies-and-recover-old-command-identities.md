# 03: Verify retained bodies and recover old command identities

Status: claimed
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C1
Blocked by: 02

## What to build

Publish hash-verified bounded bodies, preserve replay facts through explicit collection, resolve evicted IDs from history, and measure replay/storage before requesting F0 freezes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.

## Acceptance criteria

- [ ] Publish hash-checked, byte-bounded bodies durably before references; explicit collection respects pins and preserves unavailable-body history.
- [ ] An old immutable command identity resolves from authoritative segments after cache eviction; a different request under the same ID is refused.
- [ ] Bound live projections and record actual recovery, memory, storage and append costs at declared workloads; request owner F0 freezes without claiming them.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 5, 6.
- Implementation decisions: 1, 2, 3, 4, 6.
- Testing decisions: 1, 2, 4, 17, 18, 20.

Canonical sections: Core roadmap C1; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-09 — Claimed for the staged build now that ticket 02 is resolved. A designer drafts the implementation brief for the advisor's ruling; the coder then works on its own branch and worktree (D41). This ticket ends C1 with the F0 freeze request (OG2).

2026-10-09 — The build advisor approved the implementation brief with changes. In OG3 the owner set the larger workload at 60 contexts, doubled the load per agent and agreed to the cold runs, asking to be told when they were ready. Applied to the brief's assumed per-change script, every per-change count doubles: 82 commits (160 records, 16 commands) and 16 bodies totalling 616 KiB per change, so at 6 changes per context-hour each context writes 492 commits, 960 records, 96 commands and 96 bodies (3.6 MiB) per hour. Change rates and the 48/80 burst sizes are unchanged. The measurement run uses 12, 20 and 60 contexts with this doubled model and asks the owner for one machine restart before the cold-recovery runs.
