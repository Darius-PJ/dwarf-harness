# Core roadmap

**Status:** 2026-10-02. No code exists yet. This roadmap orders the core work in steps C0 to C10. It does not approve designs that the source documents mark as proposals. Each step's status here matches the status board in `ROADMAPS.md`; change both in the same commit.

## Start here

Read `ROADMAPS.md` first. It holds the step format, the dependency graph across roadmaps, the status board and the rules for several agents working at once (D41). `DECISIONS.md` holds every decision cited here by ID, and the open items awaiting the owner. `CONTRACTS.md` holds the contracts between roadmaps: C1 owns `ledger-format` and `ledger-reader`, C7 owns `launcher-protocol`, and C3 waits for R1's `context-module`. Then read your step in §2 and the design sections it cites. An agent that cannot continue stops at its step's stop point and sets the status to *Blocked: reason*, here and in `ROADMAPS.md` (rule 5).

**Authority.** Entries in `DECISIONS.md` are settled. Everything marked **[PROPOSAL]** or **[INFERENCE]**, here or in a design document, is a recommendation the build may revise, provided the revision is recorded as a B entry in `DECISIONS.md` (`ROADMAPS.md`, rule 6). Anything a document marks as awaiting the owner goes back to the owner.

**Working principles for every step**, kept from the earlier single roadmap. Write the test before the code. Never let a component read the wall clock inside anything that must be replayable. Never let a component trust an agent's report where a host observation is possible. Add a dependency only under D29.

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
| B1 | Line endings | C0 (in place); C2 for the agents' repositories |
| V3 | Check retirement authority | C5 |
| V6 start | O4 plus canaries; sample re-runs off by default | C4 |
| R11 | Router measurements reach the field layer only as ledger events | C1 defines the event types; R1 writes them |

## 2. Steps

Every step uses the format in `ROADMAPS.md`. Definitions tables are kept from the earlier roadmap where it had them. §3 indexes the open questions.

### C0. Repository, toolchain, check command, decision log

**Goal.** A Go module whose structure mirrors the layer boundaries in the designs, so that the separations the documents depend on (core, router, Undercurrent, launchers) are enforced by package imports from the first commit. The step also gives every later step its toolchain and the one check command that gates integration.

**Delivers.**

- `go.mod` with module path `github.com/Darius-PJ/dwarf-harness` (D43), `go 1.27` and `toolchain go1.27.1` (D28, D45). Its `tool` lines pin staticcheck 2026.2.1 and govulncheck (D42).
- The package layout in D45: `cmd/dwarf-harness/`, `contract/{ledger,launcher,module,law}/`, `internal/{core,router,undercurrent}/`, `tools/check/`, `docs/`. C0 creates the directories that have content at C0 (`cmd/dwarf-harness`, `tools/check`); every other package is created by the step that owns it (D41: no placeholders). The nested `gui/` module and `go.work` arrive with the GUI roadmap (D44).
- The import-boundary test, which runs `go list -deps` for every package and fails on any import outside D45's table.
- `tools/check`, run as `go run ./tools/check` (D42): gofmt, `go vet`, staticcheck and govulncheck; the import-boundary test; tests with `CGO_ENABLED=0`; the tests again with `-race`, with `CC` set explicitly to the pinned compiler (D28); Linux and macOS builds with `CGO_ENABLED=0`.
- Test conventions (D42): `testing/synctest`, Go's native fuzzing, rapid v1.3.0 as a test-only dependency, and golden samples for contracts. D42 records staticcheck, govulncheck and rapid under D29.
- `CLAUDE.md`, written following `skill://writing-for-agents`. It holds the reading order from `ROADMAPS.md`; the working rules in `ROADMAPS.md` (D41, D42, D46); the working principles at the top of this roadmap; D45's layout and import table; D29's rule on when to write code ourselves and when to use a recorded library; and the commit rules (Conventional Commits with the package as the scope; commit on your own branch when the tests you touched pass; the full check passes before `main` moves; never push without the owner).
- The toolchain on the owner's machine (D28): go1.27.1 from the official installer, and llvm-mingw 20260922 (clang 23.1.2, UCRT, x86_64 host), pinned and installed outside `PATH`.
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
| Go version and minimum toolchain: go1.27.1 from the official installer; `go 1.27` in `go.mod` | D28 |
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

**Open questions.**

| Question | Notes |
|---|---|
| How the check finds the compiler | D28 keeps llvm-mingw off `PATH`, and the check sets `CC` explicitly. Where the check learns the compiler's location is not decided |
| Race-detector compiler in Linux and macOS CI | D28 names only the Windows compiler. Go's race detector needs an installed C compiler on systems other than macOS (core Appendix A.9). Which compiler the Linux runner uses for `-race`, and whether the macOS runner needs one |
| govulncheck without a network | [INFERENCE] govulncheck reads the Go vulnerability database over the network. Whether the check may need a network, or uses a local copy of the database |

**Exit criterion.** The module builds with `CGO_ENABLED=0`. `go run ./tools/check` passes on Windows, including the `-race` tests. The import-boundary test exists, passes, and fails on a planted forbidden import. `CLAUDE.md` is present. `DECISIONS.md` contains every decision in §1, which has been true since 2026-10-02.

**Status.** Ready.

### C1. Action ledger and body store

**Goal.** The single source of world facts (D15). Every later component either writes typed events here or reads them. This is the step whose schema mistakes are most expensive, because Undercurrent replays from it. Its two contracts are the first handoff to another roadmap: once the owner freezes them, U1 may start (D20).

**Delivers.** An append-only ledger with typed events, a reader with a cursor, a content-addressed store for large bodies, schema versioning, and the field contract from Undercurrent §15.1 represented in the event types. In detail:

