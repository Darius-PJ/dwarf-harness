# 12: Accept only exact-subject review and failure results

Status: resolved
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3
Blocked by: 11

## What to build

Bind callbacks to task/assignment version, subject, evidence, authority and deadline; duplicate results are idempotent and stale results cannot approve new work.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- C0 completed; relevant owner-frozen consumed ledger/producer families before implementation.
- Explicit integrated D52 boundary approval; no inferred approval from ticket publication.
- Owner-provided private remote and Windows/Linux/macOS CI; live provider credentials only when a live test is attempted.
- Testing-only harness workloads until whole C4; any A2 claim additionally needs actual per-profile C10 qualification.

## Acceptance criteria

- [x] Typed review/failure requests and results bind exact assignment/version/subject, acceptance/check/config and evidence plus authorization revision and deadline.
- [x] Duplicate callbacks are idempotent; stale or cross-context callbacks cannot approve different work or mutate current state.
- [x] Chat, boards and narratives never replace authoritative task/gate state.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 29.
- Implementation decisions: 1, 2, 3, 4, 11.
- Testing decisions: 1, 2, 8, 17, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Answer

Ticket 12 is built, reviewed and on `main`. Core's private-state service, `internal/core/agents`, now takes typed review and failure decisions. The owner, or an agent holding the decision's own authority, `task.complete` for a review or `task.fail` for a failure, requests one by naming a reviewer and binding the task's exact version, assignment and generation, the assignee, the subject, the acceptance, check and configuration digests, the required evidence, the authorization revision and a deadline. A request advances no task version, and a task holds at most one current request, which a new one replaces only by naming it. The named reviewer's result repeats that binding exactly: an approve completes the task and a fail fails it, each in one ledger group that also releases the task description's pin, and a reject records the judgment and keeps the request guarding. While a request is pending, even past its deadline, or rejected, a direct complete or fail refuses `ErrReviewRequired`, the owner's included; cancel and reassign stay open. A duplicate returns its original receipt without reading the clock or charging again, and a stale, replaced, revoked, late or cross-context callback decides nothing. Subjects are artifacts and exact private revisions; every other kind is refused, so an approve completes a task and promotes nothing. Time comes from the service's clock seam, sampled after the duplicate check and again just before the commit, against a floor that a backwards clock cannot pass; Open, queries and retries read no clock. Task state comes only from the replayed ledger: private writes, a result-shaped description and forged state files change nothing. S4's measurement found that the read-authority check for an artifact loaded every task the participant's grants named before knowing the artifact's own, so each decision's cost grew with the whole history for each of the participant's grants. S5 corrected the check's order so that it loads only the artifact's task. The service writes F3-12 as the project manager froze it ([D144](../../../docs/DECISIONS.md#d144-f3-12-review-and-failure-decision-records-frozen)). [B33](../../../docs/DECISIONS.md#b33-c3-exact-subject-review-and-failure-results-ticket-12) records:
- the design as ruled;
- the project manager's answers that changed a reviewed rule or the ruling's text;
- S5;
- the reviews;
- the service's limits.

The rows ran on one Windows machine. CI is paused ([D143](../../../docs/DECISIONS.md#d143-ci-paused-a-push-starts-no-run)), so no hosted run covers this ticket.

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [89](89-report-waiting-ready-waiting-blocked-and-active-task-facts.md) adds C3's waiting-ready, waiting-blocked and active task-fact query over these task records, which the fortress stockpiles consume. These extensions add no blocker or criterion to this ticket.

2026-10-24 — Ticket 11, this ticket's only blocker, is resolved: C3's scoped grants, tasks and non-paid capacity are in `internal/core/agents` on `main` ([B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11)). Its `runtime.task_changed` records carry a submission, failure evidence or awaited artifact as a `SubjectRef` whose `kind` is an open token, which version 1 writes as `artifact`, so this ticket adds subject kinds by addition ([D140](../../../docs/DECISIONS.md#d140-f3-11-scoped-authority-task-and-non-paid-work-records-frozen)). A direct `complete` or `fail` is an explicit authorized decision that asserts no review, and ticket 11's [ruling](../c3-evidence/ticket-11-design-ruling.md), P7, has this ticket add its guards to the same transition. S2's review carried one change here: before the first producer of an unfamiliar kind, `SubjectRef.MarshalJSON` canonicalizes unfamiliar members with `json.Marshal(json.RawMessage(raw))`. The other start gates above stand.

2026-10-24 — Claimed for design by the project manager. Its only blocker, ticket 11, is resolved (B32). The start gates hold: C0 is Done; F0, F2, F3-10 and F3-11, the families it consumes, are frozen; D112 approved the D52 boundary "before C2 or C3 implementation"; the private remote's three-OS CI passed (runs 7–13). No live provider credentials are needed: review and failure results make no provider call.

2026-10-24 — The owner had named this ticket next, to be followed by ticket 13, and approved proceeding in answer to the project manager's report, which named this ticket's design and build as next. The designer wrote the [design brief](../c3-evidence/ticket-12-design-brief.md) from the source list and a reader's bounded map of the service's clocks, `serviceDeps`, `Limits`, projections and F3-11 paths: 15 choices, four F3-12 record types with a schema-3 canonical submission, and five slices, with only conditional owner gates. A fresh build advisor approved it with changes. Of the 15 choices the [ruling](../c3-evidence/ticket-12-design-ruling.md) accepted 9 and modified 6, rejecting none, and it made ten findings: two major, six minor and two nits. The major findings remove cost and weight: the check and configuration become bound digests, not artifact references; and a `maxRequest` watermark and source witnesses read by the reader's cursor, not from the start, keep whole-history scans off the ordinary path. Among the minor findings, one `runtime.review_result` with a closed verdict replaces two result types, giving three types; a callback rechecks the current chains instead of requiring the colony's authority version unchanged; and the guard on direct complete and fail moves to S3a. None of the ruling's owner gates applied. Under D117 the ruling ended the design step; it wins where it and the brief differ.

2026-10-24 — S1, the record-free rules, reached `main` as 7feffa3: the request slot's state machine, the binding comparison, the guard on direct complete and fail, the reviewed-failure fact in the task kernel, the clock's sample, floor and deadline rule and the request-ID watermark, on unexported values. Its only exports are the three ruled sentinels, `ErrReviewRequired`, `ErrReviewClock` and `ErrReviewExpired`, which the project manager read note 1's "no exported API" to allow. A fresh build advisor confirmed that reading and approved S1 with one minor finding, deleting the unruled `reviewUnixNano`, carried to S3b, and a nit, dropping the guard's unreachable terminal-state clause, carried to S3a. `FuzzScopedGroupReplay` ran 25,063 executions in 120 s without a failure, and the check without its race step (D129) passed on 7feffa3 in 465.9 s.

2026-10-24 — The owner set a pause point for the Codex coders and asked why coders were used for several slices, preferring a fresh coder for each slice unless retaining its context is demonstrably better. From then each slice had a fresh coder: CoderT12S1, revived for S2 at 14:09, was stopped at 14:13, and its partial files went to a fresh coder, CoderT12S2, as a draft. Codex coders ran until Codex reached that pause point, and the project manager withdrew the stop after S2 that he had proposed.

2026-10-24 — [D143](../../../docs/DECISIONS.md#d143-ci-paused-a-push-starts-no-run): CI is paused. At the owner's instruction the repository runs no CI tests until his final decision. D143's commit, b332e47, removed the workflow's `push` trigger, so a push starts no run. It was placed on S2's branch, reviewed with S2 and reached `main` with it. No hosted run covers this ticket's commits.

2026-10-24 — S2, the F3-12 contract, reached `main` as bfc54c7, with D143's b332e47 and its freeze record:
- three version-1 types, `runtime.review_requested`, `runtime.review_result` and `runtime.review_receipt`, with the catalogue at forty-one;
- schema 3's canonical submission;
- `private_revision` as a known `SubjectRef` kind, with the `SubjectRef.MarshalJSON` correction carried from ticket 11;
- the F3-12 section of the package documentation, with the F3-11-only reading;
- `FuzzDecodeF3_12`;
- a golden of 25 commits and 90 records.

CoderT12S2 built it from CoderT12S1's partial start. A fresh build advisor approved S2 at bfc54c7 together with the record, its evidence and b332e47, S2's D41 sign-off, and confirmed the project manager's three answers during S2. Its minor finding and one nit were applied to the record before the freeze, and its second nit asked for no change. The project manager then froze F3-12 under D125 ([D144](../../../docs/DECISIONS.md#d144-f3-12-review-and-failure-decision-records-frozen)). In 120 s each, `FuzzDecodeF3_12` ran 1,016,491 executions and the `FuzzDecodeF3_11` rerun 3,386,095, without a failure. The check without its race step passed on bfc54c7 in 510.7 s and on the record commit df72ff6 in 657.6 s. A Markdown-only amendment, 7fcd582, filled in that result (D128), and `main` moved from 7feffa3 to 7fcd582.

2026-10-24 — S3a, the reducer, reached `main` as 3344892. It holds:
- replay of all three F3-12 types through Open and the golden;
- the request slots and their lifecycle;
- the `maxRequest` watermark and the cold lookups;
- book usage and the task loaders;
- `ReviewStatus` and `ReviewCommandStatus`;
- the guard on direct complete and fail, live and at replay.

CoderT12S3a built it. Two of the project manager's four answers changed reviewed text. A group whose receipt outcome is unknown is refused at preflight and at Open as inconsistent (answer 2), and the S2 review's note 16, which had asked that such groups replay as unapplied, was withdrawn by its author. The ruling's literal witness cursor fails on the frozen reader, which refuses a cursor inside a commit, so witnesses are read from the start of the segment holding the smallest one (answer 3), and `sourceRecord` took the same cursor. A fresh build advisor confirmed all four answers and approved S3a with one minor finding, `loadTask`'s cold scan decoding whole families, and three test nits, all carried to S3b. In 120 s each, `FuzzReviewGroupReplay` ran 1,366 executions, `FuzzScopedGroupReplay` 19,558 and `FuzzPrivateGroupReplay` 622,455, without a failure, and the check without its race step passed on 3344892 in 508.3 s.

2026-10-24 — Codex reached its pause point at 16:28, during S3b. Under the owner's rule CoderT12S3b paused at 16:30 with a pause report: the producers were built and their tests passed, and the guard order, the recovery rows, break I and the final runs were left. Asked through the question tool how S3b should continue, the owner chose a fresh Claude coder at once. CoderT12S3bOpus, the default task agent on Claude Opus 5.5, told to follow the coder role, finished S3b at effort high. The owner then set effort xhigh for the next stand-in Claude coder. A new role, `coder-claude`, is the coder role on Claude Opus 5.5 at effort xhigh with the same tools, and the coders of S4 and S5 ran on it.

2026-10-24 — S3b, the producers, reached `main` as 587d930: `RequestReview`, `RequestFailure`, `ResolveReview` and `ResolveFailure` for the owner and for an agent, the clock seam, unlocked witness preparation, the request-ID check and one `Submit`, with `reviewUnixNano` deleted (S1's minor) and S3a's minor and nits closed. CoderT12S3b began it and CoderT12S3bOpus finished it. The guards run in two passes, and seven pinned rows hold their precedence. The project manager's three answers each changed reviewed text:
- a direct F3-11 fail carrying `private_revision` evidence stays `ErrDenied`, ticket 11's class, not the `ErrInvalid` the S3a review's nit asked for (answer 1);
- finding 7's forged-file row forges `core/state.json` and `Memory.md` together, because the service ignores a stray `core/state.json` (answer 2);
- within the brief's step 4 trusted time comes before subject rights, so a late caller without them gets `ErrReviewExpired`, which discloses nothing (answer 3).

A fresh build advisor confirmed all three and approved S3b with one minor finding, `ReviewReceipt.ThroughSequence` meaning two things, and two nits, carried to S4. In 120 s each, `FuzzReviewGroupReplay`, with its valid branches narrowed, ran 15,566 executions and `FuzzScopedGroupReplay` 20,125, without a failure, and the check without its race step passed on 587d930 in 520.5 s.

2026-10-24 — S4 reached `main` as e296375: four injected crash rows and the final `doc.go`, with S3b's carried items:
- `ReviewReceipt.ThroughSequence` deleted;
- every successfully read clock sample raising the incarnation floor;
- the prose test's inert check covering the direct guard and the book.

CoderT12S4 built it on the `coder-claude` role from 17:53, under outside load. Its measurement driver's trial found a material regression, and `agents.Open` at ten times the history projected to about 30 hours. The project manager answered that S4 fixes nothing and measures at about twice the baseline history, labelled; the brief's §5.3 sends a material regression to the advisor. A fresh build advisor, AdvisorT12S4, approved S4: "S4 e296375 approved: ThroughSequence deleted, every read clock sample raises the incarnation floor, assertInert covers the guard and the book, four positively waited child-process crash rows over private real stores with breaks A–E red then restored, and a labelled 2× measurement that found Open O(N·G·H) through ticket 11's artifactAuthority (119.8 s → 800.5 s for 2.07× history, 65–77 % of CPU), which this review routes to a ticket-12 fix slice S5 — a check-order correction, no index — before the close." Its major finding is about the ticket, not S4's code: ticket 11's artifact-read check, which every review decision and every replayed F3-12 group calls, loaded every task a participant's grants named before knowing the artifact's own, against the ruling's finding 2. The review ruled a fifth slice, S5, to correct that before the close, and folded two `doc.go` nits into it. The full check, uncached and with its race step, passed on e296375 as an evidence check: `check PASSED: all 9 steps passed in 1397.5s` (tests 440.1 s, race 889.6 s). S4 changes no fuzzed code.

2026-10-24 — Asked for a quiet machine for about an hour, the owner confirmed through the question tool that the machine was quiet. The project manager reran S4's driver on e296375 from 20:30 to 20:44, with the busiest process at 0.08 of a core:
- `agents.Open` took 43.9 s at 1,047 records and 330.6 s at 2,163;
- one decision took 1.16 s and 6.0 s;
- the coder's loaded run had measured 119.8 s, 800.5 s, 1.91 s and 16.06 s.

Asked to keep the machine quiet until about 23:00, the owner left the machine to the project manager for the rest of the timing runs.

2026-10-24 — S5, now this ticket's last code slice, reached `main` as d37ca15. It holds the S4 review's ruled correction of the artifact-read authority check's order: the check resolves the artifact's source once and loads only the task that source names, with no cache or index and no change to any accept or deny outcome. It also rewrote `doc.go`'s cost paragraph, which names no unexported identifier and states the retry's cost as built. The S4 review's ruling is the slice's design under D117, so no designer brief was needed. CoderT12S5 built it on the `coder-claude` role from 20:31. Its rule break, dropping the task comparison, turned `TestScopedTaskTransitionsAndProjection`, an existing ticket-11 test, red. Only corrupt or unreadable data behaves differently: a source that cannot be read or decoded now errors before any task loads, and a failed scan of a task the source does not name no longer surfaces. AdvisorT12S5, a fresh build advisor, reviewed it with the close in one review: "S5 d37ca15 approved: artifactAuthority resolves the artifact's source once, at the first task resource the caller may inspect, compares its TaskID before loadTask and loads only that task, with every other check in its order and no state; dropping the comparison turned ticket 11's TestScopedTaskTransitionsAndProjection red; the quiet driver put the read-rights path at 0.000 of every profile, one approve at 85 → 160 ms for 2.07× history and 94 → 671 ms for a measured 10.04× (exponent 0.85), and agents.Open at 13.2 → 47.8 s and 15.6 → 1,191.1 s (exponents 1.78 and 1.88, 9–11 % of it the review path's witness batch and the rest ticket 10's and 11's scans), inside the ruling's O(N·(G + segment)); doc.go's cost paragraph names no unexported identifier and states the retry's cost as built; one nit, no change required." The full check, uncached and with its race step (D129), passed on d37ca15: `check PASSED: all 9 steps passed in 1681.3s` (tests 519.4 s, race 1057.0 s). In 120 s each, `FuzzReviewGroupReplay` ran 13,544 executions and `FuzzScopedGroupReplay` 16,700, without a failure. After S5, quiet and under the profiler, the driver rerun on d37ca15 put the read-rights path at 0.000 of every profile: at 2× (the project manager's rerun) `agents.Open` took 14.7 s at 1,047 records and 50.7 s at 2,163 (exponent 1.70; the coder's run 13.2 s → 47.8 s, 1.78) and one approve 84 ms → 140 ms (the coder's 85 → 160 ms); the coder's run on the tree whose hashes equal d37ca15's blobs reached a measured 10.04× (H 10,515, N 868, G 328): `agents.Open` 15.6 s → 1,191.1 s (exponent 1.88) and one approve 94 → 671 ms (0.85). What remains of Open's growth is ticket 10's and 11's per-scope history validation (about 45 % of its CPU) and scoped replay (23–29 %), with the review path's witness batch at 9–11 %, the ruling's O(N·(G + segment)) in a one-segment log; the driver's 10× label was changed by the project manager's answer, hashes recorded, and the 10× log's unmeasured-10× line is false for that run ([evidence](../c3-evidence/ticket-12-evidence.md#s5-the-artifact-read-check-order-2026-10-24)).

2026-10-24 — This ticket is resolved: S1, S2, S3a, S3b, S4 and S5 are on `main`, the ruling's slicing with S5, which the S4 review added. This Markdown-only commit (D128) records them. It adds the design brief, the ruling and the [evidence](../c3-evidence/ticket-12-evidence.md) of every slice, of S4's measurement and of CI's pause. No owner gate was triggered: the ruling's OG-D52, OG-BREAK and OG-DEP stayed untriggered, and no design, freeze or approval question was put to the owner. Every review was a fresh build advisor (Fable), and no stand-in reviewer ran. [B33](../../../docs/DECISIONS.md#b33-c3-exact-subject-review-and-failure-results-ticket-12) records the build: the design as ruled, the project manager's answers that changed a reviewed rule or the ruling's text, S5, the reviews and the limits. F3-12 was frozen by D144, whose evidence this close does not repeat. S4's measurement found that ticket 11's artifact-read check made the review path's cost grow with the history times the participant's grants (`agents.Open` with exponent 2.6–2.8 in the history, one decision 2.3–2.9), against the ruling's finding 2, and S5 corrected the check's order.

Five limits stand, recorded at the ruling's request:
- after a reject, F3-11 has no way back to active, so the ways on are a replacement, a cancel or a reassignment;
- a pending failure request goes stale when the assignee moves the task on;
- a requester must hold the decision authority it delegates;
- the guard binds the owner, whose escapes are a replacement naming himself, cancel and reassign;
- a backwards clock refuses new decisions until it passes the floor.

Ticket 140 still inherits the F3-12 groups in its index, the cold request and receipt lookups as index sites, and the witness batch's reading up to each witness's position in a one-segment log (the ruling's P13). No hosted CI run covers this ticket: CI is paused (D143), and, as the brief's §6.4 says, a missing run is missing evidence.

On Windows, these tests show each criterion met, from the ruling's acceptance coverage checked against the tests on `main`:
- **Requests and results bind the exact assignment, version, subject, acceptance, check and configuration digests, evidence, authorization revision and deadline.** `TestReviewExactBindingAndTransitions`, `TestReviewPrivateSubjects`, `TestReviewDeadlineAndClock` and `TestReviewAuthorityAndDisclosure`. S1's `TestReviewBindingRejectsSingleMemberSubstitution`, `TestReviewBindingPreservesSubjectOccurrences`, `TestReviewBindingMatchesOwnedEquivalentValues`, `TestReviewClockSeparatesDeadlineAndRegression` and `TestReviewClockExpiryCannotLowerIncarnationFloor` hold the binding and clock rules.
- **Duplicates are idempotent; stale or cross-context callbacks approve no different work and mutate nothing.**
  - `TestReviewDuplicateAndReplacement`.
  - `TestReviewResultRaces`, run 20 times under race at S3b.
  - `TestReviewAuthorityAndDisclosure`, `TestReviewLedgerRecovery`, `TestReviewDirectGuard` and `TestReviewStatusViews`.
  - `TestReviewGroupRules`, where a late or revoked callback written straight into a valid commit refuses at Open.
  - `FuzzReviewGroupReplay`.
  - S1's `TestReviewSlotTransitionsRefuseIllegalEdges`, `TestReviewSlotReplacementFencesOldIdentity`, `TestReviewGuardBindsOwnerAndAgentButAllowsEscapes`, `TestReviewGuardNeverTransfersToDifferentTaskBinding`, `TestReviewRequestWatermarkDoesNotTreatEqualityAsNew`, `TestScopedTaskReviewedFailureRequiresAuthority` and `TestScopedTaskReviewFactCannotBypassOtherOrigins` hold the slot, guard, watermark and kernel rules.
- **Chat, boards and narratives never replace authoritative task state.** `TestReviewProseCannotDecideTask`: private writes, a result-shaped description and a forged `core/state.json` with `Memory.md`, across a real reopen, change neither the task, the request nor the dependent task's readiness, and the typed approve from the reviewer's unaffected context then enables the dependent. Gate state is not created at all (B33's Limits).
- **S4's rows.**
  - `TestReviewInjectedCrashRecovery`'s four injected rows hold the second criterion: `before_submit`, `torn_result`, `approval_before_projection` and `rejection_lost_ack`. Each is a positively waited child of the test binary (`TestReviewCrashChild` is its protocol), over a private copy of a closed real store. A result stopped before `Submit` or torn in the log applies nothing and consumes no command ID. A committed approve or reject whose acknowledgement was lost recovers once: its retry equals the original receipt, with no second transition, unpin or charge.
  - `TestReviewDeadlineAndClock/refused-call-sample-raises-incarnation-floor` holds the first criterion's clock rule.
  - For the third, `TestReviewProseCannotDecideTask`'s inert check now also holds that the direct guard and the book are unchanged.
- **S5's rows.** S5 adds no test. Its rule break turned the existing `TestScopedTaskTransitionsAndProjection` red, where a `task.inspect` grant on one task then authorized reading another task's description with the same bytes. The tests of its acceptance pass unchanged: ticket 11's authority, subject and crash rows, `TestReviewAuthorityAndDisclosure`, `TestReviewPrivateSubjects`, `TestReviewGroupRules` and the three goldens through the service.

F3-12's `TestF3_12Payloads`, `TestF3_12Values`, `TestF3_12NestedMembers`, `TestF3_12EvidenceAndOpenTokens`, `TestF3_12OutputCap`, `TestF3_12SubmissionConditions`, `TestF3_12Compatibility`, `TestGoldenF3_12` and `FuzzDecodeF3_12` hold its records, with `TestNewCatalogue` at forty-one types, and `TestReviewFrozenGoldenReplay` opens F3-10's, F3-11's and F3-12's goldens through the service.

2026-10-30 — Public run 2 on d8336c8 passed on Windows, Linux and macOS and covers this ticket's code as well as ticket 13's code. This supplies the hosted evidence missing at the historical close; it does not waive C3's final three-system CI gate. This run preceded D153's per-slice CI cutover; each later code slice passes CI on all three systems before `main` moves.
