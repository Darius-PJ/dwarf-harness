# 40: Publish scoped expiring R11 facts without creating budget authority

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R1 observation capability; not whole R1
Blocked by: 37

## What to build

Publish authorized true-scope headroom, availability and accounting facts that expire to unknown.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen C1 ledger-format, ledger-reader and ledger-appender, including the actual R1 provider/headroom/budget-exposure/usage family. No law-format/U1 dependency.
- Each ticket owns the specification of its real producer payloads and golden examples; C1 records the additive family freeze, authorized callers, consumers, privacy and replay-essential fields. Pause before consuming/emitting an unfrozen family. No separate schema-only ticket or placeholder payload. Specific family/scope: for R1 measurements
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- Deliver status transitions, router-headroom/v1, configured budget-exposure threshold crossings and per-attempt usage attribution through the sole Core appender.
- Use Core BudgetBook views; no diagnostic-JSONL replay authority, new money ledger, field callback, refresh probe or U1.L law capability (ticket [04](04-load-and-identify-a-strict-law-independently-of-field-execution.md)) dependency.
- Field owns the later authorized projection/normalization and does not obtain control over routes, budgets or retries.

## Acceptance criteria

- [ ] Durably publish each qualifying fact at observation time with true provider/account/model/region/metric and originating-context scope; status remains distinct from local budget exposure. Unknown financial holds remain held even when a field series expires or is replaced.
- [ ] Normalize only supported requests/tokens/input_tokens/output_tokens integers in [0,2^63−1], requiring positive limit; missing/malformed/negative/overflowing values are unknown. Clamp observational remaining to limit only, never financial usage. The exact empty/critical/low/constrained/ample boundaries use the specified integer comparisons.
- [ ] Expiry is min(valid reset, observation+15 minutes), or observation+15 minutes without reset; malformed reset or nonfuture expiry is unknown. Replay/consumers treat expired evidence as unknown without fabricating a provider measurement.
- [ ] Compare with the last durably published same-scope sample. Publish band/knownness changes, qualifying five-minute extensions and fresh same-band evidence after short expiry. Publish a formerly supported metric's missing/unknown withdrawal once, not a stream of repeated unknowns.
- [ ] Keep bounded latest measurement state and piggyback qualifying facts on existing commits; do not persist every raw header or make a model/provider refresh call. Local exposure includes admission-period settlements plus every open hold across periods, deduplicated by attempt, without child/parent rollup double-counting.
- [ ] Exact counts, attribution and amounts remain inside current authorized source/accounting scope. The handoff for other audiences exposes only explicitly authorized coarse band/freshness, with no private task/path/consumer/account/provider identifier; field expiry cannot clear Router blocks, authorize spending or schedule retries.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 31, 32, 69, 70, 72.
- Implementation decisions: 1, 2, 3, 4, 6, 12, 24, 25.
- Testing decisions: 1, 2, 4, 11, 12, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R1
- Routing design §§5.4–5.5,6.4,11
- Contract register R1 producer family
- Decision log B9–B10,B13

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