- The colony log as D34 to D37 specify: segments, commits, recovery of a half-written final commit, the hash chain, the gapless sequence, and lookup indexes rebuilt in memory at startup.
- The body store under `bodies/`, keyed by SHA-256 (D36, D38).
- The colony directory with `colony.json` and the single-writer `lock` (D38; core §3.1), so that a second core cannot write the colony's ledger (D26).
- Drafts of the `ledger-format` and `ledger-reader` contracts (`CONTRACTS.md`): standard-library-only packages with golden samples for every type version (D40). The step ends with a request to the owner to freeze both.
- A synthetic ledger generator covering every event in catalogue v1: every Undercurrent §15.1 event as that section stands at freeze, every routing R11 event, and D23's parent-scratch and authoring-context fields.
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
| Cursor | A reader's position in the sequence; launchers, the router's consumers and Undercurrent each hold their own |

**Waits for.** C0.

**Stop point.** Do not start before C0 is Done. C1 ends with the freeze request; it does not wait for the owner to freeze the contracts. Other roadmaps build against them only once they are frozen (D40).

**Unblocks.** C2. U1, once the owner freezes both contracts: that freeze is the D20 handoff, from which U1, and after it the field-level part of U2, may start.

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
| Retention: the log is never deleted or compacted, though old segments may be compressed; bodies may be garbage-collected, so replay needs only the records. This resolves Undercurrent Q16 | D36 |
| IDs: UUIDv7 from Go 1.27's standard library; names never grant authority. A time held back after the clock moved backwards is flagged; durations use the monotonic clock | D37 |
| One ledger per colony; every event carries the colony's globally unique ID | D26 |
| Location: a default root per OS with a per-colony override; `colony.json`, `lock`, `log/` and `bodies/` | D38 |
| Contracts: standard-library-only packages with golden samples; a draft changes freely; the owner freezes | D40 |
| Scratch events carry parent-scratch and authoring-context fields | D23 |

**Open questions.**

| Question | Notes |
|---|---|
| Segment size and rotation | When a segment closes and the next begins |
| Flush semantics per OS | What the flush after each write (D34) guarantees and costs on Windows, Linux and macOS |
| Lock mechanism per OS | How `lock` (D38) keeps out a second core, and how a stale lock is recovered after a crash |
| Body garbage-collection policy | The operator policy D36 allows; replay never needs a body |
| Index rebuild budget and snapshot trigger | D34 adds snapshots only if startup replay is too slow, and a SQLite index only when replay time or index memory exceeds a budget. The budgets are not set |
| Write throughput target | Agent turns occur at human-scale rates; measure before optimizing. Core Appendix A.8 measured 0.52 ms for one durable single-event commit of this design on the owner's machine |
| Event catalogue v1 | v1 covers Undercurrent §15.1 as it stands at freeze, and routing R11. §15.1 has three **[PROPOSAL]** rows (a graph delta, a law version taking effect, a post-promotion interference check result); if they are not ready at freeze, they join later by addition (D40). Open: which of the other types D33 names (turns, approvals, reservations, messages) are in v1, and which later steps add |
| View cursors (`ledger-reader`) | Core §4's event sketch numbers events "consecutive within this session", but D37 makes the colony sequence the only replay order, and D33 keeps streamed text in memory only. How a view's cursor relates to the colony sequence, and how in-memory streamed deltas are addressed. Shared with C7 (`launcher-protocol`) |

**Exit criterion.** Events of every planned type can be written, read back by cursor and replayed in order after a restart. The synthetic ledger generator produces every catalogue v1 event without missing fields, including every Undercurrent §15.1 event in v1, every routing R11 event and D23's scratch fields. Readers skip unknown event types safely, and stop with a clear error on a consumed type they cannot decode (D35). Fuzzed truncation of segments recovers to the last complete commit. `ledger-format` and `ledger-reader` are proposed to the owner for freeze.

**Status.** Waiting.

### C2. Workspaces, observation, promotion, temporary scratch

**Goal.** Private scratch per work context and core-performed promotion (core §3.5), with every workspace action recorded as a ledger event. The step also builds D23's temporary scratch at the workspace layer; C3 decides when turns use it.

**Delivers.** Workspace registration, scratch worktree creation, host-side observation of changed paths and content hashes, fast-forward promotion with compare-and-swap on a core-owned canonical ref, and the workspace events in Undercurrent §15.1, including D23's authoring-context and parent-scratch fields and the temporary-scratch merge-back event. In addition:

- **Temporary scratch (D23).** Create a temporary worktree on its own branch from the task scratch's last commit. Commit the changes observed in a scratch. Merge a temporary scratch back by fast-forward when the task scratch has not moved; otherwise compute `git merge-tree --write-tree`, commit a clean result, and report a conflict as work for the agent. C3 wires these operations to turns.
- **Remedies for the costs in core Appendix A.7 [INFERENCE].** On the owner's machine, `git worktree add` took 19–21 s for 10,000 files, and hashing every file of a fresh 10,000-file worktree took 305 s. The remedies: a pool of spare worktrees, reused with `git switch` after their untracked files are removed (0.3 s when few files differ); and observation that hashes only files whose stat data changed (a stat-only walk took 0.6 s).
- **Windows hazards.** `core.autocrlf` in the agents' repositories (open, below). Long paths: git's `core.longpaths` is unset by default, and D38's short IDs keep scratch paths short. Files held open by running processes can block a worktree's removal [INFERENCE]. Paths are case-insensitive on Windows, unlike Linux (D32) [INFERENCE].
- **[PROPOSAL] Git operations go through the `git` program.** D23 already names `git merge-tree --write-tree`. Core Appendix A.7 measured `git update-ref <ref> <new> <old>` refusing a stale old value, which is the compare-and-swap that promotion needs.

