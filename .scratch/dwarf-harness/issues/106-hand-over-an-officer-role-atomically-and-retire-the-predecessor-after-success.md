# 106: Hand over an officer role atomically and retire the predecessor after success

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 officer cutover and retirement (D93–D94)
Blocked by: 91, 105

## What to build

Cut a ready Aspirant into the officer role through one reviewed Core transition that hands over the office's messages, decisions and open tasks, then retire the predecessor. Service loss of either party pauses the succession until restoration.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C8 succession family; the service-loss state from ticket [91](91-record-service-loss-and-restoration-for-every-agent.md).

## Acceptance criteria

- [ ] A reviewed cutover transfers the role atomically: exactly one holder exists before, during and after, and replay never yields zero or two holders or repeats handover effects.
- [ ] The predecessor retires only after successful handover, keeping its D83 read-only delivered history and private memories; it does not become an adviser or take another role automatically.
- [ ] Incumbent service loss pauses the succession at its last saved step until restoration; Aspirant service loss does the same while the Incumbent stays the sole holder, and the owner can cancel the succession at any time. There is no timeout promotion, waived handoff or failure-as-success here; only the owner's unrecoverable declaration in ticket [134](134-hand-over-an-office-after-an-outage.md) lets an Aspirant take over without the live handoff.
- [ ] At every cutover, the outgoing officer's unanswered messages pass to the new holder with their original sender, marked as passed on, and each sender is told at its next naturally scheduled turn. Pending decisions, task proposals and the owner's decisions for the office pass too, but no conversation history or private memory.
- [ ] The outgoing officer's open tasks are listed in the cutover review and start under the new holder in a fresh private context. Unsubmitted changes in the outgoing officer's private workspace do not carry over and are never submitted automatically.
- [ ] A GUI or terminal detach does not stop Core succession, and the administrator can inspect predecessor records without counterpart-private memory reaching the successor.
- [ ] Stale or competing cutover commands are refused by expected version, and retries return the recorded outcome.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 160, 162, 168.
- Implementation decisions: 1, 2, 3, 4, 16, 18, 64.
- Testing decisions: 1, 2, 8, 20, 22, 23.
- Fortress acceptance: FG20, FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C8 "Officer recovery and retirement" and exit criterion
- Core design §§7.1, 13.10, 13.11
- Decision log D93, D94, D99, D100

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D102 rework: succession now follows Core design §13.11. Incumbent or Aspirant service loss pauses it at its last saved step, and every cutover hands over unanswered messages, pending decisions, proposals, the owner's decisions for the office and open tasks in a fresh private context, never submitting unsubmitted changes automatically ([D99](../../../docs/DECISIONS.md#d99-officer-service-loss-outage-work-rules-owner-stand-in-and-office-handover), [D100](../../../docs/DECISIONS.md#d100-officer-service-loss-proposal-cap-corps-status-grouped-prompts-and-open-tasks)). Number and blockers are unchanged; ticket [134](134-hand-over-an-office-after-an-outage.md) adds handover after an unrecoverable declaration. This grants no separate implementation, contract-freeze or release approval.
