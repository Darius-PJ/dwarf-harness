# 138: Repair the first three-OS CI run

Status: claimed
Type: task
Published: 2026-10-20
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: None (can start immediately)

## What to build

The first run of the three-OS CI ([D133](../../../docs/DECISIONS.md#d133-three-os-ci-for-c3)), [39366517220](https://github.com/Darius-PJ/dwarf-harness/actions/runs/39366517220) on c6bfc5e, failed on all three systems. Setup passed on each, so the failures are the code's, for four reasons:

1. **Temporary folders with two names (Windows and macOS).** On the Windows runner the temporary folder is an 8.3 short name, `C:\Users\RUNNER~1\…`; on macOS, `/var` is a link to `/private/var`. Git and Go report the canonical name.
   - The Go analyzer (ticket 61's S5) compared the root it created, in its alias form, with canonical paths, and left in-tree files unknown. That is a product defect.
   - Workspace test fixtures expected the alias, so their shared Git seed failed, and about 200 workspace tests failed with it.
2. **Helpers used only by Windows code (Linux and macOS).** Four functions declared in platform-neutral files are used only from Windows files, so staticcheck's U1000 check flagged them.
3. **Static steps for the host only.** The local check ran vet, staticcheck and govulncheck only for windows/amd64, so reason 2 reached CI ([D137](../../../docs/DECISIONS.md#d137-the-check-analyzes-every-supported-target)).
4. **A handle leak in a Windows test, found while repairing reason 1.** The graph foundation's Job observer started its child with every inheritable handle of the test process, so it could capture a concurrent `git`'s pipes. That `git` then exited 0, but its Wait hit the 5 s `WaitDelay`.

## Acceptance criteria

- [x] The Go analyzer resolves the temporary root it creates once, so it compares one form of each path. Regression rows run it through an 8.3 alias on Windows and a symbolic link on Linux and macOS. A temporary folder below a junction is unsupported and fails closed.
- [x] Test fixtures take their paths from canonical temporary roots.
- [x] The four helpers live in the Windows files that use them, and staticcheck passes for all five targets.
- [x] `go run ./tools/check` runs vet, staticcheck and govulncheck for every supported target (D137).
- [x] The observer passes `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` with its three standard handles. A deterministic subtest shows that an unrelated inheritable pipe is not inherited.
- [ ] A three-OS run on the repair passes, or each remaining failure has its own repair ticket.

## Comments

2026-10-20 — Claimed and built at the owner's instruction to fix it now, continuing with the fixes for A and B in a new coder and deferring the advisor's review. The project manager added reason 3 at the owner's request (D137) and found reason 4 while the repair was verified.
- The repair is on `fix/ci-portability`: ebc24b8 (the check) and e6ac925 (the rest).
- Before the observer fix, two of three race runs of the affected packages failed; after it, three of three passed.
- The full check on e6ac925: `check PASSED: all 9 steps passed in 1091.5s` (vet 15.1 s, staticcheck 55.0 s and govulncheck 16.2 s for all five targets; tests 421.7 s, race 568.3 s).
- The build advisor's review waits until after 2026-10-22 18:59. The push waits until the owner can read the run's logs right after it, the next morning.
