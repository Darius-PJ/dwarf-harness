# 03: Verify retained bodies and recover old command identities

Status: resolved
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

- [x] Publish hash-checked, byte-bounded bodies durably before references; explicit collection respects pins and preserves unavailable-body history.
- [x] An old immutable command identity resolves from authoritative segments after cache eviction; a different request under the same ID is refused.
- [x] Bound live projections and record actual recovery, memory, storage and append costs at declared workloads; request owner F0 freezes without claiming them.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 5, 6.
- Implementation decisions: 1, 2, 3, 4, 6.
- Testing decisions: 1, 2, 4, 17, 18, 20.

Canonical sections: Core roadmap C1; its cited controlling Core design and latest decision amendments.

## Answer

C1's second half is built, reviewed and on `main`, and C1 is Done. The store publishes bodies durably and verifies their size and hash on every read; a commit that references an unpublished body is refused; explicit collection respects pins and records what it deletes, so a collected body reads as unavailable rather than missing. A command identity is recorded in the same commit as its effect, an old identity still resolves from the log's segments after it leaves the bounded in-memory index, and a different submission under the same ID is refused. Opening a colony rebuilds every bounded projection and fails closed on a record that breaks an F0 rule. The assistant decisions are recorded in [B21](../../../docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities). The measurement run at 12, 20 and 60 contexts, against the workload the owner set at OG3, is recorded in [Core Appendix A.10](../../../docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03), with the raw results and the freeze packet under `.scratch/dwarf-harness/c1-evidence/`. C1 ended with the owner freeze request for `ledger-format`, `ledger-reader` and `ledger-appender` (OG2), sent on 2026-10-10 without claiming the freeze.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-09 — Claimed for the staged build now that ticket 02 is resolved. A designer drafts the implementation brief for the advisor's ruling; the coder then works on its own branch and worktree (D41). This ticket ends C1 with the F0 freeze request (OG2).

2026-10-09 — The build advisor approved the implementation brief with changes. In OG3 the owner set the larger workload at 60 contexts, doubled the load per agent and agreed to the cold runs, asking to be told when they were ready. Applied to the brief's assumed per-change script, every per-change count doubles: 82 commits (160 records, 16 commands) and 16 bodies totalling 616 KiB per change, so at 6 changes per context-hour each context writes 492 commits, 960 records, 96 commands and 96 bodies (3.6 MiB) per hour. Change rates and the 48/80 burst sizes are unchanged. The measurement run uses 12, 20 and 60 contexts with this doubled model and asks the owner for one machine restart before the cold-recovery runs.

2026-10-09 — Slices S1-S3 merged: `main` moved by fast-forward to f46be19 (S1 d0ed68c, S2 f2ac64f, S2 review fix-up 99093a7, S3 f46be19), after a rebase onto 657165f. The build advisor approved each slice on detached-worktree evidence. The pre-merge check passed on f46be19: all nine check steps, 20 passes of each of the six stress tests, the golden logs regenerated byte-identically, `go mod tidy` without change and `go mod verify`. No dependency was added and no owner decision was changed, so no D41 sign-off was needed. S4 (the workload harness), the measurement run, S5 (documents, B20) and the F0 freeze request (OG2) remain.

2026-10-09 — Slice S4 merged: `main` moved by fast-forward to bce3213, the workload measurement harness (test files only). The build advisor approved it on evidence from a detached worktree: all nine check steps, linux and darwin vet, the smoke run of every scenario group at 12, 20 and 60 contexts (35 result lines), and the harness skipped in ordinary and race runs. The model runs the per-change script twice, following the owner's OG3 answer to double the traffic. The measurement run (M) follows, with one machine restart before the cold runs, as the owner allowed.

2026-10-09 — Measurement run (M), step 1: the harness at bce3213 generated the seven measurement colonies (12, 20 and 60 contexts, one day with bodies and seven days of log, plus 20 contexts over 30 days; 46.4 GiB in 2,445 s) outside the repository, with the per-change script run twice (OG3). Machine: NTFS, go1.27.2. The owner allowed the restart for the cold runs. The results are recorded in Core Appendix A.10 when M ends.

2026-10-10 — Measurement run (M) complete: after the owner's restart (boot at 22:44 local time on 2026-10-09), the cold recovery of every colony ran first, then the warm recovery and the append, body, crash, lookup and collection groups. Every group passed, with no store-invariant failure and no failed call. The colonies were deleted afterwards to free the disk; the raw results are kept.

2026-10-10 — S5a merged: `main` moved by fast-forward to 1785a3b after all nine check steps passed on it. It adds Core Appendix A.10 and its §8.6 row, the raw results file (35 lines, SHA-256 4ddb3b4e7f1d8cdbe2d7245e73d4dbf32eea22e5da4f8f1c717cdced89bb18ef) and the OG2 packet, whose V1–V8 results, `go doc` output and golden digests were taken at c8b1d93.

2026-10-10 — OG2 sent at 02:48 local time as one question-tool request, pointing to the packet at `.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md`. The owner answered by freezing all three contracts, asking for a Markdown note at the repository root on the user needs of the three parts, and holding further work. C1 ends with the request, so this ticket records it as sent and not granted; the freeze and the requested document follow in their own commits.

2026-10-10 — The build advisor approved S5 on its final review: Core Appendix A.10 and every number in it, recomputed from the raw results; the OG2 packet; B21; every status line; and this closing. Each code slice of this ticket (S1, S2 with its review fix-up, S3 and S4) was approved earlier on evidence collected in detached worktrees.

2026-10-10 — The owner's freeze is recorded: `main` moved by fast-forward to 9101178 after all nine check steps passed on it. Commit 2773034 appends [D111](../../../docs/DECISIONS.md#d111-f0-ledger-contracts-frozen-at-version-1), which freezes `ledger-format`, `ledger-reader` and `ledger-appender` at version 1 at c8b1d93, and updates the register and every status line. Commit 9101178 adds `LEDGER-USER-NEEDS.md` at the repository root, as he asked in the same answer. The build advisor approved both. Work now holds for his continuation, as he asked.

2026-10-12 — A load-sensitive test, recorded here as the OG5 packet's R8 row says: `TestBodiesConcurrentUse` in `internal/core/ledgerstore` asserts that 200 references are acknowledged within one minute (`bodies_test.go:741`). A full check of ticket 05's S2 fix-up, d4e3032, run while three other heavy jobs used the machine, failed only its race step, on this test: "138 references acknowledged within a minute, want 200". Alone under the race detector the test passed three of three runs at about 14 s each, and the full check then passed all nine steps on the same commit. Neither the test nor this ticket's code changed. Full checks now run in a quiet window, with no other Go command running; whether to relax the bound is a later question.
