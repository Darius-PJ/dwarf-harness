# 87: Specify the ordinary officer connection-loss and service-restoration policy

Status: resolved
Type: grilling
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C3 ordinary officer service-recovery specification (D94), consumed by C8 succession
Blocked by: None (can start immediately)

## What to build

Settle with the owner what Core does when an Orchestrator, Griot or Diplomat loses connection or service during ordinary operation, and what evidence establishes restoration, so that succession can defer to that ordinary procedure.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner interview: the owner decides the policy semantics. The interviewer proposes options and records answers; it does not select policy on the owner's behalf.
- Documentation outcome only: no implementation, contract freeze, D52 approval or roadmap-status change.
- Feeds ticket [91](91-record-service-loss-and-restoration-for-every-agent.md), the implementation, and through it ticket [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md)'s succession recovery.

## Acceptance criteria

- [x] The questions cover what counts as officer connection or service loss versus an ordinary interrupted turn; what pauses or continues for the officer's Corps, tasks, messages and spending holds; who is notified; and what evidence establishes authoritative restoration.
- [x] The recorded policy keeps D94's limits: no emergency successor promotion, timeout takeover, waived handoff or GUI bypass, and neither a connection failure nor an animation ever counts as a successful handover.
- [x] A new decision-log entry records the owner's answers with its originator under the canonical project instructions, and the Core design and roadmap name the policy where C3 and C8 consume it; generic interrupted-turn recovery is not presented as this policy.
- [x] Ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s acceptance criteria are completed from the decision, and its status moves from needs-info only when it is fully specified.
- [x] Branches the owner leaves open are recorded as explicit open questions rather than invented mechanisms.

## Verification boundary

The deliverable is the owner's recorded decision and the updated producer documentation. No implementation, test result or runtime evidence is claimed or established by this ticket.

## Specification coverage

- User stories: 160, 162.
- Implementation decisions: 1, 2, 3, 4, 10, 18.
- Testing decisions: 1, 2, 20, 22.
- Fortress acceptance: FG20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 "Ordinary officer service recovery (D94)" and C8 "Officer recovery and retirement"
- Core design §13.10
- Decision log D93, D94, D96

## Answer

The owner specified the ordinary procedure in four interview rounds (Q48–Q79), confirmed the shared understanding, and answered Q80 when he approved the ticket landing. [D97](../../../docs/DECISIONS.md#d97-officer-service-loss-causes-onset-held-questions-and-restoration-evidence), [D98](../../../docs/DECISIONS.md#d98-officer-service-loss-corps-operation-owner-limits-owner-stand-in-and-unrecoverable-officers), [D99](../../../docs/DECISIONS.md#d99-officer-service-loss-outage-work-rules-owner-stand-in-and-office-handover), [D100](../../../docs/DECISIONS.md#d100-officer-service-loss-proposal-cap-corps-status-grouped-prompts-and-open-tasks), [D101](../../../docs/DECISIONS.md#d101-officer-service-loss-procedure-confirmed-for-consolidation) and [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) record it, and Core design §13.11 states it.

- **Service loss** covers every agent stopped by a classified provider failure, an exhausted owner money limit, an execution failure or, once such hosts exist, lost contact with an out-of-process host. It begins after a 2-minute grace counted from the router's pause (from the first refused call until the router exists), or at once for causes only the owner can clear. It ends only with the agent's first completed call after the cause clears or stops applying. No probe or model call is added.
- **The owner** gets one notice when an outage begins and one when it ends, one wait-or-stop prompt per cause, and a Corps status showing paused members and waiting questions.
- **A lost officer's Corps keeps working.** Further questions become reserved questions counted against an allotment of three, and a member at its allotment pauses. Work stays within existing tasks plus at most three task proposals per member. Core's Service Restoration message releases members in step.
- **The owner may stand in** for the office under his own label. He may also declare an officer unrecoverable so that a prepared, aligned Aspirant takes over without the live handoff; this is the one change to D94's limits, made by the owner in [D98](../../../docs/DECISIONS.md#d98-officer-service-loss-corps-operation-owner-limits-owner-stand-in-and-unrecoverable-officers). Emergency or timeout promotion and GUI bypass stay prohibited, and neither a connection failure nor an animation counts as a handover.
- **Every handover** passes unanswered messages, pending decisions, proposals, the owner's decisions for the office and open tasks.
- **Open producer work:** record encodings, and calibration of the allotment, the proposal cap and restart bursts at the workload gate. The owner left no interview branch open.

Ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) now carries the Core service-loss criteria. Under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing), tickets [39](39-pause-and-safely-resume-the-exact-context-on-an-owner-selected-provider.md) and [106](106-hand-over-an-officer-role-atomically-and-retire-the-predecessor-after-success.md) were reworked and tickets [130](130-count-router-pauses-toward-service-loss.md)–[136](136-show-outages-and-stand-in-for-an-office-from-the-gui.md) published.

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Claimed for the owner interview, which began today. Its questions continue the fortress interview numbering from Q48.

2026-10-08 — Round 1 (Q48–Q54) answered and recorded in [D97](../../../docs/DECISIONS.md#d97-officer-service-loss-causes-onset-held-questions-and-restoration-evidence); round 2 asks Q55–Q65.

2026-10-08 — Round 2 (Q55–Q65) answered and recorded in [D98](../../../docs/DECISIONS.md#d98-officer-service-loss-corps-operation-owner-limits-owner-stand-in-and-unrecoverable-officers); round 3 asks Q66–Q75.

2026-10-08 — Round 3 (Q66–Q75) answered and recorded in [D99](../../../docs/DECISIONS.md#d99-officer-service-loss-outage-work-rules-owner-stand-in-and-office-handover); round 4 asks Q76–Q79.

2026-10-08 — Round 4 (Q76–Q79) answered and recorded in [D100](../../../docs/DECISIONS.md#d100-officer-service-loss-proposal-cap-corps-status-grouped-prompts-and-open-tasks). No interview branches remain; the procedure summary went to the owner for confirmation of shared understanding.

2026-10-08 — Resolved: the owner confirmed the procedure in [D101](../../../docs/DECISIONS.md#d101-officer-service-loss-procedure-confirmed-for-consolidation) and approved its landing in [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing). The decision log, Core design §13.11, the roadmaps, the contract register and both specifications record it. This tracker has no map to update. Resolving this ticket grants no implementation, contract-freeze, release or study approval.
