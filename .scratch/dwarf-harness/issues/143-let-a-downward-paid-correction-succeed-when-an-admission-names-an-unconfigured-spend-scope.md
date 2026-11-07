# 143: Let a downward paid correction succeed when an admission names an unconfigured spend scope

Status: ready-for-agent
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

- [ ] A ruled design chooses the remedy before code.
- [ ] A downward correction of a settlement whose admission named an unconfigured scope succeeds, live and after the paid service reopens, and afterwards every scope's settled total, configured or not, equals what a fresh replay of the ledger gives.
- [ ] Pruning still bounds the paid projection's memory as ticket 13's ruling (P13) and B3 require, and as the package documentation's paid cost paragraph states: unresolved holds and fences never evict, settled results stay a bounded hot set, rebuildable state never becomes authority, and any cold work the fix adds is explicit, labelled and documented, never a whole-history scan on an ordinary path.
- [ ] Upward corrections, caps, refusals and replay are otherwise unchanged, and the frozen goldens replay unchanged. If a record or contract change proves necessary, it waits for the owner.
- [ ] A regression test fails on today's code: it settles an attempt whose admission names an unconfigured scope, triggers pruning (paid-state installation and a reopen of the paid service), and corrects the settlement downward.

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
