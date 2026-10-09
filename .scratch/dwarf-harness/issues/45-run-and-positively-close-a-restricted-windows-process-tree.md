# 45: Run and positively close a restricted Windows process tree

Status: ready-for-agent
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

- [ ] A real permitted test process receives only authorized run-specific input and returns bounded output to Core; per-run AppContainer/SID access restriction and a non-breakaway Job Object are both active.
- [ ] Launch is suspended with an explicit inherited-handle allowlist, restrictions apply before resume, and network is denied by default. A child cannot reach canonical/common Git, ledger/bodies, private peers, credentials, owner IPC or sealed results.
- [ ] Actual spawned descendants and escape attempts remain confined; paths/links and inherited handles cannot expand the allowed roots.
- [ ] Cancellation, timeout and supervisor loss establish positive process-tree exit before slot/root reuse. An ambiguous stop quarantines resources and cannot publish or restart automatically.
- [ ] Required finite execution/input/output limits fail closed when unenforceable. Record actual start/stop/resource behavior under Defender; a Job Object, successful kill request or worktree alone is never reported as the security boundary.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 62, 63.
- Implementation decisions: 1, 2, 3, 4, 21.
- Testing decisions: 1, 2, 16, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C10
- Core design §13.9

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
