# Router roadmap

**Status:** Planning document, 2026-10-02. No router code exists. This roadmap orders the router's rollout, routing §14 phases 0–5, as steps R0–R5 (D19). It names what each step waits for, where it stops and what it unblocks in the other roadmaps. It does not approve designs that `DESIGN-ROUTING-LAYER.md` marks as proposals.

## Start here

| Order | Read | For |
|---|---|---|
| 1 | `ROADMAPS.md` | The step format, the dependency graph across roadmaps, the status board and the working rules (D41, D42) |
| 2 | `DECISIONS.md` | Every owner decision, and the open items awaiting the owner; the shared-prefix cache record (R0) is one of them |
| 3 | `CONTRACTS.md` | The contracts the router consumes (`ledger-format`, `ledger-reader`) and the one it owns (`context-module`) |
| 4 | This roadmap | Goal, deliverables, decided items, open questions and stop point for each router step |
| 5 | `DESIGN-ROUTING-LAYER.md` (draft 0.7) | The router design; §14 is the rollout these steps follow, §15 its open questions |
| 6 | `DESIGN-CORE-AND-LAUNCHERS.md` §7.3; `DESIGN-UNDERCURRENT.md` §15.2 | The adopted context model (D21); what the field layer needs from router phase 1 |

In this roadmap, "routing §N" refers to `DESIGN-ROUTING-LAYER.md`, "core §N" to `DESIGN-CORE-AND-LAUNCHERS.md` and "Undercurrent §N" to `DESIGN-UNDERCURRENT.md`. Step IDs C0–C10 are in `ROADMAP-CORE.md`; U1–U10 are in `ROADMAP-UNDERCURRENT.md`.

The earlier single roadmap's step 6, "Router phases 0 and 1", is now R0 and R1. Its definitions and questions are under R1.

## 1. Decisions the router carries

| Decision | What it means for the router | Source |
|---|---|---|
| D4: no automatic provider switching in v1; user-chosen changes work; provider betas stay behind flags, off by default | No step adds automatic switching. R1 makes the user-chosen change safe (routing §9.8). Every control that needs a beta, such as per-message effort or OpenAI Flex, stays off until its flag is turned on | Core §2; routing §9.8 |
| D22 safeguard 4: before a call, its maximum cost is reserved against every applicable budget scope; afterwards the reservation is settled against reported usage | C3 owns the budget service: scopes, reservations and settlements. R1 builds on it, adding prices from the capability file, threshold crossings and scoped status as R11 events. Every later step's calls go through the same reservation | `DECISIONS.md` D22; routing §11 |
| D24: simultaneous turns on a cold shared prefix may each pay the cache-write price; no staggered starts, no pre-warm requests | No step schedules a pre-warm request or delays a turn start to save a cache write. A separate shared-prefix cache record is not decided (R0) | `DECISIONS.md` D24; routing §9.2, §15 question 9 |
| D25: the prompt tail lists the agent's other active contexts by counterpart name or task title only | C3 renders the note as a fixed tail section. R1 turns it into a context module once `context-module` is frozen | `DECISIONS.md` D25; routing §5.1; `CONTRACTS.md` |
| D33: the router's per-call decision record stays a separate JSON-lines file | R0 writes the decision record as its own file. Router measurements (R11) and budget reservations and settlements go to the colony log | `DECISIONS.md` D33; routing §11 |
| R11: router measurements reach the field layer only as ledger events; router phase 1 precedes real-data resource pressure and any agent-visible impression | R1 delivers the R11 events and tail context modules. U5, U6 and U7 wait for R1 | Routing §5.4, §5.5, §14 |
| D20, D40: other roadmaps build against a contract only once the owner has frozen it; a frozen contract changes only by addition; a breaking change needs a decision entry listing every step it affects | The router consumes `ledger-format` and `ledger-reader` (C1) only once frozen. R1 owns `context-module` and requests its freeze | `DECISIONS.md` D20, D40; `CONTRACTS.md` |

D21 and D22 shape every step. Router state belongs to one `(agent_id, context_id)` and never carries over to another context (routing §5.1, §6.4). Several of an agent's contexts may run turns at once, up to the per-agent limit, with at most one turn per context, so every step's state, ledgers and tests must hold under concurrent turns of one agent. Race testing applies from the first concurrent code (D22, D28, D42).

## 2. Steps

Each step follows the format in `ROADMAPS.md`. Each waits for the step before it; R0 waits for the core's agent runtime, C3. Update a step's status here and in the `ROADMAPS.md` status board in the same commit.

### R0. Shadow mode

**Goal.** Run the context-bound router beside the harness without letting it change any request, so that its classifications, plans and cache predictions can be compared with what actually happened (routing §14 phase 0).

**Delivers.**

