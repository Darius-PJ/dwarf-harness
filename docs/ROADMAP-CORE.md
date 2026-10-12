# Core roadmap

**Status:** each step's status is in its **Status.** line below and on the [status board](ROADMAPS.md#status-board), and each contract's in [`CONTRACTS.md`](CONTRACTS.md). This roadmap orders Core work in steps C0 to C10; GUI work is specified in [`ROADMAP-GUI.md`](ROADMAP-GUI.md). A selected requirement is not implementation, a frozen contract or a passed gate.

**Consolidated design, D89–D96:** the [fortress/RPG design](../.scratch/fortress-gui/spec.md) now binds C3 runtime eligibility/tasks, C6 relationship/evidence/publication, C8 creation/Purpose/alignment/succession, C9 backend pinned skills and C7 shared launcher/presentation handoffs. [D96](DECISIONS.md#d96-pre-alpha-fortress-alignment-and-consolidation-boundary) authorizes this pre-alpha stopping point; reconciliation no longer awaits owner confirmation. D97–D102 add the ordinary service-loss and officer-recovery procedure (Core §13.11). Exact attenuation/rubric/encoding, presentation persistence and producer schemas remain explicit work. The consolidation changed no step status, contract freeze or D52 approval, and ran no evidence gate.

## Start here

Read `ROADMAPS.md` first for the cross-project capability graph and status board; this file owns Core step details. `DECISIONS.md` remains the decision log and `CONTRACTS.md` the six-contract register: C1 owns `ledger-format`, `ledger-reader` and `ledger-appender`; C7 owns `launcher-protocol`. C3 initially renders D25's fixed tail, with R1 later owning `context-module`; C3 does not wait for R1. Read the assigned step and controlling Core §13, including §13.10's open producer work. The owner approved C2's promotion boundary (D112); its F2 family must be frozen under D40 in parts, each part before the code that writes it (D113). C8 requires actual C9 **backend** alignment capability, not whole C9/GUI; C3 generic guards and C7 base gain no reverse C8/GUI dependency. No roadmap text claims completed implementation or freeze.

**Authority.** Entries in `DECISIONS.md` are settled. Everything marked **[PROPOSAL]** or **[INFERENCE]**, here or in a design document, is a recommendation the build may revise, provided the revision is recorded as a B entry in `DECISIONS.md` (`ROADMAPS.md`, rule 6). Anything a document marks as awaiting the owner goes back to the owner.

**Testing-only until validated delivery (D52).** C2 builds workspace mechanics, C3 the runtime, and C4 the real validation-and-promotion integration. All harness agents and otherwise-unblocked layer work use test workloads until C4's end-to-end gate passes and C4 is Done. Real colony assignments and delivery wait; private test output is not delivery. C2/C3 builds refuse promotion as unavailable, with no permissive validator or bypass. C4 proves accepted and refused promotions on test workspaces using the real validator. This does not restrict engineers developing DwarfHarness under D41/D46, waive any later release gate, or provide C10's OS isolation.

**Working principles for every step**, kept from the earlier single roadmap. Write the test before the code. Never let a component read the wall clock inside anything that must be replayable. Never let a component trust an agent's report where a host observation is possible. Add a dependency only under D29.

**Performance is feasibility (D78).** Before architecture choices, contract freeze or readiness, state the realistic every-change workload, steady and burst accepted-throughput/latency expectations, and CPU, RAM, disk and process costs. Account for contention, speculative rework, ordered publication and failure/recovery while preserving safety; a strong safety argument alone is insufficient. Distinguish models and assumptions from measurements, and specify concrete runtime verification at the relevant implementation gate. Refusing work or serializing its backlog is not throughput. The illustrative capacity analysis belongs in [core §3.5](DESIGN-CORE-AND-LAUNCHERS.md), not a claimed benchmark or an invented numerical SLA.

**References.** "Core" is `DESIGN-CORE-AND-LAUNCHERS.md`; its Appendix A.7 to A.9 hold the measurements of 2026-10-02. "Routing" is `DESIGN-ROUTING-LAYER.md`; its requirements are written "routing R10" or "routing R11", while a bare R0 to R5 is a step in `ROADMAP-ROUTER.md`. "Undercurrent" is `DESIGN-UNDERCURRENT.md`, with Q numbers in its §17. "Validation" is `RESEARCH-ADMISSIBLE-VALIDATION.md`, with V numbers in its §8. The archive folder `../dwarfharness/`, outside this repository, is not a source for any step.

## 1. Decisions this roadmap implements

The entries themselves are in `DECISIONS.md`; this table shows only where each is built. D1 to D14 are recorded in core §2, and steps cite the ones they rely on.

| Decision | Subject | Steps |
|---|---|---|
| D15 | Append-only action ledger of typed deltas; content-addressed bodies | C1; every later step writes to it |
| D16 | Private scratch as git worktrees; canonical changes only through core promotion | C2; C10 makes the boundary enforceable |
| D17, amended by D23 | Scratch per bound work context and workspace root | C2, C3 |
| D18 | Fast-forward-only promotion | C2; C4 adds the gate |
| D19 | Separate roadmaps; the runtime step; the step format | C3; every step |
| D20 | Undercurrent builds only against frozen contracts | C1, whose freeze is the handoff to U1 |
| D21 | One execution session per agent, many private contexts | C3; relationship and board contexts in C6 |
| D22 | Simultaneous turns with four safeguards | C3 |
| D23 | Temporary scratch for a second writer to one task | C1 (event fields), C2 (worktrees), C3 (turns) |
| D24 | Prompt-cache double payments accepted | C3: no staggered starts |
| D25 | A turn lists its agent's other active contexts | C3 |
| D26 | One core and one ledger per colony | C1, C7 |
| D27 | Windows first; portable to macOS and Linux | C0, C2, C3, C10 |
| D28 | Toolchain and cgo | C0, C3 |
| D29 | Dependency policy | C0; every step that adds a dependency |
| D30 | Repository | C0 |
| D31 | Decision log and commit rules | C0 |
| D32 | `Agent.md` spelling | C3, C8 |
| D33 | What the colony log holds | C1, C3, C6 |
| D34 | Ledger storage: our own append-only log | C1 |
| D35 | Record format and evolution | C1 |
| D36 | Integrity and retention | C1 |
| D37 | IDs and time | C1, C3 |
| D38 | Where colony data lives | C1, C2, C3, C4 |
| D39 | Two programs | C3, C7 |
| D40 | Contracts between roadmaps | C1, C7 |
| D41 | Several agents in one repository | C0; every step |
| D42 | Check command and CI | C0, C3 |
| D47 | Validation parameters belong to U1.L's versioned strict-JSON `law-format`; C4 waits for U1.L Done and owner-frozen law-format, not the whole U1.E engine | C4; C5 consumes them |
| D48 | `main` is canonical and never checked out; promotion authority and human changes use the validation-gated branch path | C2; C10 makes the child-process boundary enforceable |
| D49 | Durable colony-ledger cursors; separate ephemeral, indexed per-turn text channel | C1, C7 |
| D50 | Single core-owned appender serializes writes and assigns the colony sequence | C1 defines the contract; C3 wires it |
| D52 | Path A: settle the promotion boundary before C2/C3 code; testing-only agents until C4 proves validated delivery | C2–C4; downstream integration gates |
| D53 | Core-owned durable promotion requests outlive the requesting turn; results stay with the original context | C1–C4 |
| D54, superseded by D77 | Durable request order retained; bounded cumulative validation replaces whole-pipeline serialization and stale-original-base refusal | C2, C4 |
| D55 | Crash recovery reconciles effects and bookkeeping, never automatically retries promotion execution | C1–C4; downstream replay |
| D56, clarified by D77 | Immutable original submission/base/capture; derived integration candidates are separate, and later scratch work remains untouched | C1–C4 |
| D57 | Separate request-specific promotion cancellation by submitting context or colony owner | C2–C4, C7 |
| D58 | Deduplicate by request ID, never merge different IDs by content; new attempts use new IDs | C1–C4, C7 |
| D59, preserved by D77 | Durable intent closes cancellation; the entire durable finalization sequence is serialized through finalization-slot release | C1–C4, C7 |
| D60 | Candidate includes tracked changes/deletions and non-ignored new files; host, not mutable index, determines contents | C2, C4 |
| D61 | Accept the cost of fresh validation after policy changes; D64 selects the activation/revalidation behavior | C2, C4; U1 policy handoff |
| D62 as amended by B4 | Bounded four-checkpoint local FIFO per enrolled context/workspace; hard local ancestry is distinct from the foreign integration predecessor | C1–C4, C7 |
| D63 | Durable results are passive by default; no follow-up model turn solely on completion | C3, C4, C7 |
| D64, clarified by D77 | Finish already-intended finalizations, activate changed policy, then refresh affected lanes with original submission/request fixed; only a still-current derived tree may stay fixed | C1–C4; U1 policy handoff |
| D65 | Only the colony owner authorizes each exact policy version; Core executes and records activation | C1–C4; U1 policy handoff |
| D66 | Immediate or one-off scheduled activation of the approved version; a scheduled time is not-before eligibility, not a deadline | C1–C4; U1 policy handoff |
| D67 | One colony-wide validation-policy version; affected workspaces switch together, with unresolved finalization delaying the switch | C2, C4; U1 policy handoff |
| D68 | Stop superseded validation children and confirm exit before replacement runs; retain history without treating supersession as test failure | C2–C4 |
| D69 | Separately changed required checks or execution environments also require controlled cutover and same-request fresh validation | C2, C4; U1 evidence handoff |
| D70 | Return busy without capture if exclusive host-managed capture cannot begin immediately; no queued capture or automatic retry | C2–C4 |
| D71, clarified by D77 | A derived tree identical to its actual durably accepted predecessor returns durable no-change; speculative redundancy is not terminal | C1–C4 |
| D72 | C4 requires real automated checks; empty/invalid/unconfigured gates are unavailable; human-oracle verdict work stays C5 | C2, C4, C5 |
| D73 | Initial C4 runs every declared required check regardless of changed paths; no path/graph-based exclusion | C2, C4; U1/U4/U5 evidence handoffs |
| D74 | Git-compatible canonical candidate contents under recorded workspace normalization settings; not raw disk hashes or mandatory LF everywhere | C2, C4; U3 identity handoff |
| D75 | Pin explicit workspace local/global ignore inputs, preserving existing private-file exclusions; no automatic following of personal settings edits | C2, C4 |
| D76 | Captured scratch `.gitignore` edits govern the same submission's new files; tracked changes remain included | C2, C4 |
| D77 | Bounded concurrent cumulative integration validation per workspace, successful-prefix publication in durable request order, serialized durable finalization and descendant invalidation | C1–C4, C7 |
| D78 | Workload/capacity reasoning before architecture, freeze and readiness; real runtime measurements at the relevant gate, without weakening safety | Every step; especially C1–C4 and C10 |
| D79 | Literature-grounded authority implementation guidance; as few necessary model calls as possible on modest laptops without sacrificing the vision | Every step; §1.1, especially C1, C3, C6, C7 and C10 |
| B1 | Line endings | C0 (in place); C2 for the agents' repositories |
| D80 | Coarse private-work pressure and permission-scoped overlap hints only | C6; field design owns projections |
| D81 | Explicit federation among independently sovereign operators; no implicit trust/global coordinator | C6 boundary, field design |
| D82 | Owner ceilings; explicit narrower task grants, no repeat consent within scope | C3, C6 |
| D83 | Retired participant retains only delivered history, read-only, holes preserved | C6, C7 |
| D84 | Grand roles coordinate corresponding chains; owner apex, no blanket descendant access | C8 |
| D85 | Fresh memory unless explicit manifest selects already-shareable lessons/artifacts | C8 |
| D86 | Check retirement excludes repairing context and clone lineage only, not all of the agent's contexts | C5, C6 |
| D87 | Exactly one demand-started native macOS VM slot on Apple Silicon; Linux guest separately budgeted; Intel Mac Linux guest only | C10 |
| D89–D90 | Fortress/RPG projections, actual waiting-task stockpiles, administrator inspection, durable current presentation with bounded per-run cosmetic history | C3, C6–C8; GUI-F |
| D91–D93 | Shared Purpose, exactly-one leadership, pinned mandatory first-action alignment, human completion, creation intent, Corp Creed and Aspirant preparation | C3, C6–C9 |
| D94–D95 | Scoped deterministic succession attenuation; signed trust; retire only after successful handover; ordinary officer recovery dependency; approved initial mythology and Grand/Corps lineage | C3, C6–C8 |
| D96 | Authorized pre-alpha consolidation, not exhaustive closure, implementation, contract freeze or readiness advancement | All affected producers |
| D97–D102 | Ordinary service loss for every agent: causes, 2-minute onset, outages, owner notices and grouped prompts, Corps operation with reserved questions and allotment pauses, outage work rules, owner stand-in, restoration by a completed call, unrecoverable officers and office handover | C3, C6–C8; R1; GUI-F |
| B16, amended by D90–D95 and D103 | Retain Fyne, the original-art pipeline (an offline Go generator since D103) and cosmetic/operational separation; replace single-axis labels, permanent cosmetic edits and preserve-only autocomplete | C6/C7; GUI-F |
| B2 | Producer-ready catalogue freezes; U1.L/U1.E split | C1, C4 |
| B3 | Durable framed file ledger, persistent lock, bounded replay/body rules | C1 |
| B4 | Four-checkpoint admission and local-parent capture ancestry (amends D62) | C2–C4, C7 |
| B5 | Cumulative train, exact activation/finalization and whole-generation disclosure barrier | C1–C4, C7 |
| B6 | Full-check validation adapters and C5 governance | C4, C5 |
| B7 | Context/task/grant/broker mechanisms | C3, C6 |
| B8 | Roles, birth, Griot and typed workflow mechanisms | C8, C9 |
| B9 | One Core-owned hard local BudgetBook | C3, C6 |
| B10 | Routing defaults/features/headroom | Routing design and Router roadmap |
| B11 | Fyne v2.8.1, launcher/IPC, Windows/Linux/macOS execution boundary | C7, GUI, C10 |
| B12–B14 | Field laws/graphs/sources, perception, and explicit federation protocol | Field design and roadmap |
| B15 | Finite resource profiles and workload/capacity/evaluation gates | C1–C4, C10 |
| V3 | Check retirement authority | C5; C6 for the Corps-authority gate |
| V6 start | O4 plus canaries; sample re-runs off by default | C4 |
| R11 | Router measurements reach the field layer only as ledger events | C1 defines the event types; R1 writes them |

### 1.1 Authority and model-call efficiency guidance (D79)

**Status of this guidance.** The owner approved these literature-grounded recommendations as a starting point, not a frozen permission API, delegation policy or performance result. Keep settled decisions and owner gates intact; record revisions under the existing B-entry rule. Preserve relationships, diplomacy, stories, initiative and environmental influence.

**Mechanisms to prefer [INFERENCE]:**

- For each consequential operation, identify who may propose and authorize it, which component executes and observes it, and who may receive the result. These are responsibilities, not five agents or services. Organizational rank is not blanket permission; preserve D57's request-specific cancellation and D65's exact-version owner authorization.
- Use small, default-deny checks inside existing Core boundaries: authenticated actor, bound context, operation, target and relevant current state. Prepare scoped handles at turn construction, but check their use at dispatch and disclosure. A guessed ID, workflow edge, story or claimed role grants no authority. No extra model call or network round trip should be needed for ordinary local authorization.
- Prefer direct in-memory lookups; cache derived authorization only if useful and with explicit invalidation. Order the final authority check and action admission against grant changes; a check followed by an unguarded later effect is insufficient. Recheck queued delivery/retrieval. An authority revision is not U1's validation-law version; unrelated membership changes must not gratuitously invalidate validation evidence.
- Where delegation is permitted, derived scope may only narrow; host-backed grants need not become cryptographic bearer tokens inside Core. Who may delegate, exact grant/revocation representation, and effects on already-running work remain to be specified. Revocation cannot retract disclosed information or undo committed effects; D59's durable-intent cutoff is unchanged.
- Keep permission separate from resource admission: D22 budget reservations, runner limits and D77 finalization remain distinct checks. Serialize conflicting state transitions, not model reasoning, peer waits or whole validation runs. Reuse the existing action ledger and appender; do not add a duplicate synchronous audit service or silently remove D33's separate router decision record.
- Use zero-model mechanical administration and passive status/results where semantics permit. Count necessary social, helper, compaction and retry calls rather than treating them as free; no model call merely to decide a deterministic permission or bookkeeping fact. This does not remove independently authorized social scheduling, select a Griot cadence or override D63/U8.

**Source basis, not a compliance or performance claim:**

| Primary source | Transferable lesson and boundary |
|---|---|
| [NIST SP 800-162](https://doi.org/10.6028/NIST.SP.800-162), [SP 800-207 §3](https://doi.org/10.6028/NIST.SP.800-207) | Subject/object/action attributes and separate policy administration, decision and enforcement are logical functions; no requirement to deploy a remote service for each |
| [NIST-hosted RBAC proposed standard](https://csrc.nist.gov/csrc/media/projects/role-based-access-control/documents/rbac-std-draft.pdf) | Roles organize permissions; hierarchy/inheritance is optional. This is the published model/draft, not a claim to have implemented or certified against the paid standard |
| [Saltzer and Schroeder, 1975](https://web.mit.edu/Saltzer/www/publications/protection/Basic.html) | Economy of mechanism, fail-safe defaults, complete mediation and least privilege; remembered permissions must track authority changes |
| [Macaroons, 2014](https://research.google.com/pubs/archive/41892.pdf) | Narrow delegated credentials by adding restrictions; borrow attenuation without importing unnecessary token machinery or assuming delegation is authorized |
| [Zanzibar, 2019](https://www.usenix.org/system/files/atc19-pang.pdf) | Permission/content ordering and revision-aware cached checks; do not import global replication or its published throughput as a local benchmark |
| [Lampson, 1973](https://www.cs.cornell.edu/andru/cs711/2003fa/reading/lampson73note.pdf) | Resource/timing effects can disclose information; numeric or aggregated fields are not inherently private |

**Placement and proof.** C1 carries attribution/version evidence through its existing event design; C3 enforces dispatch and resource admission; C6 checks communication and retrieval; C7 preserves bound authority across views; C10 closes bypass paths under its stated threat model. At those gates exercise spoofed identity, cross-context reuse, revocation/reassignment, queued access and recovery as applicable. Measure authorization latency including contention, bounded memory/lookup work, idle work and whole-process CPU/RAM/disk on stated modest hardware; ordinary local checks should add no model calls or remote authorization dependency. Retain parallel validation and independent conversations. Compare model calls, tokens, cost and latency per successful outcome, not call count alone.

**Limits.** Local API checks do not contain an unrestricted same-user child. C10 remains separate. Private-work contributions to shared fields still require an explicit disclosure decision (`FIELD-PHILOSOPHY.md` §9); guidance does not approve new field sources, cross-colony communication, automatic rank inheritance or new dependency edges. Accounting uncertainty is the next research topic, not a policy selected by this section.


## 2. Steps

Every step uses the format in `ROADMAPS.md`. Definitions tables are kept from the earlier roadmap where it had them. §3 indexes the open questions.

### C0. Repository, toolchain, check command, decision log

**Goal.** A Go module whose structure mirrors the layer boundaries in the designs, so that the separations the documents depend on (core, router, Undercurrent, launchers) are enforced by package imports from the first commit. The step also gives every later step its toolchain and the one check command that gates integration.

**Delivers.**

- `go.mod` with module path `github.com/Darius-PJ/dwarf-harness` (D43), `go 1.27` and `toolchain go1.27.2` (D28, D45, D106). Its `tool` lines pin staticcheck 2026.2.1 and govulncheck v1.8.0 (D42, B17).
- The package layout in D45: `cmd/dwarf-harness/`, `contract/{ledger,launcher,module,law}/`, `internal/{core,router,undercurrent}/`, `tools/check/`, `docs/`. C0 creates only `tools/check`, the one directory with content at C0; `cmd/dwarf-harness` and every other package are created by the first step that gives them real content (D41: no placeholders; B17). The nested `gui/` module and `go.work` arrive with the GUI roadmap (D44).
- The import-boundary test, which runs `go list -deps` for every package and fails on any import outside D45's table.
- `tools/check`, run as `go run ./tools/check` (D42): gofmt, `go vet`, staticcheck and govulncheck; the import-boundary test; tests with `CGO_ENABLED=0`; the tests again with `-race`, with `CC` set explicitly to the pinned compiler (D28); Linux and macOS builds with `CGO_ENABLED=0`.
- Test conventions (D42): `testing/synctest`, Go's native fuzzing, rapid v1.3.0 as a test-only dependency, added to `go.mod` with the first test that imports it (B17), and golden samples for contracts. D42 records staticcheck, govulncheck and rapid under D29.
- `AGENTS.md`, the canonical agent instructions, which `CLAUDE.md` imports (B17), written following `skill://writing-for-agents`. It states the workspace, check, commit, sign-off, layer, dependency and Go-command rules every coding turn needs, and points to the reading order and working rules in `ROADMAPS.md` (D41, D42, D46), the working principles at the top of this roadmap, D45's import table as encoded in `tools/check`, and D29's rule on when to write code ourselves and when to use a recorded library.
- The toolchain on the owner's machine (D28, D106): go1.27.2 from the official installer, and llvm-mingw 20260922 (clang 23.1.2, UCRT, x86_64 host), pinned and installed outside `PATH`.
- Already in place since 2026-10-02: the repository, `docs/` with the design documents and `DECISIONS.md` (D30, D31), and `.gitattributes` (B1).

| Definition | Meaning |
|---|---|
| Decision record | A short dated entry: the question, the decision, who made it (owner or build) and the documents it changes. Owner decisions are numbered D19 onward and build decisions B1 onward; a build decision may revise anything marked **[PROPOSAL]** or **[INFERENCE]** (`DECISIONS.md`) |
| Layer boundary | A package that may not import another; for example `undercurrent/...` may not import the router, broker or launchers (Undercurrent §13.1) |
| Import allowlist | D45's table: what each package tree may import besides the standard library. `contract/...` nothing else; `internal/undercurrent/...` only `contract/...`; the router and core add `golang.org/x/...` and recorded exceptions; `gui/...` never imports `internal/...` |
| Check command | `go run ./tools/check`. It gates fast-forwarding `main` (D42, D46); agents commit on their own branches when the tests they touched pass |

**Waits for.** Nothing. Every owner input has arrived (D43–D46); the repository and `docs/` were created on 2026-10-02.

**Stop point.** None before the exit criterion. Dependencies beyond the standard library and the pinned tools wait for a D29 record before reaching `main` (D41).

**Unblocks.** C1.

**Decided.**

| Decided | Source |
|---|---|
| Go version and minimum toolchain: go1.27.2 from the official installer; `go 1.27` in `go.mod` | D28, D106 |
| The core and the terminal program never use cgo. Every build states `CGO_ENABLED`, because Go's default switches cgo on when a C compiler is on `PATH` | D28; core Appendix A.9 |
| C compiler: llvm-mingw 20260922 outside `PATH`, reached through `CC` only for race-detector test builds and the GUI build. Not TDM-GCC or Microsoft's compiler | D28; core Appendix A.9 |
| Import boundaries are enforced by the import-boundary test, against the per-layer allowlist | D29 |
| Dependency tiers and records; vendoring when there is a reason; copied code only in `third_party/<name>/` | D29 |
| Repository: `dwarf-harness` in its own folder, with `docs/` as the only living copy of the design documents; module path `github.com/Darius-PJ/dwarf-harness`; no remote yet | D30, D43 |
| A nested `gui/` module, created with the GUI roadmap; a committed `go.work`; the GUI module also tested with `GOWORK=off` | D44 |
| The package layout and the import table the check enforces | D45 |
| The full check passes before `main` moves; agents commit on their branches when the tests they touched pass | D46 |
| The check's contents; tools pinned with `tool` lines; test tooling, with rapid in place of the earlier `testing/quick` suggestion | D42 |
| The decision log with its numbering and amendment rules; commit rules, as amended by D46 | D31, D46 |
| One agent per step, each in its own worktree and branch; rebase, check, fast-forward `main`; owner sign-off for changes that touch a frozen contract, an owner decision or a new dependency | D41 |
| Line endings: `* text=auto eol=lf` | B1 |
| Windows first; the check also builds for Linux and macOS | D27, D42 |

**Prerequisites and evidence gates.** C0's mechanism choices are specified in core §13; the remaining work is configuration and proof, not an open compiler/database design.

| Prerequisite | Gate |
|---|---|
| Windows race-check compiler | Owner supplies the absolute pinned `DWARF_CC` path; missing/mismatched toolchain fails the race check without installing or guessing, while ordinary cgo-free checks remain available |
| Linux/macOS CI compiler | Linux uses image-pinned `/usr/bin/gcc`; macOS obtains its native compiler through `xcrun` only for relevant cgo checks; record versions |
| Vulnerability database | Use the official online database or an explicit unpacked official snapshot with manifest/source/digest and provisional seven-day age cap; unavailable/expired data cannot pass |
| Proof | Run on 2026-10-09 on Windows with go1.27.2 and llvm-mingw 20260922 (ticket 01): the full check passed, including `-race`; a planted forbidden import and a Linux-only cgo file each failed the import-boundary step |
**Exit criterion.** The module builds with `CGO_ENABLED=0`. `go run ./tools/check` passes on Windows, including the `-race` tests. The import-boundary test exists, passes, and fails on a planted forbidden import. `AGENTS.md` holds the build section, and `CLAUDE.md` imports it (B17). `DECISIONS.md` contains every decision in §1, which has been true since 2026-10-02.

**Status.** Done (2026-10-09, ticket 01). Linux and macOS native check runs wait for C3's CI, and the vulnerability database is online-only (B17).

### C1. Action ledger and body store

**Goal.** The single source of operational world facts (D15). Later operational producers write typed events here or read them; C7's D90 bounded cosmetic state follows the separate retention boundary below, not permanent per-edit logging. C1 owns `ledger-format`, `ledger-reader` and appender drafts; schema mistakes affect all replay consumers, including the first U1 handoff (D20).

**Delivers.** An append-only ledger with typed events, a reader with a cursor, a content-addressed store for large bodies, schema versioning, and the field contract from Undercurrent §15.1 represented in the event types. C1 defines the appender contract; C3 wires one core-owned appender instance per colony. In detail:

- The colony log as D34 to D37 specify: segments, commits, recovery of a half-written final commit, the hash chain, the gapless sequence, and lookup indexes rebuilt in memory at startup.
- The body store under `bodies/`, keyed by SHA-256 (D36, D38).
- The colony directory with `colony.json` and the single-writer `lock` (D38; core §3.1), so that a second core cannot write the colony's ledger (D26).
- Drafts of the `ledger-format`, `ledger-reader` and appender contracts (`CONTRACTS.md`): standard-library-only packages with golden samples for every type version (D40). The appender contract specifies serialized appends and sequence assignment; only `internal/core/...`, `internal/router/...` and `internal/undercurrent/graph/...` use it (D50). The step ends with a request to the owner to freeze the contracts.
- C1 freezes F0 only: the stable ledger envelope, hash/commit framing, reader/appender, catalogue and golden samples plus fully specified C1-produced records. F2 adds C2 promotion lifecycles before C2 producer implementation (D113); F3 C3 context/task/turn/approval/accounting; F4 C4 validation; F5 C5 governance; F6 C6 broker records. R1 context-module, C7 launcher-protocol, U3 and U1.L add their own producer-ready families later. Each catalogue entry names type/version, payload owner, producer, permitted appender, consumer, privacy, replay-essential fields and fixture version. There are no placeholder events; C1 still owns the envelope and catalogue, and the owner still freezes each contract version (B2/B3; core §13.1).
- Promotion records distinguish immutable request/submission, local ancestry, derived generation, predecessor, validation attempt, durable intent, disclosure state and accepted result. The exact fields and lifecycle are specified in core §§13.2–13.3; the records join F2 in parts, each part frozen before the code that writes it (D113).
- D90–D96 retain append-only **operational** birth/alignment/Purpose/publication/succession history. C7 must separately specify Core-owned replaceable/bounded presentation snapshots/checkpoints and finite cosmetic retry retention under the existing storage discipline; current state/drafts/TODO survive without keeping every edit forever. C1 catalogues only the actual producer-ready records, not a speculative storage schema or second GUI database.
- Fuzz tests that truncate segments (D42).

| Definition | Meaning |
|---|---|
| Action delta | One typed event: actor, bound context, operation, targets, outcome, measurement path, start and end times or a pending marker, lineage hints, body references (core §3.2; Undercurrent §15.1). In-flight work is a start and an end sharing an operation ID (D37) |
| Record envelope | Format version; sequence; namespaced event type and type version; event time; colony, agent, execution-session, context and turn IDs; actor kind; measurement path; operation ID; previous record's hash; payload; body references (D35) |
| Commit | One record, or several that belong together, written with one write followed by a flush; its last record is marked (D34, D35) |
| Segment | A log file named by the sequence number of its first record (D34) |
| Ledger sequence | A monotonic integer assigned on append; the only ordering replay uses. Gapless within a colony (D37) |
| Event time | Time recorded by the host when it observed the event; replay uses this, never the wall clock at read time. UTC Unix nanoseconds, never decreasing within a colony (D37) |
| Body | Any payload too large for the event (logs, diffs); stored by hash and referenced. Message bodies, diffs and run logs go here (D33) |
| Measurement path | How the host observed the fact (process exit, file scan, adapter response); distinguishes host observation from report |
| Durable cursor | A reader's position in the colony-wide ledger sequence; launchers, router consumers and Undercurrent each hold their own. Ephemeral model text uses a separate turn-ID/delta-index channel with an attach-time current-text snapshot and included delta high-water index; live deltas resume above it and are not replayable after core exit (D49) |

**Waits for.** C0.

**Stop point.** Do not start before C0 is Done. C1 ends with the freeze request; it does not wait for the owner to freeze the contracts. Other roadmaps build against them only once they are frozen (D40).

**Unblocks.** C2, once the owner freezes `ledger-format` and `ledger-appender`; U1, once the owner freezes `ledger-format` and `ledger-reader`, which is the D20 handoff for U1 and, after it, the field-level part of U2.

**Decided.**

| Decided | Source |
|---|---|
| Append-only, typed deltas, content-addressed bodies, never from an agent's report alone | D15; core §3.2 |
| Field contract fields and events | Undercurrent §15.1 |
| Router events: provider status, headroom, budget crossings, usage attribution | Routing R11 |
| What the log holds: one colony log with one sequence for actions, workspace and validation events, turns, approvals, budget reservations and settlements, messages and inbox processing, and router measurements. Streamed model text stays in memory. Private context content stays in the agent directory and is logged by hash. The router's per-call decision record stays a separate JSON-lines file | D33 |
| Physical storage: our own append-only log, standard library only. A SQLite index may come later only on a measured trigger, and must be rebuildable from the log | D34; core Appendix A.8 |
| Event encoding: one JSON object per line, prefixed by its SHA-256 | D35 |
| Schema evolution: a written record is never rewritten; new fields are optional, and a missing field means absent; a breaking change gets a new type version, with upgraders tested against stored samples; a reader skips types it does not consume and stops with a clear error on a consumed type it cannot decode | D35 |
| Hash function: SHA-256 for bodies and for the record chain | D36 |
| Tamper evidence: every record carries the previous record's hash from the start; external anchoring waits for assurance A3 | D36 |
| Retention: the operational log is never deleted or compacted, though old segments may be compressed; bodies may be garbage-collected, so replay needs only records. D90's bounded cosmetic history does not weaken these operational facts | D36; D90 |
| IDs: UUIDv7 from Go 1.27's standard library; names never grant authority. A time held back after the clock moved backwards is flagged; durations use the monotonic clock | D37 |
| One ledger per colony; every event carries the colony's globally unique ID | D26 |
| Location: a default root per OS with a per-colony override; `colony.json`, `lock`, `log/` and `bodies/` | D38 |
| Contracts: standard-library-only packages with golden samples; a draft changes freely; the owner freezes | D40 |
| Scratch events carry parent-scratch and authoring-context fields | D23 |
| One in-process appender per colony serializes appends and assigns the ledger sequence; its contract is used only by core, router and `internal/undercurrent/graph` | D50 |
| Durable view cursors use the colony-ledger sequence; streamed text uses a separate ephemeral per-turn channel keyed by turn ID and delta index, with current text supplied to a newly attached view | D49 |

**Prerequisites, producer work, tunables and proof.**

| Item | Gate |
|---|---|
| Format, record/commit/segment caps, flush, lock and body publication | Specified in core §13.1 (B3); platform durability/error/locking behavior still requires implementation and verification |
| Retention and replay | Operational replay facts stay in the never-compacted ledger; explicit body collection respects reachability/pins and records unavailable bodies. C7's bounded presentation persistence/retry-expiry mechanism remains separate producer work, not an immortal edit catalogue. Measure configured budgets/replay triggers; no constant-space claim |
| Event catalogue | Freeze F0 after C1's owner-approved `ledger-format`/reader/appender review; add F2–F6 and later producer families only before their corresponding producer implementation (B2) |
| Workload evidence | Measure cold/warm recovery, peak memory, durable-write latency and disk against 12/20 every-change contexts and the actual larger enrolled population. Appendix A.8's 0.52 ms historical single-event commit is not concurrent colony throughput (D78) |
**Exit criterion.** C1's implementation writes, reads, replays and truncation-recovers every F0 catalogue record; unknown types are skipped, consumed unsupported versions fail clearly, and the golden fixture generator reproduces F0 exactly. Record actual cold/warm replay, durable-write, peak-memory and disk evidence at 12/20 every-change contexts plus the declared larger workload. Request the owner freeze for F0 `ledger-format`, `ledger-reader` and `ledger-appender`; later event families are frozen before their own producers, not pulled into F0. Nothing here claims the contracts are frozen or those measurements pass.

**Status.** Done (2026-10-10, tickets 02 and 03). Linux and macOS platform runs wait for C3's CI (B18).

### C2. Workspaces, observation, promotion, temporary scratch

**Goal.** Private scratch per work context and core-performed promotion (core §3.5), with every workspace action recorded as a ledger event. The step also builds D23's temporary scratch at the workspace layer; C3 decides when turns use it.

**Delivers.** Workspace registration, scratch worktree creation, host-side observation of changed paths and content hashes, and the fast-forward compare-and-swap mechanics for canonical `main`. C2 owns workspace/promotion orchestration and the final ref mutation; C4 supplies the real validation evidence before a promotion can be accepted (D52). C2 completion does not enable accepted promotions. Each workspace's `main` is never checked out; people and agents work on branches, and human changes use the same branch-and-promotion path (D48). Direct Git movement that bypasses the gate is unsupported. The workspace events in Undercurrent §15.1 include D23's authoring-context and parent-scratch fields and the temporary-scratch merge-back event. In addition:

- **Temporary scratch (D23).** Create a temporary worktree on its own branch from the task scratch's last commit. Commit the changes observed in a scratch. Merge a temporary scratch back by fast-forward when the task scratch has not moved; otherwise compute `git merge-tree --write-tree`, commit a clean result, and report a conflict as work for the agent. C3 wires these operations to turns.
- **Scratch lifecycle and scan cost.** Keep one dedicated linked worktree per context/workspace; do not pool a spare worktree across private contexts or reuse a prior context's files. At each protected capture, use a host-built bounded full-tree manifest against the trusted tree rather than trusting the child-mutable index or stat-only change hints as proof. Stream canonical content and reject path/mode/case collisions, unsupported file kinds and byte/file-cap exhaustion explicitly. Appendix A.7's 19–21 s checkout and 305 s full-tree hash are historical single-run measurements, not throughput; the actual scan cost is a blocking C4 feasibility gate.
- **Windows hazards.** `core.autocrlf` in the agents' repositories (settled by D74; capture inputs in core §13.2). Long paths: git's `core.longpaths` is unset by default, and D38's short IDs keep scratch paths short. Files held open by running processes can block a worktree's removal [INFERENCE]. Paths are case-insensitive on Windows, unlike Linux (D32) [INFERENCE].
- **[PROPOSAL] Git operations go through the `git` program.** D23 already names `git merge-tree --write-tree`. Core Appendix A.7 measured `git update-ref <ref> <new> <old>` refusing a stale old value, which is the compare-and-swap that promotion needs. B23 adopts this under `ROADMAPS.md` rule 6: Core runs every Git operation through the `git` program, version 2.42 or later, with three controlled environments (ticket 05).
- **Post-promotion interference check [PROPOSAL].** After each promotion the workspace service runs `git merge-tree` of each open scratch against the promoted change and emits the result event Undercurrent §15.1 proposes: the promotion, the scratch checked, the grade and the overlapping paths. U4 reads the event; Undercurrent never runs git. One `git merge-tree --write-tree` took 0.09–0.15 s on the owner's machine (core Appendix A.7). If the type is not in catalogue v1 it joins `ledger-format` by addition (D40), with the owner's sign-off (D41).
- **Scratch lifecycle events [PROPOSAL].** The workspace catalogue in Undercurrent §15.1 records a scratch's base only at creation and has no closure. C2 emits two further events, which C1 adds to §15.1 as **[PROPOSAL]** rows when it drafts the catalogue: *Scratch base advanced* (context, scratch, old and new base commits), emitted after a context integrates canonical without promoting; and *Scratch closed* (context, scratch, reason: promoted, abandoned, garbage-collected, temporary merged back). The owner froze *Scratch closed*, with the creation record, in F2-05 (D115): version 1 defines the reason `abandoned`, later F2 parts add the other reasons by addition, and §15.1 carries both as frozen rows; *Scratch base advanced* remains proposed. Every *scratch changed paths* event carries the full current set of changed paths relative to the current base, never a delta. U4 needs the current base lag and the set of open scratches, U9 needs occupancy lifetimes, and D36 forbids recovering any of them from bodies.
- **Unavailable promotion (D52).** Without the real validator, requests return an explicit unavailable result, leave canonical unchanged and emit no accepted-promotion event. There is no allow-all validator, temporary bypass or direct-ref workaround. Mechanical tests may exercise the ref-update primitive in disposable repositories; these are not successful promotions through the product. Integration of accepted promotions and the post-promotion observer belongs to C4's gate.

| Definition | Meaning |
|---|---|
| Workspace root | A registered git repository; v1 requires git (core §3.5) |
| Canonical | The workspace's `main` branch, never checked out in a working tree; advanced by core compare-and-swap through validation-gated promotion (D48) |
| Scratch | A linked worktree for one bound work context, on a context-specific branch, cut from canonical |
| Base commit | The canonical commit a scratch was cut from or last integrated |
| Observed change set | Changed paths and content hashes the host derives by scanning the scratch, never from the git index (core §3.5) |
| Original submission | Immutable request/context identity, original base and host-captured contents; later scratch edits and derived-candidate rebuilds do not replace it (D56, D77) |
| Derived integration candidate | Explicit integration of that submission over an expected predecessor; separately identified by candidate, predecessor and generation, with checks/environment/law evidence bound to the exact tree (D77) |
| Promotion | Validation-gated fast-forward compare-and-swap of `main` from the actual durably accepted predecessor to its validated derived candidate, in durable request order. A predecessor mismatch blocks publication and invalidates dependent evidence; an older original submission base alone is not a refusal (D18, D48, D59, D77) |
| Task scratch | The scratch of a task context. Only the task's own turn writes to it (D22) |
| Temporary scratch | A worktree on its own branch, cut from the task scratch's last commit, for a second of the same agent's turns that writes to the task while its scratch is in use. It is created on that turn's first write (D23) |
| Dedicated scratch | One context/workspace-owned worktree retained through explicit closure and outcome resolution; v1 has no cross-context spare-worktree pool. Dormant cleanup is explicit and cannot remove unrepresented edits |

#### Promotion boundary: owner approval before implementation (D52)

The owner must approve this complete specification before C2 or C3 code is built. The mechanisms are designed here and in core §13, and the owner approved this specification on 2026-10-12 (D112). The owner decision is a formal gate, not a choice left to parallel builders.

| Owner of the implementation | Responsibility |
|---|---|
| C2 | Immutable capture, explicit integration-candidate preparation, bounded workspace orchestration and the serialized final canonical mutation sequence; no acceptance without C4 evidence |
| C3 | Bind agent/owner requests to their authority and original context; distinguish submission, derived attempts and durable promotion results through the core command/event boundary |
| C4 | Execute bounded concurrent full-required-set validation, produce exact-candidate evidence, and prove integrated ordered publication, invalidation, recovery and runtime capacity |

**Recorded lifecycle choices (D53–D78):**

| Policy | Recorded behavior or direction |
|---|---|
| Core-owned request (D53) | Durably record the request, then return its ID and pending result. It can outlive the requesting turn; results belong to the original context. Optional waiting does not own its lifetime. D52's missing-validator path still returns unavailable, not a pending request behind a nonexistent validator |
| Bounded cumulative validation (D77, superseding D54) | Validate cumulative candidates such as `M+A`, `M+A+B`, `M+A+B+C` concurrently within explicit workspace/colony runner bounds. Publish only the successful prefix in durable request order; independent passes for `M+A` and `M+B` do not validate `M+A+B`. Validation may overlap, but durable finalization may not |
| Reconcile without retry (D55) | After a crash, finish missing bookkeeping for a proven Git update without applying it again. Requests proven not to have promoted end interrupted, including queued requests; the agent or owner explicitly resubmits. An unresolved effect blocks further promotions in that workspace. D37's interrupted operation end does not prove Git was unchanged |
| Immutable original submission (D56, D77) | Preserve original submission/base/capture, request identity and context. Scratch work may continue under D22/D23 without replacing or invalidating that submission. Derive integration candidates explicitly and separately; a later correction does not withdraw the older submission. Withdrawal requires authorized cancellation; replacement submission requires a new request |
| Derived candidates and invalidation (D77) | Identify each candidate, expected predecessor and generation/attempt; bind full check/environment/law evidence to it. Mechanical integration and conflicts are explicit, never silent substitution or automatic semantic conflict resolution. A changed predecessor or failed/removed/cancelled request invalidates dependent evidence: rebuild and fully validate affected candidates without withdrawn changes, and never publish a passing aggregate over a failed prefix. Preserve actual attempt outcomes; cumulative failure alone does not identify a culprit |
| Separate cancellation (D57, D59) | A request-specific promotion command, authorized for the submitting context or colony owner. Normal turn completion, `turn.cancel` and view disconnection do not cancel promotion. Supervisory position alone grants no authority. Cancellation must win core's ordering before the finalization intent commits durably; afterward it reports too late, not success |
| Request deduplication (D58) | Same ID and immutable submission return the original current/terminal state without another queued job or execution; changed submission/context under that ID is a mismatch error. Different IDs stay separate even if content matches. An explicit new attempt after interruption/cancellation uses a new ID; knowing an ID grants no authority |
| Finalization and recovery (D59, D77) | ONE serialized entire sequence per workspace: durable intent → guarded fast-forward Git update with durability established → durable acceptance → finalization-slot release. No later durable intent before the predecessor's proven durable outcome. Intent is not acceptance; unresolved effects block publication. Use the prerequisites and mapping below, never blind retries |
| Candidate inputs (D60, D74–D76) | Use a Core-built bounded full-tree manifest with Git-canonical contents, pinned `WorkspaceCaptureInputs` (Git/object format, normalization, attributes, filters, reviewed host local/global exclusions and their digests) and captured scratch `.gitignore`. Root eligibility baseline is the Core-recorded scratch anchor; successor baseline and delta base are its latest admitted local parent's immutable sealed tree. Keep physical scratch base and foreign train predecessor distinct. Tracked paths remain eligible despite ignore rules; the mutable index is never authority. Unsupported conversion/filter, special file, collision or lossy normalization refuses capture. This complete capture contract is in core §13.2, not open |
| Policy activation and revalidation (D61, D64, D77) | Hold new finalization intents, finish already-intended finalizations to proven durable outcomes, then activate; unresolved effects delay activation. Refresh every affected pre-intent lane under the new policy, preserving original submission/request and historical attempts. A still-current derived tree may stay fixed; a predecessor rebuild is a distinct generation requiring full fresh validation. Not-yet-validated candidates start on the new policy. No extra snapshot, automatic post-crash retry or exact-time guarantee |
| Activation control (D65–D67) | Only the colony owner approves each exact version; Core executes and records immediate or one-off scheduled activation. Scheduled time is not-before eligibility, never a safety bypass or exact-time guarantee. One colony-wide validation-policy version; affected workspaces switch together, so unresolved finalization in one delays the shared switch. No recurring scheduler or per-workspace policy-version rollout; checks/toolchains may still differ |
| Superseded children (D68, D77) | Across affected lanes, stop superseded children and confirm exit before replacement validation or lane/directory reuse, whether superseded by requirement refresh or predecessor invalidation. Unproven cleanup blocks affected replacement/reuse. Activation does not wait merely for old checks to finish. Retain actual old outcomes; supersession alone is neither test failure nor request cancellation |
| Changed checks/environments (D69, D77) | Deliberately adopted required-check or execution-environment changes use controlled cutover and fresh validation across affected pre-intent lanes, even with unchanged law. Keep original submission/request fixed and bind evidence to the new requirements; only a still-current derived tree may remain fixed. A changed predecessor invalidates evidence and requires a new generation, not stale-original-base refusal. Scratch edits, check-retirement authority and pinned historical failures remain separate |
| Bounded checkpoint admission (B4 amends D62) | One FIFO with `K=4` immutable checkpoints per enrolled code-working `(context, workspace)` pair; `Q_workspace=4×N`, colony capacity is the sum of declared pairs. Each successor names the admitted local head; a fifth request is busy before snapshot. D70 still requires immediate capture-or-busy, never a capture queue. Local ancestry is a hard publication dependency distinct from the cumulative train predecessor. See core §13.2 |
| Capture contention (D70) | Reserve authority, validator, byte and per-lineage slot before capture; one capture per physical scratch, different scratches may capture concurrently within explicit bounds. If any precondition/resource is absent, roll back and return busy with no snapshot or retry. |
| No-change result (D71, D77) | With real validation available, compare the derived Git file tree with its actual durably accepted predecessor. Equality yields durable no-change even if commit histories differ: no required checks, intent, canonical movement or accepted delivery. Redundancy against a speculative predecessor is not terminal; resolve the predecessor first, and rebuild if it changes. Preserve scratch work/history and separate request IDs |
| Initial automated gate (D72, D73) | For a ref-changing candidate, C4 runs the full declared per-workspace required-check set regardless of changed paths; acceptance needs passing bound evidence for every required check. This is not every repository test and uses no initial path/graph-based exclusion. Empty/invalid/unconfigured gates are unavailable, with no human-approval fallback; human-oracle verdict work stays C5 |
| Passive results (D63) | Durable terminal results are immediately available via status/events and surface in the original context's next naturally scheduled turn. Completion alone creates no follow-up model turn; an explicitly waiting caller can continue normally and a context can query sooner |
| Feasible capacity (D78; B15) | Calibration envelope: 12/20 code-working pairs offer one required change each per 600 seconds (72/120 per hour); test a synchronized four-checkpoint burst per pair (48/80 requests) and burst-over-steady. Use `K=4`, `P=4` colony lanes, at most four lanes/workspace and four prepared/running/result-held generations in a workspace's leading window. These are finite design/calibration inputs, not measured throughput, colony-size caps or owner SLA. Reserve finite capture/run/memory/CPU/process/output/disk/retention bounds before work; no refusal counts as delivery. Measure real offered/admitted/accepted/no-change/refused work, backlog trend, p50/p95/p99, rework and full CPU/RAM/disk/AV cost at actual larger populations. Provisional engineering target is accepted capacity ≥1.25× declared steady demand with stable backlog and completion of the preregistered finite burst schedule; the target is a hypothesis, not observed capacity or owner policy |

**Recovery prerequisites and mapping (D59, D71, D77).** True no-change is determined against the actual durably accepted predecessor before finalization, without an intent or canonical update. For a ref-changing finalization, establish an intact durable intent identifying the derived candidate/generation and expected predecessor, matching validation evidence, exclusive workspace mutation ownership, any previous Git process stopped or fenced out, and no later durable intent past the unfinished sequence. The expected predecessor and derived candidate tips must differ. Under those prerequisites:

| Observed state | Recovery action |
|---|---|
| Expected predecessor tip remains canonical | Record interruption without retrying promotion |
| Intended derived candidate is canonical | Complete missing acceptance bookkeeping without another Git update |
| State or proof is inconsistent/insufficient | Block publication rather than infer success or non-promotion |

Do not duplicate existing durable acceptance. D37's unmatched operation starts still receive interrupted ends; reconcile the request's actual effect separately without rewriting history. A cancellation ordered before durable intent prevents that request's later Git update and invalidates dependent candidates; after intent, no cancellation abort path is allowed. Failed finalization records its outcome before the finalization slot is released; an unresolved effect holds publication, and no later intent may bypass it. Queued or speculatively validated requests proven not promoted end interrupted after a crash, without automatic execution restart (D55). Git objects, references and the ledger need explicit durability handling and platform verification: this protocol does not itself prove power-loss resilience or C10's protection against same-user ref manipulation.

**Selected promotion specification (B4–B5; see core §§13.2–13.3).** All command, immutable-submission, generation, attempt, durable-intent, disclosure and accepted-result identities; host capture inputs/baselines; Git `merge-tree --write-tree` explicit-base derivation; cancellation/recovery; exact activation cutover; and bounded resource/scheduler contracts are selected there. No technical/schema question below this point remains an owner-choice gate. D52's formal approval was given on 2026-10-12 (D112). D40/D41 change approval, C4's real required-check evidence, C10's containment proof and actual capacity/durability measurements remain prerequisites.

**C2 boundary scenarios (design acceptance, not executed here):**

1. With `R1` outstanding, admit distinct `S1=M+a`, `S2=M+a+b`, `S3=M+a+b+c`, `S4=M+a+b+c+d`; a fifth request returns capacity-busy before capture. Keep every request/attempt/outcome distinct; no coalescing.
2. Replay an identical known ID after later scratch edits without recapture; reject a changed fingerprint under that ID and reject a skipped/stale lineage head before capture.
3. Derive each successor delta from its local parent's sealed tree while integrating over the current foreign train predecessor; preserve the foreign change and never resubmit an earlier local delta.
4. Fail/cancel/interrupt `R1` and prove `R2–R4` become dependency-failed after child cleanup; no child can publish or resurrect `R1`'s contribution. Require an explicit new root and capture for repair.
5. Remove a foreign train predecessor while local ancestry remains valid; rebuild survivors without that predecessor and rerun the complete gate. Do not turn a foreign predecessor removal into local-lineage failure.
6. Simulate writer contention, byte-cap overflow, missing check resources and unavailable C4; return immediate busy/unavailable, record no accepted result and change no canonical ref.
7. Exercise a ref move before durable acceptance, every D59 recovery boundary, C10 stop proof, and BD2 diagnostics with a private marker in logs/path/hash/verdict: no raw result or agent-visible canonical advance before its exact durable acceptance witness.
8. Recover a one-off approved activation after its not-before time while an affected finalization remains unresolved: restore the durable hold, keep the old policy active, reconcile without restarting work, then record at most one actual activation after proof. Exercise clock rollback, the one-pending-slot bound, exact-ID cancellation/replacement, stale expected manifests/authority and crashes around each commit (core §13.2).


**Snapshot choices Q50–Q52 (D74–D76; A selected for each).** These determine what capture means, not whether the full required gate runs (D73). Git's [attribute rules](https://git-scm.com/docs/gitattributes#_checking_out_and_checking_in) distinguish stored blobs from checked-out bytes; text conversion can store LF while checkout uses CRLF. Its [ignore rules](https://git-scm.com/docs/gitignore#_description) include repository files, local exclusions and global exclusions, but do not exclude already-tracked files. The alternatives below are not adopted.

| Question | Selected A | Unselected B and tradeoff |
|---|---|---|
| Q50 / D74: candidate content representation | Git-compatible canonical contents, using recorded workspace normalization settings and applicable repository attributes. Keep raw on-disk observation hashes distinct from Git candidate identity; do not impose this repository's LF-only B1 on other repositories | Preserve disk bytes directly, without implicit normalization. Simpler byte identity, but checkout-only line-ending differences can become real Git changes and repositories relying on conversion need a separate compatibility policy |
| Q51 / D75: local/global ignore inputs | Explicit, host-managed workspace ignore inputs, initially imported/reviewed from existing local/global exclusions and then fixed until deliberately updated. Do not silently drop existing private-file exclusions | Read live local/global exclusions at each capture and record those exact inputs. More familiar automatic response to personal Git settings, but the same scratch files can yield different candidates after an unrelated settings edit |
| Q52 / D76: repository `.gitignore` edits | Use the rules in the captured scratch, including edits in that submission. New rules can exclude new files immediately, rather than waiting for promotion of the rules | Use only canonical's rules until the change is promoted. Delays an agent's eligibility-rule changes, but can capture generated/private files that its edited `.gitignore` appeared to exclude |

The selected policies preserve tracked modifications/deletions (D60), immutable admitted submissions (D56, D77) and capture-busy behavior (D70). Root/successor eligibility and delta baselines, exact host capture inputs, exclusion review/update authority (the owner alone, D112), body pins, output materialization and Git integration are specified in core §§13.2–13.3. External clean/smudge filters and unsupported path conversions fail closed; D74 grants no arbitrary executable filter or C10 exemption.

This is a Core-internal boundary specification, not a new cross-roadmap package. Its event fields still obey C1's frozen `ledger-format` and D40's change rules (`CONTRACTS.md`).

**Waits for.** C1 and the owner-frozen `ledger-format` and `ledger-appender` contracts (D40, D50), plus owner approval of the promotion-boundary specification above (D52).

**Stop point.** Do not implement before C1 is Done, the owner freezes `ledger-format` and `ledger-appender`, and the owner formally approves this D52 boundary. C2 remains unavailable for accepted promotion; no permissive validator or manual direct-ref workaround. Defender remains enabled for `scratch/`; capture/checkout and full required-check costs are measured as-is. The provisional resource profile may not silently reduce enrolled work.

**Unblocks.** C3 and C10; U3's graph foundation, not its accepted-promotion integration gate; U4, together with C4, C5, U1 and completed U3.

**Decided.**

| Decided | Source |
|---|---|
| Private scratch per unit of work, as git worktrees, not clones; canonical `main` changes only through core promotion | D16, D48; core §3.5 |
| One scratch per bound work context and workspace root. A promotion may carry changes from several of one agent's contexts, and the ledger records each change's authoring context | D17, amended by D23 |
| Canonical promotion remains fast-forward only; D77 derives an explicit integration candidate over its expected predecessor, without silently rewriting the original submission or resolving semantic conflicts | D18, D77 |
| Temporary scratch: created on a second turn's first write, cut from the task scratch's last commit, merged back by fast-forward or `git merge-tree --write-tree`; a conflict becomes work | D23; core Appendix A.7 |
| Only a task's own turn writes to its scratch; another context's write follows D23 | D22, safeguard 3 |
| `workspaces/` and `scratch/<short id>/` under the colony root; short IDs because git's `core.longpaths` is unset by default | D38 |
| Git object IDs stay in each repository's own format; git 2.55 still creates SHA-1 repositories by default | D36; core Appendix A.7 |
| B1's LF rule covers this repository only; it does not change the agents' repositories | B1 |
| Windows first; portable to macOS and Linux | D27 |
| `main` is canonical, never checked out; people and agents use branches, and the promoting context has promotion authority subject to the validation gate. Direct movement of `main` outside this path is unsupported | D48; core §3.5 |
| C2 proves workspace mechanics and refuses unavailable validation; successful validated promotion is C4's integration gate | D52 |
| Core-owned requests, bounded cumulative validation in durable request order, serialized durable finalization and reconciliation without automatic retry | D53, D55, D59, D77; approved policies above |
| Immutable original submission while scratch work continues; distinct derived integration candidates; separate request-specific cancellation by submitting context or owner | D56, D57, D77; approved policies above |
| Request-ID deduplication; durable intent as cancellation cutoff; finalization-slot release only after durable outcome; dependent evidence invalidated on predecessor change | D58, D59, D77; approved policies above |
| Architecture/readiness requires workload/capacity reasoning and a runtime verification plan, not safety alone | D78 |

**Prerequisites and implementation evidence—not open design questions.**

| Prerequisite or gate | What remains |
|---|---|
| Formal D52 approval | Approved 2026-10-12 (D112): the complete boundary in core §§13.2–13.3 |
| C1 handoff | C2 records F2 proposal before producer implementation; C1 is Done and F0 `ledger-format`/`ledger-appender` are owner-frozen (D111) |
| C2 mechanics | Capture identity/tree equality, root and successor eligibility, dedup/admission, local-parent failure suffix, foreign-predecessor rebuild, conflict, busy, unavailable and conditional recovery scenarios above |
| C4 gate | Full D73 declared checks, O4/A1 plus the complete preregistered V6 suite in validation §4.9.1, whole-generation output quarantine, actual acceptance/recovery and every-change capacity evidence; C2 alone cannot promote |
| C10 and OS proof | Prevent child changes to `main`, common Git metadata, ledger, private contexts and owner IPC; positively stop all descendants before slot/directory reuse |
| Workload and resources | Record bounded normal/burst scenarios and full-tree capture/materialization/disk costs under D78/B15; no throughput, capacity or evidence result is claimed |
| Runtime and interruption evidence | D42 cross-platform CI; abrupt restart, incomplete private revision, storage pressure, unavailable validation and process shutdown drills |
| Scope of proof | C2 demonstrates workspace/admission mechanics only. C4 must prove the complete validation-to-canonical path; C10 must prove positive OS containment before any hostile-work claim |

Capture/materialization/integration and disk costs must be recorded against the D78/B15 workload; this is an implementation gate, not an evidence result in this roadmap.

**Status.** In progress (ticket 05 resolved, B23 and B24; tickets 06–09 remain): D52 boundary approved 2026-10-12 (D112); proof not claimed. Its F2 family must be frozen under D40 in parts, each part before the code that writes it (D113).

### C3. Agent runtime

**Goal.** One agent running test work (D19, D52): its directory, execution session and bound contexts; streamed model turns through hand-written adapters; host tools confined to the bound scratch; simultaneous turns under D22's safeguards; and a ledger event for every action. The router's shadow mode (R0) and the validation runner (C4) build on it. C3 completion is not readiness for real colony assignments or delivery.
- **Ownership of `contexts/<id>/`.** Core owns the private directory and immutable revision objects; `Memory.md`/`Handoff.md` are Core-managed materializations, with `core/state.json` rebuildable and `core/objects/<sha256>` holding immutable revisions/transcript chunks. Router state/provider artifacts live in their named context subdirectory; other producer directories appear only when their producer exists. Core writes and durably publishes private bytes before a ledger `private.revision` event; a restart mismatch blocks/reports rather than guessing (core §13.5). Other layers write only in their named subdirectories.
**Delivers.**

- Agent directory loading, with `Agent.md` and general `Memory.md` under `agents/<name>-<short id>/` (D32, D38), and the agent's one execution session (D21).
- **Staged lifecycle eligibility (D91–D93).** Supply Core first-action/continuation and ordinary-work guards for later C8 lifecycle state; C3 itself does not wait for C8/C9/GUI. C8 integrates them with the actual C9 backend pinned `grill-with-docs`: every new Orchestrator/Griot, including Grand and agent-born leaders, first aligns against shared Purpose; a new Corps's ordinary work waits for both human acceptances. Messages, task assignment or launcher selection cannot bypass eligibility. Preserve admitted first-turn/result identity across crash/reattach without automatic paid replay; full leadership integration is proved at C8.
- **Actual task projections (D90–D92).** Expose self-assigned/delegated waiting tasks with ready/blocked distinction and separate active work. Stockpile data is not message backlog, age-based invented “stall” status or an understaffing judgment; task facts never wait for cosmetic animation.
- **Service loss for every agent (D97–D102; Core §13.11).** Record service loss with its cause (a classified provider failure, an exhausted owner money limit or an execution failure), its 2-minute onset (counted from the first refused call until R1's router pause exists, and immediate for owner-only causes), one outage per cause with owner begin and end notices, and restoration by the first completed call after the cause clears or stops applying. No probe, heartbeat or model call is added, and generic interrupted-turn recovery is not this state. Officer consequences are C8's; base C3 waits for neither R1 nor C8.
- General-work and task contexts. Relationship and board contexts arrive with C6.
- **Admission and passive results (D62 as amended by B4; D63).** Use the bounded `K=4` local checkpoint FIFO and hard local-parent chain defined by C2/core §13.2. A different fifth request is busy before capture; same-ID retransmission returns its original state. Busy work stays undelivered, and no retry/coalescing/model call is invented. Durable terminal results surface to the original context on a naturally scheduled turn or explicit query, never a completion-only model call. Command receipts, immutable requests, generations and result presentation are separate.
- Apply §1.1's D79 authority and efficiency guidance to dispatch and budget admission; the permission check is deterministic, while required owner consent remains a separate operation.
- Hand-written streaming Anthropic and OpenAI adapters on a static configuration (D19; core §8.3). Verify provider facts against routing Appendix B and current provider documentation before coding. Develop against a local fake SSE server; live calls need the owner's API keys.
- Default tools are scoped read/search/write/edit plus typed Core status/task/memory/context operations; shell, process, connector and network effects need explicit grants and separate capabilities. The host enforces the exact bound context, not an ambient scratch-only assumption.
- Child-process lifetime (core §3.3): Core-owned handles and strict quotas; Windows Job Objects, Linux namespace/Landlock/cgroup containment and a separate macOS virtualization-backed per-run guest (core §13.9). Positive descendant quiescence is required before reuse; C3 does not call process groups or scratch separation an A2 boundary. OS-specific proof remains C10.
- Headless credentials require explicit environment configuration; the native GUI may use a narrow platform OS credential-store adapter. Never persist secrets in the ledger, context or execution guest; missing credentials refuse dispatch.
- **BudgetBook and sealed provider dispatch (B9; routing §9.4).** Core owns dispatch, hard local reservations and settlements across model/provider, tools, validation, queue, storage and autonomous work. Seal each routed request with non-secret semantic headers and an immutable Core credential-version reference; secrets never enter that binding or ledger. Input/output token maxima are separate inclusive hard limits; tool/system prompt tokens count once in input. Follow routing §9.4 for canonical usage/quote handling—do not subtract requested output from input or duplicate its formula. The router supplies versioned rates/usage; this adds no ledger/service/API and grants no global provider-spend guarantee or implicit paid allowance.
- D22's safeguards: agent-wide files use expected-revision writes; task claims/assignments/reservations are atomic Core records; one active turn per context; provisionally at most three simultaneous turns per agent, subject to the C3 workload gate. StartTurn is immediate admitted-or-busy; fair ready-agent then ready-context rotation schedules existing eligible work without a polling turn or goroutine per dormant relationship (core §13.5).
- D23 wired to turns: a temporary scratch on a second turn's first write to a task whose scratch is in use; a commit of the observed changes at the end of every turn; merge-back as soon as the turn ends and nothing else writes to the task scratch; a note to the task's next turn naming the files changed and the context that changed them.
- D25's list of the agent's other active contexts, rendered as a fixed tail section until R1 freezes `context-module` (`CONTRACTS.md`).
- Events (D33): turn starts and ends with the provider account (Undercurrent §15.1), approvals and their outcomes, reservations and settlements, and every action. Streamed text stays in memory for the views watching the turn; the finished message is recorded. Changes to private context files are logged by hash.
- Crash recovery (D37): an `interrupted` end for every unmatched start. Success is never assumed, and no tool effect is rerun blindly (routing R10).
- The plain scrollback terminal subscribes to all active turns, appends concurrent output in arrival order, and labels each displayed line with its bound agent/context/turn (short IDs); new owner input names or selects its context. Event identity, not the view's current selection, controls attribution (§4.2).
- **The core §4 boundary from the start.** The terminal talks to the core only through the command and event interface of core §4, even while the two share one process, so that C7 adds a transport rather than rewriting the turn loop (core §4, §5; D39).
- **The adapter surface the router needs.** The provider adapters expose from the start what routing §6.5, R5 and R10 require: provider-neutral canonical transcript records per context; raw usage, including cache read and write tokens and the service tier actually served; rate-limit headers; classified errors; and opaque provider artifacts (reasoning blocks, continuation IDs) stored with the context. R0 wraps these adapters and never re-implements them (routing §6.5, R5, R10).
- **Context state and operations.** Use the private revision layout in core §13.5. The baseline scoped tools are file read, bounded text/path search, scoped write/edit and typed status/task/memory/context commands. Shell/process, network, connectors and other external effects require explicit additional grants; model-call capability is separate. A current scoped grant does not prompt on every operation; expansions return to owner ceilings (D82).
- **One scheduler and one appender (D50).** One scheduler per colony admits turns, enforcing D22's per-agent limit and per-context exclusivity. C3 wires exactly one in-process appender per colony, owned by the core; it serializes appends and assigns the colony sequence through C1's contract. Only core, router and `internal/undercurrent/graph` use the appender contract; other Undercurrent packages remain readers. C1 defines the contract; the goroutine topology is C3's choice.
- **Testing-only promotion behavior (D52).** Bind requests and present the owner-approved C2 boundary's unavailable result without changing canonical or reporting delivery. Authorized live calls are tests; production work waits for C4's end-to-end gate. The runtime does not supply a permissive validator to make its tests pass.
- **Core-owned promotion requests (D53, D55, D77).** Expose request IDs and pending/results through the core command/event boundary without tying work to a still-active requesting turn. Results remain bound to the original context. Support explicit resubmission after a proven non-promotion interrupted by a crash; never automatically restart queued or speculative execution. C2 owns bounded workspace orchestration and serialized finalization, C4 the concurrent validation runs. C3 still exercises unavailable validation until the real runner exists; synthetic frozen-contract fixtures do not establish accepted promotion.
- **Submission, integration and cancellation (D56–D59, D77).** Present immutable original submission/base/capture separately from derived candidate generations, expected predecessors, attempt results and later scratch work. Passing speculative checks is not delivery. Request-specific cancellation requires the submitting context or colony owner, not supervisory position alone; `turn.cancel` and scratch edits do not cancel promotion. Same-ID/same-submission retries retrieve the request; mismatches are refused and new IDs stay separate. Report too late after durable intent without implying acceptance, and represent dependent-candidate invalidation without inventing failed checks or changing the original context.
- **Delivery and workflow records.** Task state transitions, claims/assignments and reservations are atomic Core records. A `ReviewRequest` names its task/assignment/version, reviewer, subject kind and digest, applicable promotion/generation/predecessor, acceptance/check/config hashes, required evidence, authorization revision and deadline; its typed result binds that same subject. `FailureResult` binds the failing evidence and cause. Callback duplicates are idempotent and stale callbacks have no effect. GUI and workflows consume durable records, not reconstructed chat.
- **Core operational command receipts, typed work and review callbacks.** Each operational mutation returns a durable receipt bound to command ID, authenticated principal, normalized-request digest, outcome, request ID if any and ledger sequence; same ID/digest returns the recorded receipt, mismatch refuses. Task transitions and review/failure results bind exact task/assignment, subject/evidence/check and applicable promotion generation/predecessor, authority revision and deadline; duplicate callbacks are idempotent and stale ones cannot mutate state. Host triggers are never acceptance authority. C7's cosmetic commands instead use their separately specified finite receipt/expiry contract, without weakening operational deduplication or retaining cosmetic edits forever.

| Definition | Meaning |
|---|---|
| Execution session | An agent's one resumable execution lifecycle: turn scheduling and an audit trail, not a pooled transcript (core §7.3; D21) |
| Private context | Keyed by `(agent_id, context_id)`: memory, handoff, prompt history, provider continuation, compaction, routing state and cache ledger. A work context also owns its scratch (core §7.3; D17) |
| Turn | One bound exchange in one context, with its tool loop, from start to end. At most one turn per context is active (D22) |
| Reservation | A core record that holds a call's maximum cost against every applicable budget scope until it is settled against reported usage (D22) |
| Static configuration | Fixed provider, model and settings, used while the router runs only in shadow (routing §14 phase 0) |

**Waits for.** C2, and the owner's private GitHub remote with CI on Windows, Linux and macOS (D42).

**Stop point.** Do not start before C2 is Done and the remote with CI exists. Live provider calls stop until the owner supplies API keys; everything that runs against the local fake SSE server can be finished first. All pre-C4 agent runs are testing-only, including live-provider drills; real colony work waits until C4 is Done (D52). C3 does not wait for C4 to complete its own runtime tests. Changes that add a dependency wait for the owner before reaching `main` (D41).

**Unblocks.** C4, C6, C7 and R0; U2's A1 suite together with C4. Generic eligibility/continuation/task capabilities also feed later C8/C7 integration without making C3 depend on C8, C9 or GUI.

**Decided.**

| Decided | Source |
|---|---|
| Simultaneous turns are provisionally bounded by three per agent, with one active turn per context; the 1/3 profiles and larger declared workload remain measurement gates | D22; AC02; B15 |
| An agent owns a directory and one execution session. Private contexts are keyed by `(agent_id, context_id)`; a change of counterpart renders a new turn, and continuations, approvals, router state and cache ledgers never carry over (core §7.3 adopted) | D8, D21 |
| A temporary scratch on a second turn's first write; a commit at the end of every turn; merge-back when the turn ends; a note to the task's next turn | D23 |
| Context-local memory, handoff, transcript and provider/router state; no inheritance across relationship contexts. Private revisions follow the Core-owned immutable-object layout | D21, D25, D38; core §13.5 |
| The prompt tail lists other active contexts by counterpart name or task title only, never their content, after the cached prefix; R1 owns conversion of C3's fixed tail section into a module | D25; `ROADMAP-ROUTER.md` R1 |
| `Agent.md`, spelled exactly | D32 |
| Turns, approvals, reservations and settlements go in the colony log; streamed text stays in memory; private content stays in the agent directory and is logged by hash | D33 |
| A start and an end share an operation ID; after a crash, an `interrupted` end for every unmatched start | D37; routing R10 |
| `agents/<name>-<short id>/` holds `Agent.md`, `Memory.md`, `contexts/<id>/` and `audit/`. The log record is the commit point for private memory; a mismatch found on restart is reported, never guessed | D38 |
| `dwarf-harness` is always built without cgo; race testing from the first concurrent code | D22, D28, D39 |
| Harness-owned control; provider features optional; no automatic provider switching in v1; provider betas off by default | D3, D4 |
| A private remote with CI on Windows, Linux and macOS exists no later than this step | D42 |
| One core-owned in-process appender per colony serializes appends and assigns the colony sequence; only core, router and `internal/undercurrent/graph` use its contract | D50; C1 defines, C3 wires |
| Agents are testing-only until C4's validated-promotion gate passes; unavailable validation cannot accept promotion | D52 |

**Prerequisites, open producer work and implementation proof.**

| Prerequisite or gate | What remains |
|---|---|
| C2/D52 and C1 | C2 Done, formal owner boundary approval, frozen ledger contracts and the remote/CI required by D42 |
| Provider/routing | R0 owns adapter/provider selection, context-specific route and estimates; use explicit provider/model configuration and the router-owned API. No automatic provider switch or invented default model; live calls wait for configured credentials |
| Credentials | Headless terminal uses explicit environment configuration; native GUI uses a native OS credential-store adapter. No secrets in context/ledger or execution guests |
| Agent/turn bounds | One turn/context; provisional per-agent max three. Explicit start beyond available capacity is busy; eligible work stays in the existing bounded fair scheduler. Measure 1/3 simultaneous turns, 12/20 contexts and actual larger population; this is not a capacity claim |
| Tool authority | Default scoped file/search/edit/status/task/memory/context capabilities; shell/process/connector/network requires an additional current grant. Owner ceilings and narrower task grants, not repeated prompts inside the grant (D82) |
| Context handoff | Use the private immutable state layout in core §13.5; R1 freezes/owns `context-module` and takes over the fixed D25 prompt tail |
| Lifecycle guards and task facts | Prove first-action/continuation gating and passive status in C3 without pretending C8/C9 producers exist; full human alignment/intent integration waits for actual C9 backend and C8. Expose ready/blocked waiting versus active tasks without a staffing diagnosis |
| Service-loss state | Implement Core §13.11's generic state, onset and restoration evidence from actual refusals and execution failures; interrupted-turn receipts alone neither establish service loss nor authorize role takeover |
| Test-only boundary | C3 uses test workloads and the C2 unavailable path; provider keys, CI, durable replay and positive child-quiescence proof remain real prerequisites |

**Exit criterion.** Core §9 phase 1's runtime gates, and drills for D22 and D23, all on test workloads (D52):

- one agent has one resumable execution session;
- a context switch leaks no history, provider continuation, approval or late result;
- two concurrent turns writing `Memory.md` produce one refusal and a re-read;
- two contexts writing one task merge clean when their changes do not conflict, and surface a conflict as work when they do;
- concurrent reservations never exceed a budget;
- ordinary local permission checks add no model calls or remote authorization dependency; stale/cross-context grants cannot admit an effect, and contention/resource measurements follow §1.1;
- generic first-action/continuation/ordinary-work guards resist explicit starts, received-message scheduling and reconnect bypass; inspecting/waiting starts no paid work, while full pinned leadership integration remains a later C8 gate;
- task queries preserve real waiting-ready, waiting-blocked and active distinctions without inferring staffing or delaying state for animation;
- service loss is recorded from actual classified refusals, exhausted owner limits and execution failures after the grace period or at once for owner-only causes, survives restart, adds no probe or model call, and ends only with a completed call after its cause clears or stops applying;
- after a crash mid-turn, restart leaves `interrupted` ends and repeats no tool effect;
- no child process survives a normal or abnormal shutdown on Windows;
- every action is in the ledger;
- a promotion request surfaces unavailable validation, leaves canonical unchanged and is never reported as successful delivery;
- the check is green on Windows, Linux and macOS in CI.

**Status.** Waiting.

### C4. Validation runner and promotion gate

**Goal.** The host-owned runner that produces admissible validation evidence (validation §4.2) and gates promotion on required checks (validation §4.8). Every promotion-pipeline field depends on it. D52 selects Path A: implement this after the runtime, prove end-to-end validated promotion on test workspaces, then permit real colony work when C4 is Done.

**Delivers.** A host-owned runner, fresh subject materialization, declared-check overlay, in-process canaries, structured language adapters, bound outcome derivation, rerun/confirmation scheduling, immutable evidence in the ledger and promotion gate. Run directories are fresh under `runs/<short id>/` (D38), logs use the body store (D33), and the attempt queue emits queue/start/finish events (Undercurrent §15.1). Bounded adjacent canonical before/after evidence plus optional bounded backward search attributes a culprit; C4 supplies actual attempts while C5 governs repair/retirement (PV16).

C4 consumes the promotion-boundary contract approved before C2/C3 implementation. It integrates the runner with C2's bounded cumulative candidate preparation and serialized finalization, and C3's bound requests/results; it does not redesign them. Each lane validates a fresh exact derived tree against its expected predecessor and generation, preserving immutable submission/request context and actual attempts. Acceptance uses current evidence for the durable successful prefix—not standalone passes or a passing descendant over a failed predecessor (D77). All output from a sealed generation remains unavailable to later foreign generations until its exact foreign predecessor is durably accepted (BD2). C4 measurements and promotion scenarios are testing, not early production use (D52, D78).

| Definition | Meaning |
|---|---|
| Check | Command plus defining files, identified by digest (validation §4.1) |
| Subject | Exact host-captured Git tree and normalized content; evidence also binds the derived candidate, expected predecessor, generation, original request, current requirements, check/environment/law versions (D74, D77) |
| Environment digest | Toolchain, sorted environment variables, platform and dependency set, digested in the style of a remote-execution action key |
| Overlay | Replacing same-path files in the materialized subject with the pinned check files before running |
| Canary | A sentinel check that must fail, run in the same process as the suite (validation §4.9.1) |
| Outcome states | Per attempt: passed, failed, timeout, skipped, incomplete, infra_failure, no_status. Per record: passed, failed, flaky, inconclusive (validation §4.3) |
| Assurance level | A1 to A3 (validation §4.9); A1 is the phase 1 target |

**Waits for.** C3 Done, U1.L Done, and the owner-frozen `law-format` contract (D47); it does not wait for U1.E.

**Stop point.** Do not start before C3 and U1.L are Done and `law-format` is frozen. C4 consumes U1's versioned parameters; it may not define duplicate values or policy for the flake-rate prior, its confidence, or quarantine threshold. C4 does not wait for U3: D72/D73 require every declared per-workspace required check, with empty/invalid/unconfigured gates unavailable and no path/graph exclusion. U3's accepted-promotion integration gate waits for C4, never the reverse. Real work remains prohibited until the integrated D77 concurrency and D78 feasibility gates pass and C4 is Done. Windows Defender remains enabled; no `runs/` exclusion is selected. A1 is not A2; hostile-child containment and positive quiescence are C10's gate.

**Unblocks.** Real colony work through the tested promotion path, subject to all other applicable gates (D52); C5; C9, together with C5 and C6; U3's accepted-promotion integration gate; U4, together with C2, C5, U1 and completed U3; U2's A1 validation suite, together with C3; U5's runner-queue node.

**Decided.**

| Decided | Source |
|---|---|
| Admissibility requirements | Validation §4.2: a **[PROPOSAL]** the earlier roadmap adopted as the working design; not an owner decision |
| Rerun counts from flake rates; repair confirmation | Validation §4.4, §4.5: **[PROPOSAL]**, with the same standing; parameter values and confidence come from U1's frozen `law-format` under D47 |
| O4 plus canaries; sample audits off; no automatic O1 relaxation. A repair at A1 ends strain only after a second, host-scheduled run in a separate run directory confirms it | V6 starting floor (PV25); exact finite suite, result transitions and current unrun status: validation §4.9.1 |
| Run directories under `runs/<short id>/` | D38 |
| Validation events go in the colony log; run logs go in the body store | D33 |
| Promotion checks test the merged result when a promotion carries changes from several of one agent's contexts | D23 |
| Validation prior, confidence and quarantine threshold are versioned law parameters owned by U1; C4 waits for frozen `law-format`, and C5 receives the parameters through C4 | D47 |
| Real work waits for this step's end-to-end validated-promotion gate; C2/C3 alone support testing only | D52 |
| Bounded concurrent cumulative validation per workspace, successful-prefix publication in durable request order and one serialized entire durable finalization sequence | D77 supersedes D54; D59 retained |
| Preserve original submission/request; bind evidence to each derived candidate/predecessor/generation; invalidate and fully revalidate dependent candidates after predecessor change | D56, D64–D69, D77 |
| Reconcile a proven promotion after a crash without applying again; requests proven not promoted, including queued requests, require explicit resubmission | D55 |
| Initial non-no-op gate runs every declared required check regardless of changed paths; acceptance needs passing bound evidence for all. Empty/invalid/unconfigured gates are unavailable; human approval cannot substitute | D72, D73 |
| Workload/capacity feasibility is part of design/readiness; runtime measurements must exercise accepted throughput and latency under contention/rework, not merely count validation attempts | D78 |

**Prerequisites and implementation proof—not open choices.**

| Prerequisite or gate | What remains |
|---|---|
| C3 and C2 | C3 Done; C2 Done only after D52 owner approval, C1's F0 contract freeze, full-tree capture and bounded admission/finalization proof |
| U1 law | U1.L Done and owner-frozen `law-format`; C4 consumes exact versioned priors/confidence/quarantine parameters, never defines them. U1.E is not a C4 dependency |
| Declared-check contract | Freeze `WorkspaceChecks` configuration and schema under D40; run every declared required check for every non-no-op subject regardless of changed paths. Bind config/check hashes; empty, invalid, missing or unconfigured gates are unavailable, never an empty pass (D72/D73) |
| Subject and materials | Capture tracked plus eligible nonignored files, read/hash every content (no stat-only shortcut), apply recorded Git normalization/attributes, build the exact tree and fresh isolated materialization. Bind tree, content, workspace, predecessor, generation, request, check, environment and law versions (D74, D77) |
| Execution and evidence | Pinned Go `go test -json -count=1`, Python pytest/JUnit with trusted collection/terminal reconciliation, and Node pinned TestsStream or JUnit/wrapper; reject missing/partial/no-test/skip-only output, exit/report disagreement, interruption and unbound output. Keep same-process canaries and O4; sampling remains off |
| Outcomes and law activation | Use the frozen V1–V8 typed states, fresh-run and confirmation rules, and exact owner-authorized policy-version transition in core §13.4. Host hooks can trigger the same authorized Core path but cannot authorize a result; promotion alone repairs nothing |
| Attribution and C5 | Bounded adjacent canonical before/after evidence, optionally bounded backward search, with actual attempt records; C5 owns repair verdict, owner-only reversible human oracle and check governance (PV16–PV20) |
| Durability and output barrier | Full-generation quarantine; no foreign-generation output, paths, logs or verdict details before exact predecessor's durable canonical acceptance (BD2). On activation/predecessor change stop superseded children and prove exit before replacement or reuse |
| Concurrency and checkpoint admission | B4 sets separate provisional bounds: `P=4` validation lanes colony-wide and at most four per workspace; `K=4` FIFO, non-coalescing checkpoints per enrolled `(context, workspace)` pair, so workspace `Q=4×N` and colony `Q` is their sum. A distinct fifth checkpoint is busy before capture when that pair's four `K` slots are occupied. `P` is service concurrency, not admission capacity. These are calibration bounds, not measured throughput or a population cap |
| Workload feasibility | Exercise 12/20 enrolled code-working `(context, workspace)` pairs at 72/120 every-change requests per hour and synchronized four-checkpoint-per-pair bursts (48/80 total), then the actual larger population. Include safe rework, accepted throughput/latency, head-of-line, capture/merge/check/finalization, CPU/RAM/disk/process and failure/recovery costs. Provisional target is accepted capacity ≥1.25× measured steady demand; no SLA or evidence result is claimed |
| Host conditions | Defender stays enabled; no scanner exclusion. C10 must prove hostile-child containment and positive quiescence before A2; C4's A1 pass cannot imply that boundary |

**Q48 downstream comparison [INFERENCE supporting D73].** The owner selected A after considering this comparison: every check explicitly declared required for the workspace, not every repository test. B's path-based applicability is not selected; its implications below describe the rejected initial-gate alternative, not added implementation work.

| Boundary | All declared required checks | Path-based applicability |
|---|---|---|
| Coverage | No extra inference that an unchanged path is unaffected; still only as good as the declared checks | Can avoid irrelevant checks, but a wrong path rule can omit the only check exposing a cross-module/configuration regression |
| C2/C4 evidence | Bind the derived candidate, predecessor, generation, current declared required set, checks, environment and law while preserving original submission/request | Also needs a reproducible selection definition and explanation of the resulting set; trust, versioning, unmatched paths and configuration/toolchain changes need specification before C2/C3 code |
| C5/U4 governance and effects | More gate observations may be available; promotion alone still does not prove a repair or grant affordance | An omitted check supplies no passing evidence and cannot clear its pinned failure; repair/confirmation runs may still be needed outside the selected gate subset |
| C4/U5 cost | Pays for the full declared gate in each required attempt; consumes bounded validation lanes and may delay ordered publication or amplify rework (D77/D78) | May reduce runner work/queue delay, but leaves capture cost and any separately required confirmation/post-promotion work; benefit is unmeasured |
| Build order and Router | Existing C2 → C3 → C4 sequence; no U3 selection dependency | Same order is possible with explicit rules, but C4 gains selector configuration and adversarial selection cases; no new U3 or Router dependency is implied |

Both options retain D69 refresh when required checks change, C5's separate retirement authority and U4's confirmation/assurance/lineage gates. The proposed post-promotion runs and gate-evidence reuse in `RESEARCH-ADMISSIBLE-VALIDATION.md` §4.8 may move omitted work after promotion rather than remove its cost; neither proposal is approved by Q48. Actual attempts alone feed U5's runner accounting. Fewer test processes are not a measured model-token saving, and D63 still forbids a model turn triggered solely by promotion completion.

**Owner choice (D73): A.** Keep the initial declared required set simple and conservative. Reconsider conditional selection only if real check inventories and measured cost justify its selection-risk contract; any adoption still needs separate approval and compatible evidence records, without silently reinterpreting old results. No path-rule implementation is part of the initial gate.

**Exit criterion.** Using the real runner on test workspaces:

- Run the finite V6 suite in validation §4.9.1 for every preregistered enabled profile: all 24 attacks are inadmissible, each of the nine controls matches its declared outcome, every launched test process reports the digest-bound canary's exact expected failure, and the injected infrastructure control is unavailable without launching a process. Any initial admissible attack pass blocks release: a repeat on its single fresh confirmation is fail, and a non-repeat is inconclusive; neither can yield suite pass. Missing/unbound evidence or any control mismatch also blocks release under the section's exact transitions; suite workspaces emit no field-source event or accepted promotion.
- Complete a successful bound request from C3 through C2 and C4: the exact validated derived candidate becomes canonical, and the recorded accepted delta equals the diff from its actual durably accepted predecessor. In `M+A` then `M+A+B`, B's delta is `(M+A) → (M+A+B)`, not `M → (M+A+B)`; preserve D23 authoring contexts and exercise post-promotion observation with actual acceptance.
- The original submission captures tracked changes/deletions and non-ignored new files, includes tracked files despite ignore matches and excludes ignored untracked files; the mutable agent index cannot change eligibility (D60). Integration preserves that immutable input and validates the exact derived tree, not an unrecorded substitute (D77).
- With real validation available and the actual durably accepted predecessor established, recorded CRLF-to-LF rules make a checkout-only difference yield D71 no-change without required checks. For a real content change, validate materialization and promote the same derived canonical tree, keeping raw observation hashes distinct and preserving bytes where conversion is not required (D74, D77).
- Preserve imported/reviewed local/global private-file exclusions. An unrelated personal Git exclusion edit does not change capture until explicit workspace inputs are deliberately updated; updates affect future captures, not an admitted submission (D75, D56).
- Apply captured scratch `.gitignore` edits to new-file eligibility in the same submission. An ignored new file stays out while matching tracked modifications/deletions stay in; later `.gitignore` edits cannot alter that submitted snapshot (D76, D60, D56).
- A failing or otherwise non-passing required check or unavailable validation prevents acceptance. When eligible for publication, a mismatch between actual canonical and the candidate's expected integrated predecessor blocks publication and invalidates dependent evidence; rebuild and fully validate before any later publication. Waiting for a speculative predecessor to publish is not itself a mismatch. An older original submission base alone is not a refusal, and no unvalidated replacement may inherit the tested candidate's evidence (D77).
- While a submission is queued or validating, authorized scratch edits remain possible. Preserve original submission/base/capture and request/context across derived generations; accept only the exact validated integration result and its actual predecessor-relative delta. Later scratch edits remain undelivered and are not discarded (D56, D77).
- Exercise explicit mechanical integration and conflict reporting: never silently substitute a different submission or automatically resolve a semantic conflict. A clean mechanical integration that changes its durable predecessor still needs every declared required check; passing standalone `M+A` and `M+B` is not evidence for `M+A+B` (D73, D77).
- A durable pending request survives the requesting turn's completion and reports to its original context (D53).
- Cancelling the originating turn leaves its submitted promotion running. Separate cancellation requires the submitting context or colony owner, not supervisory position alone. Cancellation before durable intent prevents that request's later Git update and invalidates dependent candidates; after intent it reports too late, never acceptance (D57, D59, D77).
- Demonstrate actual overlapping real validations for cumulative `M+A` and `M+A+B` within the provisional `P=4` lane bound (at most four colony-wide and four per workspace), preserving durable request order so a later candidate cannot publish ahead of its successful prefix. `P=4` is a calibration bound, not evidence of measured or default capacity (D77, D78).
- Serialize the entire finalization sequence: durable intent, guarded fast-forward Git update and durability, durable acceptance, then finalization-slot release. Even after a predecessor's ref update, no later durable intent may begin before its proven durable outcome; unresolved effects block publication (D59, D77).
- Fail, remove or cancel a predecessor, and change an expected predecessor after dependent evidence exists. Invalidate affected descendants, stop superseded children and confirm exit before replacement/reuse, then rebuild and fully validate without withdrawn changes. A passing aggregate cannot bypass a failed prefix or reuse stale evidence. Keep real historical outcomes; supersession is not a fabricated test failure, and cumulative failure alone does not establish the newest request as culprit (D68, D77).
- Exercise crashes before intent, after durable intent but before Git update, after Git update but before durable acceptance, and after acceptance but before finalization-slot release, with speculative lanes present. Under D59/D77's evidence/fencing prerequisites, reconcile the expected predecessor as interruption without retry and the intended derived candidate as missing bookkeeping without another update. Insufficient proof or an unfenced prior writer blocks publication; repeated recovery must not duplicate acceptance. Preserve D37's interrupted operation ends; queued or speculative requests proven not promoted also end interrupted without automatic restart (D55).
- Same-ID/same-submission retries return one request and its actual pending/terminal result without another queued job or validation execution. Changed submissions under the same ID are refused; different IDs are not silently coalesced. An interrupted/cancelled ID is not reopened; an explicit new attempt uses a new ID (D58).
- A distinct fifth checkpoint for one enrolled `(context, workspace)` pair receives busy before capture only when its `K=4` admitted slots are occupied; workspace `Q=4×N` and colony `Q` are the admission bounds, independent of `P=4` validation lanes. Do not coalesce; a busy/refused request is not accepted throughput. Other pairs may use their own slots subject to shared resource limits and D70's immediate safe-capture-or-busy rule (B4, D70, D77, D78).
- Completion makes durable status/events available and surfaces the result in the original context's next naturally scheduled turn without creating a follow-up model turn solely for that result. An explicitly waiting caller can continue normally (D63).
- During a policy change, hold new finalization intents while already-intended finalizations reach proven durable outcomes; unresolved effects prevent activation. Refresh all affected pre-intent lanes before publication, preserving original submission/request and real prior attempts with no new capture. A still-current derived tree may remain fixed; predecessor rebuild instead creates a new generation needing full fresh validation. Superseded evidence never authorizes a new intent (D64, D77).
- Only the colony owner authorizes an exact policy version. Core executes/records that version, not later file contents; one-off scheduled transitions cannot begin before eligibility or bypass unfinished finalizations. Affected workspaces share one colony-wide version switch; unresolved finalization in one delays it without imposing identical check sets/toolchains (D65–D67). Use core §13.2's one pending durable request, exact UTC not-before instant, expected-manifest binding, owner CAS cancellation/replacement and overdue-restart procedure; historical replay cannot invent an activation or restart promotion/validation.
- After activation or predecessor invalidation, stop superseded validation children across affected lanes and establish exit before replacement runs or lane/directory reuse. Unproven cleanup blocks affected replacement/reuse, not permission to finalize on old evidence. Retain actual historical outcomes; supersession alone is neither test failure nor request cancellation (D68, D77).
- With law unchanged, adopt changed required checks or execution environments through controlled cutover. Affected pre-intent lanes require fresh bound evidence under the new requirements, keeping original submission/request fixed without extra capture; only a still-current derived tree may stay fixed. Changed predecessors require a new generation and full validation, while pinned historical-failure governance remains intact (D69, D77).
- If safe exclusive capture cannot begin immediately, return busy without a snapshot, queued capture, writer interruption or automatic retry. When capture succeeds, host-managed writes and merge-back cannot interleave with it; later scratch edits remain possible during validation (D70).
- With real validation available, derived trees identical to their actual durably accepted predecessor yield durable no-change for both identical and differing commit tips: no required checks, intent, ref movement, acceptance or false validation evidence. Speculative redundancy is not terminal: if B appears redundant over speculative A, B waits for A's proven outcome; if A is removed, rebuild B without A and run the full required gate if B now changes the durable predecessor. Preserve scratch work/history and independent IDs (D71, D77).
- Refuse empty, invalid or unconfigured automated gates as unavailable. Require actual passing bound evidence for every declared required check, never human approval as a substitute or absent checks as a pass. A declared required check failing or not running prevents acceptance even when its associated paths were not changed; passing a selected subset is insufficient (D72, D73).
- Measure actual steady and synchronized-burst every-change workloads with safe same-workspace overlap and ordered durable publication. Report attempted work separately from accepted throughput and end-to-end latency, including busy/refused work, head-of-line delay, full-required-check costs, CPU/RAM/disk/process contention, invalidation/rework, activation and failure/recovery. Compare the results against the pre-build workload/capacity assumptions and chosen bounds; neither a serial safety demonstration nor ideal lane arithmetic proves feasible capacity (D78).
- Show the requesting context/owner the actual outcome, distinguish test results from delivery, and record the evidence for these gates.

Only after all C4 gates pass and the step is Done may real colony assignments begin (D52). This does not waive C5/C6 governance, routing or Undercurrent release gates, or C10's isolation requirements.

**Status.** Waiting.

### C5. Check registry and governance

**Goal.** Make checks first-class: pinned identity, lineage, open failures, quarantine and the V3 retirement flow.

**Delivers.** A registry derived from ledger events, check lineage records, the Core-assembled retirement packet, Orchestrator review, quarantine and human-oracle verdict events. V3's scoped retirement is a **[PROPOSAL]**: before C6, only the owner retires; afterward, only an authorized member of the owning Corps, with the Orchestrator able to overturn. The repairing context and its clone descendants are excluded, not every context of that agent (D86). C5 does not authorize owner-verdict-only promotion: D72 retains human-oracle evidence production but C4 has no human substitute for a failed or unavailable gate.

D72 retains human-oracle verdict production at C5; it does not approve owner-verdict-only promotion here. C4 has no such fallback, and any later human-only promotion mode needs a separate approved design.

| Definition | Meaning |
|---|---|
| Check lineage | Author context, clone ancestry, model family, and derivation from other checks (validation §4.7; Undercurrent Q2) |
| Open failure | A failure at grade F2 or above that no repair or retirement has ended |
| Owning Corps | The Corps of the context that promoted the check's current digest (validation §4.6.1) |
| Retirement packet | Host-assembled evidence for a retirement, plus the agent's reason category and argument |
| Quarantine | Automatic suspension of a check past a law-defined flake rate |
| Human-oracle verdict | An owner-issued verdict recorded in the ledger; the only failure or repair source for work with no automated oracle |

**Waits for.** C4, which waits for C3, U1.L Done and owner-frozen `law-format` before parameter-dependent behavior (D47); no other U1 track is a C4/C5 dependency.

**Stop point.** Do not start before C4 is Done. At C5 the owner is the only retirer: Corps membership and the Corps-authority integration gate arrive with C6. The step does not fake Corps membership to test the agent path (D41).

**Unblocks.** C6's Corps-authority integration gate; C9, together with C4 and C6; U4, together with C2, C4, U1 and U3; U2's governance cases, together with C6.

**Decided.**

| Decided | Source |
|---|---|
| An agent in the owning Corps may retire a check; the Orchestrator has the last say | V3 (owner, 2026-10-02); validation §4.6.1 |
| Governance events are typed, versioned events in the colony log | D33, D35 |

**Prerequisites and implementation proof—not open choices.**

| Prerequisite or gate | What remains |
|---|---|
| C4 and U1 | C4 Done; use only the exact frozen U1 law revision/parameters |
| Before/after C6 | Owner is interim retirer. After C6, require owning-Corps authority and the real Orchestrator notify-and-overturn gate; never fake membership to pass |
| Retirement eligibility | Deny the repairing context and clone descendants only; a separate otherwise-authorized context of the same agent may retire (D86) |
| Human-oracle correction | V7: owner may reverse only its exact prior verdict with an append-only corrective event bound to the original subject/artifact digest, criterion revision, scope and reason; do not rewrite history |
| Reproduction checks | V8: agent-authored reproduction is evidence only; do not automatically make it canonical or required. Explicit governed adoption is separate |
| Quarantine | Required quarantined check makes validation unavailable. Release needs explicit owner authorization and fresh qualifying evidence for that digest/environment or an approved replacement; no timer, promotion-based release or silent skip |
| Repair finding | Promotion alone repairs nothing. Require reproduced fail-before/pass-after, law-required fresh passes and maintained prior checks, using bounded attribution evidence |
| Replay | Retirement, overturn, verdict correction, quarantine and release events rebuild the same registry, lineage and failure state |

**Exit criterion.** On C5's owner-only interim path, retirement and replay work; repairer-context/clone-descendant exclusion and its narrow scope hold; exact-owner verdict correction appends without erasing; required quarantine cannot pass or clear itself, and owner release requires fresh qualifying evidence. C6 separately proves owning-Corps retirement and Orchestrator overturn.

**Status.** Waiting.

### C6. Broker, role graph and Corps boards

**Goal.** Durable, authorized messaging between agents, the first part of core §9 phase 2. Agents reach one another only along the role graph, and accepted messages survive a restart.

**Delivers.**

- The role graph (core §7.2): permitted direct counterparts and board output per role, with default deny for every unlisted pair and operation, checked on send, delivery and retrieval (core §7.4).
- Apply §1.1's D79 guidance to current grants, queued access and reassignment without serializing independent conversations or introducing per-message model approval.
- The embedded broker (core §7.4): committed message records with core-assigned message IDs and sender-scoped submission IDs; auth before capacity, then dedup before enqueue; durable acceptance, references rebuilt from records, processing checkpoints, asynchronous send/yield. AC06 message/ref caps remain provisional. Core §13.6 reserves a separate per-colony admission/service slot for one pending shutdown and one pending grant revocation; ordinary messages cannot consume them, a full class returns busy before acceptance, and bounded control priority preserves ordinary progress. Controls affect Core state without promising delivery/acknowledgment to offline consumers; paid follow-up stays under BudgetBook. Other overflow is visible busy/backpressure, never silent loss.
- Corps boards for worker discussion (core §7.5): each post stored once, a cursor per reader, and the posting rights in core §7.2. Griot story publication follows in C8.
- Relationship and board contexts (core §7.3), completing the context kinds of D21.
- The `conversation.send` and `board.post` commands and the `conversation.appended` and `message.processed` events (core §4.2).
- Messages and inbox processing in the colony log; message bodies in the body store (D33).
- Explicit Corps membership and parent-link records, which the role graph needs in any case, without waiting for C8's birth or Griot designs (core §7.2, §7.6).
- D89–D95 social handoff ([fortress GUI design](../.scratch/fortress-gui/spec.md)): stable agent/Corps/relationship/context IDs, membership epochs and topology revisions; separate general/task/board/directional memories and delivered history. Authenticated colony ADMINISTRATOR inspection covers all colony records/private memories, without impersonation or widened agent recipients; provider credentials stay Core-held. Classifiers see only evidence available to the named agent, not administrator-only facts.
- C6 owns perspective-bound trust/sentiment annotation and evidence bindings: Core §13.6's adopted 3×3 vocabulary, signed trust -1/0/+1, explicit unknown/partial axes and no fabricated midpoint. Relevant eligible evidence changes automatically request coalesced, spend-bounded refresh. Keep a still-authorized prior label visibly outdated/updating while pending; revoke inaccessible evidence. Numeric rubric/cutpoints/encoding remain producer/testing work. Viewing/hover/repaint never triggers a call; actual configured producers retain ordinary budget and applicable external-helper R5/C10 gates.
- Recipient-scoped evidence/publication support for C8: authorized shared Purpose/Corp Creed/mythology sources with version provenance, and explicit Incumbent→Aspirant handoff of processes, lessons and directional relationship roster. D94–D95 permit deterministic transformed evidence, not raw private contexts, counterpart memories or grant inheritance. C6 supplies the scoped evidence path; C8 owns readiness, officer cutover, retirement and initial-publication approval.
- The Corps-authority integration gate with C5 (V3; validation §4.6.1): a retirement by an agent in the owning Corps takes effect, the repairing context's retirement is refused, and the Orchestrator can overturn. C5's own gate is owner-only.
- The Orchestrator instruction set: V3 notify-and-overturn and the bounded U8 unusual-mood interruption criteria; role or mood is not authorization to inspect private contexts or issue unrelated grants.

Broker and board decisions use the detailed single-ledger contract in core §13.6. This replaces the former [INFERENCE] prototype language; no cross-colony route or global coordinator is implied.

| Definition | Meaning |
|---|---|
| Conversation | Canonical exchanged messages: an authorized pair or a shared Corps board (core §7.3) |
| Inbox | Pending authorized message references and processing state (core §7.3) |
| Submission ID | A sender-scoped ID, so that a resend after reconnecting returns the original acceptance instead of a second message (core §7.4) |
| Processing checkpoint | A committed record covering a message's resulting context changes and outgoing messages. Only it marks a message processed (core §7.4) |

**Waits for.** C3; C5 for the Corps-authority integration gate.

**Stop point.** Do not start before C3 is Done. C6 does not wait for C7; core §9 phase 2 orders the broker before shared live attachment (`ROADMAPS.md`). The Corps-authority integration gate needs C5's retirement flow: if C5 is not Done when the rest of C6 is, stop before that gate and set the status to *Blocked: waiting for C5*; nothing stands in for the registry (D41).

**Early testing-only social milestone.** Schedule the private-context/relationship restart drill as soon as C3 is Done and the relevant C6 producer family is owner-frozen, without waiting for C4 Done or C5's retirement integration. Use the actual Core context store and broker with two test agents on an authorized relationship: each keeps distinct private memory, handoff, transcript, provider continuation and approvals while sharing only the canonical exchanged messages. Demonstrate distinct private views, authorized send/receive, forbidden-route refusal before inbox insertion, and a crash/restart after durable send but before processing. Restart must preserve the private separation, exact delivered/processed cursors and original command receipt, with no duplicate message or automatic replay of interrupted model/tool work. Revocation before a subsequent read prevents newly unauthorized body disclosure. Record the actual commands, receipts and observations; a pure state model is not this runtime proof. Provider-backed turns require configured credentials and authorization; offline adapter drills are labelled separately. All work remains testing-only, no accepted promotion is invented, and D52 still forbids real colony work until C4's actual gate passes. This partial milestone does not mark whole C6 Done or add a full-C6 dependency to C4.

**Unblocks.** C8 together with C3 runtime and actual C9 backend alignment capability; C9 backend together with C4/C5; U2 governance with C5; U8 with U7. C6 itself does not wait for C8/C9, C7 or GUI; later succession/publication integration uses its real evidence/recipient primitives.

**Decided.**

| Decided | Source |
|---|---|
| Private contexts include relationships and boards; delegated build choice B7 selects a Core-owned single-ledger broker (specified in Core §13.6), not a separate agent or service | D21; B7 |
| Permitted communication paths per role, recorded in core §7.2. The vertical-versus-lateral reading there is [INFERENCE] | D11 |
| Workers talk to other workers only through the Corps board | D11; core §7.5 (owner requirement) |
| No cross-colony route by implication; cross-colony messaging needs its own design | D26; core §7.2 |
| Messages and inbox processing go in the colony log; message bodies go in the body store | D33 |
| Message IDs are UUIDv7 | D37 |
| An agent in the owning Corps may retire a check; the Orchestrator has the last say. C6 runs the gate, on C5's retirement flow | V3; validation §4.6.1 |

**Prerequisites, open producer work and implementation proof.**

| Prerequisite or gate | What remains |
|---|---|
| Authorization graph | Enforce parent/child and same-role edges plus actual Diplomat-mediated cross-Corps routing; default deny and recheck at send, delivery and retrieval. No implicit federation/global coordinator |
| Durable message and receipt | Same ledger as Core; sender-scoped idempotency, immutable message ID, body refs, membership/auth revision, separate conversation sequence and recipient checkpoint, atomic context/outgoing-envelope processing |
| Admission bounds | AC06 direct/board caps remain provisional: 64 KiB/message, 16 attachments, 128 pending direct refs/context, 1,024 board refs/board, 4,096 colony-wide refs. Separately reserve one Core admission/service slot per colony for a pending shutdown and one for a pending grant revocation; ordinary message refs cannot consume them. A distinct same-class request while its slot is occupied returns visible busy before acceptance; controls are ledgered with bounded priority, not delivered as ordinary messages, and have no offline-consumer delivery guarantee. Authorize before capacity, deduplicate after auth before enqueue, and keep paid follow-up under BudgetBook; calibrate message caps against actual storage/traffic |
| Reassignment, retirement and administrator | Close old agent grants/future delivery. D83 retains only delivered history read-only with holes, no unseen backlog/successor/future access, including counts/existence. Separately prove administrator-wide inspection without impersonation, agent-input leakage or provider keys |
| Relationship projections/classification | Freeze actual C6 families with C1 after specifying the perspective rubric, numeric representation/cutpoints and partial/stale handling. Use Core §13.6's nine labels and signed trust; eligible evidence changes refresh automatically with bounded/coalesced spend. Only still-authorized labels may remain marked stale; geometry/memory creates no route |
| Scoped handoff evidence | Recipient/source/direction/version provenance, authorized process/lesson/roster disclosure and deterministic normalized attenuation toward zero with trust retained more strongly. Exact function/coefficients/sentiment normalization/encoding remain open pending testing; no linear default, unknown-as-zero, private-memory or grant inheritance |
| Shared artifacts/publication | Authorized Griot-channel/board delivery and source-version lineage for Creed/prehistory/Corps stories; no broad board/private access. C8 owns human initial-publication decision and role lifecycle; C6 generic evidence primitives do not wait for C8 |
| Processing and fairness | Accept/deliver/process/reply remain distinct; checkpoint commits context revision and outgoing messages atomically. The existing fair ready-work scheduler uses bounded queues and round-robin service without per-peer blocking; independent AC06 control reserves use bounded ledger-append priority so ordinary writes also progress. C6 measures service fairness and latency, but v1 promises no peer-response deadline or reply SLA |
| Corps authority | C5 owner-only interim path and its real C6 owning-Corps/Orchestrator overturn gate; no stand-in registry or membership |
| D80 and unusual mood | Resource-pressure and permission-scoped overlap hints are coarse, audience-bounded, expiring types. Strange-mood notification reaches owner without blanket approval/inspection; emergencies bind a specific interruption, deadline, impact and alternatives under the Orchestrator criteria |
| Federation | Cross-colony messaging requires an explicit independently sovereign federation agreement and restricted projections; protocol belongs to B14, not C6 |
| Recovery | Restart/replay, duplicate commands, partial deliveries, revocation, busy, sequence gaps and unauthorized history queries retain exact authorization and receipt semantics |

**Exit criterion.** Core §9 phase 2's messaging gates: allowed direct and board paths work; forbidden paths fail before delivery; accepted messages survive restart. The Corps-authority integration gate (V3; validation §4.6.1): a retirement by an agent in the owning Corps takes effect, the repairing context's retirement is refused, and the Orchestrator overturns one. The routing §13 role drill: a worker switching among its Orchestrator, Diplomat and Corps-board contexts leaks no history, provider continuation, approval or late result between them, and a worker-to-worker direct message is rejected before delivery. R0's isolation gate covers the general and task contexts; R1 does not wait for this drill.

**AC06 control-reserve gate.** Under saturated ordinary direct/board queues, one shutdown and one grant revocation must still reach their durable Core records through the separate slots. A second distinct same-class command while its slot is occupied returns busy before acceptance; a same-ID retry returns the original receipt. When ordinary work is waiting, it receives an append after no more than two consecutive reserved-control commits. Offline consumers are not treated as delivery acknowledgments, and any paid follow-up remains BudgetBook-reserved. This behavior still requires implementation and load/replay proof.

**D89–D96 producer acceptance.** Against the actual broker/context store, observe stable topology revisions across rename/reassignment/retirement/restart; forbidden worker-peer/Griot-worker DMs never appear. Inspect general/task/board and both directional memories separately. Prove D83-retired-agent history holes and administrator-wide inspection as different principals; neither inspection nor classifier inputs leak administrator-only failures, private counterpart evidence or provider secrets.

After the exact rubric is specified, exercise all adopted grid regions plus unknown/partial/contradictory evidence, independently directional results and signed distrust/neutral/trust. Eligible evidence changes must coalesce/admit/account actual refresh calls; repeated inspection costs none. A pending refresh may show only a clearly stale still-authorized label; revoked evidence disappears. Prove actual configured classifier and applicable R5/C10 qualification before claiming live labels, not a stand-in or `unknown` fallback.

For C8 handoff integration, reject wrong recipient/scope and raw private-context/counterpart-memory/grant transfer. After selecting/testing the attenuation implementation, prove deterministic movement toward zero, stronger trust retention, unknown preservation and source/direction provenance. Authorized shared mythology/version references must not widen board access or become operational achievements. These future acceptance cases neither claim a selected numeric formula nor make generic C6 depend on C8/GUI.

**Status.** Waiting.

### C7. Colony host, local IPC and launcher protocol

**Goal.** Turn the core into a colony host that several views attach to (D26, D39): the rest of core §9 phase 2, apart from the GUI client.

**Delivers.**

- The colony host: one core host process per active colony, the sole writer of its ledger, holding C1's lock (D26). `dwarf-harness` hosts the colony or, when a host already runs, attaches to it as a terminal view (D26, D39; core §3.1).
- Local IPC from the Core-owned protocol boundary (core §13.7): Windows local-only named pipe with restrictive intended-user DACL and `PIPE_REJECT_REMOTE_CLIENTS`; Linux/macOS Unix socket in a private per-colony directory with restrictive permissions. Authenticate every connection; ACL/socket permissions are not an execution sandbox.
- C7 owns `launcher-protocol`: four-byte big-endian length-framed strict versioned JSON, UUIDv7 idempotency, authenticated binding, expected entity revision, sequence and authorization. Provisional frame cap is 1 MiB; negotiate versions and reject duplicate keys, malformed lengths, sequence errors and unknown required variants. This roadmap does not claim a frozen contract.
- Views with independent durable cursors into the colony ledger sequence. Streamed text is separate ephemeral in-memory data keyed by turn ID and delta index; attach receives the current-text snapshot and its included delta high-water index, then only later deltas above that index (D49). An approval or router notice is resolved once by its ID across views; the core orders competing commands. Turn cancellation reaches the shared turn, not its submitted promotions; promotion cancellation is a separate request-specific command under D57 (core §4.1).
- Promotion retries preserve D58's original request identity/context across views; the same ID is not another job or authority grant. Present immutable submission separately from current derived candidate/generation, expected predecessor and real attempt outcomes (D77); a speculative pass is not acceptance. Show D59's too-late cancellation distinctly and recover durable outcomes without duplicating promotion. Transport shapes follow the approved C2 boundary and C7 contract freeze.
- Result availability is not a model wakeup: D63 makes terminal outcomes available through status/events and the original context's next naturally scheduled turn. Views preserve D62 as amended by B4: FIFO `K=4` independent checkpoints per enrolled `(context, workspace)` pair (`Q=4×N` per workspace, colony `Q` their sum), with no coalescing; a fifth distinct checkpoint is busy before capture when that pair's four slots are full. This admission bound is separate from `P=4` validation lanes. Same-ID lookup is idempotent and creates no extra capture or model turn (D58, D77).
- Staged D89–D96 presentation handoff ([fortress GUI design](../.scratch/fortress-gui/spec.md)): typed Core-owned snapshots/commands for current appearance/tileset, geometry, drafts, manual-mode setting, stable change groups/TODO and reviewed topology baseline. Core alone validates and durably saves; GUI computes previews. Cosmetic state changes no membership, memory, permissions, execution, messaging or field input.
- Bounded per-GUI-run undo/history disappears on close; current state/drafts/TODO persist through a replaceable/bounded Core-owned snapshot/checkpoint mechanism under the existing storage discipline. Specify that mechanism, finite cosmetic retry-receipt retention/expiry and recovery before freeze; no separate GUI database or permanent per-edit ledger. Preserve retained append-only operational birth/alignment/Purpose/publication/succession history.
- Atomically save layout and associated TODO with current authorization, expected presentation/topology revisions and immutable command IDs. Same-ID retries within the declared retention contract recover one result; stale/expired retries cannot reapply old edits. Failures remain unsaved; conflicts require reload/reconciliation; unsupported schemas fail read-only. Exact schemas/limits and C1 catalogue support still need producer specification/owner freeze.
- Authorized topology/presentation snapshots and high-water recovery, separate directional relationship/memory/stat/telemetry projections and actual C3/C6/C8/helper facts. Preserve administrator-wide inspection separately from agent-visible evidence; no provider keys. Keep reviewed topology, pending construction and `through_seq` independent. Startup/live topology notices apply even when manual planning is off; autocomplete/manual/later disposition and partial work persist without duplicate births, paid calls or placements.
- Shared versioned creation drafts/interview progress, Create versus Create and activate intent, human alignment/Purpose/initial-publication approvals and succession state, backed by actual C3/C6/C8/C9 producers. Both launchers resume the same paid first-turn/result rather than starting another from inspection/reconnect.
- Authenticated ID-bound real terminal attachment to the existing colony/agent/session/context. Prefer a qualified complete off-the-shelf native embedded terminal within D92's approximately 5–10% overall-project integration/adaptation allowance; otherwise use the user-selected default external terminal. No bespoke emulator/render/history, secret arguments or duplicate execution loop; exact attachment syntax and native qualification remain work.
- Staged D97–D102 outage additions (Core §13.11): authorized snapshots and events for outages, each agent's service-loss state and cause, and Corps or colony status with paused members and reserved questions filtered for the inspecting principal; the grouped outage prompt, owner stand-in commands, the unrecoverable declaration and the restoration-before-cutover choice, each resolved once across views and with no model call.

| Definition | Meaning |
|---|---|
| Colony host | The one core host process of an active colony, the sole writer of its ledger (D26) |
| View | A launcher attached to the host, with its own cursor. It never builds prompts, chooses models, executes tools or changes permission state (core §4.2) |
| Attach | Opening a view of an existing session, after the protocol versions are checked (D39) |

**Waits for.** C3 Done and C1's frozen ledger reader/appender contract; owner D40 `launcher-protocol` freeze precedes GUI implementation. C7 **base** does not wait for C6, C8, C9 or GUI. Later social/lifecycle/interview/presentation extensions require their actual C3/C6/C8/C9/helper producers, specified bounded persistence/encoding, C1 catalogue support and corresponding owner freezes; this staging adds no reverse GUI dependency.

**Stop point.** Do not implement before C3 and C1's required producer-ready interfaces exist. The host is a detached Core service; closing its terminal or GUI view does not stop the colony. Stop GUI-specific parity work until C7's protocol contract is owner-frozen; GUI is a separate client.

**Unblocks.** GUI-A/B host/cursor work after C7 completion/protocol freeze; native GUI retains selected Fyne v2.8.1. C9 backend → GUI-E parity → whole C9 remains independent. GUI-F additionally consumes actual C3 task/eligibility, C6 social/evidence, C8 lifecycle and C9 backend alignment capabilities plus staged C7 additions; C9 parity alone does not prove that slice. Applicable R5/C10 external-helper proof is not a blanket prerequisite for opening GUI.

**Decided.**

| Decided | Source |
|---|---|
| One core host process per active colony, the sole writer of that colony's ledger | D26 |
| `dwarf-harness` is the host plus the terminal, built without cgo. `dwarf-harness-GUI` is a display client: it starts `dwarf-harness` as host if none runs, holds no colony logic or API keys, and checks protocol versions when it connects. A GUI crash cannot stop a turn | D39 |
| `launcher-protocol` is a contract owned by this step | D40; `CONTRACTS.md` |
| Both launchers have the same functionality and may show one live session at once | D5 |
| Durable view cursors follow the colony ledger sequence; live model text is ephemeral, per-turn, and attached through snapshot/high-water index plus later indexed deltas | D49; core §4 |

**Prerequisites, open producer work and implementation proof.**

| Prerequisite or gate | What remains |
|---|---|
| C3/C1 | C3 Done and C1's producer-ready reader/appender contract; one host holds the colony writer lock and alone mutates authoritative state |
| Owner freeze | Owner D40 approval/freeze of exact `launcher-protocol` schemas before GUI integration; `go-winio` requires separate D41 approval if used |
| Staged presentation persistence | Specify/freeze Core-owned replaceable/bounded current-state snapshots/checkpoints, finite receipt expiry and atomic layout/TODO recovery under existing storage discipline. Persist exact choices/drafts/review baseline; discard bounded per-run undo on close, not operational history. No GUI writer, generic unvalidated blob or immortal per-edit ledger |
| Later inspection/lifecycle projections | Stage actual C3/C6/C8/C9/helper producers and owner-frozen schemas; administrator inspection is not agent evidence authorization. Separate memory directions, grid/partial/stale labels, delivered history, telemetry and topology/review/event cursors. Base C7 has no C6/C8/C9/GUI dependency |
| Shared interview/approval state | Durable Purpose/pin/run/turn and human decision bindings, versioned Creed/mythology drafts and recorded creation intent; restart, reattach and two-view races cannot duplicate first-turn spending, approve stale content or create ordinary-work eligibility |
| Real terminal attach | OS-authenticated existing colony/agent/session/context binding, no secret-bearing/name-built shell command, explicit failure/manual attach and detach without cancelling work. D92 reuse-first native qualification and effort allowance or user-selected external fallback; historical candidate research is not runtime/scrollback proof |
| Outage projections | Stage the actual C3 service-loss, R1 grouped-prompt and C8 Corps-operation, stand-in and handover producers. Filter question text and counts per principal (the owner as administrator sees all); stand-in and choice commands are typed, idempotent and revision-checked; viewing or reconnecting makes no model call |
| Host lifecycle | Authenticated attach, detached start/readiness, single-instance lock, recovery after stale discovery, last-view disconnect and explicit shutdown; discovery file is advisory only |
| Durable events | Independent per-view `through_seq`, authorized snapshot+high-water recovery on gaps, then later events. Event cursors never acknowledge task/message completion |
| Live text | Per-turn ephemeral snapshot/high-water plus later indexed deltas; no persistence/replay after host exit. Do not mix it with durable event cursors |
| Command authority | Authenticate/authorize each versioned command, preserve idempotency across retransmission, and serialize only conflicting per-context/Core records; independent contexts can proceed concurrently |
| GUI handoff | Base C7/frozen protocol enables GUI-A/B. C9 backend → GUI-E parity → whole C9 remains; C8 separately waits for backend, not parity. GUI-F consumes real C3/C6/C8/C9 capabilities and corresponding frozen C7 extensions; no reverse GUI dependency |
| Failure proof | Two-view races/reconnect, sequence gaps, malformed/incompatible frames, unauthorized identities, host/view crashes and shutdown; do not claim implementation evidence here |

**Exit criterion.** One turn appears in two attached views without duplicate execution. Competing commands from two views cause one execution. A view reconnecting to a live core catches durable events up from its colony-sequence cursor and receives the current live-text snapshot plus later deltas; text is not replayed after core exit. A version mismatch is refused on attach.

**D89–D96 staged producer acceptance.** Before corresponding GUI-F handoff, exercise actual Core/C7 commands, not a pure state model. Save partial current layout/appearance/draft/TODO and topology disposition, close/restart and recover them while prior-run undo/history is gone. Crash before commit and after commit before reply; within the defined receipt lifetime retries recover one atomic result. After expiry reject/reconcile stale commands without reapplying them or relying on permanent per-edit history. Reject stale revisions, failed storage, unauthorized bindings and unsupported schemas without resetting state or marking failed saves complete. Retained operational history must be unchanged.

Reconcile startup/live terminal-created topology even with manual planning off, including stream gaps and rename/reassignment/retirement; preserve review baseline separately from pending work, with no duplicated births, paid work or placements. Inspect both relationship perspectives as administrator without widening agent evidence or exposing credentials. Two native/terminal views must resume one versioned interview/approval and reject stale answers; reconnect cannot repeat a paid first turn or activate a Create-only team. Cosmetic saves/inspection must leave execution, delivery and field state unchanged and cost no model calls.

Attach the actual terminal to the selected existing Core context and observe one shared session; detach without stopping Core. Qualify the reusable embedded native candidate, including scrollback and bounded adaptation effort, or exercise the user-selected external fallback—historical research alone passes neither path. These later producer gates do not make base C7 wait for C6/C8/C9 or GUI; rendering/art/accessibility remain native GUI gates.

**Status.** Waiting.

### C8. Corps, Grand chains, clones, birth, Griot publication

**Goal.** Core §9 phase 3: specialized Corps and Grand chains, shared Purpose, reviewed births/clones, human-approved initial leadership alignment, Corp Creed/mythology and safe officer succession.

**Delivers.**

- Corps with exactly one Orchestrator, Griot and Diplomat, plus workers; Grand role chains retain their authorized scope. One mutable shared Corps Purpose with role mandates, and a Colony Purpose for Grand leadership, never independently editable agent Purposes.
- Cloning (core §7.6): new identity/directory/session and independent contexts, copies source `Agent.md`, and starts with fresh private memory. Only a chosen manifest can disclose already-shareable lessons/artifacts; no provider continuation, cache, inbox, grants or writable source files cross over (D85).
- Immutable content-addressed `BirthProposal` binds the exact scratch/clone batch, roles/destinations, selected aspects/shareable refs, Purpose/mandates, Create or Create and activate intent and requested scope/grants/budgets. Reversible drafts do not create agents or spend on inspection. New Corps establish Purpose, complete leadership triad, then add workers; existing Corps may receive one/multiple additions. Recheck role occupancy and authority at final reviewed creation; changed content invalidates approval. Agent-initiated birth retains its current-superior exact-manifest approval and Grand owner-only boundary; GUI one-way doors require explicit final review, unlike cosmetic boundary extension.
- **Mandatory first action:** every new Orchestrator/Griot, including Grand and agent-born counterparts, executes actual pinned C9 backend `grill-with-docs` against shared Purpose in its work/values domain. Team onboarding orders Orchestrator then Griot. Both creation choices start the first admitted interview immediately after an advance paid-work warning. Ordinary grants/budgets/admission still apply; durable progress prevents duplicate first turns or unattended rounds on waiting/restart/reattach.
- Human acceptance is mandatory for initial alignment; superior-agent approval cannot substitute. A new Corps waits for both leadership acceptances before ordinary work; alignment/inspection remain usable and messages confer no bypass. Added workers do not restart an aligned Corps interview. Honor recorded intent afterward: Create stays ready/inactive; Create and activate proceeds if actual gates permit, without another mandatory Activate step.
- Human-controlled completion may retain open questions; guide stopping when questions no longer matter, not exhausting a tree or trusting a model completion claim. Stage Purpose revisions while the prior approved revision governs work, then cut over after the human decision. Meaningful work/value changes target Orchestrator/Griot re-alignment; corrections do not force an interview. Humans may skip/shorten/disable re-alignment only, never initial new-leader alignment.
- Orchestrator alignment produces a versioned **Corp Creed** with Purpose, mandates and resolved vocabulary/decisions for affected agents; Griot alignment produces distinct reviewable mythology. Keep approved shared artifacts, administrator-inspectable private raw interviews and separately reviewed project exports separate; no automatic repository documentation writes.
- Grand Griot authors loose, possibly fictional prehistory, asks/suggests present/future Corps and may explain later arrival. Corps Griots use the approved shared source/version plus their Purpose and may reinterpret heroes/events. Initial publication of each Grand/Corps founding story requires human approval; draft mythology does not block alignment/activation and later stories gain no blanket approval gate. Existing authorized Griot channels/publication audiences and append-only provenance remain; fiction is not actual birth, authority or achievement. Retain provisional AC15 bounded dirty-gated later updates, normal budgets and no empty turn.
- Prepared/aligned, non-operational **Aspirant** remains outside the **Incumbent** officer slot. Use explicit recipient-authorized process/lesson/relationship-roster handoff, deterministic normalized attenuation toward zero with trust retained more strongly than sentiment, source/direction and unknown preservation. Exact formula/coefficients/encoding/cutpoints remain open pending testing. Exactly one holder persists through reviewed Core cutover; no occupied slot is vacated without a ready successor. Retire predecessor only after successful handover and preserve D83 records/private memories, not an automatic adviser.
- Ordinary service loss (Core §13.11, D97–D102). While an officer is lost its Corps keeps working: covered members and its superior get passive notices, further questions become reserved questions counted against an allotment of three (configurable per colony), members at the allotment pause while paused officers still serve their own Corps and the officers they lead, work stays within existing tasks plus capped task proposals, and the owner may stand in under his own label. Restoration sends the Service Restoration message and releases members in step. Incumbent or Aspirant service loss pauses a succession at its last saved step; the owner may declare an officer unrecoverable, and a prepared, aligned Aspirant then takes over without the live handoff, using a handoff Core builds from records. At every succession, unanswered messages, pending decisions, task proposals and open tasks pass to the new holder. No emergency GUI bypass, timeout promotion or fabricated handover; readiness proof and atomic cutover remain producer work.
- Explicit lesson handoffs follow permitted paths. Unusual-mood notification informs owner without ordinary approval or blanket inspection; only a specifically authorized, evidence-bound interrupt can cancel that work, never a submitted promotion.

**Waits for.** C6 Done, C3 runtime guard/continuation capabilities and the actual **C9 backend pinned first-action alignment capability**, plus owner-approved D40 freeze of the relevant C8 creation/Purpose/alignment/role/handoff/publication families. C8 does not wait for whole C9 or GUI parity. Officer outage handling and succession acceptance additionally require C3's actual service-loss capability (Core §13.11); its absence is not a GUI workaround.

**Stop point.** Remain Waiting on those real capabilities and freezes. Core §§7.6/12.4/13.6/13.11 settle the owner semantics, not the exact attenuation, readiness/cutover or record encodings. Specify and test them before their affected freeze/implementation gate. A named skill, fake alignment fixture or superior model's approval is not the actual backend/human-alignment prerequisite.

**Unblocks.** C7's corresponding lifecycle/interview extensions and GUI-F after their other actual producers and owner-frozen interfaces; never C3, base C7 or the C9 backend.

**Decided.**

| Decided | Source |
|---|---|
| Cloning copies `Agent.md`, with fresh private memory except explicitly authorized shareable lesson/artifact refs | D8 as amended by D85 |
| Birth retains exact-manifest superior approval and owner apex; human initial-alignment acceptance is separate, including agent-born leaders | D10, D84–D85, D91–D93 |
| Exactly one Corps Orchestrator/Griot/Diplomat; shared Purpose and Colony Purpose, with communication paths in Core §7.2 | D11, D92 |
| Grand roles coordinate their respective chains only; no blanket descendant access | D12, D84 |
| Corps specialize within a Colony | D13 |
| The Griot guides workers only through story output on the Corps board | Owner requirement; core §7.5 |
| `Agent.md`, spelled exactly | D32 |
| Initial pinned alignment, warned immediate first turn, human-ended completion and recorded creation intent; optional re-alignment only | D91–D93 |
| Versioned Corp Creed, distinct mythology, Grand prehistory/Corps reinterpretation and human-approved initial publication without an activation gate | D93–D94 |
| Non-operational Aspirant, scoped attenuated handoff, one officer holder, retirement after successful handover and ordinary-recovery dependency; signed trust with exact implementation open | D93–D95 |
| Ordinary service loss and officer recovery: the Corps keeps working, reserved questions and allotment pauses, outage work rules, owner stand-in, Service Restoration message with in-step release, unrecoverable officers and office handover | D97–D102 |

**Prerequisites, open producer work and implementation proof.**

| Prerequisite or gate | What remains |
|---|---|
| Capabilities and event families | C6, C3 runtime guards and actual C9 backend pinned alignment; D40 owner freeze of real C8 lifecycle/Purpose/alignment/Creed/handoff/publication records. Neither whole C9 nor GUI parity is a prerequisite |
| Role authority and cardinality | Chain-limited Grand grants and owner apex; exactly one holder per Corps officer role, no silent duplicate leadership clone, reviewed readiness/cutover with Aspirant outside the slot |
| Reviewed creation and intent | Exact batch/hash, current parent/role/authority/destination validation, superior/Grand birth approvals and GUI final review; changed drafts invalidate approval. Persist Create versus Create and activate without spending or birth from inspection |
| Alignment and Purpose | Specify durable identity/Purpose/pin/run/turn/human-decision bindings; enforce first-action and whole-new-Corps work gates, warned first-turn admission, human completion and staged revisions. Optional re-alignment cannot waive initial acceptance or budgets |
| Memory and handoff | D85 fresh memory/shareable manifest plus D93–D95 explicit recipient-scoped transformed process/lesson/roster handoff. No raw private context, counterpart memory, grants or provider state. Test exact deterministic attenuation after choosing it; no adopted linear coefficients |
| Officer recovery and retirement | Implement Core §13.11's Corps operation, outage work rules, owner stand-in, unrecoverable declaration and office handover on C3's service-loss state; prove readiness and atomic role cutover/replay without vacant or duplicate holders. Retire only after successful handover; service loss alone never promotes or completes it |
| Clone and repair lineage | New identity/context; preserve clone ancestry so C5 excludes only repairing context plus clone descendants (D86) |
| Creed and Griot | Versioned shared Creed versus private interviews/reviewed exports; authorized approved Grand-source lineage and Corps Purpose reinterpretation. Human approval before initial founding publication, no additional activation gate; bounded budgeted later cadence remains provisional |
| Strange mood | Owner notification without ordinary approval; exact scoped issue, evidence, deadline/impact and alternatives for any authorized interruption; no unrelated context inspection or submitted-promotion cancellation |
| Replay/privacy | Preserve operational birth/alignment/Purpose/publication/succession history and original paid-turn identities through restart/two-view races; D83 agent history and administrator inspection stay distinct. No successor-history, inbox, provider-state or automatic document-export leakage |

**Exit criterion.** Core §9 phase 3's real lifecycle and social producers satisfy:

- A scratch/clone batch creates exactly the reviewed identities/roles once; stale approvals and occupied leadership clones are rejected, private state is fresh, and agent-initiated birth cannot bypass its superior.
- GUI-, terminal- and agent-born Orchestrators/Griots, including Grand counterparts, actually execute the pinned first-action skill. An authorized warned Create-only first turn leaves questions waiting; restart/reattach/two-view inspection duplicates neither turn nor spending. Refused grants/admission never bypass alignment.
- Only the human accepts initial alignment, even with questions remaining. Both leaders gate ordinary new-Corps work; messages cannot bypass it, added workers do not restart it, and post-acceptance behavior distinguishes inactive Create from gated Create and activate without extra activation.
- Prior approved Purpose continues during a staged revision; human cutover and optional domain-specific re-alignment work without waiving new-leader alignment. Creed, private interview, mythology and export audiences remain distinct, with no automatic repository writes.
- Initial Grand/Corps founding publication refuses an unapproved version, records authorized source/version provenance on approval, and never creates narrative-only Corps or achievement. Unpublished mythology does not block aligned work; later ordinary story publication and worker/Griot board rules retain their existing boundaries.
- A scoped handoff delivers only permitted process/lesson/roster evidence, with tested deterministic normalized attenuation preserving unknown/source/direction. Reviewed cutover maintains one holder and retires the predecessor only after success. Incumbent or Aspirant service loss pauses the succession at its last saved step until restoration, an unrecoverable declaration lets a prepared Aspirant take over without the live handoff, and nothing takes over on timeout. Unanswered messages, pending decisions, task proposals and open tasks pass to the new holder. Replay preserves predecessor records/private memories and does not repeat handover effects.
- During an actual outage, covered members receive passive notices, reserved questions count against the allotment and pause members at it while paused officers still serve their dependents, outage work stays within existing tasks and capped proposals, owner stand-in acts are labelled and count as answers, and restoration sends the Service Restoration message and releases members in step, with no duplicate messages, notices or spending across restart.

These are unrun acceptance obligations. The exact attenuation and readiness/cutover implementation must be specified before the corresponding cases can pass; D96–D102 do not supply that evidence.

**Status.** Waiting.

### C9. Recipes, workflows and skills

**Goal.** Core §9 phase 4: independently implemented recipes, workflows and skills.

**Delivers.** Core backend for typed finite `WorkflowSpec` v1/`WorkflowRun`, bounded JSON recipes with typed host acceptance, and shared-format skills with lazy loading/version-checked edits. It additionally supplies the actual **pinned `grill-with-docs` first-action execution capability** consumed by C8 (Core §12.4): stable skill/version/content binding, durable run/turn/progress, human-question yielding and no automatic paid replay. C3 provides generic guards; C8 binds lifecycle/Purpose/intent/human approval later, so backend does not depend on C8/GUI. Every workflow edge uses the real role graph; no scripting, shell interpolation, generic plugin authority or archive catalogue. Recipe acceptance remains the **[PROPOSAL]** for pinned C4/C5 lineage in validation §4.7, subject to owner freeze.

**Waits for.** Backend waits for C4, C5 and C6, not C8 or GUI. C8 initial alignment waits on that actual backend capability. GUI-E parity additionally waits for C7/frozen `launcher-protocol`, Fyne v2.8.1 and the backend; whole C9 waits for parity. Preserve `C9 backend → GUI parity → whole C9` alongside `C9 backend → C8`, with no reverse C3/C7/GUI dependency.

**Stop point.** Do not implement before C4/C5/C6 and owner D40 freeze of the real C9 record families, including the pinned skill/run capability. Exact pin/progress/continuation encoding still needs producer specification; a document reference or optional slash command is not executable proof. The archived `recipes/` folder remains out of scope.

**Unblocks.** Actual backend capability unblocks C8 first-action alignment and GUI-E parity after their other dependencies; GUI parity completes the remaining whole-C9 gate. C8/GUI-F still require real lifecycle/social/presentation producers, not a generic skill-name claim.

**Decided.**

| Decided | Source |
|---|---|
| Capabilities, workflow primitives, recipes and skills as specified in core §11 and §12, carried forward from the three early-design sections the owner authorized | Core §11, §12 |
| Skill edits are written by the core with a version check | D22, safeguard 1 |
| Pinned `grill-with-docs` is a Core first-action prerequisite for every new Orchestrator/Griot, not optional skill advice; human completion and budgeted continuation remain mandatory | D91–D93 |

**Prerequisites, open producer work and implementation proof.**

| Prerequisite or gate | What remains |
|---|---|
| Core backend | C4, C5 and C6 Done; owner D40 freeze for exact workflow/recipe/skill event families |
| Typed acceptance | Bounded file existence/content, configured required-check reference or typed connector readback. Pin the exact subject/recipe/config/evidence; unsupported or unavailable is never pass. C4/C5 lineage/retirement gates remain real |
| Workflow schema | Versioned content-addressed JSON, exact node variants, finite bounds and mandatory escape for capped loops; reject unauthorized edges and unbounded cycles |
| Pinned alignment capability | Specify actual version/content pin, Purpose/domain input and durable run/turn/progress identity; ordinary discovery/edits cannot silently replace it. Yield at human questions; neither capped-loop exhaustion nor model verdict grants alignment |
| Replay and effects | Durable node/run/turn completion keys and receipts, idempotent typed transitions and no blind repeat of uncertain effects. Waiting joins/interviews release the context; inspection/restart/reattach cannot duplicate first-turn dispatch or create unattended rounds |
| GUI parity | C7 host completion and frozen launcher protocol, Fyne v2.8.1, and C9 backend. Same workflow model and Core execution path as terminal |
| Evidence | Test finite limits, stale specs/results, forbidden routes, capability escalation, busy/unavailable check references, replay and bounded output; no implementation/gate result claimed here |

**Exit criterion.** Two gates. **Backend/core-and-terminal:** selected workflows execute against actual Core/broker authority, acceptance uses pinned checks, skill precedence and finite limits hold without an inspirational runtime. Execute the actual pinned alignment skill with ordinary grants/reservations, persist a human-question wait and resume explicit answers; opening another view, restart or a same-name skill edit must not duplicate a first turn or replace the pin. Neither model verdict nor loop exhaustion marks alignment accepted. This proves the reusable backend capability; C8 separately proves its lifecycle/whole-Corps/human-approval binding. **GUI-E parity**, owned by the GUI roadmap, selects/edits/runs the same Core workflow. Whole C9 is not Done until that parity gate passes, but C8 need not wait for it.

**Status.** Waiting.

### C10. Isolation boundary and assurance A2

**Goal.** Close core §10 item 8: an enforceable boundary protecting canonical refs, shared git metadata, the ledger and private files from same-user child processes. It raises validation assurance to A2. Under the V6 starting configuration (O4), affordance requires A2 (validation §4.9.1).

**Delivers.** Implement and measure the selected A2 boundary, not an architecture label: Windows per-run AppContainer plus non-breakaway Job Object; Linux namespaces/Landlock/`no_new_privs`/capability drop plus positive process-tree closure; Apple-silicon native-macOS tools in the D87 single disposable VM slot; separately budgeted Linux guest where required, including Intel Mac. A2 includes A1 plus proven filesystem/process restriction under the stated threat model.

**Waits for.** C2 canonical workspace/ref boundary and C3's Core-owned child/execution interface; implementation can begin only with owner-frozen relevant C1/B11 contracts.

**Stop point.** Do not use an A2 label or expose agent affordance before positive access-control, escape-denial and descendant/VM-stop evidence across the declared platforms. C10 does not claim protection from a compromised OS/kernel or unrestricted administrator/owner.

**Unblocks.** U2's A2 measurements; U7, only if the V6 floor needs A2; and any agent-visible affordance, which requires A2 under the V6 starting configuration (`ROADMAPS.md`, notes on the edges).

**Decided.**

| Decided | Source |
|---|---|
| Windows first: this step needs Windows mechanisms, not only the Linux ones the earlier roadmap listed | D27 |
| The core host is the sole writer of the colony's ledger | D26 |
| Affordance requires A2 in the V6 starting configuration | V6 start; validation §4.9.1 |
| One core-owned appender serializes ledger appends and assigns the colony sequence; only core, router and `internal/undercurrent/graph` use its contract | D50 |

**Prerequisites and implementation proof—not open choices.**

| Prerequisite or gate | What remains |
|---|---|
| Threat model | Protect canonical refs/common `.git`, ledger/body/private contexts, credentials, task/grant/budget/approval state, sealed results and owner IPC from hostile tools and every descendant; OS/kernel, unrestricted owner/admin and side channels remain trusted/out of scope |
| Windows | Per-run AppContainer profile/SID for access/network restriction plus non-breakaway Job Object for lifetime/resources. Start suspended with explicit handle allowlist, apply restrictions before resume, deny network by default and prove positive process-tree exit. Neither mechanism alone suffices |
| Linux | Private user/mount/PID/IPC/network namespaces, Landlock, capability drop, `no_new_privs`, narrow trusted init and cgroup/descendant closure. Probe required ABI/features and fail isolation closed when absent |
| Apple-silicon macOS (D87) | Exactly one demand-started disposable native macOS VM slot; fresh disk/aux state per protection domain, bounded copied regular-file input/output, dedicated non-admin tool UID, no host stores/credentials/`.git`/home/device/default NIC, and positive `VZVirtualMachine.stop` completion before reuse. macOS 13+ shared-directory route; Intel Mac has Core/GUI plus Linux guest only |
| Linux checks | Separately budget Linux guest on Intel or Apple Silicon, with per-run namespace/Landlock restrictions; guest OS/kernel/image/toolchain are part of evidence, not a substitute for native macOS |
| Helper and supervisor | One Core authority/ledger/provider; narrow signed native macOS helper is an auxiliary executor only. Core-channel loss cancels, fences boot nonce, stops guest/VM and never auto-reexecutes. No helper/guest holds owner credentials or a second task/accounting database |
| Disclosure | Keep speculative input and every output, path, hash, log, report and receipt host-private until exact predecessor is durably accepted (BD2); no guest transport, workspace, common `.git`, host home, writable cross-run cache or agent-observable egress |
| Positive controls and hostile attempts | Show authorized outputs work while writes to canonical/shared metadata/private peer state, owner IPC, network (disabled profile) and inherited handles fail; kill children/helper/Core/guest, race cancellation, and prove no slot/root reuse before stop |
| Performance and platform support | Measure guest/helper CPU/RAM/disk/start/stop and accepted-throughput under Defender, contention and D78 workloads. VM costs and native helper build/signing are unmeasured; one macOS slot is not a throughput promise |

**Exit criterion.** With real hostile-process drills on supported Windows, Linux and Apple-silicon macOS paths, demonstrate positive controls and denials against canonical refs, common metadata, ledger, private peer files, credentials, owner IPC and unauthorized network. On cancellation, timeout, crash or Core/helper loss prove every descendant/VM is stopped or keep its resources quarantined; no ambiguous run may publish, reuse a slot or restart automatically. Record actual costs and platform limits before any A2 claim.

**Status.** Waiting.

## 3. Remaining prerequisites and evidence

Remaining prerequisites, explicit pre-alpha producer work and evidence are listed by step. D96 authorizes consolidation, not exhaustion of the design space or advancement of any status.

| Step | Prerequisites and unproven gates |
|---|---|
| C0 | Linux and macOS native runs wait for C3's CI, and the vulnerability database is online-only (B17) |
| C1 | The retention rules are recorded: explicit collection over reachability, active pins and age, with `ledger.bodies_collected` records of unavailable bodies (B21). Workload measurements on one Windows NTFS machine are in Core Appendix A.10; budgets and replay triggers remain the owner's to set. Linux and macOS platform flush/lock/rename proof remains for C3's CI (B18). Catalogue C7 presentation records only after its bounded persistence/retry mechanism is specified; no permanent cosmetic edit log |
| C2 | Full-tree capture and D74–D78 boundary proof; every-change workload/capacity evidence and real interruption/recovery |
| C3 | C2 and remote/CI, credentials, adapter/runtime/replay, generic eligibility guards/task facts, the Core §13.11 service-loss state and context isolation. No backward R1/C8/C9/GUI dependency; workload/quiescence proof remains |
| C4 | C3; real full runner, finite V6/V2 evidence and canaries, BD2 barrier, activation/recovery and D77 ordering remain unproven. Exercise the bounded `K=4`/`Q=4N` admissions separately from `P=4` validation lanes against B15's 12/20-pair bursts and the actual larger workload (D78) |
| C5 | C4/U1 law; replayable owner verdict correction/quarantine/retirement; C6 separately proves Corps-authorized V3 and Orchestrator overturn |
| C6 | C3 and C5's actual authority gate; broker/control reserves/D83 history with distinct administrator inspection. Specify/test perspective rubric, partial/stale refresh, numeric bindings and recipient-scoped deterministic handoff evidence; no implicit federation or private-memory inheritance |
| C7 | Base C3/C1/frozen launcher protocol and real IPC/reconnect/two-view proof; staged actual C3/C6/C8/C9/helper extensions. Specify bounded presentation snapshots/checkpoints, atomic recovery and finite retry expiry; shared interview/pin/progress, no duplicate spending; qualify terminal reuse or external fallback; stage the §13.11 outage projections and commands |
| C8 | C6 + C3 runtime + actual C9 backend pinned alignment, not whole C9/GUI; frozen lifecycle/Purpose/Creed/publication/handoff families. Specify/test attenuation, readiness/atomic cutover, the §13.11 outage operation, stand-in and office handover, post-success retirement and approval/version/privacy replay |
| C9 | C4/C5/C6 and owner-frozen workflow/recipe/skill/pinned-run families; actual first-action backend capability then feeds C8 and GUI-E parity. C7/Fyne/backend GUI parity is still required for whole C9 |
| C10 | C2/C3 and OS/helper interfaces; positive hostile-process, VM/descendant closure and no-reuse proofs; measured D78 cost with Defender enabled |

Question IDs beginning with Q are in `DESIGN-UNDERCURRENT.md` §17, and IDs beginning with V in `RESEARCH-ADMISSIBLE-VALIDATION.md` §8. Routing's remaining prerequisites are in `DESIGN-ROUTING-LAYER.md` §15; former Core §10 mechanism lists are superseded by Core §13.
