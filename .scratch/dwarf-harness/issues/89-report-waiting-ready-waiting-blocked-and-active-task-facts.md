# 89: Report waiting-ready, waiting-blocked and active task facts

Status: resolved
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C3 actual task projections (D90–D92)
Blocked by: 12

## What to build

Let authorized callers query each agent's self-assigned and delegated tasks as waiting-ready, waiting-blocked with reasons, or active, straight from committed Core task records.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C3 task and assignment families; no C6, C7, C8 or GUI dependency.
- Consumed later by the fortress stockpiles (ticket [114](114-show-waiting-task-stockpiles-workshop-activity-and-authorized-rails.md)); C3 completion does not wait for any GUI work.

## Acceptance criteria

- [x] A typed Core query returns, for an authorized principal, each agent's waiting self-assigned and delegated tasks split into ready-but-queued and blocked with the blocked reason, plus active tasks separately, using actual task identities, versions and states. Tests: `TestTaskFactsBucketsAndAssignments`, `TestTaskFactsTransitionRecovery`; S3's exported-API smoke.
- [x] Results derive only from committed task transitions; no stall-age threshold, staffing or competence judgement, or message backlog enters the projection. Tests: `TestTaskFactsCommittedReadiness`, `TestTaskFactsQueryRace`.
- [x] Claims, assignments, completions, failures and revocations move a task between states exactly once in sequence order, including across restart; stale or duplicate callbacks cannot move it. Tests: `TestTaskFactsTransitionRecovery`, `TestTaskFactsReviewTransitions`; S3's reopen smoke.
- [x] Authorization filters task identities, titles, counts and blocked reasons per principal and context before results are built; a forbidden task contributes neither a row nor a count. Tests: `TestTaskFactsAuthorization` (selected-chain, audience and count rule breaks each fail and restore green), `TestTaskFactsPagination`; no title is synthesized.
- [x] Results are bounded and paginated with a revision or high-water marker so consumers can recover without rescanning history; no view, animation or cosmetic state can delay or alter a fact. Tests: `TestTaskFactsPagination`, `TestTaskFactsCommittedReadiness`, `TestTaskFactsTransitionRecovery`, `TestTaskFactsQueryRace`; S3's stale-cursor and same-head-reopen smoke.

## Answer

`Service.ListTaskFacts` and `Agent.ListTaskFacts` return one target agent's assigned waiting-ready, waiting-blocked and active tasks with exact task and assignment metadata, blocked reasons and description references, not bodies or synthesized titles. Unassigned, submitted and terminal tasks are excluded. For an agent's query, its current selected inspect chain and the task audience filter tasks before rows, counts or look-ahead (the owner's entrance is the trusted path); readiness uses committed state, excluding temporary preparation. A combined `Limit` bounded by `MaxLiveTasks` returns same-head UUID-ordered pages and complete visible counts; `ErrTaskFactsStale` requires discarding an incomplete enumeration and restarting with a nil cursor, without consumer ledger access. The [package documentation](../../../internal/core/agents/doc.go) describes the boundary.

Limits: one target agent per query; a full retained-task scan per page with existing authority/readiness lookup costs, O(T·Limit) ID moves and O(Limit) candidate/output storage, not a latency or capacity guarantee. Any commit can restart an enumeration; count-only consumers need one call at Limit 1. Returned rows evaluate readiness twice under the same lock. No scheduler, GUI, contract change or freeze is included; remaining capability status is stated only in the roadmap's Status line and board.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 132, 135.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 57.
- Testing decisions: 1, 2, 8, 20.
- Fortress acceptance: FG21.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 "Actual task projections (D90–D92)" and its exit criteria
- Fortress GUI spec §2 work representation and §9 C3 handoff
- Decision log D90–D92

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-24 — Ticket 12, this ticket's only blocker, is resolved: C3's review and failure decisions are in `internal/core/agents` on `main` ([B33](../../../docs/DECISIONS.md#b33-c3-exact-subject-review-and-failure-results-ticket-12)), and their records, F3-12, are frozen ([D144](../../../docs/DECISIONS.md#d144-f3-12-review-and-failure-decision-records-frozen)). An approve or fail `runtime.review_result` is a task transition: it advances the task's version and state as a `runtime.task_changed` complete or fail would, without such a record. A consumer of tasks therefore reads both families in ledger order, and a reader of F3-11 alone must not be used on a log that holds F3-12 (D144). A review or failure request advances no task version. While one is pending, even past its deadline, or rejected, a direct complete or fail refuses `ErrReviewRequired`. A duplicate result returns its original receipt, and a stale or cross-context one moves nothing. Ticket 12 built no task-fact listing; it stays this ticket's. The other start gates above stand.

2026-10-30 — Claimed for design after ticket 13's close; ticket 12, its only blocker, is resolved (B33). No numerical performance bound or owner question was introduced.

2026-10-30 — DesignT89 supplied the design brief; fresh advisor RuleT89 approved with four amendments. Authorization and pagination proofs land with the query in S1, ordered transitions and review results in S2, and concurrency, package documentation and an exported-API smoke in S3. No contract change or freeze is required.

2026-10-30 — S1 reached `main` as e492213: CoderT89S1 (GPT-6 Astra) built the complete owner/Agent query and bucket, assignment, committed-readiness, authorization and pagination regressions. Fresh Fable advisor ReviewT89S1 approved with nits; all three disclosure rule breaks failed their named test and restored green. Check 17 (run 41074489129) succeeded; `main` a678705..e492213.

2026-10-30 — S2 reached `main` as 85b8d56: CoderT89S2 (GPT-6 Astra) added ordered task transitions, review-result effects, duplicate/stale refusals, reassignment, revocation and same-head/lowered-limit recovery proofs. Fresh Fable advisor ReviewT89S2 approved with nits. Hosted check: check 19 (run 41081799823) succeeded on 85b8d56. Merge: `main` e492213..85b8d56.

2026-10-30 — S3 reached `main` as 749b218: CoderT89S3 (GPT-6 Astra) added the concurrent query/mutation proof, package documentation and the temporary exported-API smoke, and made the PM-authorized recovery-test corrections. ReviewT89S3 approved with no blocking findings; package race and twentyfold query-race runs passed. The smoke demonstrated bounded pages, stale-cursor recovery and same-head reopen, and its temporary package was removed before the commit. Hosted check: check 20 (run 41090448256) succeeded on 749b218. Merge: `main` 85b8d56..749b218.

2026-10-30 — Closed with the five criteria's evidence above. Close review: approved by a fresh Fable advisor, with notes only. Public tracker/roadmap edits and private design/evidence records land separately (D145); no build-record entry is added (D161). The other start and authority gates above stand.