- The router interface (routing §5.3) running in shadow inside the core: `beginTurn`, `compile` and `observe` run for every bound turn and call, but the runtime still sends C3's static configuration.
- One `ContextRouteState` per `(agent_id, context_id)` (routing §6.4), kept in the named subdirectory the core assigns under `contexts/<id>/` (C3), each with its own shadow cache ledger: prediction, and correction from reported usage (routing §9.2); the cost model (routing §9.4).
- The default classifier stages proposed in routing §12 (commands, continuation, rules and the heuristic scorer; routing §7.1–§7.5) and the policy's `Plan` (routing §8), logged and never applied.
- The versioned capability file, seeded from routing Appendix B with each value's source and verification date.
- The per-call decision record as a separate JSON-lines file (routing §11; D33).
- The replay set and labelled subset (routing §13) that the exit criterion measures against.
- Optionally, R11 events emitted in shadow. Routing §14 notes that phase 0 may emit them, which lets the field engine test against real event shapes before R1 [INFERENCE].

| Definition | Meaning |
|---|---|
| Shadow mode | The router computes and records its decisions; the runtime sends the static configuration regardless |
| Shadow ledger | A context's predicted cache state, kept as if the router's plans had been sent; the baseline for savings (routing §11) |
| Replay set | Recorded context conversations, with consent: prompts, transcripts, usage and protected bindings (routing §13) |
| Labelled subset | Tasks re-run at several rungs; the ground truth is the cheapest rung that succeeds (routing §13) |
| Shared prefix | The tools and system-instruction segments that all of one agent's contexts render identically (routing §9.1) |

**Waits for.** C3, the agent runtime with its static configuration and hand-written Anthropic and OpenAI adapters (D19). Emitting R11 events in shadow also needs the router event types in a frozen `ledger-format` (C1; D40).

**Stop point.** Before C3 is done, an agent may write the capability file and the cost functions of routing §9.4, which read only that file and fixed inputs [PROPOSAL]. It stops before anything that consumes C3's bound turns, transcripts or adapters, and never writes a stand-in for them (D41). If `ledger-format` does not yet carry the R11 types, R0 completes without shadow emission.

**Unblocks.** R1.

**Decided.**

- Router state per context, never carried over (D21; routing §5.1, §6.4).
- Simultaneous turns of one agent, each with its own shadow ledger (D22).
- Cache-write double payments accepted; no staggered starts or pre-warm requests (D24).
- The decision record stays a separate JSON-lines file (D33).
- Shadow mode changes no provider and no setting (D4; routing §14).
- The router's `ProviderAdapter` (routing §6.5) wraps C3's hand-written adapters, which expose from the start the canonical transcript records, raw usage with cache tokens and service tier, rate-limit headers, classified errors and opaque provider artifacts that routing §6.5, R5 and R10 require; R0 never re-implements them (`ROADMAP-CORE.md` C3).

**Open questions.**

| Question | Notes |
|---|---|
| The shared-prefix cache record (routing §15 question 9) | Awaiting the owner; D24 left it undecided, and `DECISIONS.md` lists it as an open item. The proposal is one record for the tools-plus-system prefix, keyed by provider, model, cache-isolation scope and prefix hash, kept separate from the per-context records. Under D22 several of an agent's contexts keep that prefix warm, and routing §9.2's per-context entries would otherwise predict it as cold and overprice a write the call does not pay. The record would hold prefix segments only, never history, so routing §9.2's rule against claiming a hit from another context's ledger still holds for history [INFERENCE]. Its answer also decides how R3 prices the prefix |
| Whether R0 emits R11 events in shadow | Routing §14 allows it [INFERENCE]. Requires the router event types in a frozen `ledger-format` |

**Exit criterion.** Routing §14 phase 0, gated on the context kinds C3 delivers: context-switch isolation holds between an agent's general and task contexts and between agents; cache predictions are calibrated per context on live calls; classifier agreement is measured on the labelled replay subset. The full routing §13 drill, a worker switching among its Orchestrator, Diplomat and Corps-board contexts and worker-to-worker rejection, needs the relationship and board contexts and the role graph, so it runs as a C6 integration gate; R1 does not wait for it. Under routing §13's gate, context and turn isolation and authorization must hold before any routing stage ships: R0's partition covers the context kinds that exist at R0, and C6's the rest.

**Status.** Waiting (C3).

### R1. Safe per-turn settings, provider awareness, R11 events

**Goal.** Let the router apply the per-turn settings that routing §14 phase 1 lists, track provider and budget status at their true scopes, make the user-chosen provider change safe, and write R11 events from the start (routing §14 phase 1; §5.5). Retrofitting R11 later would change every emitting path and leave the ledger without history from before the change (routing §14).

**Delivers.**

