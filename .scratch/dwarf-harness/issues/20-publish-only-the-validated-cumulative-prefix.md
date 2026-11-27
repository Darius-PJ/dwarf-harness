# 20: Publish only the validated cumulative prefix

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4
Blocked by: 17

## What to build

Validate and land checked work one change at a time, in submission order. Each candidate runs its full required checks on top of the current canonical tree, foreign outputs stay sealed, and only a passing candidate publishes its exact predecessor-relative delta, in durable order with honest cancellation/no-change/recovery. Overlapping speculative validation is ticket [23](23-measure-every-change-delivery-under-steady-and-burst-load.md)'s, when its trigger schedules it (D182).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until Release 1's exit test passes (D182); any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Validate and land one change at a time in submission order: each candidate's full required checks run on top of the current canonical tree, and only a passing candidate advances canonical.
- [ ] Bind all evidence to exact generation/predecessor and seal every foreign diagnostic/artifact path until current authorization and exact accepted-predecessor witness permit disclosure.
- [ ] A failed local parent ends its dependent local suffix, and actual no-change stays distinct from failure; every non-no-op candidate requires fresh full checks.
- [ ] Observe real Git and durable acceptance at each interruption/cancellation boundary; preserve precise incremental attribution and later scratch edits.
- [ ] Results stay original-context/passive; malformed/unavailable/nonpassing gates never advance canonical.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 8, 9, 10, 12, 13, 14, 15, 16, 17, 18, 36, 37.
- Implementation decisions: 1, 2, 3, 4, 7, 8, 13.
- Testing decisions: 1, 2, 9, 17, 18, 20.

Canonical sections: Core roadmap C4; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-27 — Release tier rework (D182, D186): Release 1. Slimmed to validate and land one change at a time, in submission order. Two things move to ticket [23](23-measure-every-change-delivery-under-steady-and-burst-load.md), On trigger: criterion 1's overlapping cumulative validation within declared lane and window limits, and criterion 3's foreign-rebuild and speculative-redundancy distinctions. Ticket 23 is their home for two reasons: overlap is the landing-throughput fix for the load its trigger names (landing waits that slow the owner down, or more than about five agents), and it already waits for this ticket through ticket [21](21-refresh-live-validation-safely-after-requirement-changes.md). The transparency criterion is added. This grants no implementation, contract-freeze or release approval.
