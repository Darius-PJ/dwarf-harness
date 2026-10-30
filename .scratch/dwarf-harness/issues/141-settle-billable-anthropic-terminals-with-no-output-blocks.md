# 141: Settle billable Anthropic terminals with no output blocks

Status: ready-for-agent
Type: task
Published: 2026-10-30
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 paid-attempt settlement; ticket 14 S2 review note 1
Blocked by: None (can start immediately)

## What to build

Let a billable, non-refusal Anthropic terminal with complete usage and zero output blocks record a nil message-object list and settle through the existing Core paid path. Today an empty but non-nil list makes result recording refuse, leaving the full hold despite complete billable evidence.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Preserve the existing frozen F3-13 rules: the producer supplies the valid nil list when there are no message objects; do not relax the record reader to accept the invalid empty-list encoding.
- Use complete controlled Anthropic evidence; no live call, credential request or new paid authorization is needed for this regression.

## Scope boundaries

Fix only the zero-output-block billable-result path and its regression. Preserve nonempty output, refusal/no-charge, unknown-hold, correction and OpenAI behavior. Do not change the frozen schema, record validation or goldens.

## Acceptance criteria

- [ ] A billable non-refusal Anthropic terminal with complete usage and zero output blocks records a nil message-object list, settles the exact observed charge once and releases the hold without inventing output.
- [ ] Duplicate result recording returns the original settlement without another charge or release; reopening and replay preserve the same result, amount, empty output and released hold.
- [ ] Frozen F3-13 rules, prior record bytes and all existing goldens remain unchanged; existing nonempty Anthropic output and OpenAI settlement proofs stay green.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 69, 70.
- Implementation decisions: 6, 10, 12, 23, 24.
- Testing decisions: 1, 2, 8, 11.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 paid-attempt settlement and recovery
- Routing design hard local BudgetBook and final usage accounting
- F3-13 paid-result producer rules

## Comments

2026-10-30 — Published with ticket [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md)'s close from its S2 review note 1. The PM carried the pre-existing Anthropic defect as a separate bounded fix rather than widening the OpenAI slice. Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.
