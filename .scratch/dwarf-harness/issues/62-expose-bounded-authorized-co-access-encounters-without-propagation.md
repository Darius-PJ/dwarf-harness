# 62: Expose bounded authorized co-access encounters without propagation

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U3 graph-foundation completion
Blocked by: 61

## What to build

Expose authorized bounded co-access hints without turning encounters into causal evidence.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen host-use/task-provenance and grant fields; no synthetic producer schema invented by the consumer.

Completes U3 graph foundation (ticket [62](62-expose-bounded-authorized-co-access-encounters-without-propagation.md)) with ticket [61](61-turn-bounded-artifact-and-usage-evidence-into-atomic-causal-graphs.md); unblocks U5 without C4 promotion integration.

## Acceptance criteria

- [ ] Only qualifying host-observed same-task-context use forms context-local encounter hints; sensing/rendering/enumeration/recursive encounter reads do not create observations.
- [ ] The full GRAPH-ENCOUNTER 30-day/two-group/64-identity/eight-neighbor rules produce deterministic authorized neighborhoods, with endpoint authority applied before ranking.
- [ ] Duplicate/correlated observations cannot increase support; saturation or expiry with incomplete identity history omits the edge until bounded authoritative reconstruction commits, never guesses an exact count.
- [ ] Encounter edges never carry propagation, validation evidence or support and never widen a grant; hidden endpoints cannot affect visible order/count/timing.
- [ ] Restart/replay and bounded rebuild reconstruct the same neighborhood or explicit unknown, retaining cumulative work/exhaustion behavior.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### GRAPH-ENCOUNTER

Rebuild at load, after 100 accepted structural changes, analyzer/configuration epoch change or version/digest mismatch. Co-access: one observation/pair/task-provenance group in rolling 30 days; require two groups; retain at most 64 identities and expose only at_least_64 at saturation; at most eight neighbors in deterministic support/last-use/NodeKey order. Incomplete identity history after saturation/expiry requires bounded authoritative rebuild or omitted unknown edge.

Source: Undercurrent design §8.2; Parent specification implementation 38–39.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 86, 95, 98.
- Implementation decisions: 1, 2, 3, 4, 38, 39, 42.
- Testing decisions: 1, 2, 4, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U3
- Undercurrent design §§8.2,10.3,13.7,14.1–14.2

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-24 — Consolidation cross-link: Ticket [139](139-register-shared-resources-and-work-domains-through-core.md) produces registered shared resources, and Undercurrent §8.2 admits co-access from host-observed use of two registered resources by one task context. It reaches this ticket through ticket [61](61-turn-bounded-artifact-and-usage-evidence-into-atomic-causal-graphs.md), which it now blocks ([D142](../../../docs/DECISIONS.md#d142-tickets-139-and-140-published-with-three-blocker-changes)). These extensions add no blocker or criterion to this ticket.