| Definition | Meaning |
|---|---|
| Workspace root | A registered git repository; v1 requires git (core §3.5) |
| Canonical | The core-owned ref that represents promoted state; recommended never to be checked out in the human's working tree (core §3.5) |
| Scratch | A linked worktree for one bound work context, on a context-specific branch, cut from canonical |
| Base commit | The canonical commit a scratch was cut from or last integrated |
| Observed change set | Changed paths and content hashes the host derives by scanning the scratch, never from the git index (core §3.5) |
| Promotion | Compare-and-swap of the canonical ref from the expected old tip to the scratch tip; refused if canonical moved (D18) |
| Task scratch | The scratch of a task context. Only the task's own turn writes to it (D22) |
| Temporary scratch | A worktree on its own branch, cut from the task scratch's last commit, for a second of the same agent's turns that writes to the task while its scratch is in use. It is created on that turn's first write (D23) |
| Spare-worktree pool | Worktrees kept for reuse with `git switch` instead of creating new ones [INFERENCE from core Appendix A.7] |

**Waits for.** C1.

**Stop point.** Do not start before C1 is Done. The owner's Windows Defender decision for `scratch/` (`DECISIONS.md`, open items) affects cost, not correctness [INFERENCE]: the work can be built and tested with scanning on, but the pool size is settled only after the owner decides.

**Unblocks.** C3 and C10; U3; U4, together with C4, C5, U1 and U3.

**Decided.**

| Decided | Source |
|---|---|
| Private scratch per unit of work, as git worktrees, not clones; canonical changes only through core promotion | D16; core §3.5 |
| One scratch per bound work context and workspace root. A promotion may carry changes from several of one agent's contexts, and the ledger records each change's authoring context | D17, amended by D23 |
| Promotion is fast-forward only; the agent integrates canonical in its scratch as ordinary work | D18 |
| Temporary scratch: created on a second turn's first write, cut from the task scratch's last commit, merged back by fast-forward or `git merge-tree --write-tree`; a conflict becomes work | D23; core Appendix A.7 |
| Only a task's own turn writes to its scratch; another context's write follows D23 | D22, safeguard 3 |
| `workspaces/` and `scratch/<short id>/` under the colony root; short IDs because git's `core.longpaths` is unset by default | D38 |
| Git object IDs stay in each repository's own format; git 2.55 still creates SHA-1 repositories by default | D36; core Appendix A.7 |
| B1's LF rule covers this repository only; it does not change the agents' repositories | B1 |
| Windows first; portable to macOS and Linux | D27 |

**Open questions.**

| Question | Notes |
|---|---|
| Canonical ref identity and protection | Core §10 item 8. Which ref, where it lives, and how the core prevents child processes from moving it. C10 builds the enforceable boundary |
| Observation mechanism | Full scan, mtime-guided scan, or filesystem notifications; how untracked and ignored files are treated. Core Appendix A.7 favours hashing only files whose stat data changed [INFERENCE] |
| Promotion authority | Who may promote: the context itself, its Orchestrator, or the owner for some roots (core §10 item 8) |
| The human's participation | How the owner's own edits enter canonical |
| Scratch retention and garbage collection | When dormant scratches and branches are removed |
| Per-workspace setup | Dependencies not tracked by git (`node_modules`, virtual environments); shared with validation question V4 |
| Defender exclusion for `scratch/` | Owner; a security decision (`DECISIONS.md`, open items). A first read of a freshly written file took about 23 ms, against 0.2 ms on repeat; attributing this to Defender is [INFERENCE] (core Appendix A.7) |
| Pool size | How many spare worktrees to keep per workspace root |
| Line endings in the agents' repositories | B1 leaves this to C2. This machine's system git sets `core.autocrlf=true`, so files on disk can differ from what git stores, while observed changes are hashed from the files on disk |
| Post-promotion interference check | Undercurrent §15.1 proposes **[PROPOSAL]** an event for the result of the `git merge-tree` check run after each promotion, emitted by the core's workspace service. Whether C2 builds that emitter or U4 does is open; U4 lists it |

**Exit criterion.** Core phase 1 gates from core §9: scratch paths are distinct per context, promotion refuses after canonical moves, and the host-derived promotion delta equals the promoted tree diff. In addition, a temporary scratch merges back clean, by fast-forward or through `git merge-tree --write-tree`, and a conflicting one is detected and reported as work. A directory reused from the pool contains no file from its previous user.

**Status.** Waiting.

### C3. Agent runtime

**Goal.** One agent at work (D19): its directory, execution session and bound contexts; streamed model turns through hand-written adapters; host tools confined to the bound scratch; simultaneous turns under D22's safeguards; and a ledger event for every action. The router's shadow mode (R0) and the validation runner (C4) build on it.

**Delivers.**

