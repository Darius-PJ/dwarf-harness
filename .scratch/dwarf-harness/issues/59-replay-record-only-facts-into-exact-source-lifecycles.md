# 59: Replay record-only facts into exact source lifecycles

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U1.E local source/replay capability
Blocked by: 04

## What to build

Reconstruct source lifecycles from permanent facts and explicit time; discard invalid snapshots.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner freeze of each consumed producer family, including activation and graph semantics; operational producers are not required for this synthetic-only capability.

Complete local projection path, not whole U1.E engine capability (ticket [60](60-propagate-sources-with-bounded-replay-safe-best-arrivals.md)); no agents or visible field output.

## Acceptance criteria

- [ ] A frozen synthetic ledger produces deterministic origin-local impulse, level, sustained and rising readings at explicit supplied times; sampling never reads wall clock, issues work or appends an action event.
- [ ] Effect, condition, conservative provenance and audience identities remain distinct: same-effect retries/copies/clones do not multiply evidence, extensive values combine only in identical metric/unit/resource/scope/period, and unknown remains unknown.
- [ ] Ending/replacing a source removes precisely its recorded contributions; graph changes recompute active level/sustained reach while impulses retain their historical graph identity.
- [ ] Historical replay uses original law activation/evidence and committed graph versions, never bodies or mutable scratch; alternate-law results are explicitly counterfactual and never rewrite history.
- [ ] Atomic checksummed state+cursor caches bind every §5 version/hash/time input; stale/corrupt/incompatible caches rebuild without resetting cumulative allowances or manufacturing interrupted effects. Unknown consumed versions fail clearly; unrelated record families are skipped.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 79, 80, 82, 83, 94, 95.
- Implementation decisions: 1, 2, 3, 4, 30, 32, 33, 36.
- Testing decisions: 1, 2, 4, 10, 12, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U1
- Undercurrent design §§5–6,13.2–13.5,13.7,14.1,15.1
- Decision log D35–D37/B12

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
