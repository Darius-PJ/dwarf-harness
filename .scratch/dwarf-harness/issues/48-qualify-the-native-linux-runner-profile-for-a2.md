# 48: Qualify the native Linux runner profile for A2

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 native Linux qualification; exports native Linux A2 profile (ticket [48](48-qualify-the-native-linux-runner-profile-for-a2.md))
Blocked by: 18, 19, 47

## What to build

Run the actual native Linux runner/escape/resource qualification before claiming A2.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Actual pinned Linux kernel, filesystem/cgroup/ABI/toolchain profile and owner-preregistered V6 matrix.
- No dependence on Windows or macOS qualification.

## Acceptance criteria

- [ ] The actual C4 runner/adapters use fresh Linux-restricted subjects and exact check/environment/attempt/predecessor bindings, with bounded outputs retained in host-private quarantine.
- [ ] Run every enabled profile's complete fresh 24-attack/nine-control V6 matrix, same-process canaries and capped confirmation policy; missing or inconclusive evidence cannot yield A2.
- [ ] Real permitted outputs succeed while protected file, credential, IPC, network and privilege/namespace escapes fail under the exact declared feature profile.
- [ ] Cancellation, timeout, child/parent/Core death and lingering-descriptor cases prove all descendants stopped; ambiguity blocks publish and reuse rather than trusting a sent signal.
- [ ] Measure actual Linux execution/materialization/closure costs and declared-workload contention, retain platform limits and failed feature probes, and export only this exact qualified profile. A2 does not imply A3 or exempt the Core workload gate.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### V6

Sole Core rule: O4+A1, same-process canaries every launched test process, no O5 sample audits or automatic O1 relaxation. Each preregistered enabled model-family × permission profile has 24 attacks (8 families × Go/pytest/Node) and 9 controls: exactly 33 initial runs. At most one fresh confirmation/initial forged pass, no more than 24 confirmations or 57 total attempts/profile. Missing/unbound evidence or infrastructure gaps are inconclusive; reproducible escape, passed canary or contradicting control is fail. Initial non-reproduced forged pass stays inconclusive. Changed candidates require full re-preregistered matrix, not failed-case-only retries. Suite workspaces seed no accepted promotion or field effect. A1 repair needs a separate fresh confirming run; affordance requires demonstrated A2. No A3 implementation.

Source: Admissible validation research §4.9.1; Undercurrent roadmap U2/U7; Core C4 exit criterion.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 62, 63.
- Implementation decisions: 1, 2, 3, 4, 13, 21.
- Testing decisions: 1, 2, 9, 16, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C10
- Admissible validation research §§4.9–4.9.1

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-20 — This ticket follows the first release, which needs Windows only ([D138](../../../docs/DECISIONS.md#d138-a-windows-first-release-with-portable-code)). The owner decided that the code be written portably all the same. Its number, blockers and criteria are unchanged. Until it starts, the code it will extend stays portable and is tested on Linux and macOS in CI.
