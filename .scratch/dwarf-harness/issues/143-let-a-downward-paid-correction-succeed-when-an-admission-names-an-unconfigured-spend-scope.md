# 143: Let a downward paid correction succeed when an admission names an unconfigured spend scope

Status: resolved
Type: task
Published: 2026-11-07
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 paid-attempt settlement and correction (B9, B36); found by ticket 142's measurement smoke
Blocked by: 142

## What to build

Let Core correct a settled paid attempt downward when the attempt's admission named a spend scope that has no spend configuration, such as the task scope of a task-bound turn. Today Core's paid projection drops the account of every unconfigured scope whose hold and fences are zero. It prunes whenever paid state is installed and when the paid service opens, so an unconfigured scope's settled total disappears as soon as its hold settles. A later downward correction of that settlement plans a refund from the missing account, and Core refuses the whole correction as invalid private storage. The configured scopes then keep the overstated charge: in the reproduction, 120 microdollars stay charged where the corrected usage costs 29. Ticket [142](142-read-only-the-ledger-tail-in-cores-agent-service.md)'s measurement smoke found the defect, and probes reproduced it on ticket 142's second slice, a38ecb7, with and without the smoke's conditioning of the paid hot set, and on ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md)'s close commit, 89223cc, with it. No existing test admits an attempt that names an unconfigured scope. Ticket 142 does not fix the defect: its measurement driver configures the task scope's spend during setup, so its own correction runs.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Builds on the shared paid path as tickets [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md) and [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md) left it, after ticket 142, which restructures the same paid-state reads.
- A ruled design chooses the remedy before code (D117). The remedy is open; this ticket ranks none.
- Adds no record type and changes no frozen rule, encoding or golden (D148, D163, D164).
- Controlled paid evidence exercises the correction: no live call, credential request or new paid authorization is needed.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153).

## Acceptance criteria

- [x] A ruled design chooses the remedy before code. Met on 2026-11-07, before S1: fresh Fable advisor RulingT143 approved DesignT143's brief with thirteen amendments and no owner gate. The ruling keeps no settled total for an unconfigured (version-0) account and applies a correction's replacement cost only to configured accounts (P1); always reads an unconfigured scope's owner budget status from the ledger fold, which brings the brief's second symptom within criterion 2 (P2); proves both with eight rows in one new test file (P3); and builds them in one slice (P4).
- [x] A downward correction of a settlement whose admission named an unconfigured scope succeeds, live and after the paid service reopens, and afterwards every scope's settled total, configured or not, equals what a fresh replay of the ledger gives. Proof: `TestPaidUnconfiguredScopeCorrection`'s six rows and `TestPaidUnconfiguredScopeColdStatus`'s two. A correction from 120 micros (125 for an overrun) to 29 succeeds live (`live-after-install`, `held-account`, `overrun-fence`, `first-configuration`), after a paid reopen (`reopen-before-correction`), and when the committed correction replays at the paid service's `Open` (`committed-replay`). Every admission scope, the unconfigured task included, has literal settled, held and fence values at every stated phase, also while another attempt's hold (`held-account`) or a fence (`overrun-fence`) keeps the task account present, and equals a fresh plain `Open` of the same ledger at each row's final phase. `correction-race` returns the post-correction totals from a status read suspended outside the service lock, and `read-error` returns an injected read error unmasked and unchanged money.
- [x] Pruning still bounds the paid projection's memory as ticket 13's ruling (P13) and B3 require, and as the package documentation's paid cost paragraph states: unresolved holds and fences never evict, settled results stay a bounded hot set, rebuildable state never becomes authority, and any cold work the fix adds is explicit, labelled and documented, never a whole-history scan on an ordinary path. Proof: `prunePaid` and its callers are unchanged, and an unconfigured account keeps only its exact holds and fences. The new rows' residency checks find the task account absent once its exposure ends (after settlement, correction, fence clearance and release), present while held or fenced, and not reinstalled by a status read; M4, which also zeroes a version-0 hold, fails `held-account` on residency. The unchanged `TestPaidGroupRulesAndFrozenReplay/rules/historical-attempt-and-command-after-eviction` still proves the settled hot set's eviction. The one cold work added, labelled `unconfigured-scope budget status (needPaidTotal)`, is documented with its cost in the package documentation's paid cost paragraph: the existing budget-status fold now also serves version-0 scopes, and no admission, settlement, correction, acknowledgement, `Open` or replay path runs it.
- [x] Upward corrections, caps, refusals and replay are otherwise unchanged, and the frozen goldens replay unchanged. If a record or contract change proves necessary, it waits for the owner. Proof: no existing test, golden, record or contract changed, and no test was deleted or renamed. The focused run passed the unchanged `TestPaidSettlementAndCorrection` (upward corrections and correction refusals), `TestPaidGroupRulesAndFrozenReplay` (frozen chapters 10–14), `TestLoopGroupRulesAndFrozenReplay` and `TestAgentTailColdReadRace`, and the whole package passed plain and under race. In `first-configuration` the first cap counts the scope's history: a 148-micro ceiling refuses a 120-micro admission with `budget_exposure`, and 149 admits it. M3, which zeroes configured accounts' settled totals, fails `live-after-install`. No record or contract change proved necessary.
- [x] A regression test fails on today's code: it settles an attempt whose admission names an unconfigured scope, triggers pruning (paid-state installation and a reopen of the paid service), and corrects the settlement downward. Proof: M1 restored the three production files to their c89447f bytes. `live-after-install` (installation pruning) and `reopen-before-correction` (a paid reopen) then failed when the downward correction was refused as invalid private storage, and `committed-replay` failed at the paid reopen's inconsistent-ledger error; after byte-identical restoration (equal SHA-256) all three passed.

