# 46: Qualify the real Windows runner profile for A2

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 Windows qualification; exports Windows A2 profile (ticket [46](46-qualify-the-real-windows-runner-profile-for-a2.md))
Blocked by: 18, 19, 45, 144

## What to build

Run the actual Windows runner/escape/resource qualification before claiming A2.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Actual pinned Windows/toolchain/environment profile, declared workload and owner-preregistered immutable V6 matrix.
- No whole-C4 dependency; no real colony work before C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md)).

## Acceptance criteria

- [ ] The real C4 adapters execute fresh exact subjects through the Windows compartment with bound request/attempt/generation, predecessor, check/environment and bounded sealed-output receipts, not child-reported authority.
- [ ] Each enabled exact model/permission/profile completes its fresh 24-attack/nine-control V6 matrix through Go, pytest and Node, retaining same-process canary evidence; confirmation attempts obey the fixed total cap of 57 and fail/inconclusive blocks qualification.
- [ ] Positive authorized outputs work while hostile writes/reads of every protected root, owner IPC, disabled-network paths and inherited handles fail on actual Windows.
- [ ] Kill/cancel/timeout/Core-loss races demonstrate descendant closure and no publication or slot/root reuse on ambiguity; a qualified outer boundary does not excuse an in-process oracle/canary bypass.
- [ ] Measure CPU/RAM/disk/start/stop/contention and service capacity for the declared steady/burst workload with Defender enabled. Export only the exact evidenced Windows profile; provide costs to C4's delivered-throughput gate without treating attempts or refusals as accepted work or claiming A3.

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

2026-10-18 — Ticket 45, one of this ticket's blockers, is resolved: C10's Windows executor, `internal/core/winexec`, is on `main` ([B30](../../../docs/DECISIONS.md#b30-c10-the-windows-containment-executor-ticket-45)). Tickets 18 and 19 remain open. The executor's request, receipt and quarantine fence are Core-internal code, not contracts. If this ticket is the first to need a durable executor fact, it specifies that fact in C4's attempt and evidence family under `CONTRACTS.md`'s rules (the [gate ruling](../c10-evidence/ticket-45-design-ruling.md), P2). Ticket 45 claims no capacity, no V6 result and no A2 label. The Limits of B30 state the boundary this qualification inherits.

2026-11-07 — D167 rework: by the owner's decision this ticket now also waits for ticket [144](144-make-the-windows-executors-race-step-tests-reliable-on-hosted-runners.md), so that the Windows A2 qualification rests on explained tests: two of the three hosted race-step failures 144 investigates are confinement proofs this qualification relies on. Its blockers were 18, 19 and 45; they are now 18, 19, 45 and 144. The number and criteria are unchanged; this grants no separate implementation, contract-freeze or release approval.

2026-11-08 — Ticket [144](144-make-the-windows-executors-race-step-tests-reliable-on-hosted-runners.md) is resolved, and it found no product defect in the Windows executor: two of the three hosted failures were the Go race runtime's startup failure inside a race-built test payload, and the third a test observer's census during a Job's rundown. The confinement proofs this qualification relies on keep their assertions: under the race detector the sandboxed payload is now a plain companion build while the executor and supervisors stay race-built, the peer test gained a live-target check, and only the exited root's rundown census was dropped. The executor's race-step tests then passed twenty consecutive hosted full-matrix runs of their final commit. Tickets 18 and 19 remain open, so this ticket's status and criteria are unchanged.
