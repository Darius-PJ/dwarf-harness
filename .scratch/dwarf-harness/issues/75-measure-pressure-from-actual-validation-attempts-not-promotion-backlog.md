# 75: Measure pressure from actual validation attempts, not promotion backlog

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U5 runner-input completion
Blocked by: 22, 23, 74

## What to build

Derive runner pressure from actual queued/active attempts, never pending promotion counts.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen actual C4 queue/start/finish/child-exit facts and finite configured runner capacity.

Completes U5 resource capability (ticket [75](75-measure-pressure-from-actual-validation-attempts-not-promotion-backlog.md)); C4 is a real blocker here only, not for all resource transformations.

## Acceptance criteria

- [ ] Queued plus active actual validation attempts produce the selected scoped ratio against real configured slots; absent/zero/incomplete capacity is unknown.
- [ ] Pending requests, ordered finalizers, speculative waiting, declarations without attempts, busy capture and true no-change create no fabricated runner load or new resource kind/node.
- [ ] Refresh and rebuilt-generation attempts count their actual work under the original request; no outcome erases prior real use, and obsolete resources remain occupied until positive child exit.
- [ ] Real full-required-check steady/burst/rework/recovery scenarios reconcile field pressure with actual runner observations and replay without counting attempts/refusals as accepted throughput.
- [ ] No paid wakeup is inferred from passive promotion completion and no field observation chooses Core queue/lane/admission limits.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 88, 95, 102.
- Implementation decisions: 1, 2, 3, 4, 33, 36, 41.
- Testing decisions: 1, 2, 9, 11, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U5 runner node
- Undercurrent design §15.1
- Decision log D63/D68–D73/D77/D78

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
