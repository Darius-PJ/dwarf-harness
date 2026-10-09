# 28: Keep shutdown and revocation admissible under saturated social traffic

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C6 AC06 real load/replay gate
Blocked by: 27

## What to build

Reserve independent shutdown/revocation capacity while preserving bounded ordinary-message service.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen control/event families and existing Core appender admission boundary.
- Declare traffic/storage workload and measured acceptance bounds; provisional caps are not performance claims.

## Acceptance criteria

- [ ] Real direct/board traffic enforces 64-KiB messages, 16 attachments, 128 pending direct refs/context, 1,024 board refs/board and 4,096 colony refs, subject to documented calibration; unauthorized work consumes no capacity and overflow is visible busy before acceptance.
- [ ] With ordinary queues saturated, one pending shutdown and one pending revocation independently reach durable Core records. Ordinary traffic cannot consume their slots; a second distinct same-class request is busy, while a same-ID retry returns the original receipt.
- [ ] The appender finishes its current bounded commit, serves controls in admission order, and gives waiting ordinary work an append after no more than two consecutive control commits, even when one control class is continuously refilled.
- [ ] A control slot is released only after its authoritative record commits. Offline consumers are not treated as acknowledgments, and any paid follow-up still needs normal BudgetBook admission.
- [ ] Crash/replay and concurrent saturation preserve receipts and control effects without duplicate execution. Measure service fairness, latency, storage and whole-process overhead without inventing a peer-response SLA or counting busy replies as completed work.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 49.
- Implementation decisions: 1, 2, 3, 4, 6, 12, 16.
- Testing decisions: 1, 2, 4, 8, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 AC06 control-reserve gate
- Core design §13.6

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
