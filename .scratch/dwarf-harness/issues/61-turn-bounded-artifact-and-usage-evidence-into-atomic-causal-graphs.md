# 61: Turn bounded artifact and usage evidence into atomic causal graphs

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U3 graph foundation, causal capability
Blocked by: 07, 09, 11, 15, 17

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

2026-10-18 — The graph foundation is built in part. Slices S1–S4 are on `main`: S2 with F-U3-61 frozen as D130, S1 (96918cd, 88ca88f), S3 (0ccb983, ef329fa) and S4 (39d2404). The service reads frozen C2 history and analyzes Python, TypeScript and a bounded SQL subset in bounded helpers. It publishes complete versions through the Core-owned appender, replays them from records alone, and rebuilds after a reopen, every 100 structural acceptances, on an analyzer change or on a projection mismatch.

The rest of this ticket waits:
- Pinned-check, declaration, resource and work-domain nodes, their edges and positive resource→consumer usage wait for producer freezes in tickets 17, 11 and 15.
- Resource and work-domain registration has no producer ticket yet. Its assignment is due at ticket 11's design brief, as a B entry.
- Go coverage stays unknown until the owner answers OG-GO.
- A rebuild stages at most about 18,000 edges within its 64 MiB allowance, so above that size a colony cannot complete one. A later slice of this ticket audits that allowance before ticket 63 relies on rebuilds.

Co-access is ticket 62, and live accepted promotion is ticket 63 (C4).

2026-10-19 — S5 is on `main`: 2c870d3, its review fix-up f0525c7, and cd21243 for D134. Go dependencies now resolve through the `go/packages` helper, `helpers/go-analyzer`, which D132 admitted after the owner approved the go/packages helper at OG-GO. D134, the owner's amendment, turns off `go list`'s module index, which cut a cold call from seconds to about 0.2 s.
- Every corpus `.go` file gets an explicit state.
- An import that resolves outside the tree, an incomplete package, or a work or size cap leaves Go unknown.
- The qualified tuple is go1.27.2 windows/amd64, toolchain SHA-256 b8cd508a0a7aac883cd750e9e75eb306d219e726bdf1cde29009490729437fbf, with the helper built from `golang.org/x/tools` v0.50.0, `golang.org/x/sync` v0.23.0 and `golang.org/x/mod` v0.41.0. Windows Job lifetime counts showed that `go list` starts no process of its own, and every check repeats that proof on the helper it builds.
- Other platforms are unqualified, so a host enables Go only for a qualified tuple.

Two items remain for a later slice of this ticket, S6, besides the producer freezes:
- the rebuild staging allowance (about 18,000 edges in 64 MiB), audited before ticket 63 relies on rebuilds;
- whether a helper result whose only failure is cleaning up its temporary folder is kept, with the leftover folder reported, rather than discarded and retried. That is the S5 review's follow-up.

2026-10-20 — The first three-OS CI run found a defect in S5: when the temporary folder has a second name, an 8.3 short name on Windows or `/var` on macOS, the Go analyzer compared the root it created, in that form, with the canonical paths `go list` reports, and left in-tree files unknown. [Ticket 138](138-repair-the-first-three-os-ci-run.md) resolves the root once when it is created, with regression rows for both kinds of alias.

2026-10-24 — Resource and work-domain registration, which this ticket's comment of 2026-10-18 left without a producer, is assigned to C3. [B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11), ticket 11's close, adds registered shared resources and owner-registered work domains to C3's row in [`CONTRACTS.md`](../../../docs/CONTRACTS.md) as a later family, added only when specified, as this ticket's ruling asked of ticket 11's design brief. No producer ticket exists yet; the project manager asks the owner whether to publish one. This ticket's resource and work-domain nodes (its first criterion) and its positive resource→consumer usage (its third, with ticket 15's action facts) wait for that family. Ticket 11, one of this ticket's blockers, is resolved; tickets 15 and 17 still block it.
