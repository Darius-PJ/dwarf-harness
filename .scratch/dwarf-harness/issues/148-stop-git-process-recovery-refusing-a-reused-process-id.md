# 148: Stop Git-process recovery refusing a reused process ID

Status: ready-for-agent
Type: task
Published: 2026-11-21
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 Git-process recovery after a restart (D168, D171), as ticket 16 built it
Blocked by: None (can start immediately)

## What to build

Stop Core's Git-process recovery, `internal/core/gitprocess`, from refusing a workspace because the process ID recorded for a Git child's root process now belongs to another process. A root of its own that is still live must still be waited for, and refused when it is not confirmed gone.

After a restart, the Windows recovery reconciles every recorded Git root before the workspace may run Git again. Take a root recorded as running whose named Job is gone in the recorded Windows session. Recovery must also prove that the root process itself has gone: it opens the process by its recorded ID and compares that process's creation time with the recorded one. The root counts as gone if no process has that ID, or if the process holding it was created at another time. If the same process is still there, recovery waits for it within the stop grace. If recovery cannot open or query the process for any other reason, or the same process outlives the grace, it refuses with `root_unconfirmed` and keeps the record. The package re-proves a root this way even if it had recorded the root as retired before the restart: a retirement hint suppresses duplicate reports, not the native proof. A completed root's ID stays on record until its batch's container retires. The package documentation states that this widens the access-denied exposure to at most 32 roots per live container.

Windows may give an exited process's ID to a new process soon afterwards. Suppose a recorded root has exited and its ID now belongs to a process the current user may not open. The open then fails with an error other than "no such process", so recovery refuses a root that is already gone. The workspace fails to open, and the process that refused keeps the root lock. Each later start refuses for as long as the other process holds the ID. Today's refusal message gives only its reason, not the native error behind it. This was found during ticket [147](147-cut-the-agents-packages-test-time.md)'s work. No hosted run has shown it. Whether the hosted Windows runner can hit it depends on which processes its account may open [INFERENCE].

To build:

1. A ruled design chooses the fix before code (D117). It weighs, without ranking them:
   - learning the current holder's creation time without opening the process, and treating a different creation time as gone;
   - whether a root whose retirement hint is set needs the process-ID proof at all. Dropping it would amend the package documentation's rule that hints suppress duplicate reports, not native proof, so the design ruling must decide that amendment explicitly;
   - a Windows restart. Process IDs are dealt afresh after one, so a recorded ID may belong to a new process, and if the recorded-session check still passes, a refusal could persist across restarts [INFERENCE].
2. Implement the fix with a deterministic regression row and the ruling's rule breaks.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Builds on `internal/core/gitprocess` as ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md) left it (D168, D171). The package documentation states its recovery protocol and residuals.
- A ruled design chooses the fix before code (D117). Because this is recovery work, the ruling also sets the ticket's rule breaks. This ticket ranks no fix.
- Needs no new dependency and no CI workflow change. The ruling lists any owner gate its design adds.
- The fix lies outside ticket [147](147-cut-the-agents-packages-test-time.md)'s packages, so the two may run in parallel. Neither lists the other as a blocker.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); a red check is neither rerun nor skipped.

## Acceptance criteria

- [ ] After a restart, the reuse of a recorded Git root's process ID by a different process, including one the current user cannot open, no longer makes reconcile refuse.
- [ ] A recorded root that is still the same live process is still waited for within the stop grace, and refused when not confirmed gone. `TestGitProcessFenceErrors/live-root` and `TestGitProcessSlotErrors/native/live-retired` keep proving it, unweakened.
- [ ] A deterministic regression row reconciles a root frame whose retirement hint is set and whose recorded process ID is held by a different process that denies the caller. Before the fix it is red with `root_unconfirmed`; after the fix it is green.
- [ ] Each rule break the design ruling names is caught at its named assertion, and no existing rule-break row is deleted or weakened.
- [ ] `UnconfirmedError`'s message carries its wrapped cause, when there is one, as well as its reason.
- [ ] No frozen contract changes: every frozen record, encoding and golden is unchanged. The `gitprocess` fence and root-identity files are outside the F3-16 freeze.
- [ ] The defect is fixed in the product under D171: no hosted runner is reconfigured and no limit is raised. No test retries a reconcile or skips a row to get past a `root_unconfirmed` refusal; a row that asserts a refusal, removes its cause and then proves recovery is not a retry.
- [ ] The final commit's hosted check passes on Windows, Linux and macOS, in both the tests and race steps.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 26, 63.
- Implementation decisions: 1, 4, 10.
- Testing decisions: 1, 2, 3.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-11-21 — Published at the project manager's proposal; the defect was found during ticket [147](147-cut-the-agents-packages-test-time.md)'s work. The owner approved ticket 148 as proposed (D178). Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.
