# 47: Run and positively close a restricted Linux process tree

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 independently usable Linux containment executor
Blocked by: 07, 09

## What to build

Confine real Linux descendants with namespace/Landlock/resource controls and prove closure.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Relevant owner-frozen OS/child/input/output/receipt interfaces.
- Real Linux kernel/cgroup/namespace/Landlock facilities; missing required features must be demonstrably unavailable, not silently substituted.

## Acceptance criteria

- [ ] A real authorized process exchanges bounded run input/output using private user/mount/PID/IPC/network namespaces, Landlock, capability drop and no_new_privs through the narrow trusted tool-init path.
- [ ] Feature/ABI/cgroup probes refuse isolation when required enforcement is absent; process groups or a container name cannot silently replace missing restrictions.
- [ ] Hostile children and descendants cannot reach canonical/common Git, Core ledger/body/private roots, credentials, owner IPC, sealed outputs or unauthorized network; traversal/link attempts cannot escape authorized roots.
- [ ] Real fork/daemon/pipe-retention, cancellation, timeout and supervisor-loss cases establish cgroup/descendant quiescence before root/slot reuse, otherwise quarantine without publication or automatic repeat.
- [ ] Finite wall/CPU/memory/process/input/output limits are enforced where required and fail closed when not enforceable. Positive permitted output and measured execution costs distinguish functional containment from a deny-everything fixture; no A2 label yet.

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

2026-10-20 — This ticket follows the first release, which needs Windows only ([D138](../../../docs/DECISIONS.md#d138-a-windows-first-release-with-portable-code)). The owner decided that the code be written portably all the same. Its number, blockers and criteria are unchanged. Until it starts, the code it will extend stays portable and is tested on Linux and macOS in CI.
