# 132: Hold outage work to the Corps' existing tasks

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C8 outage work rules (D99–D100)
Blocked by: 131

## What to build

While an officer is in service loss, let its covered members claim tasks already waiting in the Corps' queue and split their own tasks within the parent's acceptance criteria, and let them draft new work only as capped task proposals that wait for the office's decision.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C8 outage and task-proposal families; the outage Corps operation from ticket [131](131-keep-a-corps-working-while-its-officer-is-in-service-loss.md).

## Acceptance criteria

- [ ] Covered members may claim tasks already waiting in their Corps' queue and split their own current tasks into subtasks that serve the parent's acceptance criteria, within existing grants.
- [ ] They cannot create top-level tasks, change goals or acceptance criteria, or start work outside existing tasks; each refusal is a visible typed result.
- [ ] New work is drafted only as task proposals, at most three waiting per member by default and configurable per colony. Further drafts are refused as busy, and proposals never count toward the question allotment.
- [ ] Proposals are pending decisions for the office: they wait for the restored officer, the owner as stand-in or a successor, and nothing accepts them automatically.
- [ ] The rules apply only while the officer is in service loss. Restart or a retried command neither duplicates nor loses a claim, split or proposal.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 167.
- Implementation decisions: 1, 2, 3, 4, 11, 18, 64.
- Testing decisions: 1, 2, 8, 20, 23.
- Fortress acceptance: FG22.

Canonical sections (resolved through the parent specification’s reading map):
- Core design §13.11
- Core roadmap C8 ordinary service-loss deliverable
- Decision log D99, D100, D101

## Comments

2026-10-08 — Published under [D102](../../../docs/DECISIONS.md#d102-officer-service-loss-moved-work-pre-router-onset-and-ticket-landing) after the owner approved the service-loss landing breakdown. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
