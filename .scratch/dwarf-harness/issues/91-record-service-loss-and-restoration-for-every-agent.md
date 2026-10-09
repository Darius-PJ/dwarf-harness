# 91: Record service loss and restoration for every agent

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C3 service-loss state for every agent (D97–D102), consumed by R1, C7 and C8
Blocked by: 16, 87

## What to build

Give Core one service-loss state for every agent. Core records when a classified provider failure, an exhausted owner money limit or an execution failure stops an agent's required work, groups the affected agents by cause into outages, tells the owner when each outage begins and ends, and ends an agent's service loss only with its first completed call after the cause clears or stops applying.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C3 runtime families for service-loss and outage state; no R1, C6, C8, C9 or GUI dependency. Classified provider refusals come from the adapters of tickets [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md) and [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md), and execution failures from ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md)'s restart and child-shutdown proofs.
- The procedure is specified in [D97](../../../docs/DECISIONS.md#d97-officer-service-loss-causes-onset-held-questions-and-restoration-evidence)–[D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) and Core design §13.11, from the owner interview in ticket [87](87-specify-the-ordinary-officer-connection-loss-and-service-restoration-policy.md).
- Live-provider evidence requires the owner's credentials and authorization; controlled refusals and failures exercise every cause.

## Scope boundaries

- Lost contact with an out-of-process agent host becomes a cause when such hosts exist; this ticket covers the three causes Core can observe now.
- The router's pause and grouped prompt join through ticket [130](130-count-router-pauses-toward-service-loss.md). Officer consequences (reserved questions, allotment pauses, outage work rules, owner stand-in and office handover) belong to tickets [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md)–[134](134-hand-over-an-office-after-an-outage.md), and the shared view projections to ticket [135](135-show-outages-pauses-and-stand-in-actions-in-every-view.md).
- No heartbeat, probe or model call detects or ends service loss, and nothing is retried automatically.

## Acceptance criteria

- [ ] Service loss is recorded with its cause for a provider failure classified at its true scope (rate-limited, overloaded, unreachable, denied or exhausted, including the provider account's own spend limit), an exhausted owner money limit that stops all of the agent's paid calls (its own, its provider account's or the colony's), or an execution failure (descendant processes not confirmed stopped, or private state failing its restart integrity check).
- [ ] Being idle, busy or awaiting a reply, a turn interrupted by a restart, a detached or closed view, a stopped colony host, a task or grant budget, unset spending authorization and revoked grants never count as service loss.
- [ ] Until the router exists, onset follows a 2-minute grace counted from the agent's first refused call. Causes only the owner can clear count at once: refused credentials, provider quota or an owner money limit exhausted without a stated reset, locked-off processes and failed integrity checks. An idle agent enters service loss only when work it cannot run arrives.
- [ ] Detection uses existing refusal and execution records only; no heartbeat, probe or model call is made to detect or end service loss.
- [ ] One cause that puts one or more agents into service loss is one outage. Through the existing Core command/event boundary, the owner receives one durable notice when an outage begins and one when it ends, each listing every affected agent, and the terminal shows each agent's service-loss state and cause.
- [ ] Restoration is the agent's first successfully completed call after its cause clears, or after the owner moves all of its work off the failed provider. A reset passing, a credential fix, a raised limit or top-up, an explicit retry, confirmed process shutdown or a repaired state only permits another attempt.
- [ ] Paid calls with unknown outcomes stay held through service loss and restoration and are never replayed; queued work is then scheduled as usual.
- [ ] Service-loss and outage state change only through typed Core transitions with replay-safe identities; a restart neither starts nor ends service loss, and generic interrupted-turn receipts alone establish neither loss nor restoration.
- [ ] Restart and duplicate loss or restoration observations converge on one recorded state without repeating effects, notices or spending.
- [ ] Service loss never causes an emergency successor promotion, timeout takeover, officer vacancy or second holder, and no other agent gains authority from it.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 162, 165, 166.
- Implementation decisions: 1, 2, 3, 4, 10, 12, 18, 64.
- Testing decisions: 1, 2, 8, 20, 22, 23.
- Fortress acceptance: FG20, FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 "Service loss for every agent (D97–D102; Core §13.11)", its "Service-loss state" prerequisite and exit criterion
- Core design §13.11
- Decision log D94, D97–D102

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D102 rework: the owner interview in ticket [87](87-specify-the-ordinary-officer-connection-loss-and-service-restoration-policy.md) specified the procedure ([D97](../../../docs/DECISIONS.md#d97-officer-service-loss-causes-onset-held-questions-and-restoration-evidence)–[D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing)), and the owner approved this rework in the landing breakdown. Retitled from “Recover a lost officer through the specified ordinary procedure”, this is now the general Core service-loss ticket for every agent, with the procedure's criteria added. Its blockers change from 27 and 87 to 16 and 87 because it no longer needs role records; officer consequences move to tickets [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md)–[134](134-hand-over-an-office-after-an-outage.md). Status moves from needs-info to ready-for-agent. This grants no separate implementation, contract-freeze or release approval.
