# 39: Pause and safely resume the exact context on an owner-selected provider

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R1 provider-awareness/handover capability; not R4 automatic tier planning
Blocked by: 38

## What to build

Pause/resume an explicitly selected provider handover using only approved fitting context.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization. Specific family/scope: for each exact target
- Owner approval of any lossy compaction or exact truncation
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- Use actual-scope ProviderStatus and typed Core notices/choices through the existing local command/event boundary; C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))/GUI mirror those records later and are not prerequisites.
- Preserve canonical history and completed tool effects; source-down fitting uses deterministic stale-tool removal followed only by approved exact earlier-complete-turn truncation.
- R1 provider changes are owner-selected, never automatic failover, switch-back or a hidden paid retry.

## Acceptance criteria

- [ ] Classified rate-limit, overloaded, exhausted, denied and unreachable evidence updates only its true provider/account/model/region scope. Local agent/task budget exhaustion remains RouteBlocked for intersecting scopes and never marks the entire provider unavailable.
- [ ] Short configured waits need no disruptive prompt but never resend automatically. Otherwise pause the bound turn. The owner gets one prompt per cause listing every turn it paused, with agent/context, cause/scope and known resume time: wait or stop is chosen once for all of them with a per-turn override, while each turn keeps its own configured-target and allowed-compaction choices with dropped-artifact information and first-target maximum exposure. A turn the same cause pauses later joins the prompt; an earlier wait covers it and stop applies only to the turns shown. Choice resolution is once-only per turn; stale choices or changed view focus cannot retarget work.
- [ ] Prepare the target from that context's canonical records with matched tool-call/result pairs, retained completed effects, compatible portable content, translated IDs and an explicit dropped-artifact report. Late or uncertain source results remain bound to the initiating context; incomplete stream content is excluded or completed without erasing audit history or blindly repeating effects.
- [ ] Remove stale tool payloads deterministically. If the source is unavailable and target still cannot fit, send only the exact owner-approved truncation of earlier complete turns; preserve the full source history. If no approved excerpt fits, remain blocked—no full-history target summary, chunk-summary cascade or implicit floor reduction.
- [ ] Each summary, compaction or target call has its own sealed quote, reservation and durable intent; summary/compaction inputs must themselves be bounded authorized excerpts. Source unavailability, a warm cache or a user preference never creates a finite bound for an unsupported target.
- [ ] Retain each context's source/target cache records, price the actual target state and record the transition and settled cost. Announce source recovery once with switch-back cost but never change back automatically. Exercise mid-turn source failure with real adapters/controlled evidence and verify another agent on the same account remains eligible after only one agent's cap is exhausted.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 26, 31, 32, 66, 69, 72, 76, 166.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 12, 23, 24, 25, 28, 64.
- Testing decisions: 1, 2, 8, 11, 20, 23.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R1
- Routing design §§5.4 R9/R10,6.4–6.5,8.5,9.8,10–11,13
- Core design §4.1
- Decision log D4,B9–B10,D100

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D102 rework: criterion 2 now gives the owner one wait-or-stop prompt per cause ([D100](../../../docs/DECISIONS.md#d100-officer-service-loss-proposal-cap-corps-status-grouped-prompts-and-open-tasks)), with per-turn overrides, per-turn switch and compaction choices, and later-paused turns joining it. Number and blockers are unchanged; ticket [130](130-count-router-pauses-toward-service-loss.md) connects the router's pause to Core service loss. This grants no separate implementation, contract-freeze or release approval.
