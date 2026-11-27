# 17: Run admissible required Go checks on exact candidates

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4
Blocked by: 04, 16

## What to build

Through the independently controlled Core runner, materialize exact subjects, run the full pinned required set with O4/same-process canaries, and refuse missing, skipped, incomplete or unbound evidence.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Completed U1.L and owner-frozen law-format; no U1.E dependency.
- Testing-only harness workloads until Release 1's exit test passes (D182); any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [ ] Host-controlled hooks/explicit requests use one authorized Core runner, exact fresh materialization, pinned checks/environment/law and all declared required checks.
- [ ] Reconcile Go test inventory, terminal status, exit code and digest-bound same-process canary; reject no-test, skip/cache-only, partial, unbound or interrupted evidence.
- [ ] O4 stays on; no human waiver, graph/path exclusion, vacuous pass or automatic sampling/relaxation.
- [ ] Emit bound actual attempt/queue outcomes and bounded F1 reproduction/adjacent attribution evidence; F1 alone schedules no job or model turn.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 34, 35, 37.
- Implementation decisions: 1, 2, 3, 4, 13.
- Testing decisions: 1, 2, 9, 12, 17, 20.

Canonical sections: Core roadmap C4; its cited controlling Core design and latest decision amendments.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-20 — Ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md) is resolved, and ticket [04](04-load-and-identify-a-strict-law-independently-of-field-execution.md) already was, so all listed blockers are clear. The criteria are unchanged. No runtime, production-delivery or other start-gate approval is inferred.

2026-11-27 — Release tier rework (D182, D185, D186): Release 1. Slimmed: criterion 5 and criterion 3's fixture clause are dropped. The runnable V6 attack and control fixtures move to tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md) and [46](46-qualify-the-real-windows-runner-profile-for-a2.md), both On trigger, and this ticket's V6 profile section moves to ticket 22. The design's owner question OG1, on giving the pytest and Node fixtures to tickets 18 and 19, falls away with them. OG2 is answered (D185 item 3). C4's per-incident fresh-execution cap is the owner-set Core startup setting `validation_capacity.incident_executions`, outside law-format v1 and the activation chain, with no default, so reproduction is unavailable without it. The owner's initial value is 12. The initial canonical observation, every reproduction and every adjacent before-and-after execution are charged to one cumulative incident reservation when queued, and the charge survives interruption and restart. No criterion said the cap awaited the owner, so none changes for it. The transparency criterion is added. This grants no implementation, contract-freeze or release approval.
