# 60: Propagate sources with bounded replay-safe best arrivals

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U1.E completion
Blocked by: 59

## What to build

Deterministic strongest-path propagation, delayed cancellation and restart-persistent work limits.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen relevant graph/law/source event families; fixed capacity workload and comparison manifest before measurement.

Completes U1.E engine capability (ticket [60](60-propagate-sources-with-bounded-replay-safe-best-arrivals.md)) with ticket [59](59-replay-record-only-facts-into-exact-source-lifecycles.md); still standalone/synthetic, not agent-visible release.

## Acceptance criteria

- [ ] Through engine reads, FIFO immutable candidates select greatest per-hop-floored integer amplitude, then fewer hops, then stable edge-byte order; diamonds/cycles publish only one final source contribution/node over approved directions, never encounter edges.
- [ ] An exhaustive small-graph oracle catches the weaker-short-prefix/downstream-floor-tie case, stable ties and descendants surviving later intermediate-winner replacement; source/graph/law revision changes or caps publish no partial reach.
- [ ] Checked hop_count*latency_ms*1,000,000 and signed-int64 time addition reject negative/overflowing generations atomically; zero latency works, and end/replacement cancels all obsolete delayed arrivals.
- [ ] Every ENGINE-LIMITS bound is enforced and observable; work/prefix bytes are charged, counters and exhaustion keys survive yield/retry/restart/cache loss/replay, and exhausted same keys do not obtain a fresh allowance.
- [ ] Long replay, law changes, cancellation and high fan-in meet exact same-build replay and the explicitly limited cross-architecture comparison; structural divergence always fails.
- [ ] Measure actual engine ingest/reach/replay/retained-state costs under declared every-change and rework loads without blocking Core; report lag/cursor and unknown coverage rather than treating refusal or stale reads as capacity success.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### ENGINE-LIMITS

50,000 graph nodes; 200,000 combined edges; 10,000 active source revisions; 50,000 destinations/200,000 examined edges per search; 250,000 cumulative work units/search and 2,000,000/logical analysis task; 10,000/quantum; 512 MiB retained state; 64 MiB temporary graph/replay state; pending arrivals at most 50,000 and 64 MiB; 256 retained provenance groups then at_least_256. Charge enqueue, dequeue, edge examination, heap operation, provenance comparison and immutable-prefix allocation separately. Counters/cursors survive yields, retries, restart, replay and cache rebuild; exhausted same keys do not auto-requeue. Same-build/same-architecture replay exact; candidate cross-architecture value tolerance abs(a-b)<=1e-10+1e-8*max(abs(a),abs(b)), with exact topology/routes and no threshold-branch identity claim.

Source: Undercurrent design §§7.1–7.2, 13.5, 13.7; Parent specification implementation 34–36.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 82, 83, 84, 94, 95.
- Implementation decisions: 1, 2, 3, 4, 32, 33, 34, 35, 36.
- Testing decisions: 1, 2, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U1
- Undercurrent design §§7.1–7.4,13.3,13.5,13.7,14.1–14.2
- Decision log D78/B12/B15

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
