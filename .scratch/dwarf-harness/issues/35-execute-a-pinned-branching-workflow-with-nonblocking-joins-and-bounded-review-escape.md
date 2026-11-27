# 35: Execute a pinned branching workflow with nonblocking joins and bounded review escape

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C9 full finite workflow core-and-terminal capability
Blocked by: 34, 153

## What to build

Persist pinned branches/receipts; release waiting joins and enforce live-authorized edges and capped review loops.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen exact WorkflowSpec/WorkflowRun and completion/acceptance families.
- C9 backend capability (tickets [35](35-execute-a-pinned-branching-workflow-with-nonblocking-joins-and-bounded-review-escape.md), [36](36-load-and-update-shared-format-skills-lazily-through-core.md)) export additionally requires ticket [36](36-load-and-update-shared-format-skills-lazily-through-core.md); no GUI or whole-C9 prerequisite.

## Acceptance criteria

- [ ] The terminal selects, inspects and executes the exact pinned spec/recipe hashes using the closed inbox, dock, round-robin splitter, finite-tag filter, required-branch joiner, bounded-review-loop and outbox variants.
- [ ] Compile and execution both check every real agent edge against current role/grant state through the broker. Revoked edges, stale specs and guessed recipients fail without hidden Diplomat forwarding or capability escalation.
- [ ] Finite hops, in-flight branches, deadline, per-message/day money and output limits are enforced. A review loop has the selected positive default cap of three and a mandatory escape; an unbounded cycle rejects.
- [ ] A waiting join releases its worker/context and resumes only from its required typed branch results. Missing, stale, duplicate or wrong-branch receipts cannot complete the join or occupy execution indefinitely.
- [ ] Node completion and the next typed Core intent commit atomically with durable branch/node keys, loop counters and receipts. Crash/replay does not repeat uncertain external effects or let duplicate completion issue another action.
- [ ] Real terminal runs demonstrate required-branch failure, capped-loop escape, busy/unavailable acceptance and inaccessible attachments with explicit authoritative state, while preserving budget holds and bounded output. This is backend completion, not GUI parity or whole C9.
- [ ] The workflow backend runs inside the single Core continuation owner rather than its own scheduler; workflow counters, waits and pending branch state change only through compulsory typed Core transitions and survive restart without automatically resuming execution.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 58, 59, 60.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 12, 20.
- Testing decisions: 1, 2, 8, 9, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C9
- Core design §§11.1–11.2,13.8

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover workflow participation in the single continuation owner and compulsory typed transitions. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-11-27 — Release tier rework (D182, D186): Release 3, teams layer. Its compile and execution check every agent edge against current role and grant state through the broker. It reached C6 only through ticket [34](34-run-a-bounded-typed-recipe-from-the-terminal-and-evaluate-its-real-acceptance.md), which now waits on ticket [20](20-publish-only-the-validated-cumulative-prefix.md) instead of tickets 28 and 29. Its blockers were 34; they are now 34 and 153, because that check needs ticket [153](153-enforce-the-corps-grand-and-diplomat-role-matrix-without-context-leakage.md)'s role matrix, which keeps a C6 ancestor after ticket 34's rewire. This grants no implementation, contract-freeze or release approval.
