# 130: Count router pauses toward service loss

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: R1 grouped outage prompt joined to C3 service loss (D97, D100, D102)
Blocked by: 39, 91

## What to build

Once the router pauses turns, start each affected agent's 2-minute service-loss grace from the router's pause instead of its first refused call, keep the turns one cause pauses in one outage with one grouped prompt, and apply Core's restoration rules to the owner's wait, stop and provider choices.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- The router pause and grouped prompt from ticket [39](39-pause-and-safely-resume-the-exact-context-on-an-owner-selected-provider.md) and the service-loss state from ticket [91](91-record-service-loss-and-restoration-for-every-agent.md); no C6, C8, C9 or GUI dependency.
- Live-provider evidence requires the owner's credentials and authorization; controlled refusals, resets and provider changes exercise the remaining cases.

## Acceptance criteria

- [ ] With the router in place, the 2-minute grace counts from the router's pause of the agent's required work, which follows the configured silent wait. Causes only the owner can clear still count at once, and the first-refused-call rule no longer applies.
- [ ] Turns that one cause pauses, across one or more agents, belong to one outage with one begin notice, one end notice and one grouped prompt; a turn the same cause pauses later joins both.
- [ ] Choosing wait or stop never restores an agent; a waiting turn restores its agent only when its call completes after the cause clears.
- [ ] When the owner moves all of an agent's work off the failed provider, for example by changing its default provider, the agent's first completed call there restores it. A single turn switched alone runs on its new provider without restoring the agent.
- [ ] After restart, the router's prompts and Core's service-loss and outage records agree: no outage, notice or prompt is duplicated, and no paid call is replayed.
- [ ] No heartbeat, probe or model call is added to detect or end service loss.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 165, 166.
- Implementation decisions: 1, 2, 3, 4, 10, 24, 25, 64.
- Testing decisions: 1, 2, 11, 20, 23.
- Fortress acceptance: FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R1 provider-status deliverable and exit criterion
- Routing design §9.8
- Core design §13.11
- Decision log D97, D100, D102

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
