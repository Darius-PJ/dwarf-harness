# 133: Stand in for a lost office as the owner

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 owner stand-in through C7 administrator commands (D98–D100)
Blocked by: 93, 132

## What to build

Let the owner answer a lost officer's reserved questions, accept or reject its task proposals and make its pending decisions through the office's channels and within its authority, with every act labelled as the owner acting for that office.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C8 stand-in family and the C7 command additions it needs; authenticated administrator commands from ticket [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md), and the outage operation and work rules from tickets [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md) and [132](132-hold-outage-work-to-the-corps-existing-tasks.md).
- Verifiable through Core's command/event boundary and the terminal; the shared view projections follow in ticket [135](135-show-outages-pauses-and-stand-in-actions-in-every-view.md).

## Acceptance criteria

- [ ] Whenever an officer is in service loss, the owner may answer its reserved questions, accept or reject its task proposals and make its pending decisions through the office's channels and within its authority; anything wider uses his ordinary owner commands.
- [ ] Every stand-in act is recorded and shown as by “the owner, acting for” that office. The owner never becomes the holder, and nothing presents his acts as the agent's.
- [ ] A reserved question the owner answers stops counting toward its sender's allotment and is not answered again.
- [ ] The restored officer finds the stand-in acts in its history and receives a list of the decisions made for its office at its next naturally scheduled turn; those decisions belong to the office's records.
- [ ] Stand-in commands are typed, idempotent and revision-checked: a stale or duplicate command returns the recorded outcome or is refused, a command for an officer not in service loss is refused, and no command makes a model call.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 168.
- Implementation decisions: 1, 2, 3, 4, 11, 17, 18, 64.
- Testing decisions: 1, 2, 7, 8, 20, 23.
- Fortress acceptance: FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core design §13.11
- Core roadmap C8 ordinary service-loss deliverable and C7 outage additions
- Decision log D98, D99, D100

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
