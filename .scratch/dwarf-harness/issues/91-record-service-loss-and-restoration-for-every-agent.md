# 91: Record service loss and restoration for every agent

Status: resolved
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

- [x] Service loss is recorded with its cause for a provider failure classified at its true scope (rate-limited, overloaded, unreachable, denied or exhausted, including the provider account's own spend limit), an exhausted owner money limit that stops all of the agent's paid calls (its own, its provider account's or the colony's), or an execution failure (descendant processes not confirmed stopped, or private state failing its restart integrity check). Proof: F3-91's typed causes (D175), `TestServiceCauses`, `TestServiceMoney` (including `credential_complete_workset` and `credential_incomplete_workset`), `TestServiceIntegrityDemand` and `TestServiceProcessFailure`; process loss requires Windows tree proof in v1 (D138).
- [x] Being idle, busy or awaiting a reply, a turn interrupted by a restart, a detached or closed view, a stopped colony host, a task or grant budget, unset spending authorization and revoked grants never count as service loss. Proof: `TestServiceNeverCounts`, `TestServiceCauses`' no-cause rows, `TestServiceMoney`'s quote, scope and authority refusals, and inert reopen in `TestServiceOnset` and `TestServiceCrashConvergence`.
- [x] Until the router exists, onset follows a 2-minute grace counted from the agent's first refused call. Causes only the owner can clear count at once: refused credentials, provider quota or an owner money limit exhausted without a stated reset, locked-off processes and failed integrity checks. An idle agent enters service loss only when work it cannot run arrives. Proof: `TestServiceOnset` preserves the first source and exact due; `TestServiceCauses`, `TestServiceIntegrityDemand` and `TestServiceProcessFailure` exercise live demand; `TestServiceMoney` covers the parked turn, cancellation and the due-time book recheck; S4's in-process run exercises the real 120-s timer.
- [x] Detection uses existing refusal and execution records only; no heartbeat, probe or model call is made to detect or end service loss. Proof: `TestServiceCauses`, `TestServiceNeverCounts`, `TestServiceRestoration` and `TestServiceUnknownMoney` count evidence and physical POSTs; `TestServiceCrashConvergence` replays only recorded observations; S4's in-process reopen adds no POST.
- [x] One cause that puts one or more agents into service loss is one outage. Through the existing Core command/event boundary, the owner receives one durable notice when an outage begins and one when it ends, each listing every affected agent, and the terminal shows each agent's service-loss state and cause. Proof: `TestServiceOutageNotices` and `TestServiceRaces` establish one incident and cumulative joins; S4's `TestServiceTerminal`, `TestServiceSessionTerminal`, `TestServiceOwnerQueries`, `TestServiceStartupRefusal` and `TestServiceStartupNormal`, its in-process loss-to-restoration run and the real program's unconfirmed-startup run establish the owner surface, deduplication and print-before-refuse path.
- [x] Restoration is the agent's first successfully completed call after its cause clears, or after the owner moves all of its work off the failed provider. A reset passing, a credential fix, a raised limit or top-up, an explicit retry, confirmed process shutdown or a repaired state only permits another attempt. Proof: `TestServiceRestoration` includes terminal acceptance and refusal, credential replacement and `all_work_moved` with its binding-left negative row; `TestServiceMoney/raised_limit_restores`, `TestServiceIntegrityDemand`, `TestServiceProcessFailure` and S4's in-process loss-to-restoration run separate clearance from restoration.
- [x] Paid calls with unknown outcomes stay held through service loss and restoration and are never replayed; queued work is then scheduled as usual. Proof: `TestServiceUnknownMoney` sends the original unknown attempt once and never replays it: a separate successful call restores service, the total stays at two POSTs through reopening, and the original hold is unchanged; `TestServiceMoney/raised_limit_restores` lets the same parked turn proceed through ordinary admission; F3-91 introduces no scheduler, retry, release or accounting effect.
- [x] Service-loss and outage state change only through typed Core transitions with replay-safe identities; a restart neither starts nor ends service loss, and generic interrupted-turn receipts alone establish neither loss nor restoration. Proof: F3-91's schema-9 commands and receipts (D175), `TestServiceReplay`, `TestContinuationFrozenReplay/frozen_91`, the reopen rows of `TestServiceOnset`, and `TestServiceCrashConvergence`; S4's startup rows and in-process reopen preserve inert recovery.
- [x] Restart and duplicate loss or restoration observations converge on one recorded state without repeating effects, notices or spending. Proof: `TestServiceCrashConvergence`, `TestServiceRaces`, `TestServiceOutageNotices`, the live crash cuts and same-handle observation rows, plus S4's in-process restart counts. Hosted landing runs: check 120 (run 44833370745) on d16c779, check 132 (44985772136) on 701337f, check 139 (45280570176) on b4547d1 and check 141 (45687027863) on e855e6e, each successful on Windows, Linux and macOS.
- [x] Service loss never causes an emergency successor promotion, timeout takeover, officer vacancy or second holder, and no other agent gains authority from it. Proof: `TestServiceNeverCounts/live_owner_slots_task_and_paid_hold` preserves native work, money and ownership, and `TestServiceNoAuthorityTransfer` refuses foreign handles and another agent's success. F3-91 changes only its projection; officer consequences remain tickets 131–134.

## Answer

Core now records service loss for every agent from qualifying provider, exhausted-money and execution evidence, groups agents by cause into outages, and restores a member only on its own qualifying completed call with a current clearance or moved-work witness. It neither probes nor changes authority, admission, scheduling, accounting or succession.

S1 froze the five-type F3-91 family and schema-9 submissions (D175). S2 built the reducer, source directory, drain and recovery, grace timer, status and outage queries. S3 connected the live evidence adapters and clear and completion witnesses, preserving the existing financial groups and unknown holds. S4: the passive `service.status` and `service.outages` queries with an outage filter and a continuation bound to caller, filter and head; colony outage events on every bound session and `service.changed` on the agent's own; `/service` and `/outages`, with outage notices printed once across session pumps and a stale-page refresh bounded at three attempts; the status snapshot before the terminal starts and before both typed startup refusals, whose refusal line is unchanged; and one reports reconcile after the turn loop is enabled. The [contract catalogue](../../../docs/CONTRACTS.md) states the frozen boundary.

From S2's rebase onward, temporary stand-ins supplied the approvals; this ticket closes on those approvals and Fable's whole-ticket audit follows its reset (D160, D177). C3 is Done with this ticket (D182), as stated in its [Status line](../../../docs/ROADMAP-CORE.md#c3-agent-runtime) and [board row](../../../docs/ROADMAPS.md#status-board); tickets 146 and 149 remain open On trigger. This grants no release or real-work approval.

### Known issues (D181)

- A parked turn's own continuation commit wakes it again, so money-blocked work can continuously re-park and grow the ledger; retained for real-use reassessment.
- The service projection retains history without a volume bound; cloning, prefix scans and full incident member lists can grow, and the startup snapshot includes historical incidents.
- A money candidate captured before a cold-read unlock can use an old workset; a concurrent period-kind change instead retains an invalid-evidence condition without writing.
- A clear held by `errServiceDueHeld` has no status condition, and `retainServicePaidLocked` treats `ErrUnavailable` as final rather than retained.
- Restoration waits while the clock is held: its completion sample must be strictly after onset.
- Process loss is Windows-only in v1; an unconfirmed Open-time Git-version call and the first two commit-time Git calls can hide the classified error.
- A network failure while reading a non-200 response body supplies no transport provenance; generic provider errors, HTTP 504 and local timeouts alone establish no provider cause in v1.
- The fixed unconfirmed-startup refusal line still says “unclassified failure”; its separate typed condition carries the cause.
- A semantic no-effect retry without its own recorded command can still meet unavailable metadata before deduplication; the wrapper and locked entry also repeat command lookup.
- The startup snapshot is printed before the terminal picks its event cursor, so an outage notice committed between the two may not display automatically; its record and `/outages` still show it.

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

2026-11-20 — Ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md) is resolved, and ticket [87](87-specify-the-ordinary-officer-connection-loss-and-service-restoration-policy.md) already was, so all listed blockers are clear. It supplies one of this ticket's execution-failure causes: a workspace Git child whose descendants are not confirmed stopped is retained as unconfirmed and refuses reuse, and an unconfirmed fence refuses the executable's startup. The criteria are unchanged. No runtime, production-delivery or other start-gate approval is inferred.

2026-11-21 — By the owner's decision, this ticket's remaining slices land after ticket [147](147-cut-the-agents-packages-test-time.md), with its S2 rebased onto that result (D176). S2's hosted checks, runs 44866294125 and 44874224657, stopped at the 30-minute package limit in `internal/core/agents`'s Windows race step. D179 subsequently amended the measuring rule: ticket 147's hosted targets count as met on its own tree, and this ticket's S2 lands on its own green run under D127. This ticket's number, criteria and blockers are unchanged. This grants no separate implementation, contract-freeze or release approval.

2026-11-22 — Consolidation cross-link: Ticket [149](149-keep-a-ledger-readers-checked-position-across-reads.md) would cut the cost of `internal/core/agents`' ledger reads, the largest left in the package's tests. After this ticket's S2 landed, the package's Windows race step took 1,591.157 s in run 44985772136, above D127's 25-minute report line, and this ticket's remaining slices run against that line. Ticket 149's implementation begins only after the owner's reassessment at the pause that follows this ticket's close (D180). These extensions add no blocker or criterion to this ticket.

2026-11-18 — DesignT91 supplied the brief from a source list, and fresh Fable advisor RulingT91 approved it with seventeen binding amendments and no owner gate (`ownerGates` empty). Four slices keep every criterion here; no numerical acceptance campaign is required.

2026-11-20 — Fresh Fable clarification ClarifyT91a rebound the pre-S3 interfaces and evidence rules, with no owner gate. The withdrawal rule stands for the corrected D-08a reason: repeats do not record the complete set of refused work, so cancelling one turn proves no clearance. An owner-cancelled, money-blocked turn's agent can still become lost at due; this is reported, not an owner question, and ticket 130 inherits what stopping within the grace means once router pauses supply onset. The close's D183 amends D175's withdrawal wording and records the v1 process limitation.

2026-11-20 — S1, c1e016f with test-only fix 52cba52, added the F3-91 family, schema 9 and golden chapter. ReviewT91S1 (fresh Fable) blocked on four missing codec-test groups; ReReviewT91S1 approved the fix and joint freeze record with no blocking finding. The freeze's public tip d16c779 landed after check 120 (run 44833370745) passed on Windows, Linux and macOS; check 119 (44825161854) was early signal only. F3-91 is frozen under D175.

2026-11-22 — S2, 88f4bb6 with fix 8c2c1e2: ReviewT91S2 (fresh Fable) blocked on a late shared-cause restoration wedging the drain and a conservative coverage claim failing replay after metadata repair. Fix 1 repaired both and put receipt matching before recovery; ReReviewT91S2 approved. Checks 121 (44866294125) and 122 (44874224657) timed out in Windows agents race; 122 also failed a macOS CAS row. Neither was rerun. These led to ticket 147 (D176). S2 rebased onto its result, with T3's lazy fuzz prefixes, as 701337f; StandInT91S2R (temporary Opus stand-in, D177) approved. It landed on check 132 (44985772136), green on all three systems, under D179. The Windows agents race line, 1,591.157 s, was reported to the owner under D127.

2026-11-23 — S3, 1e09bfc, connected the live evidence. StandInT91S3 (temporary Opus stand-in, D177) blocked on the process-proof flag off Windows and missing live credential-workset/moved-work coverage. Fixes 1–4, 8b060a9, c710f75, 3d255f7 and b4547d1, were each approved by fresh temporary Opus stand-ins: StandInT91S3Fix1–Fix4. Checks 135–138 (45000678492, 45006934626, 45073342007, 45256714377) were red and never rerun: the process flag, a test's removed-turn lookup, real time reaching due, then a test clock below its live sample. Check 139 (45280570176) on b4547d1 passed on all three systems and landed S3, meeting fix 1's portability condition. Windows agents race 1,673.0 s was reported under D127; check 136 also crossed that line on Windows and macOS.

2026-11-23 — D181's test clean-up landed at 9828a67 after check 140 (45317050154) passed on all three systems. It cut some of this ticket's tests, including `FuzzServiceGroupReplay`'s seeds, 228 → 21, keeping functional and format coverage. Windows agents race fell to 1,335.150 s, below D127's report line; the earlier red/green proofs remain historical evidence, not a claim that every old row is retained.

2026-11-25 — S4 review: Temporary GPT-6 Astra stand-in reviewer: approve ticket 91 S4 at e855e6ee8685c4bc0a66cf30ce1edd1aea71e6de; landing remains gated on D153’s successful exact-SHA hosted check. Landing: `main` 9828a67 → e855e6e by fast-forward at 15:31:52 on 2026-11-25, after check 141 (run 45687027863) passed on Windows, Linux and macOS.

2026-11-25 — Closed with each criterion's evidence above. Close review: a temporary GPT-6 Astra stand-in (StandInT91Close) rejected two inaccurate proof statements, both corrected before landing; a fresh stand-in (StandInT91CloseFix1) then approved the close documents. C3 closes here under D182; tickets 146 and 149 stay open On trigger. The ticket closes on stand-in approvals; Fable audits the whole ticket after its reset (D160). Public ticket, paired-roadmap and contract prose edits and private design, clarification, inventory and evidence records land separately (D145). One short assistant entry amends D175; there is no build record (D161). The start and authority gates above stand.

2026-11-27 — Fable audit (D160): a fresh Fable advisor confirmed every stand-in review of this ticket (S2's rebase, S3 and its four fixes, S4 and the close), with no fix ticket. It adds two known issues (D181). An unfiltered `/outages` lists every incident ever recorded, oldest first, on a continuation bound to the colony head, so on a busy colony with more than one page it stops at the oldest 128 rows after three stale attempts; `/service`, the outage notices and an outage-filtered query are unaffected. A pending member whose expiry attempt fails stays pending past due until the next service event, a successful reconcile or a restart.
