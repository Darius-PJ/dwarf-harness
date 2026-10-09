# 94: Classify A-about-B relationships in the nine-region grid from eligible evidence

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C6 perspective-bound trust/sentiment annotation (D90–D92, D95)
Blocked by: 93

## What to build

Produce for each directed pair a validated trust/sentiment classification and one of nine labels, or unknown, from evidence only the named agent is privy to, through Core's actual configured producer.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Specify the rubric, input window, numeric encoding, sentiment normalization and grid cutpoints, with D78 cost reasoning, before the annotation family is frozen with C1; B16's six-word rubric is not the contract.
- Actual configured producer under ordinary BudgetBook admission; an external helper additionally needs its R5/C10 qualification (ticket [52](52-run-an-external-routing-helper-only-inside-a-qualified-finite-compartment.md)).
- Owner-frozen C6 relationship and annotation families.

## Acceptance criteria

- [ ] Trust is signed and normalized (-1 distrust, 0 neutral, +1 trust) and sentiment uses the specified encoding; labels map to the adopted grid (negative: Hostile, Strained, Grudging; neutral: Wary, Neutral, Trusted; positive: Hopeful, Friendly, Bonded across low/medium/high trust), with unknown outside it, partial axes explicit and no fabricated midpoint.
- [ ] Each result binds agent, counterpart, context, membership scope, immutable evidence versions, input window, classifier and rubric version and freshness; malformed values or words outside the vocabulary are rejected, never shown as classifier prose.
- [ ] Inputs contain only evidence A is privy to, including failed tasks A knows of; administrator-only facts, B's private memory and hidden task failures never enter A's input, even when an administrator can see them.
- [ ] A-about-B and B-about-A are produced and stored independently; rank, clothes, proximity, dog presence, model choice and independent performance telemetry are never inputs.
- [ ] Calibration on real eligible evidence exercises every grid region plus unknown, partial and contradictory evidence and records the evidence behind the selected cutpoints.
- [ ] Every classification is a real admitted and accounted Core attempt whose usage is attributed once.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 140.
- Implementation decisions: 1, 2, 3, 4, 12, 15, 59.
- Testing decisions: 1, 2, 8, 11, 20, 21.
- Fortress acceptance: FG07.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 "Relationship projections/classification" and D89–D96 producer acceptance
- Core design §13.6
- Fortress GUI spec §5
- Decision log D91, D92, D95

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