- Zero-model commands (routing §7.2), output guardrails (routing §8.4), Batch for deferrable work, bound permission ceilings (routing R4) and continuation inheritance (routing §7.3).
- Tail context modules (routing R3, §6.5), and the `context-module` contract: a standard-library-only Go package with golden samples (D40). R1 owns the contract and requests its freeze from the owner.
- The D25 note as a context module, replacing the fixed tail section C3 renders (`CONTRACTS.md`).
- The `ProviderStatus` registry at its true scope (routing §6.4), and user-chosen provider changes with the handover, notice and recovery rules of routing §9.8, R9 and R10.
- Budgets that build on C3's budget service (D22 safeguard 4): prices from the capability file, `warn_at` threshold crossings, blocking only within an exhausted budget's scope, and scoped status (routing §11).
- R11 events written at observation: status transitions, headroom samples when the bucket changes, budget threshold crossings, and usage attribution per bound context and provider account (routing §5.4).

| Definition | Meaning |
|---|---|
| `ProviderStatus` | Availability at its true scope: provider, account, and model and region where relevant (routing §6.4) |
| Headroom bucket | A band of remaining requests or tokens; a sample is written only when the bucket changes |
| Budget scope | A named agent or provider-account budget; an agent's exhaustion never marks the account exhausted (routing §9.8, §11) |
| Budget reservation | A call's maximum cost held against every applicable budget scope until the call's reported usage settles it (D22 safeguard 4) |
| Tail context module | Volatile content placed after the cached prefix with a declared token cost (routing R3, §6.5) |

**Waits for.** R0. Under D40 it builds against `ledger-format` and `ledger-reader` only once they are frozen (`CONTRACTS.md` lists R1 as a consumer of both), and against `law-format` too if headroom buckets are versioned with the law (open question below). The routing §13 role drill runs as a C6 integration gate; R1 does not wait for it.

**Stop point.** If the R11 event types are not in a frozen `ledger-format`, finish everything except the R11 emission, then stop and set the status to *Blocked*. If the bucket question places buckets in law files and `law-format` (U1) is not frozen, stop before headroom sampling. Waiting for the owner to freeze `context-module` does not stop the rest of R1, but U6 cannot start until it is frozen.

**Unblocks.** R2; U5 (needs the R11 events); U6 (needs tail context modules); U7.

**Decided.**

- No automatic provider switching; user-chosen changes work; betas flagged off (D4; routing §9.8).
- Router state, approvals and grants per context and turn (D21; routing §5.1).
- Reserve, then settle, against every applicable budget scope (D22 safeguard 4; routing §11).
- The other-active-contexts note in the tail (D25).
- Budget reservations, settlements and R11 measurements in the colony log; the decision record in its own file (D33).
- R11 is built with phase 1, not after it (R11; routing §14).
- R1 owns `context-module`; consumers build against it only once frozen (D40).
- The core's adapters are hand-written for Anthropic and OpenAI (D19; core §8.3).

**Open questions.**

| Question | Notes |
|---|---|
| Undercurrent Q28 and routing §15 question 7: which headroom buckets adapters use, and whether their boundaries live in router configuration or in law files | The answer decides whether R1 consumes `law-format` (`CONTRACTS.md`) |
| Routing §15 question 8: the disclosure scope of usage attribution | Ties to Undercurrent Q5. Attribution is recorded in the ledger either way; the question is who may perceive it |
| Provider adapters | Verify current rate-limit headers and error codes against provider documentation before coding; routing Appendix B was verified on 2026-09-28 and provider details change |
| The context-module interface | Fix it here, since Undercurrent's renderer plugs into it at U6 and the D25 note is its first module. Request the freeze when both fit it |
| Routing §15 question 3: which providers to offer when the active one stops, and whether local budgets ship with default values | Needed for the R9 choices and for budget configuration |
| Routing §15 question 5: how the owner-facing flow resolves a required floor above the budget ceiling | No valid `Plan` means no dispatch; the blocked-result shape is undesigned (routing §5.3, §8.2). R1 is the first step that blocks a call |
| Routing §15 question 6: what bounded compaction or owner-approved reduction handles a history that cannot fit the target while the source is unavailable | Part of the handover in routing §9.8 |

**Exit criterion.** Routing §14 phase 1: no success regression; measurable savings on internal jobs; bound handover drills pass; every status transition and budget crossing in the drills appears in the action ledger with its correct scope.

**Status.** Waiting (R0).

### R2. Effort ladder

**Goal.** Spend capability through effort on the active model before any tier change, using cache-safe controls, and escalate on failure signals (routing §14 phase 2; §8.1, §8.4, §10).

**Delivers.** The ladder of (tier, effort) rungs per profile (routing §8.1); effort changes through cache-safe controls where they exist (routing §8.3, §9.5); escalation with counters, ceilings and history bound to the context and turn (routing §10); escalation costs in the decision record.

