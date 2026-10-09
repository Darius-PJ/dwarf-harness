# 63: Update graphs from real durable accepted deltas exactly once

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U3 accepted-promotion integration
Blocked by: 22, 23, 62

## What to build

Apply actual durable predecessor-relative promotion deltas once, retaining provenance.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen exact C2/C4 acceptance/reconciliation identities and authoring provenance.

Completes whole U3 capability (ticket [63](63-update-graphs-from-real-durable-accepted-deltas-exactly-once.md))/whole U3. C4 never waits on this ticket.

## Acceptance criteria

- [ ] Actual Core-validated test promotions drive graph updates from the durably accepted predecessor to target; in M+A then M+A+B, B contributes only (M+A)→(M+A+B).
- [ ] Graph identity binds original request/context, derived generation/predecessor and D23 authoring provenance while preserving canonical normalization and captured eligibility inputs.
- [ ] Normal and reconciled acceptance yield identical graph state; same-ID receipt replay, policy refresh, rebuilt generations and repeated recovery never duplicate an effect, while distinct request IDs are not content-coalesced.
- [ ] Intent, too-late cancellation, busy/refused/cancelled/no-change and unknown interrupted outcomes create no accepted graph effect; later mutable scratch content is neither delivered nor implicitly closed.
- [ ] Incremental graph updates agree with full immutable-tip rebuild and record-only replay under real acceptance/recovery and measured workload.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 83, 86, 87, 96.
- Implementation decisions: 1, 2, 3, 4, 32, 38, 40.
- Testing decisions: 1, 2, 9, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U3 integration
- Undercurrent design §§8.2,9,15.1
- Decision log D23/D52/D55–D60/D64–D77

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
