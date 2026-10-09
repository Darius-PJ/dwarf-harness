# 61: Turn bounded artifact and usage evidence into atomic causal graphs

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U3 graph foundation, causal capability
Blocked by: 07, 09

## What to build

Bounded analyzers publish complete graph versions and report unsupported coverage as unknown.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen ledger-appender/reader and specified U3 graph-delta family.
- Explicit D29 approval for Go go/packages before that resolver is enabled; unavailable approval/analyzers mean unknown coverage, not a silent substitute.

No actual accepted-promotion integration yet; does not wait for U1.E engine capability (ticket [60](60-propagate-sources-with-bounded-replay-safe-best-arrivals.md)) or C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md)).

## Acceptance criteria

- [ ] Covered artifacts/modules, pinned checks, registered resources and owner work domains yield stable typed nodes and evidenced dependency-to-consumer edges; agents, guessed directory aggregates and hyperlinks do not become invented causal nodes/edges.
- [ ] Bounded Go/Python/installed Node-TypeScript/SQL adapters perform no package installation or target-code execution and never run on Core's sequencer path; unsupported/dynamic syntax, missing adapters and exhausted work expose unknown coverage.
- [ ] Registered resource-to-consumer usage derives only from host-bound actual use or scoped declarations, not code footprint or guessed budgets; R1-dependent live usage waits for R1 while frozen synthetic fixtures remain permitted.
- [ ] Multi-chunk mutations become visible only as complete durable logical versions via the Core-owned appender; replay at every cursor sees wholly old or new state and needs no bodies.
- [ ] The GRAPH-ENCOUNTER rebuild schedule produces the same normalized graph as incremental updates, cannot overwrite newer acceptance with an old rebuild, and preserves unknown graph-pending sources.
- [ ] ENGINE-LIMITS graph/logical-analysis bounds persist across retries/restarts; measure rebuild/helper/resource costs without blocking Core.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### ENGINE-LIMITS

50,000 graph nodes; 200,000 combined edges; 10,000 active source revisions; 50,000 destinations/200,000 examined edges per search; 250,000 cumulative work units/search and 2,000,000/logical analysis task; 10,000/quantum; 512 MiB retained state; 64 MiB temporary graph/replay state; pending arrivals at most 50,000 and 64 MiB; 256 retained provenance groups then at_least_256. Charge enqueue, dequeue, edge examination, heap operation, provenance comparison and immutable-prefix allocation separately. Counters/cursors survive yields, retries, restart, replay and cache rebuild; exhausted same keys do not auto-requeue. Same-build/same-architecture replay exact; candidate cross-architecture value tolerance abs(a-b)<=1e-10+1e-8*max(abs(a),abs(b)), with exact topology/routes and no threshold-branch identity claim.

Source: Undercurrent design §§7.1–7.2, 13.5, 13.7; Parent specification implementation 34–36.

### GRAPH-ENCOUNTER

Rebuild at load, after 100 accepted structural changes, analyzer/configuration epoch change or version/digest mismatch. Co-access: one observation/pair/task-provenance group in rolling 30 days; require two groups; retain at most 64 identities and expose only at_least_64 at saturation; at most eight neighbors in deterministic support/last-use/NodeKey order. Incomplete identity history after saturation/expiry requires bounded authoritative rebuild or omitted unknown edge.

Source: Undercurrent design §8.2; Parent specification implementation 38–39.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 86, 95, 96, 97.
- Implementation decisions: 1, 2, 3, 4, 36, 38.
- Testing decisions: 1, 2, 4, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U3 foundation
- Undercurrent design §§8.1–8.3,13.6–13.7,15.1
- Decision log D29/D36/D50/D52/B2

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
