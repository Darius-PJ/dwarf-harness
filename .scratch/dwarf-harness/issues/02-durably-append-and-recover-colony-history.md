# 02: Durably append and recover colony history

Status: claimed
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C1
Blocked by: 01

## What to build

Commit ordered typed records through one locked writer; recover torn tails, reject corruption and replay without executing effects.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.

## Acceptance criteria

- [ ] One writer lock and sequencer assign gapless sequences; acknowledge only a complete durably flushed commit.
- [ ] Short/uncertain writes poison the incarnation until recovery; torn final commits recover, earlier corruption/gaps fail closed.
- [ ] Golden F0 format/reader/appender boundaries retain absent-field and consumed-version semantics; replay issues no effects.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 3, 4.
- Implementation decisions: 1, 2, 3, 4, 6.
- Testing decisions: 1, 2, 4, 17, 18, 20.

Canonical sections: Core roadmap C1; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-09 — Claimed for wave 2 of the staged build. A designer drafts the implementation brief for the advisor's ruling; the coder then works on its own branch and worktree (D41).

2026-10-09 — Slice S1 (`contract/ledger`: the draft ledger-format and ledger-reader) and its status changes reached `main` at commit 458a2d8, after the build advisor approved them and the full check passed on that exact commit. S1 adds no dependency, so it needed no owner sign-off (D41). Slices S2 and S3 stay on branch `build/02-ledger` until the owner signs off `golang.org/x/sys` (OG1).