- Agent directory loading, with `Agent.md` and general `Memory.md` under `agents/<name>-<short id>/` (D32, D38), and the agent's one execution session (D21).
- General-work and task contexts. Relationship and board contexts arrive with C6.
- The turn and tool loop. Tool dispatch checks authority, capability, argument schema and consent, in that order (core §11.1). An approval is resolved once by its ID, so a stale answer cannot rerun an action (core §4.1).
- Hand-written streaming Anthropic and OpenAI adapters on a static configuration (D19; core §8.3). Verify provider facts against routing Appendix B and current provider documentation before coding. Develop against a local fake SSE server; live calls need the owner's API keys.
- Host file and shell tools that run only in the bound context's scratch (D19; core §3.5).
- Child-process lifetime (core §3.3): on Windows, job objects through `golang.org/x/sys/windows`, `WaitDelay` and output limits; on Linux and macOS, process groups, with `Pdeathsig` on Linux. Verified per OS in CI (D42).
- Credentials from environment variables or the OS keychain, read by the core (core §3.2). go-keyring would be a recorded dependency (D29); whether v1 reads environment variables only is open.
- Minimal agent and account budget reservation and settlement (D22, safeguard 4; routing §11). R1 adds the threshold events (routing R11).
- D22's other safeguards. The core writes agent-wide files (`Agent.md`, general `Memory.md`, skill edits) with a version check, so a write based on an out-of-date copy is refused and the turn re-reads. Task assignments and reservations are core records changed atomically. At most one turn per context is active. The per-agent limit is a setting.
- D23 wired to turns: a temporary scratch on a second turn's first write to a task whose scratch is in use; a commit of the observed changes at the end of every turn; merge-back as soon as the turn ends and nothing else writes to the task scratch; a note to the task's next turn naming the files changed and the context that changed them.
- D25's list of the agent's other active contexts, rendered as a fixed tail section until R1 freezes `context-module` (`CONTRACTS.md`).
- Events (D33): turn starts and ends with the provider account (Undercurrent §15.1), approvals and their outcomes, reservations and settlements, and every action. Streamed text stays in memory for the views watching the turn; the finished message is recorded. Changes to private context files are logged by hash.
- Crash recovery (D37): an `interrupted` end for every unmatched start. Success is never assumed, and no tool effect is rerun blindly (routing R10).
- The plain terminal `dwarf-harness` (core §5), in one process with the core and built without cgo (D39). C7 later splits the host from its views.

| Definition | Meaning |
|---|---|
| Execution session | An agent's one resumable execution lifecycle: turn scheduling and an audit trail, not a pooled transcript (core §7.3; D21) |
| Private context | Keyed by `(agent_id, context_id)`: memory, handoff, prompt history, provider continuation, compaction, routing state and cache ledger. A work context also owns its scratch (core §7.3; D17) |
| Turn | One bound exchange in one context, with its tool loop, from start to end. At most one turn per context is active (D22) |
| Reservation | A core record that holds a call's maximum cost against every applicable budget scope until it is settled against reported usage (D22) |
| Static configuration | Fixed provider, model and settings, used while the router runs only in shadow (routing §14 phase 0) |

**Waits for.** C2, and the owner's private GitHub remote with CI on Windows, Linux and macOS (D42).

**Stop point.** Do not start before C2 is Done and the remote with CI exists. Live provider calls stop until the owner supplies API keys; everything that runs against the local fake SSE server can be finished first. Changes that add a dependency wait for the owner before reaching `main` (D41).

**Unblocks.** C4, C6, C7 and R0; the validation-level part of U2, together with C4.

**Decided.**

| Decided | Source |
|---|---|
| The runtime step: one agent with its execution session and bound contexts; hand-written Anthropic and OpenAI adapters on a static configuration; host tools that run only inside a context's scratch; a plain terminal; credentials from environment variables or the OS keychain; a ledger event for every action | D19 |
| An agent owns a directory and one execution session. Private contexts are keyed by `(agent_id, context_id)`; a change of counterpart renders a new turn, and continuations, approvals, router state and cache ledgers never carry over (core §7.3 adopted) | D8, D21 |
| Simultaneous turns up to a per-agent limit, which is a setting (start near 3; a limit of 1 gives one turn at a time), under four safeguards | D22 |
| A temporary scratch on a second turn's first write; a commit at the end of every turn; merge-back when the turn ends; a note to the task's next turn | D23 |
| Turns that start together may each pay the cache-write price; no staggered starts or pre-warm requests | D24 |
| The prompt tail lists other active contexts by counterpart name or task title only, never their content, after the cached prefix | D25 |
| `Agent.md`, spelled exactly | D32 |
| Turns, approvals, reservations and settlements go in the colony log; streamed text stays in memory; private content stays in the agent directory and is logged by hash | D33 |
| A start and an end share an operation ID; after a crash, an `interrupted` end for every unmatched start | D37; routing R10 |
| `agents/<name>-<short id>/` holds `Agent.md`, `Memory.md`, `contexts/<id>/` and `audit/`. The log record is the commit point for private memory; a mismatch found on restart is reported, never guessed | D38 |
| `dwarf-harness` is always built without cgo; race testing from the first concurrent code | D22, D28, D39 |
| Harness-owned control; provider features optional; no automatic provider switching in v1; provider betas off by default | D3, D4 |
| A private remote with CI on Windows, Linux and macOS exists no later than this step | D42 |

**Open questions.**

| Question | Notes |
|---|---|
| Default per-agent limit | D22: start near 3. The shipped default is not fixed |
| Turn starts beyond the per-agent limit | Wait, queue or report busy. D22 does not say; core §4.1 keeps "reports busy" as [INFERENCE] |
| Default tool set | The lightweight principle (`DECISIONS.md`) asks for a small default set; core §11.1 leaves the default grants open |
| Approval defaults | Which actions ask, and the default reach of an answer: once, this context, always or deny (core §4.2) |
| Keychain or environment only | Whether v1 reads credentials from environment variables only, or also from the keychain through go-keyring |
| Simultaneous turns in the terminal | How the plain scrollback terminal (core §5) shows several turns at once |
| Static configuration | Which provider and model it uses |
| Private-context file layout | Core §10 item 3. D38 fixes `contexts/<id>/`, not the files inside it |
| Budget defaults | Routing open question 3: whether local budgets ship with default values |
| The D25 note as a module | When R1 freezes `context-module`, the fixed tail section becomes a module (`CONTRACTS.md`). No step owns that change yet |
| New dependencies | `golang.org/x/sys` (job objects) and `golang.org/x/term` (line editing, core §5) each need a decision-log entry; go-keyring, if used, needs a record (D29) |

**Exit criterion.** Core §9 phase 1's runtime gates, and drills for D22 and D23:

