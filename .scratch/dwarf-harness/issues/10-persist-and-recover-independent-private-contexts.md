# 10: Persist and recover independent private contexts

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: 07, 09

## What to build

Expose context-bound memory, handoff and durable receipts with revision-checked writes and honest missing-revision/interrupted recovery; never share continuation or private history.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Owner-provided private remote and Windows/Linux/macOS CI; live provider credentials only when a live test is attempted.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] One execution session owns distinct context memory/handoff/transcript/continuation and scratch; switching contexts never imports private state.
- [ ] Durable private bytes precede revision record; mismatches block/report on restart and expected-revision concurrent writes refuse stale state.
- [ ] Current-authorized immutable command receipts survive restart and same-ID replay; interrupted operations never imply effect success.
- [ ] Context, memory, handoff and receipt records change only through producer-owned typed Core transitions that bind actor, context, operation and target, check expected version and current authority, publish required private bytes and a bounded replay-complete event group through the existing sequencer, then update rebuildable authorized projections before acknowledging; no caller writes these records directly, appends ad hoc state or keeps separate persistence, projection or restart hooks.
- [ ] Deleting or rebuilding an optional projection cache loses no committed state, and rebuilding state never dispatches work.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 23, 26.
- Implementation decisions: 1, 2, 3, 4, 6, 10.
- Testing decisions: 1, 2, 4, 8, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover compulsory typed Core transitions for context state. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-10-19 — The CI that this ticket's start gates name is prepared but has not run. The owner's private remote, `https://github.com/Darius-PJ/dwarf-harness`, created on 2026-10-19, exists, and the reviewed workflow, `.github/workflows/check.yml`, is on `main` at fe5f758; the owner's first push and its first run are pending, so no CI result exists yet. [D133](../../../docs/DECISIONS.md#d133-three-os-ci-for-c3) records the owner's answers: the CI's only action is `actions/checkout` v7.0.1, pinned by commit, and it downloads Go 1.27.2 and, on Windows, llvm-mingw 20260922, each checked against its SHA-256; and this ticket's CI gate clears when the workflow runs on the remote and its Windows job passes, with Linux and macOS repairs worked alongside C3. Every run is the full check on native `windows-2022` amd64, `ubuntu-24.04` amd64 and `macos-15` arm64 jobs, started by a push to `main` that changes more than Markdown or by manual dispatch. Known gaps in native coverage: C1 and C2 code has never run natively on Linux or macOS, so the first run may be red there; the three native graph helper rows skip, since their controls are empty; and Intel macOS and Linux arm64 are only cross-built. A red product row becomes a repair ticket linked from here and from its producing ticket; a setup or network failure is not a pass. The coder counted 585 tracked files and 157 commits, far below GitHub's limits for path filters (3,000 files, 1,000 commits); if the first push starts no run, the owner dispatches it once. Clearing the CI gate resolves nothing else here: the other start gates above stand.

2026-10-20 — The first run, [39366517220](https://github.com/Darius-PJ/dwarf-harness/actions/runs/39366517220) on c6bfc5e, was red on all three systems. The first push started no run; the owner dispatched it. Setup passed everywhere, so the failures are the code's:
- **Windows** (`windows-2022`, image 20261012.333.1, Git 2.56.0.windows.2, go1.27.2): the tests and race steps failed in `internal/core/workspace`, `helpers/go-analyzer`, `internal/undercurrent/graph` and `internal/core/graphfoundation`. The check took 663.6 s.
- **Linux** (`ubuntu-24.04`, image 20261018.339.1, Git 2.55.0): only staticcheck failed, on four U1000 findings. The tests and race steps passed; the check took 615.0 s.
- **macOS** (`macos-15-arm64`, image 20260907.0337.1, Git 2.55.0): staticcheck, tests and race failed, in the same four packages. The check took 207.4 s.

Causes and repairs are in [ticket 138](138-repair-the-first-three-os-ci-run.md). This ticket's CI start gate (D133, a green Windows job) is not met; a passing run on the repair clears it.

2026-10-20 — The second run, on the repair branch `fix/ci-portability` at 6c59119, passed on Linux (665.1 s) and macOS (361.1 s). On Windows (1,805.8 s) one test failed, `TestCaptureUnreadable`, in both the tests and race steps; the cause and its repair are in [ticket 138](138-repair-the-first-three-os-ci-run.md). The gate is still not met.
