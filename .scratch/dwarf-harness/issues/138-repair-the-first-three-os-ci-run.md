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
5. **The capture could read files Git could not (Windows, second run).** Go's `os.Open` opens read-only files with Windows' backup flag. With SeBackupPrivilege enabled, as on the hosted runner, such an open gets past another handle's lock and a denying ACL; Git's plain open does not. So the capture read a locked file that Git then failed on, and returned a generic Git error instead of refusing the file as unreadable. That is a product defect on any host that enables the privilege, such as a service running as an administrator.

## Acceptance criteria

- [x] The Go analyzer resolves the temporary root it creates once, so it compares one form of each path. Regression rows run it through an 8.3 alias on Windows and a symbolic link on Linux and macOS. A temporary folder below a junction is unsupported and fails closed.
- [x] Test fixtures take their paths from canonical temporary roots.
- [x] The four helpers live in the Windows files that use them, and staticcheck passes for all five targets.
- [x] `go run ./tools/check` runs vet, staticcheck and govulncheck for every supported target (D137).
- [x] The observer passes `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` with its three standard handles. A deterministic subtest shows that an unrelated inheritable pipe is not inherited.
- [x] On Windows the capture opens each candidate as Git does, without the backup flag, so a file Git cannot read is refused as unreadable even with SeBackupPrivilege enabled.
- [ ] A three-OS run on the repair passes, or each remaining failure has its own repair ticket.

## Comments

2026-10-20 — Claimed and built at the owner's instruction to fix it now, continuing with the fixes for A and B in a new coder and deferring the advisor's review. The project manager added reason 3 at the owner's request (D137) and found reason 4 while the repair was verified.
- The repair is on `fix/ci-portability`: ebc24b8 (the check) and e6ac925 (the rest).
- Before the observer fix, two of three race runs of the affected packages failed; after it, three of three passed.
- The full check on e6ac925: `check PASSED: all 9 steps passed in 1091.5s` (vet 15.1 s, staticcheck 55.0 s and govulncheck 16.2 s for all five targets; tests 421.7 s, race 568.3 s).
- The build advisor's review waits until after 2026-10-22 18:59. The push waits until the owner can read the run's logs right after it, the next morning.

2026-10-20 — The owner pushed the branch and dispatched the second run, on 6c59119.
- Linux passed (`ubuntu-24.04`, image 20261018.339.1, Git 2.55.0; 665.1 s), and so did macOS (`macos-15-arm64`, image 20260907.0337.1, Git 2.55.0; 361.1 s).
- Windows (`windows-2022`, image 20261012.333.1, Git 2.56.0.windows.2; 1,805.8 s) failed one test, `TestCaptureUnreadable`, in both the tests and race steps: reason 5.
- Two runs on the development machine confirmed the mechanism and the fix:
  - a probe showed that an open with the backup flag reads a file held with share mode 0 only while SeBackupPrivilege is enabled;
  - with the privilege enabled, the test failed on 6c59119 exactly as on CI and passed on the fix, f3fcdde.
- The full check on f3fcdde: `check PASSED: all 9 steps passed in 973.4s` (tests 386.4 s, race 521.4 s; the workspace package 380.1 s and 480.4 s).
