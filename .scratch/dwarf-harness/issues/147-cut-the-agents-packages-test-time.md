# 147: Cut the agents package's test time

Status: ready-for-agent
Type: task
Published: 2026-11-21
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 integration-check cost for `internal/core/agents` (D127, D176)
Blocked by: None (can start immediately)

## What to build

Cut the test time of Core's agent package, `internal/core/agents`, so that its race-detector run fits well under D127's 30-minute per-package limit on all three hosted systems, with room for later growth. Do this without losing or weakening a deliberate-bug (rule-break) or frozen-format (golden) test, changing D127's limits, or skipping a test or taking it out of the hosted check.

The package's race step has grown as C3 tickets landed. On the hosted Windows runner it took 1,478.731 s in run 44471432711 and 1,660.945 s in run 44595543703, both on 2026-11-19; the second passed D127's 25-minute report line. On run 44866294125, ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s S2 at 88f4bb6, the race step stopped at the limit (`panic: test timed out after 30m0s`, `FAIL internal/core/agents 1800.653s`). No test had failed, and existing turn-loop tests were still running. The package's tests step, without the race detector, took 1,312.323 s in that run. Run 44874224657, S2's first fix at 8c2c1e2, timed out on Windows the same way. In the same run on macOS, `TestTurnLoopConcurrentPrivateRuntime/openai/shared_memory_cas` failed after 170.24 s with “wrong applied/stale/POST counts”, and the package's race step took 1,512.960 s. Linux passed both runs. By the owner's decision the package's test time is cut rather than the limit raised, and D127's 30-minute limit and 25-minute report line stay (D176).

To build:

1. Measure where the package's test time goes: per-test times in the tests and race steps, setup costs, waits and effective parallelism.
2. From those measurements, a ruled design chooses the changes and their targets before code (D117).
3. Implement the changes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Builds on `internal/core/agents` as it is on `main`, plus ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s S2 commits on `build/91-s2`, whose tests count toward the time target.
- A ruled design chooses the changes and targets before code (D117).
- Adds no record type and changes no frozen encoding or golden.
- Any production change keeps consumer-visible behaviour.
- The hosted runner is not reconfigured to absorb the cost (D171).
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); a red check is neither rerun nor skipped.
- Ticket 91's remaining slices land after this ticket, with its S2 rebased onto this ticket's result (D176).

## Acceptance criteria

- [ ] The package's race step takes at most 20 minutes on the hosted windows-amd64 runner, measured with ticket 91's S2 tests included: on the hosted check of ticket 91's S2 rebased onto this ticket's result, read from that job's log. Once measured, the ruled design may set stricter targets, including for macOS, Linux and the tests step.
- [ ] Every rule-break test and frozen-format (golden) test still catches its bug: no such test is deleted, and each row the change edits keeps its guarded mutant red.
- [ ] `TestTurnLoopConcurrentPrivateRuntime/openai/shared_memory_cas` passes reliably under race on slow runners without weakening what it proves.
- [ ] D127's limits are unchanged; no test is skipped, moved behind a flag, or taken out of the hosted check.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 2.
- Implementation decisions: 1, 4, 5.
- Testing decisions: 1, 2, 3.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-11-21 — Published at the project manager's proposal. After the agents package's race step timed out at the 30-minute limit on the hosted Windows runner, the owner chose to cut the package's test time rather than raise the limit, and approved this ticket (D176). Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.

2026-11-21 — Consolidation cross-link: Ticket [148](148-stop-git-process-recovery-refusing-a-reused-process-id.md) fixes a Git-process recovery behaviour found during this ticket's work: after a restart, recovery refuses a recorded Git root whose process ID now belongs to another process that the current user cannot open. Its fix is in `internal/core/gitprocess`, outside this ticket's packages, so the two may run in parallel. These extensions add no blocker or criterion to this ticket.