- one agent has one resumable execution session;
- a context switch leaks no history, provider continuation, approval or late result;
- two concurrent turns writing `Memory.md` produce one refusal and a re-read;
- two contexts writing one task merge clean when their changes do not conflict, and surface a conflict as work when they do;
- concurrent reservations never exceed a budget;
- after a crash mid-turn, restart leaves `interrupted` ends and repeats no tool effect;
- no child process survives a normal or abnormal shutdown on Windows;
- every action is in the ledger;
- the check is green on Windows, Linux and macOS in CI.

**Status.** Waiting.

### C4. Validation runner and promotion gate

**Goal.** The host-owned runner that produces admissible validation evidence (validation §4.2) and gates promotion on affected checks (validation §4.8). Every promotion-pipeline field depends on it. Validation §6 proposes moving a minimal runner into core phase 1 **[PROPOSAL]**; this roadmap places it directly after the runtime.

**Delivers.** A runner interface, subject materialization from tree hashes, check overlay, in-process canaries, outcome derivation, rerun and confirmation scheduling, the validation record in the ledger, and a promotion gate. Run directories live under `runs/<short id>/` (D38), run logs go in the body store (D33), and the runner's queue emits the queue entry, start and finish events of Undercurrent §15.1.

| Definition | Meaning |
|---|---|
| Check | Command plus defining files, identified by digest (validation §4.1) |
| Subject | Workspace state identified by a host-computed tree hash |
| Environment digest | Toolchain, sorted environment variables, platform and dependency set, digested in the style of a remote-execution action key |
| Overlay | Replacing same-path files in the materialized subject with the pinned check files before running |
| Canary | A sentinel check that must fail, run in the same process as the suite (validation §4.9.1) |
| Outcome states | Per attempt: passed, failed, timeout, skipped, incomplete, infra_failure, no_status. Per record: passed, failed, flaky, inconclusive (validation §4.3) |
| Assurance level | A1 to A3 (validation §4.9); A1 is the phase 1 target |

**Waits for.** C3.

**Stop point.** Do not start before C3 is Done. C4 does not wait for U3: until the coupling graph exists, the gate uses a declared per-workspace check list (V5, below). The owner's Defender decision for `runs/` (`DECISIONS.md`, open items) affects cost, not correctness: as in C2, the runner can be built and measured with scanning on [INFERENCE], and the materialization-cost question below waits for the owner's decision.

**Unblocks.** C5 and C9; U4, together with C2, C5, U1 and U3; the validation-level part of U2, together with C3; U5's runner-queue node.

**Decided.**

| Decided | Source |
|---|---|
| Admissibility requirements | Validation §4.2: a **[PROPOSAL]** the earlier roadmap adopted as the working design; not an owner decision |
| Rerun counts from flake rates; repair confirmation | Validation §4.4, §4.5: **[PROPOSAL]**, with the same standing |
| O4 plus canaries; sample re-runs off by default. A repair at A1 ends strain only after a second, host-scheduled run in a separate run directory confirms it | V6 start (owner, 2026-10-02); validation §4.9.1 |
| Run directories under `runs/<short id>/` | D38 |
| Validation events go in the colony log; run logs go in the body store | D33 |
| Promotion checks test the merged result when a promotion carries changes from several of one agent's contexts | D23 |

**Open questions.**

| Question | Notes |
|---|---|
| V4: dependency materialization | Install per run, cache per environment digest, or snapshot per workspace; reproducibility versus speed |
| V5: gate check selection and time budget | Reverse dependencies through the coupling graph once U3 exists; before that, a declared per-workspace check list |
| V1: flake-rate prior and confidence | Law parameters; start conservative and record the choice |
| Where law parameters live before `law-format` | Validation §5 makes the flake-rate prior, the confidence and the quarantine threshold versioned law, and `law-format` belongs to U1. Neither C4 nor C5 waits for U1 |
| Language adapters | How structured results are collected for Go (`go test -json`), Python (pytest JUnit XML) and Node.js (a JUnit reporter); how "the suite ran" is detected for each |
| Process control | Process groups, timeouts, `WaitDelay`, output size limits (core §3.3). C3 builds the child-process lifetime layer |
| Concurrency and queueing | How many runs at once; the queue becomes a resource node for `resource_pressure` |
| Affordable fresh materialization | Fresh run directories pay core Appendix A.7's per-file cost on Windows: roughly 2 ms per file on the owner's machine with scanning on [INFERENCE]. How to keep fresh, ephemeral materialization affordable without reusing directories in a way that breaks admissibility (validation §4.2) |
| Defender exclusion for `runs/` | Owner; a security decision (`DECISIONS.md`, open items) |
| Culprit attribution | The culprit-attribution and revert-or-repair events in Undercurrent §15.1, which grades F3 and R3 need (validation §4.4, §4.5), have no assigned step. C4's rerun scheduling is a candidate [INFERENCE] |

**Exit criterion.** The runner rejects every adversarial case in validation §5 that is in scope at A1: printed pass lines without a passing exit, skipped suites, suites that never ran, edited tests (overwritten by overlay), and a canary reported as passing. A promotion with a failing affected check is refused and recorded.

**Status.** Waiting.

### C5. Check registry and governance

**Goal.** Make checks first-class: pinned identity, lineage, open failures, quarantine and the V3 retirement flow.

**Delivers.** A registry derived from ledger events, check lineage records, the retirement packet assembled by the host, the Orchestrator review hook, quarantine rules and human-oracle verdict events. They include the check events of Undercurrent §15.1: check added, modified, retired or quarantined, and human-oracle verdict. The retirement mechanism follows validation §4.6.1, a **[PROPOSAL]** implementing V3: retirement within the owning Corps, a host-assembled packet, and Orchestrator review that acts only to overturn.