| Definition | Meaning |
|---|---|
| Rung | A (tier, effort) pair; tiers map to concrete models per provider in configuration (routing §8.1) |
| Profile | Defaults for regular users: difficulty floors, output sizes, reasoning reserve, ceiling rung, escalation limit (routing §8.1) |
| Escalation | A revision of the bound turn's plan after a failure signal, within the profile's limit (routing §10) |

**Waits for.** R1.

**Stop point.** Nothing in R2 starts before R1 is done.

**Unblocks.** R3.

**Decided.** Cache-safe controls that need a beta stay off until their flag is on (D4). Escalation state belongs to the bound context and turn (D21; routing §10). Escalated calls are reserved and settled like any other call (D22 safeguard 4).

**Open questions.**

| Question | Notes |
|---|---|
| Routing §15 question 1: should `balanced` be the default profile for regular users | Routing §12 proposes it; the ladder makes the choice take effect |

**Exit criterion.** Routing §14 phase 2: net savings after escalation costs.

**Status.** Waiting (R1).

### R3. Cache planner active

**Goal.** Let the cache planner decide when optional changes apply, instead of only predicting (routing §14 phase 3; §9.5–§9.7).

**Delivers.** Pending changes and change points (routing §9.6), the apply-or-defer rule (routing §9.5), TTL selection and breakpoints (routing §9.7), and scheduled compaction.

| Definition | Meaning |
|---|---|
| Change point | A moment when the selected context's cache is being rebuilt anyway (routing §9.6) |
| Pending change | An optional cache-breaking change waiting for a change point (routing §6.4, §9.5) |

**Waits for.** R2.

**Stop point.** Nothing in R3 starts before R2 is done.

**Unblocks.** R4.

**Decided.** No pre-warm requests and no staggered starts (D24). Each context's ledger is its own; another context's activity does not refresh it (D21; routing §9.2).

**Open questions.** None from routing §15. How the planner prices the shared prefix follows the R0 answer on the shared-prefix record.

**Exit criterion.** Routing §14 phase 3: observed hit rate at or above the static baseline.

**Status.** Waiting (R2).

### R4. Priced model changes

**Goal.** Change tier within the active provider when the price, including cache and reasoning-artifact loss, justifies it (routing §14 phase 4; §9.8).

**Delivers.** Tier changes within the active provider ladder at new-turn boundaries or on mandatory in-turn escalation, priced by routing §9.5; `convert` with its dropped-artifact report (routing §6.5).

**Waits for.** R3.

**Stop point.** Nothing in R4 starts before R3 is done.

**Unblocks.** R5.

**Decided.** Changes stay within the active provider; provider changes remain the user's choice (D4).

**Open questions.** None from routing §15.

**Exit criterion.** Routing §14 phase 4: net savings; no conversion failures.

**Status.** Waiting (R3).

### R5. Optional modules

**Goal.** Add the optional stages and integrations, each only if it pays for itself on replay (routing §14 phase 5; §7.6, §12).

**Delivers.** The LLM classifier and the local embedding matcher (routing §7.1 stages 4 and 5, behind the value gate of §7.6); provider tool search and meta-tools; a stable plugin protocol, with plugins as child processes (routing §12).

**Waits for.** R4.

**Stop point.** Nothing in R5 starts before R4 is done.

**Unblocks.** None in the current roadmaps.

**Decided.** Provider betas stay behind flags, off by default (D4). The core never uses cgo, so a native encoder runs only as a separate helper program (D28, D29). Each new dependency needs its record under D29.

**Open questions.**

| Question | Notes |
|---|---|
| Routing §15 question 2: may the optional LLM classifier send prompts to a different provider from the main model | Decide before the classifier is enabled |

**Exit criterion.** Routing §14 phase 5: each module passes the value gate on replay.

**Status.** Waiting (R4).

## 3. Routing open questions by step

| Routing §15 question | Step |
|---|---|
| 1. `balanced` as the default profile | R2 |
| 2. LLM classifier sending prompts to another provider | R5 |
| 3. Providers offered when the active one stops; default budget values | R1 |
| 4. Reconciling the remaining material in `superceeded-Design.md`, kept in the archive folder `../dwarfharness/`, outside this repository | None; an owner question that blocks no router step |
| 5. A required floor above the budget ceiling; the blocked-result shape | R1 |
| 6. Bounded compaction when a history cannot fit the target | R1 |
| 7. Headroom buckets and where they are configured (Undercurrent Q28) | R1 |
| 8. Disclosure scope of usage attribution (Undercurrent Q5) | R1 |
| 9. The shared-prefix cache record (D24) | R0; awaiting the owner |
