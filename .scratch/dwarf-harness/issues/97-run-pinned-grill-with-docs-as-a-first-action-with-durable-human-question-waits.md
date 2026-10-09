# 97: Run pinned grill-with-docs as a first action with durable human-question waits

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C9 backend pinned first-action alignment capability (Core §12.4)
Blocked by: 35, 36

## What to build

Execute the actual grill-with-docs skill, pinned by identity, version and content, as a durable run bound to an agent, a Purpose revision and its domain, yielding at human questions and resuming only on explicit answers.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C9 skill/workflow families including the pinned-run capability; no C8 or GUI dependency.
- Record the pinned skill's source, licence and content hash in DwarfHarness-owned skill storage.
- Actual provider credentials and ordinary grants/budgets for the paid turns.

## Acceptance criteria

- [ ] A pinned run binds skill identity, version and content hash, the target agent and context, a Purpose revision reference and the work or values domain; ordinary discovery or a same-name skill edit cannot silently replace an in-progress pin.
- [ ] The first interview turn is an ordinary admitted paid attempt; its questions then wait durably for a human answer, releasing the context and active-turn slot without unattended follow-up rounds.
- [ ] An explicit answer resumes the same run through the single continuation owner; restart, a second terminal view or reattach observes the existing run and creates no duplicate first turn or spending.
- [ ] Neither a model's completion claim, an empty question frontier nor loop exhaustion marks alignment accepted; the run reports progress, and acceptance stays a separate human decision bound later by C8.
- [ ] The run is exercised from a real terminal against the actual Core with ordinary grants and reservations, including a refused admission shown explicitly.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 155, 156, 157, 159.
- Implementation decisions: 1, 2, 3, 4, 10, 18, 20.
- Testing decisions: 1, 2, 8, 20, 22.
- Fortress acceptance: FG17.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C9 "Pinned alignment capability" and backend exit criterion
- Core design §§12.1–12.4, 13.8
- Decision log D91–D93

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
