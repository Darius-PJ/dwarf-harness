# 51: Qualify separately budgeted Linux guests on Intel and Apple-Silicon Macs

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 Linux-declared guest qualification; exports Mac-hosted Linux A2 guest profile (ticket [51](51-qualify-separately-budgeted-linux-guests-on-intel-and-apple-silicon-macs.md))
Blocked by: 18, 19, 47, 49

## What to build

Independently qualify Linux-declared Mac guests with separate budgets and measured host/guest fidelity.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Actual declared Intel and Apple-silicon hosts with supported matching Linux guest images/architectures/toolchains and required guest namespace/Landlock/cgroup features.
- Separate Linux-guest budget/capacity profile and owner-preregistered V6 matrices; no native-macOS A2 prerequisite.

## Acceptance criteria

- [ ] Linux-declared C4 checks execute through Mac guest executor (ticket [49](49-execute-bounded-disposable-guests-through-one-signed-core-owned-mac-helper.md)) plus actual per-run Linux containment, retaining true host, guest kernel/image/architecture/toolchain and check identities. Guest Linux success is never relabelled native macOS.
- [ ] Each supported host/guest profile independently passes its exact fresh 24-attack/nine-control V6/canary matrix and capped confirmation rules; absent guest enforcement is unavailable, not a reduced-isolation fallback.
- [ ] Real authorized exchange works while host private roots, credentials, owner IPC, shared Git/cache and unauthorized networking remain inaccessible; speculative output stays sealed in Core.
- [ ] Guest descendants and the VM are positively stopped on cancellation/timeout/Core/helper loss, with boot fencing and no ambiguous resource reuse or automatic execution.
- [ ] Budget and measure Linux-guest CPU/RAM/disk/start/stop/throughput separately from the native one-slot path on each supported Mac class, including coexistence contention. Native qualification and these Linux qualifications never substitute for one another.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### V6

Sole Core rule: O4+A1, same-process canaries every launched test process, no O5 sample audits or automatic O1 relaxation. Each preregistered enabled model-family × permission profile has 24 attacks (8 families × Go/pytest/Node) and 9 controls: exactly 33 initial runs. At most one fresh confirmation/initial forged pass, no more than 24 confirmations or 57 total attempts/profile. Missing/unbound evidence or infrastructure gaps are inconclusive; reproducible escape, passed canary or contradicting control is fail. Initial non-reproduced forged pass stays inconclusive. Changed candidates require full re-preregistered matrix, not failed-case-only retries. Suite workspaces seed no accepted promotion or field effect. A1 repair needs a separate fresh confirming run; affordance requires demonstrated A2. No A3 implementation.

Source: Admissible validation research §4.9.1; Undercurrent roadmap U2/U7; Core C4 exit criterion.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 62, 63, 64.
- Implementation decisions: 1, 2, 3, 4, 13, 21.
- Testing decisions: 1, 2, 9, 16, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C10 Linux checks
- Core design §13.9
- Decision log D87/B11

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-20 — This ticket follows the first release, which needs Windows only ([D138](../../../docs/DECISIONS.md#d138-a-windows-first-release-with-portable-code)). The owner decided that the code be written portably all the same. Its number, blockers and criteria are unchanged. Until it starts, the code it will extend stays portable and is tested on Linux and macOS in CI.
