# Router roadmap

**Status:** Planning document, 2026-10-05; updated 2026-10-08 for D100's grouped outage prompt and the D97–D102 service-loss onset. No router code exists. This roadmap orders routing §14 phases 0–5 as steps R0–R5 (D19), with selected engineering choices and explicit dependencies, stop points and evidence gates. It does not report implementation, contract freeze or owner approval.

## Start here

| Order | Read | For |
|---|---|---|
| 1 | `ROADMAPS.md` | The step format, the dependency graph across roadmaps, the status board and the working rules (D41, D42) |
| 2 | `DECISIONS.md` | Every owner decision, and the open items awaiting the owner |
| 3 | `CONTRACTS.md` | The contracts the router consumes (`ledger-format`, `ledger-reader`, `ledger-appender`) and the one it owns (`context-module`) |
| 4 | This roadmap | Goals, deliverables, resolved choices, dependencies and stop points for R0–R5 |
| 5 | `DESIGN-ROUTING-LAYER.md` | The router design; §14 is rollout, §15 records risks and resolved choices |
| 6 | `DESIGN-CORE-AND-LAUNCHERS.md` §7.3; `DESIGN-UNDERCURRENT.md` §15.2 | The adopted context model (D21); what the field layer needs from router phase 1 |

In this roadmap, "routing §N" refers to `DESIGN-ROUTING-LAYER.md`, "core §N" to `DESIGN-CORE-AND-LAUNCHERS.md` and "Undercurrent §N" to `DESIGN-UNDERCURRENT.md`. Step IDs C0–C10 are in `ROADMAP-CORE.md`; U1–U10 are in `ROADMAP-UNDERCURRENT.md`.

The earlier single roadmap's step 6, "Router phases 0 and 1", is now R0 and R1. Shared budget/provider questions are resolved in R0/R1; implementation contracts still wait for their named freeze gates.

