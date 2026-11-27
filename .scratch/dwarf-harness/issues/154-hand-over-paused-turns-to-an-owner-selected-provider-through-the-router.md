# 154: Hand over paused turns to an owner-selected provider through the router

Status: ready-for-agent
Type: task
Published: 2026-11-27
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R1 provider-awareness/handover capability, split from ticket 39; not R4 automatic tier planning (D182, D186)
Blocked by: 38, 39

## What to build

Once the router runs, pause a bound turn its provider cannot serve and let the owner hand it over, live, to another configured provider using only approved fitting context. This covers provider status at its true scope, one grouped prompt per cause, the handover of a paused turn with its late and incomplete results, source-down fitting, and a notice when the source recovers. Ticket [39](39-pause-and-safely-resume-the-exact-context-on-an-owner-selected-provider.md) moves an idle context between turns without the router; this ticket is the router's part, On trigger with the router tickets (D182).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization. Specific family/scope: for each exact target
- Owner approval of any lossy compaction or exact truncation
- Until Release 1's exit test passes (D182), every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.
- On trigger with the router tickets [37](37-observe-bound-turns-without-changing-the-c3-request.md), [38](38-compile-safe-per-turn-controls-and-the-single-required-d25-module.md), [40](40-publish-scoped-expiring-r11-facts-without-creating-budget-authority.md) and [41](41-qualify-the-complete-r1-path-against-its-static-baseline.md) (D182): scheduled when the owner wants per-turn model routing or live provider limits. The cheaper first response is a fixed model per context, moved between turns through ticket 39, with the spend view of ticket [151](151-see-and-troubleshoot-from-the-terminal.md).

## Scope boundaries

- Use actual-scope ProviderStatus and typed Core notices/choices through the existing local command/event boundary; C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))/GUI mirror those records later and are not prerequisites.
- Preserve canonical history and completed tool effects; source-down fitting uses deterministic stale-tool removal followed only by approved exact earlier-complete-turn truncation.
- R1 provider changes are owner-selected, never automatic failover, switch-back or a hidden paid retry.
- Target preparation, fitting, pricing and the recorded move are ticket 39's; this ticket adds the router's scopes, pauses and live handover. Counting router pauses toward service loss is ticket [130](130-count-router-pauses-toward-service-loss.md)'s.

## Acceptance criteria

- [ ] Classified rate-limit, overloaded, exhausted, denied and unreachable evidence updates only its true provider/account/model/region scope. Local agent/task budget exhaustion remains RouteBlocked for intersecting scopes and never marks the entire provider unavailable.
- [ ] Short configured waits need no disruptive prompt but never resend automatically. Otherwise pause the bound turn. The owner gets one prompt per cause listing every turn it paused, with agent/context, cause/scope and known resume time: wait or stop is chosen once for all of them with a per-turn override, while each turn keeps its own configured-target and allowed-compaction choices with dropped-artifact information and first-target maximum exposure. A turn the same cause pauses later joins the prompt; an earlier wait covers it and stop applies only to the turns shown. Choice resolution is once-only per turn; stale choices or changed view focus cannot retarget work.
- [ ] A paused turn's target is prepared as ticket 39 prepares a moved context. In addition, late or uncertain source results remain bound to the initiating context, and incomplete stream content is excluded or completed without erasing audit history or blindly repeating effects.
- [ ] If the source is unavailable and the target still cannot fit after deterministic stale-tool removal, send only the exact owner-approved truncation of earlier complete turns; preserve the full source history. If no approved excerpt fits, remain blocked—no full-history target summary, chunk-summary cascade or implicit floor reduction.
- [ ] Each summary, compaction or target call has its own sealed quote, reservation and durable intent; summary/compaction inputs must themselves be bounded authorized excerpts. Source unavailability, a warm cache or a user preference never creates a finite bound for an unsupported target.
- [ ] Announce source recovery once with switch-back cost but never change back automatically. Exercise mid-turn source failure with real adapters/controlled evidence and verify another agent on the same account remains eligible after only one agent's cap is exhausted.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 31, 66, 69, 72, 76, 166.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 12, 23, 24, 25, 28, 64.
- Testing decisions: 1, 2, 8, 11, 20, 23.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R1
- Routing design §§5.4 R9/R10,6.4–6.5,8.5,9.8,10–11,13
- Core design §4.1
- Decision log D4,B9–B10,D100

## Comments

2026-11-27 — Published by the owner's decision, split from ticket [39](39-pause-and-safely-resume-the-exact-context-on-an-owner-selected-provider.md). The release tiers keep the owner's move of a context to another configured provider between turns, without the router, in Release 2, and leave the router's live handover for later (D182), and he approved its publication (D186). Its criteria are ticket 39's provider-status, pause-and-prompt, live-handover, source-down, summary and recovery criteria as last reworked under D102. Release tier: On trigger, with the router. This grants no implementation, contract-freeze or release approval; the start and authority gates above stand.
