# 45: Run and positively close a restricted Windows process tree

Status: resolved
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 independently usable Windows containment executor
Blocked by: 07, 09

## What to build

Confine real Windows descendants with AppContainer/Job controls and prove positive closure.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Relevant owner-frozen OS/child/input/output/receipt interfaces within existing C1/B11 boundaries.
- Real supported Windows machine with Defender enabled and required AppContainer/Job Object facilities.
- Can begin with real test processes after C2; claims neither C3 integration nor A2 until later qualification.

## Acceptance criteria

- [x] A real permitted test process receives only authorized run-specific input and returns bounded output to Core; per-run AppContainer/SID access restriction and a non-breakaway Job Object are both active.
- [x] Launch is suspended with an explicit inherited-handle allowlist, restrictions apply before resume, and network is denied by default. A child cannot reach canonical/common Git, ledger/bodies, private peers, credentials, owner IPC or sealed results.
- [x] Actual spawned descendants and escape attempts remain confined; paths/links and inherited handles cannot expand the allowed roots.
- [x] Cancellation, timeout and supervisor loss establish positive process-tree exit before slot/root reuse. An ambiguous stop quarantines resources and cannot publish or restart automatically.
- [x] Required finite execution/input/output limits fail closed when unenforceable. Record actual start/stop/resource behavior under Defender; a Job Object, successful kill request or worktree alone is never reported as the security boundary.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 62, 63.
- Implementation decisions: 1, 2, 3, 4, 21.
- Testing decisions: 1, 2, 16, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C10
- Core design §13.9

## Answer

