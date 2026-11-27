# 39: Pause and safely resume the exact context on an owner-selected provider

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R1 owner-selected provider move between turns, on Core's static paid path without the router (D182, D186); not R4 automatic tier planning
Blocked by: 150

## What to build

Let the owner move one context to another configured provider and model between turns, without the router, using only approved fitting context. Core prepares the target from that context's canonical records, prices it and records the move, and the context's next turn runs there; nothing moves automatically, mid-turn or back. The router's handover of paused turns belongs to ticket [154](154-hand-over-paused-turns-to-an-owner-selected-provider-through-the-router.md).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization. Specific family/scope: for each exact target
- Owner approval of any lossy compaction or exact truncation
- Until Release 1's exit test passes (D182), every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.
- No router dependency: the move runs on Core's static paid path (tickets [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md), [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md) and [15](15-run-bounded-context-bound-model-and-tool-turns.md)), with the providers, routes, credentials and ceilings the owner configures through ticket [150](150-set-up-a-colony-from-the-terminal.md).

## Scope boundaries

- Use typed Core notices and choices through the existing local command/event boundary; C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))/GUI mirror those records later and are not prerequisites.
- Between turns only. Provider-status scopes, the router's pauses and grouped prompt, the handover of a paused turn, source-down fitting, summary and compaction calls and the source-recovery notice belong to ticket [154](154-hand-over-paused-turns-to-an-owner-selected-provider-through-the-router.md).
- Preserve canonical history and completed tool effects; fitting uses deterministic stale-tool removal followed only by approved exact earlier-complete-turn truncation, with no summary or compaction call.
- Provider moves are owner-selected, never automatic failover, switch-back or a hidden paid retry.

## Acceptance criteria

- [ ] From the terminal, the owner moves one context to another configured provider and model between turns. The move is a revision-checked Core command bound to that context: a repeated command returns its receipt, and a context with an active turn or an unresolved paid attempt refuses it with that cause. A crash leaves the context on its source or its target, and no other context moves.
- [ ] Prepare the target from that context's canonical records with matched tool-call/result pairs, retained completed effects, compatible portable content, translated IDs and an explicit dropped-artifact report, which the owner sees before the move takes effect.
- [ ] Remove stale tool payloads deterministically. If the target still cannot fit, send only the exact owner-approved truncation of earlier complete turns; preserve the full source history. If no approved excerpt fits, refuse the move with that cause: no target summary, chunk-summary cascade or implicit floor reduction.
- [ ] Each target call has its own sealed quote, reservation and durable intent within the target's configured ceilings. A warm cache or a user preference never creates a finite bound for an unsupported target.
- [ ] Retain each context's source/target cache records, price the actual target state and record the move and its settled cost. Nothing moves back automatically; moving back is another owner move.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 26, 32, 66, 69, 76.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 12, 23, 24, 28.
- Testing decisions: 1, 2, 11, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R1 owner-selected provider changes
- Routing design §§6.4,8.5,9.8,11
- Core design §4.1
- Decision log D4,B9–B10,D182

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D102 rework: criterion 2 now gives the owner one wait-or-stop prompt per cause ([D100](../../../docs/DECISIONS.md#d100-officer-service-loss-proposal-cap-corps-status-grouped-prompts-and-open-tasks)), with per-turn overrides, per-turn switch and compaction choices, and later-paused turns joining it. Number and blockers are unchanged; ticket [130](130-count-router-pauses-toward-service-loss.md) connects the router's pause to Core service loss. This grants no separate implementation, contract-freeze or release approval.

2026-11-27 — Release tier rework (D182, D186): Release 2. By the owner's decision the ticket is split. It keeps the owner's move of an idle context to another configured provider between turns, without the router. Provider-status scopes, the router's pauses and grouped prompt, the live and source-down handover, summary and compaction calls and the source-recovery notice move to ticket [154](154-hand-over-paused-turns-to-an-owner-selected-provider-through-the-router.md), On trigger with the router tickets. Its number and title are unchanged, and the title now covers resuming the exact context on an owner-selected provider between turns; pausing turns belongs to ticket 154. Its roadmap capability, What to build, gates, scope, criteria and specification coverage are rewritten to match, with the transparency criterion added, and its third gate now ends testing-only work at Release 1's exit test (D182). Tickets [41](41-qualify-the-complete-r1-path-against-its-static-baseline.md) and [130](130-count-router-pauses-toward-service-loss.md) wait on ticket 154 instead of this ticket. Its blockers were 38; they are now 150, because the move runs on Core's resolved static paid path (tickets [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md)–[15](15-run-bounded-context-bound-model-and-tool-turns.md)) and needs the target provider configured from the terminal by ticket [150](150-set-up-a-colony-from-the-terminal.md), not the router. This grants no implementation, contract-freeze or release approval.