| Definition | Meaning |
|---|---|
| Check lineage | Author context, clone ancestry, model family, and derivation from other checks (validation §4.7; Undercurrent Q2) |
| Open failure | A failure at grade F2 or above that no repair or retirement has ended |
| Owning Corps | The Corps of the context that promoted the check's current digest (validation §4.6.1) |
| Retirement packet | Host-assembled evidence for a retirement, plus the agent's reason category and argument |
| Quarantine | Automatic suspension of a check past a law-defined flake rate |
| Human-oracle verdict | An owner-issued verdict recorded in the ledger; the only failure or repair source for work with no automated oracle |

**Waits for.** C4.

**Stop point.** Do not start before C4 is Done. Until Corps exist, the owner is the only retirer (below). The step does not fake Corps membership to test the agent path (D41).

**Unblocks.** U4, together with C2, C4, U1 and U3.

**Decided.**

| Decided | Source |
|---|---|
| An agent in the owning Corps may retire a check; the Orchestrator has the last say | V3 (owner, 2026-10-02); validation §4.6.1 |
| Governance events are typed, versioned events in the colony log | D33, D35 |

**Open questions.**

| Question | Notes |
|---|---|
| Interim retirement authority before Corps exist | Corps membership arrives with C6's role graph. Until then no agent belongs to an owning Corps, so the owner is the only retirer [INFERENCE from V3] |
| Exclusion of the repairing context and its clones | Kept in the proposal; confirm or drop (validation §4.6.1) |
| Orchestrator review criteria | Written as part of the Orchestrator skill suite; review is notify-and-overturn |
| V7 | Are human-oracle verdicts reversible, and by whom? |
| V8 | Are agent-authored reproduction checks promoted into the suite automatically after repair? |
| Quarantine threshold and release | Law parameters (see C4 on where they live before `law-format`); how a quarantined check returns to service |
| Whether C5 waits for C6 | The exit criterion's retirement by another agent needs Corps membership from C6; `ROADMAPS.md` has no edge from C6 to C5 |

**Exit criterion.** A retirement by the repairing context is refused; a retirement by another agent takes effect and is reversible by the Orchestrator (or by the owner, before Corps exist); every governance event replays correctly.

**Status.** Waiting.

### C6. Broker, role graph and Corps boards

**Goal.** Durable, authorized messaging between agents, the first part of core §9 phase 2. Agents reach one another only along the role graph, and accepted messages survive a restart.

**Delivers.**

- The role graph (core §7.2): permitted direct counterparts and board output per role, with default deny for every unlisted pair and operation, checked on send, delivery and retrieval (core §7.4).
- The embedded broker (core §7.4): committed message records with core-assigned message IDs and sender-scoped submission IDs; acceptance only after a durable append; inbox references rebuilt from committed records; processing checkpoints; asynchronous send and yield, so that no agent blocks on a peer.
- Corps boards for worker discussion (core §7.5): each post stored once, a cursor per reader, and the posting rights in core §7.2. Griot story publication follows in C8.
- Relationship and board contexts (core §7.3), completing the context kinds of D21.
- The `conversation.send` and `board.post` commands and the `conversation.appended` and `message.processed` events (core §4.2).
- Messages and inbox processing in the colony log; message bodies in the body store (D33).

The broker (core §7.4) and the board handling (core §7.5) are marked [INFERENCE] in the core document, and D21 keeps the broker a proposal.

| Definition | Meaning |
|---|---|
| Conversation | Canonical exchanged messages: an authorized pair or a shared Corps board (core §7.3) |
| Inbox | Pending authorized message references and processing state (core §7.3) |
| Submission ID | A sender-scoped ID, so that a resend after reconnecting returns the original acceptance instead of a second message (core §7.4) |
| Processing checkpoint | A committed record covering a message's resulting context changes and outgoing messages. Only it marks a message processed (core §7.4) |

**Waits for.** C3.

**Stop point.** Do not start before C3 is Done. C6 does not wait for C7; core §9 phase 2 orders the broker before shared live attachment (`ROADMAPS.md`).

**Unblocks.** C8; U8, together with U7.

**Decided.**

| Decided | Source |
|---|---|
| Private contexts include relationships and boards; the broker remains a proposal | D21 |
| Permitted communication paths per role, recorded in core §7.2. The vertical-versus-lateral reading there is [INFERENCE] | D11 |
| Workers talk to other workers only through the Corps board | D11; core §7.5 (owner requirement) |
| No cross-colony route by implication; cross-colony messaging needs its own design | D26; core §7.2 |
| Messages and inbox processing go in the colony log; message bodies go in the body store | D33 |
| Message IDs are UUIDv7 | D37 |

**Open questions.**

| Question | Notes |
|---|---|
| Broker persistence and checkpoint layout | Core §10 item 3 |
| Queue limits | Core §10 item 3. Core §7.4 asks for explicit message and inbox limits with visible backpressure |
| Relationship reassignment and retirement | Core §10 item 3. Core §7.4: reassignment closes the old grants |
| Historical-read retention | Core §10 item 3 |
| Fair scheduling and response deadlines | Implementation choices (core §7.4) |

**Exit criterion.** Core §9 phase 2's messaging gates: allowed direct and board paths work; forbidden paths fail before delivery; accepted messages survive restart.

**Status.** Waiting.

### C7. Colony host, local IPC and launcher protocol

**Goal.** Turn the core into a colony host that several views attach to (D26, D39): the rest of core §9 phase 2, apart from the GUI client.

**Delivers.**

