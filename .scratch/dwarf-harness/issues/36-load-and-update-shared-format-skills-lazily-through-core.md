# 36: Load and update shared-format skills lazily through Core

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C9 skill capability
Blocked by: 28, 29

## What to build

Lazily load and revision-check shared-format skill updates without a separate runtime.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C9 skill/update families and normal file-write permissions.
- No workflow-runtime, GUI or optional router-module dependency.

## Acceptance criteria

- [ ] A session discovers one-level <name>/SKILL.md frontmatter with first-name-match precedence: DwarfHarness-owned, project .agents, global .agents, then explicitly opted-in other-agent roots; real-path duplicates collapse and name collisions are reported.
- [ ] Only names/descriptions enter the startup catalogue. A body is read only on request after rechecking its resolved path against currently permitted roots; a recipe hint grants neither access nor execution authority.
- [ ] Core performs expected-version edits under ordinary permissions/consent. In-place changes affect the discovered file; copy mode copies the complete folder into owned storage, edits the copy and leaves the source unchanged.
- [ ] A harness-mediated edit immediately invalidates the affected index. External changes appear at the next session-start snapshot without polling or a new watcher service.
- [ ] A real terminal/context flow observes the new authorized version and refuses a competing stale edit. Newly created skills use owned storage; discovery does not imply bundled private libraries or compatibility with another harness's executable commands.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 61.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 20.
- Testing decisions: 1, 2, 8, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C9
- Core design §§12.1–12.3

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [97](97-run-pinned-grill-with-docs-as-a-first-action-with-durable-human-question-waits.md) adds the pinned grill-with-docs first-action run, which ordinary discovery and same-name edits from this capability cannot replace once pinned. These extensions add no blocker or criterion to this ticket.