Ticket 45 is built, reviewed and on `main`. C10's Windows executor, `internal/core/winexec`, runs a real test process with only its authorized, copied input in a fresh per-run AppContainer with zero capabilities. The process is created suspended inside a non-breakaway Job, inherits exactly its three standard handles, and resumes only after its token, its Job membership and every limit have been read back. Its network is denied. Its bounded output returns to the caller only once the whole process tree is proved gone: the Job reports no active process. Cancellation, timeout, Close and the loss of the supervisor end in that proof or in a durable quarantine that nothing reuses or restarts, and an ambiguous result is unpublishable. Every finite limit maps to one native control, and a limit that cannot be enforced refuses the run. The package is Core-internal: it writes no ledger record and is no contract, and C3's runtime and C4's adapters will call it as the tests do now. [B30](../../../docs/DECISIONS.md#b30-c10-the-windows-containment-executor-ticket-45) records the reconciliation of the ticket's start gate, the qualified choices (the regular AppContainer, since a Go program's file I/O fails under LPAC with zero capabilities on the development machine's Windows version), the project manager's answers during the build and the executor's limits. All of this is verified on one Windows machine. The Linux and macOS executors are tickets 47 and 49, and the A2 qualification is ticket 46.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-17 — Claimed for design by the project manager after ticket 09 closed C2 (B28, ee798a5): the frontier was tickets 10 (C3; waits for the owner's private remote with Windows, Linux and macOS CI), 45, 47 and 49 (C10), 61 (U3) and 137 (art). Tickets 61 and 45 are designed first because this Windows machine can build them; 47 and 49 need Linux and macOS hosts.

2026-10-17 — The designer's gate analysis found the ticket blocked, reading `ROADMAP-CORE.md`'s "C10 waits for C3" as a wait for a frozen C3 child interface. The build advisor's gate ruling, carried as §0 of the [ruling](../c10-evidence/ticket-45-design-ruling.md), reversed that verdict for all five criteria. This ticket's own gate is "after C2", as the capability graph and the status board also say. Nothing it consumes is unfrozen, and the interfaces the gate names are C10's own Core-internal code, not contracts. "C10 waits for C3" is the step's integration and A2-qualification wait, carried by tickets 16, 46 and 91. No owner question was asked. [B30](../../../docs/DECISIONS.md#b30-c10-the-windows-containment-executor-ticket-45) records the reconciliation, with an "Amended by" line under B28 and a clarification of C10's Waits-for line and its row in `ROADMAP-CORE.md` §3.

2026-10-17 — The build advisor approved the [design brief](../c10-evidence/ticket-45-design-brief.md) with P7 and P14 amended and eight findings: one major, five minor and two nits. The ruling wins where they differ. Under D117 it ended the design step, with no owner gate. LPAC, the brief's stricter profile, was approved as the first profile to qualify, with one switch to the regular AppContainer if a Go program could not start under it. S1 began with a checkpoint on the native qualification. S2 and the close waited for the coder's weekly budget, due to reset on 2026-10-22.

2026-10-17 — S1's checkpoint and the ruling's three revisions. `CreateProcess` first failed with error 203. The [probes](../c10-evidence/ticket-45-checkpoint-probes.md) traced it to the child's environment, since no AppContainer launches without `LOCALAPPDATA`, and showed that token class 46 cannot be queried on the development machine's Windows version. §6 kept LPAC, set the environment block and dropped class 46. Go's file I/O then failed under LPAC: [further probes](../c10-evidence/ticket-45-checkpoint-probes-2.md) showed `WSAStartup` failing with 10107 under zero capabilities, and §7 applied P14's switch to the regular AppContainer. `TestProfileGrantBoundary` pins that profile's `S-1-15-2-1` boundary. Under the race detector a Go grandchild died at ThreadSanitizer start. After two probes, §8 gave the rows that need only a held descendant a System32 `cmd.exe` grandchild, in both builds.

2026-10-17 — S1, the complete module, reached `main` as 01cbada: 1ed46a5 rebased onto 88ca88f, with all 19 files unchanged. The project manager's answers during S1: an observer test's invalid-handle error came from the test's own handle lifetime, so the observer now duplicates the Job; x/sys's race wrapper for `ReadFile` needs a non-nil count cell, which the executor keeps; and after a crashed race run the coder removed its leftovers, seven test profiles and eight temporary roots, by their exact names. The fresh reviewer the project manager started was stopped by Anthropic's cyber-content filter after 6 s, on the defensive sandbox material, and the build advisor who had ruled the design reviewed S1 instead. He approved it with one major finding, carried to S2 with its regression test: an already exited run was relabelled timed out, cancelled or shut down when the wall, the caller's deadline or Close arrived during its drain or cleanup. He also made two minor findings and two nits. The Defender sentence went into S1's evidence, and the duplicate-path admission and the pipe grant went to S2. The check without its race step (D129) passed on 01cbada in 1,070.5 s, its tests step slowed by Go's test cache, which D131 then removed, and `main` moved from 88ca88f to it.

2026-10-18 — S2 reached `main` as 77b9968, with 6e168e6 above it, and the documents commit that records the ticket follows. S2 is the adversarial matrix, the crash matrix and the measurements. It started on 2026-10-18, when the coder's budget reset early. S2 fixes S1's major finding first, with `TestNaturalExitKeepsStopGrace`, refuses duplicate and case-colliding input paths, and drops the run SID's grant on the pipes. During its final runs a Job readback was once refused before resume. Native buffers that crossed x/sys's `uintptr` wrappers were on the stack, and the project manager approved pinning them, as the compiler's escape analysis shows. The build advisor approved S2 at 77b9968 on 2026-10-18 with no product findings and three nits, of which one is applied in the evidence. His one minor finding, a test observer's unpinned Job queries, is fixed in 6e168e6. `FuzzFenceDecode` ran 4,129,250 executions in 120 s on 6e168e6 without a failure, and the full check, with its race step (D129), passed on 6e168e6, the commit `main` moved to by fast-forward (`check PASSED: all 9 steps passed in 749.2s`). This ticket is resolved: S1 and S2 are on `main` (the ruling's slicing). This commit adds the design brief, the ruling with its gate ruling and revisions, both checkpoint probe files and the [evidence](../c10-evidence/ticket-45-evidence.md) of both slices. [B30](../../../docs/DECISIONS.md#b30-c10-the-windows-containment-executor-ticket-45) records the build: the start gate, the qualified choices, the project manager's answers, the new importer of `golang.org/x/sys` and the executor's limits. On Windows, these tests show each criterion met:
- **Input, output and both restrictions.** A permitted process receives only its authorized input and returns bounded output, with the per-run AppContainer and the non-breakaway Job both active: `TestPermittedInputAndBoundedOutput`, `TestProfileGrantBoundary` and `TestNativeMemoryLimit`.
- **Launch and denials.** The launch is suspended with an explicit handle allowlist and restrictions before resume, the network is denied, and every protected target is unreachable: `TestProtectedTargetsDenied`, `TestOnlyExplicitStandardHandlesInherited`, `TestNetworkDenied` and `TestAppContainerJobAndSuspendOrder`.
- **Confinement.** Descendants and escape attempts stay confined, and links and inherited handles cannot widen the roots: `TestDescendantsCannotBreakAway`, `TestLinksAndInputAliasesCannotExpandRoots`, `TestSeparateRunsCannotReadPeerInput` and `TestNativeProcessLimitAndHostJobNesting`.
- **Closure before reuse.** Cancellation, timeout and supervisor loss end in positive tree exit before reuse, and an ambiguous stop is quarantined, unpublishable and never restarted: `TestCancelWaitsForEveryDescendant`, `TestWallDeadlineStopsWholeTree`, `TestNaturalExitKeepsStopGrace`, `TestSupervisorLossFenceMatrix`, `TestAmbiguousStopAndClosedFenceWriteFailure` and `TestClosedCleanupDoesNotFollowReparseOrReuseEarly`. `TestCorruptWrittenFenceCannotAuthorizeCleanup` and `FuzzFenceDecode` hold the fence's strict decoding.
- **Limits and evidence.** Finite limits fail closed, and start, stop and resource behavior is recorded under Defender: `TestNativeUserCPULimit`, `TestRequiredEnforcementUnavailable`, `TestLimitsNeverDefaultToUnlimited` and `TestDefenderResourceEvidence`. A kill request, root exit or pipe EOF never counts as closure; only the Job's count of active processes does (the ruling, P11).

The Linux and macOS builds compile, vet and refuse with `ErrUnsupported`; their executors are tickets 47 and 49. Nothing is frozen, so `CONTRACTS.md` is unchanged.