- The colony host: one core host process per active colony, the sole writer of its ledger, holding C1's lock (D26). `dwarf-harness` hosts the colony or, when a host already runs, attaches to it as a terminal view (D26, D39; core §3.1).
- Local IPC as core §3.4 recommends [INFERENCE]: on Windows, a named pipe with an explicit intended-user DACL and `PIPE_REJECT_REMOTE_CLIENTS`; on Linux and macOS, a Unix-domain socket in an owner-only directory.
- The `launcher-protocol` contract (`CONTRACTS.md`): versioned, length-framed JSON commands and events with frame limits and request IDs (core §3.4, §4.2), and a version check on attach (D39). A draft first, then a freeze request; the GUI roadmap builds against it once frozen (D40).
- Views with their own cursors (core §4, §4.1). An approval or router notice is resolved once by its ID across views; the core orders competing commands; cancellation reaches the shared turn (core §4.1).
- The terminal becomes a view of the host. The GUI client belongs to the GUI roadmap.

| Definition | Meaning |
|---|---|
| Colony host | The one core host process of an active colony, the sole writer of its ledger (D26) |
| View | A launcher attached to the host, with its own cursor. It never builds prompts, chooses models, executes tools or changes permission state (core §4.2) |
| Attach | Opening a view of an existing session, after the protocol versions are checked (D39) |

**Waits for.** C3.

**Stop point.** Do not start before C3 is Done. C7 does not wait for C6. The step stops at the host side of the protocol; the GUI client is not built here.

**Unblocks.** The GUI roadmap, which also waits for toolkit selection (core §10 item 1).

**Decided.**

| Decided | Source |
|---|---|
| One core host process per active colony, the sole writer of that colony's ledger | D26 |
| `dwarf-harness` is the host plus the terminal, built without cgo. `dwarf-harness-GUI` is a display client: it starts `dwarf-harness` as host if none runs, holds no colony logic or API keys, and checks protocol versions when it connects. A GUI crash cannot stop a turn | D39 |
| `launcher-protocol` is a contract owned by this step | D40; `CONTRACTS.md` |
| Both launchers have the same functionality and may show one live session at once | D5 |

**Open questions.**

| Question | Notes |
|---|---|
| IPC bindings, instance discovery, recovery, and the core's lifetime after its last view disconnects | Core §10 item 2 |
| `go-winio` | A candidate recorded dependency for named pipes (D29) |
| View cursors and streamed text (`launcher-protocol`) | Shared with C1 (`ledger-reader`). Core §4's event sketch numbers events "consecutive within this session", but D37 makes the colony sequence the only replay order, and D33 keeps streamed text in memory only. How a view's cursor relates to the colony sequence; how in-memory streamed deltas are addressed; what a view that attaches during a turn shows of the text streamed before it attached |
| A host without its terminal | Under D39, how a `dwarf-harness` started by the GUI runs with no terminal view, and how a host outlives its own terminal view while the GUI stays attached (core §10 item 2) |

**Exit criterion.** One turn appears in two attached views without duplicate execution. Competing commands from two views cause one execution. A reconnecting view resumes from its cursor. A version mismatch is refused on attach.

**Status.** Waiting.

### C8. Corps, Grand chains, clones, birth, Griot publication

**Goal.** Core §9 phase 3: specialized Corps, Grand role chains, clones, superior-approved birth once its protocol is designed, and Griot story publication.

**Delivers.**

- Corps, each with an Orchestrator, Griot, Diplomat and workers, specialized within the Colony, and the Grand triad's role chains (core §7.1, §7.2).
- Cloning (core §7.6): a new identity, directory and execution session that starts from the source's `Agent.md`, with fresh memory unless inheritance is selected, and with its own Corps and parent links and conversation handles.
- Birth with the superior's permission, once its protocol is designed (core §7.6).
- Griot story publication on the Corps board: versions with provenance and a Corps-owned Markdown projection (core §7.5).
- Explicit lesson handoffs along permitted paths (core §7.5).

**Waits for.** C6, and the design of core §10 items 4 and 5.

**Stop point.** Do not start while the status is *Not ready: design open*. Never invent Grand mandates, the birth protocol or the Griot cadence to pass a gate (core §9).

**Unblocks.** No step in the current graph.

**Decided.**

| Decided | Source |
|---|---|
| Cloning copies `Agent.md`; copying memory is optional | D8 |
| Drift is a feature. Birth needs the initiating Orchestrator's superior's permission; its selection policy, process and protocol are not chosen | D10 |
| Every Corps has an Orchestrator, Griot and Diplomat, with the communication paths in core §7.2 | D11 |
| Grand Orchestrator, Grand Griot and Grand Diplomat sit at the top; their detailed functions come later | D12 |
| Corps specialize within a Colony | D13 |
| The Griot guides workers only through story output on the Corps board | Owner requirement; core §7.5 |
| `Agent.md`, spelled exactly | D32 |

**Open questions.**

| Question | Notes |
|---|---|
| Grand-role mandates, intermediate levels, Grand-boundary birth approval | Core §10 item 4 |
| Griot update cadence and excerpt selection | Core §10 item 5 |
| Birth-selection policy, process and protocol | Core §10 item 5 |
| Memory inheritance: selection and disclosure policy | Core §10 item 5; core §7.6 |

**Exit criterion.** Core §9 phase 3: peer and parent contexts survive resume; clone memory is independent; birth cannot bypass superior permission; worker and Griot board rules and explicit lesson handoffs hold.

**Status.** Not ready: design open (core §10 items 4 and 5).

### C9. Recipes, workflows and skills

**Goal.** Core §9 phase 4: independently implemented recipes, workflows and skills.

**Delivers.** Workflows as one executable graph (core §11.1), with the primitives and limits in core §11.2 and every agent-to-agent edge checked against core §7.2. Recipes with host-checked acceptance (core §11.3). Skills with shared-format discovery, lazy loading and update modes (core §12). Validation §4.7 proposes **[PROPOSAL]** that recipe acceptance rows become pinned checks with lineage, run by C4's runner and tracked by C5's registry. The graphical treatment belongs to the GUI roadmap.

