# 05: Create private scratch and capture exact eligible trees

Status: claimed
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C2
Blocked by: 03

## What to build

Register isolated scratch, pin normalization and exclusion inputs, and capture host-observed immutable content or immediately refuse busy/unsupported work without a queued snapshot.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Canonical main is never checked out; scratch belongs to one bound context/workspace and cleanup cannot discard unrepresented edits.
- [ ] Capture full Git-canonical eligible contents against the trusted baseline: tracked edits/deletions stay included, new files use captured scratch ignores and pinned reviewed host exclusions.
- [ ] Reserve before capture; contention returns immediate busy without snapshot/wait/retry/writer interruption; missing validator is unavailable.
- [ ] Refuse unsupported conversions/filters, special files, collisions, lossy normalization and cap exhaustion; later scratch/index/config edits cannot alter captured bytes.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 7, 8, 9, 11.
- Implementation decisions: 1, 2, 3, 4, 7.
- Testing decisions: 1, 2, 9, 18, 20.

Canonical sections: Core roadmap C2; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-12 — Claimed for the staged build by the project manager, on the owner's instruction of 2026-10-12 to take the next tickets once this was adjusted, each pausing for his approval before work moved on. The owner answered the D52 approval request the same day; the decision entry recording it lands in its own commit. A runner maps the requirements and a designer drafts the brief. C2's F2 event families are proposed and frozen by the owner before C2 producer implementation, and no code starts before the owner approves the design.

2026-10-12 — The owner's D52 approval is recorded. The approval request (question tool) pointed to the packet at `.scratch/dwarf-harness/c2-evidence/d52-c2-approval-packet.md`, and he approved it. [D112](../../../docs/DECISIONS.md#d112-c2-promotion-boundary-approved-d52) approves the C2 promotion boundary, Core roadmap C2 and Core design §§13.2–13.3 at 9bbfd8a, with only the owner reviewing and approving a workspace's pinned exclusions. C2 is Ready, and this ticket's D52 start gate is met. C2's F2 event families must still be frozen by the owner under D40 before C2's producer implementation.

2026-10-12 — The owner answered both design gates. At OG-A, the approval of the design before any code, he approved it as ruled; at OG-B, how C2's F2 event families are frozen, he chose freezing in parts. F2 is frozen in parts, ticket by ticket, each part before the code that writes it; this ticket's record-free slice S1 may reach `main` after review and before any F2 freeze. Core design line 844 and `ROADMAP-CORE.md` line 220 are clarified to "before C2 producer implementation" in a separate documents commit with a decision entry. The brief and the advisor's ruling are at `.scratch/dwarf-harness/c2-evidence/` (this commit); the ruling wins where they differ. S1 is built on branch `build/05-workspace`. Ticket 05's four `workspace.*` record types reach `main` only with the owner's F2-05 freeze, a later question.

2026-10-12 — OG5, the freeze request for F2-05, was sent as one question-tool request pointing to the packet at `.scratch/dwarf-harness/c2-evidence/f2-05-freeze-packet.md`. It asks the owner to freeze this ticket's four `workspace.*` record types at version 1, by addition to `ledger-format`, at commit d4e3032 on branch `build/05-f2`: S2 (31103d8) and the fix-up that the build advisor's review asked for. The packet's results, `go doc` excerpt and digests were taken at d4e3032; the golden's SHA-256 is 843663a168e472adf4656fdba4ffa6db770e588feec0020bb10e20bf14def437. S2 stays off `main`, and S3 waits, until the owner answers and his decision entry is recorded (D113).

2026-10-12 — In OG5 the owner approved the freeze as presented. [D115](../../../docs/DECISIONS.md#d115-f2-05-workspace-and-scratch-records-frozen) freezes F2-05, this ticket's four `workspace.*` record types, by addition to `ledger-format` version 1, at d4e3032. S2 reached `main` as bce48a0, whose `contract/ledger` is byte-identical to d4e3032's, with the documents commit that records the freeze and drops the "proposed" wording. Two lines inside D112's approved range change: `ROADMAP-CORE.md` C2's proposed scratch lifecycle events record the adoption, and C2's status line records the freeze; neither changes a mechanism. S3, the workspace service that writes these records, may start.
