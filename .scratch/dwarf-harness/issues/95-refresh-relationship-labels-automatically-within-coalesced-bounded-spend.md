# 95: Refresh relationship labels automatically within coalesced bounded spend

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C6 relationship refresh lifecycle (D92–D93)
Blocked by: 94

## What to build

Reclassify a directed pair only when relevant eligible evidence changes, coalescing bursts under spending bounds, while consumers see still-authorized prior labels marked outdated or an explained unknown.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen refresh-validity records in the C6 annotation family; ordinary BudgetBook admission for every attempt.

## Acceptance criteria

- [ ] A relevant eligible-evidence change marks the affected directed pair dirty and coalesces into bounded, admitted refresh attempts; several changes before dispatch produce one attempt.
- [ ] No view open, hover, repaint, map move, CLI attach, inspection or reconnect starts a classification, and no speculative sweep classifies every colony pair.
- [ ] While a refresh is pending, the last still-authorized label is returned marked outdated/updating, never as current; with no usable label the result is unknown with its reason.
- [ ] Revoked or newly inaccessible evidence is removed from retention, display and future input; a label that depended on it is withdrawn rather than shown as outdated.
- [ ] Busy, refused or failed attempts stay visible, earn no free retry and leave the prior state honest; restart preserves dirty and pending positions without duplicate spending.
- [ ] Actual calls, tokens and cost of refresh are measured under a declared relationship-change workload, and repeated inspection costs nothing.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 140.
- Implementation decisions: 1, 2, 3, 4, 12, 15, 59.
- Testing decisions: 1, 2, 8, 11, 18, 20, 21.
- Fortress acceptance: FG07.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 "Relationship projections/classification" and D89–D96 producer acceptance
- Fortress GUI spec §5
- Decision log D92, D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