**Waits for.** C4, and the design of core §10 item 6.

**Stop point.** Do not start while the status is *Not ready: design open*. The `recipes/` folder in the archive folder `../dwarfharness/`, outside this repository, is not an input (`ROADMAPS.md`); core §11.3 adopts no imported catalogue.

**Unblocks.** No step in the current graph.

**Decided.**

| Decided | Source |
|---|---|
| Capabilities, workflow primitives, recipes and skills as specified in core §11 and §12, carried forward from the three early-design sections the owner authorized | Core §11, §12 |
| Skill edits are written by the core with a version check | D22, safeguard 1 |

**Open questions.**

| Question | Notes |
|---|---|
| Recipe and workflow catalogues and schemas | Core §10 item 6, with any reconciliation beyond the three incorporated early-design sections |
| "Either launcher" in the exit criterion | Core §9 phase 4's gate names both launchers, which needs C7 and the GUI roadmap; `ROADMAPS.md` lists only C4 before C9 |

**Exit criterion.** Core §9 phase 4: core execution matches the selected workflow in either launcher; acceptance and skill precedence work without an inspirational project's runtime.

**Status.** Not ready: design open (core §10 item 6).

### C10. Isolation boundary and assurance A2

**Goal.** Close core §10 item 8: an enforceable boundary protecting canonical refs, shared git metadata, the ledger and private files from same-user child processes. It raises validation assurance to A2. Under the V6 starting configuration (O4), affordance requires A2 (validation §4.9.1).

**Delivers.** The boundary on Windows first, then on Linux and macOS (D27), under a stated threat model and with tests. Validation runs at assurance A2: A1 plus filesystem and process restriction (validation §4.9, **[PROPOSAL]**). A measured cost per run.

**Waits for.** C2: canonical refs must exist to be protected.

**Stop point.** Do not start building before C2 is Done. Researching the mechanisms needs no code and can start earlier **[PROPOSAL]**.

**Unblocks.** U7, only if the V6 floor needs A2; and any agent-visible affordance, which requires A2 under the V6 starting configuration (`ROADMAPS.md`, notes on the edges).

**Decided.**

| Decided | Source |
|---|---|
| Windows first: this step needs Windows mechanisms, not only the Linux ones the earlier roadmap listed | D27 |
| The core host is the sole writer of the colony's ledger | D26 |
| Affordance requires A2 in the V6 starting configuration | V6 start; validation §4.9.1 |

**Open questions.**

| Question | Notes |
|---|---|
| Mechanism on Windows | Researched first (D27). Candidates: restricted tokens, AppContainer, a separate local user |
| Mechanism on Linux | Separate user, user namespaces (bubblewrap), Landlock (Linux 5.13 or later), Yama ptrace scope |
| Mechanism on macOS | Not researched |
| Ledger write path | The ledger must be writable only by the core process |
| Performance cost per run | Measure; affects whether O2 becomes affordable |
| Threat model | The exit criterion needs one; none is written yet |

**Exit criterion.** Child processes provably cannot move canonical, write the ledger or read another context's private files, under a stated threat model and tests.

**Status.** Waiting.

## 3. Open questions by step

Details are in each step. "Owner" marks items awaiting the owner.

| Step | Open questions |
|---|---|
| C0 | How the check finds the compiler; the race-detector compiler in Linux and macOS CI; govulncheck without a network |
| C1 | Segment size and rotation; flush semantics per OS; lock mechanism per OS; body garbage-collection policy; index rebuild budget and snapshot trigger; write throughput target (measure); event catalogue v1; view cursors and streamed deltas (with C7) |
| C2 | Owner: Defender exclusion for `scratch/`. Also: canonical ref identity and protection (core §10 item 8); observation mechanism; promotion authority; the human's participation; scratch retention and garbage collection; per-workspace setup (V4); pool size; line endings in the agents' repositories (B1); the emitter of the post-promotion interference check |
| C3 | Default per-agent limit; turn starts beyond the limit; default tool set; approval defaults; keychain or environment only; simultaneous turns in the terminal; the static configuration; private-context file layout (core §10 item 3); budget defaults (routing open question 3); the D25 note as a module; entries for new dependencies |
| C4 | Owner: Defender exclusion for `runs/`. Also: V1, V4, V5; where law parameters live before `law-format`; language adapters; process control; concurrency and queueing; affordable fresh materialization; culprit attribution |
| C5 | Interim retirement authority; repairer exclusion; Orchestrator review criteria; V7; V8; quarantine threshold and release; whether C5 waits for C6 |
| C6 | Core §10 item 3: broker persistence and checkpoint layout, queue limits, reassignment and retirement, historical-read retention. Fair scheduling and response deadlines |
| C7 | Core §10 item 2: IPC bindings, discovery, recovery, the core's lifetime after its last view; a host without its terminal; `go-winio`; view cursors and streamed text (with C1) |
| C8 | Core §10 item 4: Grand mandates, intermediate levels, Grand-boundary birth approval. Core §10 item 5: Griot cadence and excerpts, birth policy and protocol, memory-inheritance policy |
| C9 | Core §10 item 6: recipe and workflow catalogues and schemas; the "either launcher" exit criterion |
| C10 | Mechanisms on Windows (first), Linux and macOS; ledger write path; performance cost per run; threat model |

Question IDs beginning with Q are in `DESIGN-UNDERCURRENT.md` §17, and IDs beginning with V in `RESEARCH-ADMISSIBLE-VALIDATION.md` §8. Routing open questions are numbered in `DESIGN-ROUTING-LAYER.md` §15, and core §10 items in `DESIGN-CORE-AND-LAUNCHERS.md` §10.