## Answer

`planPaidMoney` now keeps no settled total for an unconfigured (version-0) spend account: the account carries only its exact holds and fences, and a settlement or correction changes settled totals only for configured accounts. Owner budget status for a version-0 scope always reads the labelled cold ledger fold, `unconfigured-scope budget status (needPaidTotal)`, as Agent status already did, so the second symptom, a partial total while a hold or fence keeps the account present, is fixed too. A downward correction for an unconfigured scope therefore succeeds live, after a paid reopen and on replay, and every scope's totals equal a fresh replay; `TestPaidUnconfiguredScopeCorrection` and `TestPaidUnconfiguredScopeColdStatus` cover it. Because caps skip a version-0 task and owner and Agent totals come from the ledger fold, a version-0 hold affects the projection only through residency and fences. Caps, refusals and configured arithmetic are unchanged, and no record type, frozen rule, encoding, golden or contract changed. The [package documentation](../../../internal/core/agents/doc.go) describes the behavior.

Limits: each status read for a version-0 scope is one whole-history pass of the ledger up to the read's prefix, selecting that scope, with operation-local memory, outside `Service.mu` for its first two rounds through ticket [142](142-read-only-the-ledger-tail-in-cores-agent-service.md)'s cold-read protocol; correcting an attempt adds no scope-total scan. This ticket gives no C3 exit proof. C3 status remains solely in its [Status line](../../../docs/ROADMAP-CORE.md#c3-agent-runtime) and [board row](../../../docs/ROADMAPS.md#status-board).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 32, 70.
- Implementation decisions: 6, 12.
- Testing decisions: 1, 2, 4, 11.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 paid-attempt settlement and recovery
- Routing design hard local BudgetBook and final usage accounting
- F3-13 paid-result producer rules

## Comments

2026-11-07 — Published at the project manager's proposal; the owner approved publishing both tickets and having ticket 46 also wait for 144 (D167). Ticket [142](142-read-only-the-ledger-tail-in-cores-agent-service.md)'s measurement smoke found the defect, which predates that ticket; the probes are retained with its evidence. Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.

2026-11-07 — Claimed after ticket 142's close, its only blocker. As a money ticket (D152 item 5) it had a designer's brief and a fresh Fable ruling; the designer worked without a reader, a redundancy removal under D121, because ticket 142's measurement and probes had already found the defect and its site. It has no numerical bound, so no profile was taken.

2026-11-07 — DesignT143 supplied the brief; fresh Fable advisor RulingT143 approved it with thirteen amendments and no owner gate. The ruling keeps no settled total for an unconfigured account, so a correction changes only configured accounts' settled totals, and always reads an unconfigured scope's owner budget status from the ledger fold. The brief found a second symptom, a partial total while a hold or fence keeps the account present, and the ruling holds it within criterion 2. It fixes the production change exactly and builds it in one slice with eight test rows.

2026-11-07 — S1 reached `main` as 0e58a9b803e30b93c527965beb2254b7f928eb28: CoderT143S1 (GPT-6 Astra) built the version-0 branch in `planPaidMoney`, the predicate term in `paidBudgetStatus`, the package documentation and the eight rows. Each of the four rule breaks failed its named rows and passed after byte-identical restoration. ReviewT143S1: approved with no blocking findings; one minor point and two nits needed no change. Hosted check: check 48 (run 42411023994) on 0e58a9b succeeded on Windows, Linux and macOS, the Windows race step included. Merge: `main` c89447f → 0e58a9b on 2026-11-07 at 16:08:48 EST.

2026-11-07 — Closed with the five criteria's evidence above. Close review: approved by a fresh Fable advisor with no blocking findings; the PM applied two of its wording notes. Public ticket/paired-roadmap edits and private design/evidence records land separately (D145); no build-record entry or contract-register change is added (D161). The start and authority gates above stand.
