# 23: Measure every-change delivery under steady and burst load

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4
Blocked by: 18, 19, 21

## What to build

Measure the full 12/20 and actual larger enrolled workloads, four-checkpoint bursts, accepted throughput/backlog/latency, rework and laptop resource costs; busy is not delivery. Before measuring, add overlapping speculative validation to ticket [20](20-publish-only-the-validated-cumulative-prefix.md)'s one-at-a-time landing. The 12/20 scenarios are Release 3's target; Release 1 and 2 gates use the owner's declared workload (D185).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until Release 1's exit test passes (D182); any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Measure 12/20 declared pairs at 72/120 required changes per hour, synchronized four-checkpoint bursts and larger actual population under normal scanner settings.
- [ ] Report offered/admitted/accepted/no-change/refused, stable backlog and p50/p95/p99 latency, invalidation/rework, recovery and whole-process resource contention.
- [ ] Evaluate preregistered capacity margin and finite burst completion; do not shrink the population, drop work or count busy as delivery.
- [ ] Report the result as Release 3's target. It does not end the testing-only restriction, which ends when Release 1's exit test passes (D182).
- [ ] On ticket [20](20-publish-only-the-validated-cumulative-prefix.md)'s one-at-a-time landing, actually overlap cumulative real validations within declared P/window limits and publish only the successful request-order prefix (moved from ticket 20, D182).
- [ ] Foreign rebuild stays distinct from a local failure suffix, and speculative redundancy from actual no-change; every rebuilt non-no-op requires fresh full checks (moved from ticket 20, D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 2, 9, 10, 12, 37.
- Implementation decisions: 1, 2, 3, 4, 7, 8, 13.
- Testing decisions: 1, 2, 9, 18, 20.

Canonical sections: Core roadmap C4; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-27 — Release tier rework (D182, D185, D186): On trigger. Schedule it when landing waits slow the owner down or he runs more than about five agents; first, read the landing timeline in ticket [151](151-see-and-troubleshoot-from-the-terminal.md). It now also holds ticket [20](20-publish-only-the-validated-cumulative-prefix.md)'s overlapping speculative validation. Its fourth criterion no longer ends the testing-only restriction, which ends when Release 1's exit test passes (D182, amending D52). Its 12- and 20-agent scenarios stay as Release 3's target, as D78's model does in every On-trigger and Release 3 ticket. Release 1 and 2 gates use the owner's declared workload: about five agents, at most three paid model exchanges at once, one or two repositories (D185). This grants no implementation, contract-freeze or release approval.