**Model-call and laptop efficiency (D79).** Follow [Core roadmap §1.1](ROADMAP-CORE.md#11-authority-and-model-call-efficiency-guidance-d79): ordinary permission checks, budget admission and mechanical accounting should be deterministic local work, not extra model calls or a remote approval service. Prefer the existing command/rule/heuristic path; optional model-based stages still need their value gate, including their own calls, tokens, latency and CPU/RAM costs. Count helper, social, compaction and retry work in cost per successful outcome. Minimize necessary calls without degrading success, dropping intended social behavior or weakening context isolation. D79 is not a spend entitlement: routing §11 requires explicit owner enrollment, and the existing D22/Core accounting policy remains authoritative.

## 1. Decisions the router carries

| Decision | What it means for the router | Source |
|---|---|---|
| D4: no automatic provider switching in v1; user-chosen changes work; provider betas stay behind flags, off by default | No step adds automatic switching. R1 makes the user-chosen change safe (routing §9.8). Every control that needs a beta, such as per-message effort or OpenAI Flex, stays off until its flag is turned on | Core §2; routing §9.8 |
| D22 safeguard 4: each paid call's maximum cost is reserved against every applicable scope before dispatch, then settled against complete reported usage | C3's existing Core `BudgetBook` owns the durable reservation and settlement. R1 adds exact endpoint bounds, tariff pinning, dispatch-intent recovery and scoped R11 observations—not a second budget service. Every paid model/helper/classifier/compaction call uses the same path; missing spend authorization blocks unless the owner explicitly chooses finite ceiling(s) or unlimited (routing §11) | `DECISIONS.md` D22/B9; routing §9.4, §11 |
| D24 and D51: simultaneous turns on a cold shared prefix may each pay the cache-write price; no staggered starts or pre-warm requests; R0 also keeps a separate shared-prefix cache record keyed by provider, model, cache-isolation scope and prefix hash | The record prices the tools-plus-system prefix across contexts without borrowing history or claiming a cross-context history hit. D24's accepted simultaneous cache-write double-payment behavior remains unchanged | `DECISIONS.md` D24, D51; routing §9.2, §15 |
| D25: the prompt tail lists the agent's other active contexts by counterpart name or task title only | C3 renders the note as a fixed tail section. R1 turns it into a context module once `context-module` is frozen | `DECISIONS.md` D25; routing §5.1; `CONTRACTS.md` |
| D33: the router's per-call decision record stays a separate JSON-lines file | R0's decision record is diagnostic only. Core's D22 action ledger/BudgetBook remains the source for real reservations and settlements; R11 measurement events begin in R1 only. Shadow telemetry never replaces or bypasses C3 accounting | `DECISIONS.md` D33; routing §11 |
| R11: router measurements reach the field layer only as ledger events; router phase 1 precedes real-data resource pressure and any agent-visible impression | R1 delivers R11 events and tail context modules. R0 emits no R11. U5, U6 and U7 wait for R1 | Routing R11, §5.4–§5.5; B10 |
| D20, D40: other roadmaps build against a contract only once the owner has frozen it; a frozen contract changes only by addition; a breaking change needs a decision entry listing every step it affects | The router consumes `ledger-format`, `ledger-reader` and `ledger-appender` (C1) only once frozen. R1 owns `context-module` and requests its freeze | `DECISIONS.md` D20, D40, D50; `CONTRACTS.md` |
| D52: Path A keeps all harness agents testing-only until C4's integrated validation/promotion gate passes and C4 is Done | Otherwise-unblocked R0/R1 work may proceed, including authorized live-provider tests, without a new C4 build dependency. Test output is not colony delivery; pre-C4 replay and drills establish test-workload results, not production savings | `DECISIONS.md` D52; `ROADMAP-CORE.md` C2–C4 |
| D79: literature-grounded authority guidance and modest-laptop efficiency | R0–R5 count their own overhead; R1's local guards and accounting use C3's service rather than a model judge. Optional model work needs demonstrated net value, not merely fewer main-agent calls | `DECISIONS.md` D79; `ROADMAP-CORE.md` §1.1 |

D21 and D22 shape every step. Router state belongs to one `(agent_id, context_id)` and never carries over to another context (routing §5.1, §6.4). Several of an agent's contexts may run turns at once, up to the per-agent limit, with at most one turn per context, so every step's state, ledgers and tests must hold under concurrent turns of one agent. Race testing applies from the first concurrent code (D22, D28, D42).

## 2. Steps

Each step follows the format in `ROADMAPS.md`. Each waits for the step before it; R0 waits for the core's agent runtime, C3. Update a step's status here and in the `ROADMAPS.md` status board in the same commit.

**Testing-only rollout (D52).** Before C4 is Done, all harness-agent runs are tests, never production assignments or delivery. C2's promotion boundary refuses requests as unavailable without the real validator: no canonical ref change, accepted-promotion record, allow-all validator, temporary bypass or manual direct-ref workaround. C4 may exercise the real validator's accepted/refused paths on test workspaces to prove its gate; real colony work starts only after that gate passes and C4 is Done. Later role, field and isolation gates still apply, and this restriction makes no C10/A2 claim. Engineers' ordinary development under D41/D46 is unchanged.

### R0. Shadow mode

**Goal.** Run the context-bound router beside the harness without letting it change any request, so that its classifications, plans and cache predictions can be compared with what actually happened (routing §14 phase 0).

**Delivers.**

- The router interface (routing §5.3) running in shadow inside the core: `beginTurn`, `compile` and `observe` run for every bound turn and call, but the runtime still sends C3's static configuration.
- One `ContextRouteState` per `(agent_id, context_id)` (routing §6.4), kept in the named subdirectory the core assigns under `contexts/<id>/` (C3), each with its own shadow cache ledger for its history and context-local segments: prediction, and correction from reported usage (routing §9.2); the cost model (routing §9.4).
  
  One separate shared-prefix cache record per provider, model, cache-isolation scope and prefix hash prices the tools-plus-system prefix (D51; routing §9.2). It is not part of any context history/cache ledger and never claims a cross-context history hit.
- The default classifier stages proposed in routing §12 (commands, continuation, rules and the heuristic scorer; routing §7.1–§7.5) and the policy's `Plan` (routing §8), logged and never applied.
- The versioned capability file, seeded from routing Appendix B with each value's source and verification date.
- The per-call decision record as a separate JSON-lines file (routing §11; D33).
- The replay set and labelled subset (routing §13) that the exit criterion measures against; before C4 Done, their agent-run data comes only from test workloads (D52).
- R11 measurements are not emitted in R0. The shadow decision record is diagnostic only; C3's ordinary real paid requests continue to reserve and settle through Core's D22 `BudgetBook`, including durable dispatch intent and recovery.

| Definition | Meaning |
|---|---|
| Shadow mode | Router computes and records decisions while C3 keeps its static provider configuration; Core admission, holds, settlement and recovery remain active for every actual paid call |
| Shadow ledger | A context's predicted cache state, kept as if the router's plans had been sent; the baseline for savings (routing §11) |
| Replay set | Recorded context conversations, with consent: prompts, transcripts, usage and protected bindings (routing §13) |
| Labelled subset | Tasks re-run at several rungs; the ground truth is the cheapest rung that succeeds (routing §13) |
| Shared prefix | The tools and system-instruction segments that all of one agent's contexts render identically (routing §9.1) |

**Waits for.** C3, the agent runtime with its static configuration and hand-written Anthropic and OpenAI adapters (D19). R0 has no dependency on R11 event types or a frozen `ledger-format`.

**Stop point.** Before C3 is done, an agent may write the capability file and pure cost functions from routing §9.4. R0 stops before consuming C3's bound turns, transcripts or adapters, and never writes a stand-in for them (D41). It emits no R11, so no R11 contract is needed. Actual C3 paid calls still use Core's real BudgetBook/ledger; disabling shadow events never disables admission or settlement. C4 is not an additional R0 build dependency, but every pre-C4 run remains testing-only under D52.

**Unblocks.** R1.

**Decided.**

- Router state per context, never carried over (D21; routing §5.1, §6.4).
- Simultaneous turns of one agent, each with its own shadow ledger (D22).
- Cache-write double payments accepted; no staggered starts or pre-warm requests (D24). The shared-prefix record prices only the shared tools-plus-system prefix and does not merge context histories (D51).
- The decision record stays a separate JSON-lines file (D33).
- Shadow mode changes no provider and no setting (D4; routing §14).
- The router's `ProviderAdapter` (routing §6.5) wraps C3's hand-written adapters, which expose from the start the canonical transcript records, raw usage with cache tokens and service tier, rate-limit headers, classified errors and opaque provider artifacts that routing §6.5, R5 and R10 require; R0 never re-implements them (`ROADMAP-CORE.md` C3).

**Resolved.** R0 emits no R11 events and has no ledger-schema dependency. R11 begins in R1 after its named format/appender contracts are frozen; this measurement gate is independent of C3's active paid-call accounting.

**Exit criterion.** Routing §14 phase 0, gated on the context kinds C3 delivers: context-switch isolation holds between an agent's general and task contexts and between agents; authorized live-call tests calibrate cache predictions for both per-context history and the shared prefix; classifier agreement is measured on the labelled replay subset. Every actual paid call remains guarded by C3/Core's D22 `BudgetBook`, durable hold, dispatch intent and settlement; R0's shadow router never bypasses this path. Before C4 Done, replay and drill data comes only from test-only agent runs, not production workloads (D52). The full routing §13 drill, a worker switching among its Orchestrator, Diplomat and Corps-board contexts and worker-to-worker rejection, needs the relationship and board contexts and the role graph, so it runs as a C6 integration gate; R1 does not wait for it. Under routing §13's gate, context and turn isolation and authorization must hold before any routing stage ships: R0's partition covers the context kinds that exist at R0, and C6's the rest. Passing these tests does not authorize production use before C4 Done.

**Status.** Waiting (C3).

### R1. Safe per-turn settings, provider awareness, R11 events

**Goal.** Apply routing phase 1 with R11 as an additional measurement stream—not the money ledger. Preserve C3's existing real paid-call Core `BudgetBook` admission, holds, settlement and recovery; make every added router call use exact bounds and the same durable Core path (routing §14, §5.3–§5.5, §11).

**Delivers.**

- Zero-model commands, output guardrails, bound permission ceilings and context-bound continuation (routing §7.2–§7.3, §8.4).
- Standard Anthropic Messages/OpenAI Responses text `BoundCall`: independent inclusive hard input/output maxima, exclusive usage classes, pinned tariff and full maximum reservation before each paid attempt. Do not subtract the requested output cap from input; provider tool/system tokens are counted once within inclusive input. Seal the exact HTTPS authority/path, model/tier, non-secret application headers, credential scope/version and serialized UTF-8 body before pricing; `requestHash` and `quoteHash` use routing §5.3's SHA-256 length-framed encoding. Core binds the sealed request, quote, capability/tariff, `TurnBinding`, period and sorted scopes into the durable hold and rechecks the same envelope/authority before intent (routing §5.3, §9.2, §9.4; Appendix B).
- Durable per-attempt state: reserve, then persist `dispatch_intent`/`SENT` before the first byte; one physical POST; unknown/full hold after intent without final usage; idempotent settlement and no automatic retry (routing §5.3, §10–§11).
- Tail context modules (routing R3, §6.5), and the `context-module` contract: standard-library-only Go with golden samples (D40). R1 owns it and requests its freeze from the owner; D25 is the required first module. A 512-token declared-estimate allowance and 64-KiB serialized-result byte cap are provisional resource defaults, not provider bounds or benchmarks; valid empty output is a no-op, optional modules may be omitted only explicitly, and required-module failure/omission blocks prompt compilation.
- `ProviderStatus` at its true scope and owner-selected provider changes with the handover, notice and recovery rules (§6.4, §9.8, R9, R10). No implicit provider/model or automatic failover. Pauses give the owner one prompt per cause (D100): wait or stop is chosen once for every turn that cause paused, with a per-turn override; switching provider and compacting stay per turn, and a turn the same cause pauses later joins the prompt. The router's pause starts the 2-minute service-loss grace of Core §13.11, and moving all of an agent's work off the failed provider lets its first completed call restore it (D102).
- Core `BudgetBook` reuse (D22): each attempt atomically holds against de-duplicated colony, agent, credential/account, active task/grant and attenuated ancestor scopes. No grant pre-reservation or second accounting service. Missing spend authorization blocks until the owner selects finite ceiling(s) or explicit unlimited; every attempt remains bounded. Warn at 80% only for an explicit finite ceiling. Open holds survive rollover.
- R11 observations written at occurrence: typed status transitions; `router-headroom/v1` metric, validated normalized counts, band, observation/expiry and optional reset at true scope. Routing §5.5 defines the exact finite integer grammar, five bands, 15-minute freshness cap and coalescing rule, including fresh same-band short-reset publication and one-time missing-metric withdrawal. Field consumes authorized band/freshness only; no raw-header stream or refresh probe.

| Definition | Meaning |
|---|---|
| `ProviderStatus` | Availability at its true scope: provider, account, and model and region where relevant (routing §6.4) |
| Headroom bucket | Versioned `router-headroom/v1`; exact bands, integer validation/clamping, expiry and publication rules in routing §5.5. R1 owns this adapter/capability schema, not `law-format`; field response weights do not redefine the measured bands. |
| Budget scope | Colony, agent, provider credential/account, active task/grant and every attenuated grant ancestor; child exhaustion cannot masquerade as global provider exhaustion (§11) |
| Budget reservation | One attempt's full maximum held atomically against each applicable scope until idempotent final-usage settlement; `SENT` without final usage remains fully held across periods (D22 safeguard 4) |
| Tail context module | Volatile content placed after the cached prefix with a declared token cost (routing R3, §6.5) |

**Waits for.** R0. The existing Core `BudgetBook` is the accounting authority. For R11 emission only, R1 consumes frozen `ledger-format` and `ledger-appender`; Core owns the concrete appender and serial assignment (D20, D40, D50). R1 defines `router-headroom/v1` in router capability/adapter data and does not consume `law-format`. The §13 role drill remains a C6 integration gate; R1 does not wait for it.

**Stop point.** Continue all work that uses existing Core accounting when R11 schemas/appender are unavailable, but stop R11 emission and do not mark R1 complete or unblock U5 until both contracts are frozen. That dependency cannot disable C3's paid-call reservation, ledger accounting, settlement or intent/recovery. `context-module` still needs the owner's freeze before its consumers use it; U6 waits for it. C4 is not an additional R1 dependency, and pre-C4 tests/R11 output remain test-only under D52.

**Unblocks.** R2; U5 (needs the R11 events); U6 (needs tail context modules); U7.

**Resolved selections.**

- No automatic provider switching; a user may choose only a configured, authorized destination (D4; routing §9.8).
- State, approvals, transcripts and grants remain per context and turn (D21).
- Every attempt reserves and settles through the same Core `BudgetBook`, against all applicable scopes (D22); no delegated grant pre-reserves capacity.
- R1 emits R11 in phase 1 only. R0 never emits it; R11 does not own/replace money accounting (D33; routing §14).
- D25 remains the context module's first use; decision diagnostics remain separate from colony accounting records (D25, D33).
- R1 requests the `context-module` contract freeze; consumers wait for it (D40). Headroom is `router-headroom/v1`, not a law-file dependency.
- Core's Anthropic/OpenAI adapters remain hand-written (D19, core §8.3); R1 wraps them and does not invent alternate provider transports.

**Resolved design choices and contractual dependencies**

| Topic | Selection / dependency |
|---|---|
| Headroom | R1 owns `router-headroom/v1`, its four metric names, exact band/expiry/coalescing rules and protected normalized counts (routing §5.5). No `law-format` dependency, raw-header stream or refresh model call. |
| Usage attribution | Exact attribution stays in its authorized source context; other audiences get only D79-authorized coarse band/freshness projection, never task, consumer, amount or private account/provider identity. |
| Provider evidence | Current supported maxima/prices and error/usage sources are recorded in routing Appendix B/D, verified 2026-10-05. Only the finite direct text allowlist is eligible; stale or unbounded capability blocks before dispatch. |
| Context module | R1 writes the standard-library Go interface/golden contract and requests owner freeze. D25 is required and first; U6 consumes it only after freeze. Estimates must sum to `TokenAllowance`; the host enforces the 64-KiB canonical-result cap, never truncates JSON, accepts explicit zero-output no-op, and fails closed for required-module omission. |
| Provider selection | Explicit configured provider/model/credential only; no default or implicit failover. Any mid-context destination change is owner-selected and separately re-bounded. |
| Spend authorization | Unset means no paid dispatch. Owner selects finite ceiling(s) or explicit unlimited; all attempts retain a finite endpoint `CostBound`, durable holds and settlement. |
| Budget hierarchy/rollover | Core checks colony, agent, credential/account, task/grant and attenuated ancestors with attempt deduplication. Grants authorize work but do not reserve funds; outstanding holds cross periods, and late usage settles the admission period. |
| Above-floor/over-bound routes | Use the scoped `RouteBlocked` consumer contract in routing §5.3; no clamp, task edit, cap increase, provider switch or retry without owner action. Actual usage above bound is recorded without clamping. |
| Source-down handover | Deterministically remove stale tool payloads, then offer exact owner-approved truncation of earlier complete turns; preserve canonical history. No full-history target summary or chunk cascade; block if nothing approved fits (§9.8). |

**Exit criterion.** Routing §14 phase 1: no success regression; measurable savings on internal test jobs; bound handover and crash/retry drills pass. Prove one physical HTTP POST per admitted attempt; reservation and durable `dispatch_intent` precede network bytes; a pre-intent crash releases only the unsent hold; `SENT` with no final usage remains unknown/full-held and never auto-retries; duplicate settlement is idempotent; over-bound actual usage is recorded, not clamped. Prove task/grant/ancestor scope de-duplication, open holds across period rollover, and late settlement to the admission period. R11 appears once with its correct scope and only authorized projection. Before C4 Done these are test-workload results, not production savings or colony delivery (D52).
At integration, prove `quoteHash` binds the exact request hash, capability/tariff versions/hash and exact components/rates, with one upward rounding of the rational total. Reject changed body, semantic headers, endpoint or credential version before intent. Include Anthropic's accepted `input + max_tokens > context` case, inclusive tool-prompt accounting without duplication, and input-rate-above-output-rate synthetic tariffs so an assumed output reservation cannot underbound input. Prove ContextModule estimate-sum/allowance and 64-KiB cap enforcement, whole-block omission signaling, valid zero-output behavior, and required-versus-optional failure handling. These remain acceptance criteria, not completed runtime proof.
Exercise one grouped prompt per cause across several paused turns and agents, per-turn overrides, a later-paused turn joining the prompt, and the router pause starting Core §13.11's service-loss grace; choosing wait or stop never restores an agent.

Measure model-call counts and net cost/latency per successful test task, including helper work; measure local guard/accounting overhead and whole-process resource use on stated modest hardware (D79). This is an implementation acceptance plan, not a measured result or a new numerical threshold.

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

**Resolved choices.** `balanced` is the selected default profile. Escalation counters, floors and ceilings stay within the bound context/turn (D21); every escalation is a new Core-admitted attempt, within the finite profile output and owner spend authorization (D22; routing §10–§11).

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

**Resolved scope.** D51's separate record prices only the shared tools-plus-system prefix; R3 never imports or counts another context's history as a hit.

**Exit criterion.** Routing §14 phase 3: observed hit rate at or above the static baseline.

**Status.** Waiting (R2).

### R4. Priced model changes

**Goal.** Change tier within the active provider when the price, including cache and reasoning-artifact loss, justifies it (routing §14 phase 4; §9.8).

**Delivers.** Tier changes within the active provider ladder at new-turn boundaries or on mandatory in-turn escalation, priced by routing §9.5; `convert` with its dropped-artifact report (routing §6.5).

**Waits for.** R3.

**Stop point.** Nothing in R4 starts before R3 is done.

**Unblocks.** R5.


**Resolved scope.** R4 changes models only within the explicitly selected active provider. It never selects a vendor or spends beyond the same Core budget/bound; a provider change remains owner-selected (D4; routing §9.8).

**Exit criterion.** Routing §14 phase 4: net savings; no conversion failures.

**Status.** Waiting (R3).

### R5. Optional modules

**Goal.** Add the optional stages and integrations, each only if it pays for itself on replay (routing §14 phase 5; §7.6, §12).

**Delivers.** The LLM classifier and local embedding matcher (routing §7.1 stages 4 and 5, behind §7.6 value/privacy gates), plus an external-module protocol carried only inside Core's existing C10-qualified execution compartment (routing §6.5, §12; Core C10). First-party Go stages remain trusted Core code, not isolated plugins. Provider-hosted tool search, server-side tool/agent iterations, media transformations and other unbounded billable features stay refused until each has a separately verified finite capability profile and gate; paid work remains per-call admitted.

**Waits for.** R4; C10 is additionally required before external plugin/native-helper execution or enablement. First-party in-process Go stages require R4 only.

**Stop point.** Do not execute or enable an external module until C10 has a supported qualified profile and its filesystem/IPC/egress, finite-resource and positive descendant-stop gates pass. Without a complete profile, keep that module off; child-process separation is not a substitute. First-party in-process Go stages remain trusted Core code.

**Unblocks.** None in the current roadmaps.

**Resolved selections.** Provider betas remain off by default (D4). If enabled, paid classification runs at most once per new user message; continuations and escalations never reclassify. It uses the same provider as the selected context unless the owner explicitly grants that exact provider for that exact context; every classifier attempt is separately reserved and settled. Embeddings remain local. Provider-hosted search/iterations/media are excluded until fully bounded. Each external plugin/dependency needs its D29 record; a native encoder, if used as a helper, runs only in C10 (D28, D29).

**External-module boundary.** A child process is not an isolation boundary or a separate sandbox/service. Each helper config must provide finite positive per-invocation limits for wall time, CPU, memory, process count, input bytes and output bytes, or bind one complete existing C10 host-limit profile supplying all six; partial or typical-cost declarations do not qualify. Core gives the external module only a bounded immutable authorized snapshot and one bounded result channel—never Core private roots, credentials or general host IPC. C10 enforces the selected profile's filesystem/IPC/egress and resource limits. Validate binding/schema; discard late output; positive descendant stop is required before any compartment/resource reuse. Remote or paid classifier work is Core-owned and separately admitted. No qualified/enforceable C10 profile means disabled.

**Exit criterion.** Routing §14 phase 5: each first-party module passes its value gate on replay. An external module additionally requires a qualified supported C10 profile; positive authorized-input/result-channel controls and filesystem/IPC/egress denials; finite configured bounds; and timeout/cancellation proof that all descendants stop and late output is discarded before resource reuse. C10's platform proof remains its own unrun gate.

**Status.** Waiting (R4; external modules also wait for C10).

## 3. Resolved-design crosswalk

| Selection | Canonical home | Step |
|---|---|---|
| `balanced` profile; no implicit provider/model; owner selects configured destinations | Routing §12, §15 | R1, R2, R4 |
| Same-provider classifier by default; exact context/provider grant for cross-provider; one call per new message | Routing §7.3, §7.6, §15 | R5 |
| Paid calls require explicit finite ceiling(s) or owner-selected unlimited; missing means no dispatch | Core D22; routing §5.3, §11, §15 | C3, R1 |
| Core-only BudgetBook; scoped task/grant ancestry, open holds across periods, durable intent/recovery | Core D22/D33; routing §11 | C3, R1 |
| R0 emits no R11; C3 paid-call admission/settlement stays active. R11 is phase-1 measurement only | Core D22/D33; routing §14 | R0, R1 |
| Headroom is versioned `router-headroom/v1`; exact attribution private, shared view coarse under D79 | Routing §5.4–§5.5, §15; Core D79 | R1 |
| Target handover uses stale-payload removal and owner-approved exact truncation; otherwise blocks | Routing §9.8, §15 | R1 |
| Historical archive reconciliation is out of router scope; do not inspect it | Routing §15 | No router blocker |
| D51 prefix record prices tools-plus-system only; D24 cache-write double payment accepted | Core D24/D51; routing §9.2 | R0, R3 |
| External plugins/native helpers use only a qualified C10 execution profile, bounded immutable input/result channels and positive descendant-stop; disable without a complete enforced profile | Core C10; routing §6.5, §12 | R5 (C10 prerequisite) |
