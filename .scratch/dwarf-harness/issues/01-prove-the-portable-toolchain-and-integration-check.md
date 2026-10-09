# 01: Prove the portable toolchain and integration check

Status: resolved
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C0
Blocked by: None (can start immediately)

## What to build

Run the pinned cgo-free build/check path, vulnerability and import-boundary gates, including a planted forbidden import and the real Windows race check.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

## Acceptance criteria

- [x] Pinned Go/tool/compiler configuration and explicit cgo settings run the agreed format/static/vulnerability/import/test/race/supported-build command; missing prerequisites fail honestly.
- [x] An actual forbidden import fails the boundary check; no placeholder owned packages or GUI dependencies enter Core.
- [x] Use existing canonical agent rules and decision provenance, and keep the GUI module deferred until its roadmap.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 1, 2.
- Implementation decisions: 1, 2, 3, 4, 5.
- Testing decisions: 1, 2, 3, 18, 20.

Canonical sections: Core roadmap C0; its cited controlling Core design and latest decision amendments.

## Answer

C0 is complete on Windows. The check command runs B17's nine steps with the pinned go1.27.2 ([D106](../../../docs/DECISIONS.md#d106-toolchain-go1272)), staticcheck 2026.2.1 and govulncheck v1.8.0; every step runs even after a failure, and the check fails if any step fails. On 2026-10-09 it passed with the pinned llvm-mingw 20260922 compiler for the race tests, and with that compiler unset only the race step failed, naming the missing setting. A planted forbidden import and a Linux-only cgo file each failed the import-boundary step, and 54 fixture and unit tests guard the checker's verdicts. C0 created no placeholder package or GUI module ([B17](../../../docs/DECISIONS.md#b17-c0-tool-pins-check-steps-and-import-table-encoding) defers the Core program until it has real behavior), and the agent rules live in the canonical agent file. Linux and macOS native runs wait for C3's CI, the vulnerability database is online-only, and rapid joins the module with its first test.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-09 — Claimed for the staged build. Work happens on branch `build/01-toolchain-check` in its own worktree (D41). The owner approved moving the toolchain to go1.27.2; that decision is recorded with this ticket's documentation changes.

2026-10-09 — Resolved: the C0 change passed the full check on Windows with go1.27.2, including `-race`, and the build advisor approved it. It reaches `main` only after the owner's sign-off (D41), because it adds the first tool dependencies and records owner decision D106.

2026-10-09 — The owner signed off under D41, and `main` fast-forwarded to commit ce05b81 after the full check passed on that exact commit.
