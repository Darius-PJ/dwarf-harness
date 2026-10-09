# 34: Run a bounded typed recipe from the terminal and evaluate its real acceptance

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C9 recipe/core-and-terminal capability
Blocked by: 28, 29

## What to build

Execute a bounded typed recipe and verify real file/check/connector acceptance through Core.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C9 recipe/acceptance families, including the selected pinned C4/C5 lineage treatment.
- No archive recipes/catalogue input; no C7 or GUI prerequisite for the Core-and-terminal backend.

## Acceptance criteria

- [ ] The terminal inspects and runs normalized versioned/content-addressed recipe JSON, loading built-ins then user overrides by ID without importing a legacy catalogue.
- [ ] Template parameters and intake choices are substituted only as data; missing required values fail visibly. Procedure text cannot become shell interpolation, scripts, hooks, automatic agent birth or extra authority.
- [ ] Acceptance runs only bounded file existence/content checks, configured required-check references or typed connector readback, pinned to exact subject, task, recipe/configuration and evidence identities.
- [ ] Unavailable/unsupported checks never pass. Real C4/C5 lineage, retirement/quarantine and fresh-evidence rules remain in force; recipe authorship cannot create independence or waive required evidence.
- [ ] The run uses an explicit existing agent/context and typed Core commands with current capability, consent and budget checks; terminal receipts expose actual acceptance/failure rather than a model claim.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 58.
- Implementation decisions: 1, 2, 3, 4, 11, 14, 20.
- Testing decisions: 1, 2, 8, 9, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C9
- Core design §§11.1,11.3,13.8
- Admissible validation research §4.7

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
