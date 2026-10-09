# 55: Inspect authorized operations and mutate a task through the real Core

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: GUI-C operational projection
Blocked by: 25, 27, 54

## What to build

Operate authorized tasks, grants, budgets and promotions without leaking through alternate UI paths.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen projection/command families for consumed producers.
- Optional R1/U6 projections stay explicitly unknown/unavailable when absent; do not add whole-router/field dependencies. Their actual native handoff proof belongs with the available producer.

## Acceptance criteria

- [ ] The native overview shows only authorized colony/Corps/role/agent/context topology in a textual tree/list and bounded operational panes; one execution session and its private contexts remain distinct.
- [ ] A task panel exposes authorized description, acceptance criteria, version, exact subject/evidence, dependencies/state and blocked reason. A real version-checked action changes Core state or displays its actual stale/permission refusal without optimistic completion.
- [ ] Grant/approval and BudgetBook/resource views show scope, ceilings, expiry/revocation, available/reserved/charged amounts, units and revision/time basis. Missing authorization or unavailable headroom/hints is never inferred as allowance.
- [ ] The promotion train shows durable order, the caller's permitted requests/receipts, readiness and generic pending when sealed. A ref move or speculative pass is not acceptance; foreign paths, hashes, logs, checks, identities and failure verdicts remain sealed across every UI route.
- [ ] Authorization precedes render/cache/search/reconnect/notification/accessibility announcement/clipboard/export/attachment access; revoke or switch principal/context during those paths and observe no forbidden content, count or existence disclosure.
- [ ] Queues, graphs, diagnostics and previews honor row/byte/page limits with authorized pagination/virtualization. Live refresh preserves focus, selection and reader position, and inert-text rules and keyboard/status semantics remain intact under load.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 117, 118, 124, 127, 128.
- Implementation decisions: 1, 2, 3, 4, 11, 14, 52, 54, 55.
- Testing decisions: 1, 2, 8, 9, 15, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- GUI roadmap colony overview, work panels, GUI-C

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [113](113-open-rpg-agent-and-corps-party-sheets-with-real-scoped-statistics.md) links fortress party sheets to these panels, and ticket [119](119-browse-scoped-memories-and-administrator-views-through-checked-paths.md) verifies GUI-C's administrator inspection on them. These extensions add no blocker or criterion to this ticket.
