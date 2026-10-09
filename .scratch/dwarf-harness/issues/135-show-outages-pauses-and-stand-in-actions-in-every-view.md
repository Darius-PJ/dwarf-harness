# 135: Show outages, pauses and stand-in actions in every view

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C7 outage projections and commands (D97–D102)
Blocked by: 130, 134

## What to build

Carry outages, each agent's service-loss state and cause, Corps and colony status with paused members and waiting reserved questions, the grouped prompt, stand-in acts, the unrecoverable declaration and the restoration-before-cutover choice to every attached view, filtered for each principal and resolved once across views.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C7 outage projection and command additions, staged on the actual producers: service loss and the router join from ticket [130](130-count-router-pauses-toward-service-loss.md), and the Corps operation, work rules, stand-in and office handover from tickets [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md)–[134](134-hand-over-an-office-after-an-outage.md).
- Base C7 does not wait for these additions; they extend the launcher capabilities from ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md).

## Acceptance criteria

- [ ] Authorized snapshots and events carry outages with their begin and end notices, each agent's service-loss state and cause, and the Corps status (the colony status for a Grand officer) with how many members are paused and how many, and which, reserved questions are waiting. Question text and counts are filtered per principal; the owner as administrator sees all.
- [ ] Two attached terminal views show the same outage notices, grouped prompt and stand-in labels. A choice or command made in one view resolves once across all views, and a stale one is refused.
- [ ] Stand-in acts appear labelled “the owner, acting for” the office in every view and in the restored officer's history.
- [ ] The grouped prompt's choices, stand-in acts, the unrecoverable declaration and the restoration-before-cutover choice are typed, idempotent, revision-checked commands that make no model call.
- [ ] When a single turn switched to another provider handles a reserved question, the question stops counting and leaves the waiting list in every view, while the officer stays in service loss.
- [ ] Viewing, reconnecting or catching up never restores an agent or starts a turn; a reconnecting view recovers from an authorized snapshot and later events.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 166, 168.
- Implementation decisions: 1, 2, 3, 4, 17, 64.
- Testing decisions: 1, 2, 7, 8, 20, 23.
- Fortress acceptance: FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C7 outage-additions deliverable and "Outage projections" prerequisite
- Core design §13.11
- Decision log D100, D101, D102

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
