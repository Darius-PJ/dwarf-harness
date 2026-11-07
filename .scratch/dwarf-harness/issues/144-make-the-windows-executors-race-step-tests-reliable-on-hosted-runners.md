# 144: Make the Windows executor's race-step tests reliable on hosted runners

Status: ready-for-agent
Type: task
Published: 2026-11-07
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 independently usable Windows containment executor (B30); its tests in the hosted check's race step
Blocked by: None (can start immediately)

## What to build

Find and remove the causes of three hosted race-step failures in the Windows executor's tests. Three hosted checks went red in step 7 (race) on the `windows-2022` runner, each time in `internal/core/winexec`, a package none of those commits changed, and each time in a different test:

- run 40773519796 on da18709 (ticket [140](140-bound-the-private-state-services-replay-and-lookup-cost.md)'s first slice, 2026-10-28): `TestNativeUserCPULimit`;
- run 41133154184 on c2d42e4 (ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md)'s second slice, check 30, 2026-10-30): `TestSeparateRunsCannotReadPeerInput`;
- run 42338417245 on a38ecb7 (ticket [142](142-read-only-the-ledger-tail-in-cores-agent-service.md)'s second slice, check 40, 2026-11-07): `TestNativeProcessLimitAndHostJobNesting`.

The project neither reruns nor skips a red check (D133, D153), so each failure held `main` until a later commit passed. The causes are unobserved: the job logs need a signed-in account, and the run's annotations carry only the failing line. Ticket [45](45-run-and-positively-close-a-restricted-windows-process-tree.md) lists the second and third tests among its confinement proofs and the first among its limits, so the evidence must show whether each failure is the product's or the test's.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Builds on the Windows executor as ticket 45 left it; B30 and that ticket's ruling say what each test proves. Earlier fixes to the executor's tests went in as small follow-ups of ticket 45; this ticket takes these failures over instead.
- The hosted job logs need a signed-in account; when a cause needs one, the project manager asks the owner for it.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); a red check is neither rerun nor skipped, and no provider credentials are needed.

## Acceptance criteria

- [ ] Each of the three failures has a cause backed by evidence, such as the hosted job's log obtained through the owner or a reproduction under comparable load, or an explicit record of what could not be established.
- [ ] Each fix removes its cause: no retry, skip or quarantine, and no widened timeout or grace that merely hides the race.
- [ ] Where the cause allows one, a deterministic regression fails before its fix and passes after it.
- [ ] Changes stay inside the Windows executor package and its tests. A product defect found there is fixed there and named in the Answer. A cause outside the package, such as the check tool, the workflow or the runner image, is recorded in the Answer and routed, not fixed here; a change under `.github/` needs the owner's sign-off.
- [ ] Afterwards, twenty consecutive hosted full-matrix runs of the final commit, one at a time, each pass the Windows race step, using no paid minutes (D133 item 3; D146 item 3 makes the public repository's standard runners free); the design chooses how the runs are started. A failure in the Windows executor's tests ends the series and returns the ticket to diagnosis, and a rerun never counts. A run that fails for another reason, such as setup, the network or another package, neither counts nor ends the series.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 62, 63.
- Implementation decisions: 4, 5, 21.
- Testing decisions: 2, 3, 16.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C10
- Core design §13.9

## Comments

2026-11-07 — Published at the project manager's proposal; the owner approved publishing both tickets and having ticket 46 also wait for 144 (D167). Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.
