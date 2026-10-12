# OpenRig persistence and recovery: comparison with DwarfHarness

**Status:** Source-grounded research snapshot, 2026-10-08. This is explanatory evidence, not a DwarfHarness decision, implementation plan, or contract freeze.

**OpenRig source pin:** [`mvschwarz/openrig` commit `5fab9ae8025e0a0279c988aaee55d01dbac4d709`](https://github.com/mvschwarz/openrig/tree/5fab9ae8025e0a0279c988aaee55d01dbac4d709), package version `0.6.6`. Repository metadata reports Apache-2.0, not archived, with that commit as the inspected revision. See [GitHub repository metadata](https://api.github.com/repos/mvschwarz/openrig).

**Evidence boundary:** OpenRig findings below come from static inspection of the pinned source and project documentation, not an installation, runtime test, crash test, or performance benchmark. DwarfHarness is also an unfinished design. The local design documents and the OpenRig source snapshot can both change.

## Executive summary

OpenRig is not simply “an SQLite event log.” Its local daemon uses **SQLite in WAL mode** for operational state, with mutable domain tables, a durable queue, event history, and JSON snapshots; it also keeps terminal/transcript evidence in files and relies on the launched coding harness for its native conversation/session behavior. The SQLite database is a practical local control-plane store, not a complete record of every token or every effect in a workspace.

DwarfHarness's selected design is different: one Core owns an append-only, hash-chained colony ledger, with large bodies stored separately by content hash. Private conversational context, provider sessions, Git workspaces, and UI projections are distinct stores with distinct ownership and lifetimes. The aim is to make operational history auditable and boundedly replayable; it is not a plan to use the OpenRig schema or process model.

The most useful shared lesson is to **separate durable work ownership from delivery/visibility and from the process that happens to execute it**. The main tradeoff is familiar SQLite simplicity and indexed queries versus DwarfHarness's stronger explicit history/content-identity model and the extra engineering burden of a custom append protocol, replay/indexing, and platform-specific durability proof. There is no comparable benchmark showing either system is faster or safer for DwarfHarness workloads.

## What OpenRig persists

### 1. A local SQLite control plane, explicitly WAL-backed

OpenRig's daemon connector creates a `better-sqlite3` connection, selects `journal_mode = WAL`, and enables foreign keys. The connector defaults to `:memory:` for tests; production startup supplies a persistent database path. OpenRig's security policy says its state lives under `$OPENRIG_HOME`, defaulting to `~/.openrig`.

Sources: [`connection.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/db/connection.ts), [`startup.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/startup.ts), [`daemon-db-path.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/db/daemon-db-path.ts), [security policy](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/SECURITY.md).

The schema is a mixture, not pure event sourcing:

- Mutable relational tables hold rigs, nodes, sessions, queue state, bindings, and related daemon data. Foreign keys and indexes support those operational queries.
- `events` is an append-only, per-rig sequence used for event history and audit/replay-related reads. Event history is intentionally not coupled to rig/node deletion.
- Snapshots store serialized state (including topology/occupant information) alongside metadata and a materialized roster event. Snapshot creation commits the snapshot and event together; restore code validates and interprets snapshot payloads rather than treating a snapshot as a running process.

Sources: [event migration](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/db/migrations/003_events.ts), [snapshot migration](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/db/migrations/004_snapshots.ts), [`event-bus.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/event-bus.ts), [`snapshot-capture.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/snapshot-capture.ts), [`snapshot-repository.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/snapshot-repository.ts).

The inspected connector does **not** specify `PRAGMA synchronous`; therefore its exact sync policy should be checked against the SQLite build/runtime actually shipped rather than inferred from the fact that it uses WAL. SQLite documents that `FULL` in WAL syncs the WAL at each commit, whereas `NORMAL` skips most per-transaction syncs and can lose recent committed transactions on power loss/system crash while remaining consistent. Either setting still protects against ordinary application-process crashes. This is a durability/latency decision, not a “WAL means durable” guarantee.

Source: [SQLite `synchronous` pragma](https://www.sqlite.org/pragma.html#pragma_synchronous).

### 2. Durable queue state is separate from terminal delivery

OpenRig's queue repository persists work items and their state transitions. Claims are state changes, not expiring worker leases. A local handoff transaction can close one item and create its successor transactionally; a tmux send/wakeup is attempted after the durable write. A failed send therefore does not roll back the queue transition. Queue `done`/`handed-off` statuses are terminal queue states, not proof that a coding agent completed and validated the requested work.

A separate seat-delivery guard checks the target session/occupant/pane before sending input. This is important because a durable request addressed to a seat must not silently be redirected to a replacement process occupying that seat.

Sources: [`queue-repository.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/queue-repository.ts), [`seat-delivery-guard.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/seat-delivery-guard.ts), [coordination architecture](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/docs/as-built/architecture/coordination-primitive.md).

### 3. The database is not the whole conversation or workspace

OpenRig persists session/seat identity, startup inputs, and native resume metadata, but the actual Claude Code/Codex/Pi processes and their provider conversation state remain external harness/runtime concerns. Snapshots preserve topology and selected continuity metadata; they do not make a dead process alive or guarantee that a provider session can be resumed. Restore distinguishes native resume, rebuild, and fresh start, and surfaces failures rather than pretending a new empty conversation is a successful resume.

Terminal evidence is also separate: transcript files are maintained under the OpenRig home, with boundary markers and a bounded recent-pane capture path. This is useful operational evidence, but it is not an immutable full-fidelity token transcript. OpenRig's architecture says captured screen text is a bounded tail snapshot, not a complete history of every model/tool event.

Sources: [`session-registry.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/session-registry.ts), [`restore-orchestrator.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/restore-orchestrator.ts), [`transcript-store.ts`](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/packages/daemon/src/domain/transcript-store.ts), [architecture map](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/ARCHITECTURE.md).

## Persistence model comparison

| Concern | OpenRig | DwarfHarness selected design |
|---|---|---|
| Operational authority | Local daemon's SQLite state and per-domain repositories | One Core-owned append-only colony ledger; one serialized writer |
| History | Indexed relational state plus append-only event rows; snapshot records | Hash-chained, sequenced commits; record schema/version and producer ownership are explicit design requirements |
| Large payloads | JSON payloads in DB/snapshot rows and separate local files | Bodies separated into content-addressed storage; ledger references exact content hashes |
| Context continuity | Native harness/provider sessions plus OpenRig seat/session metadata and transcript files | Separate private relationship/conversation context and provider continuation; durable context artifacts are not implicitly shared |
| Recovery | Query DB, validate snapshots, decide native resume/rebuild/fresh; re-deliver queue work after commit | Validate/replay ledger and indexes, reconcile body references and process/workspace state, then make explicit recovery/admission decisions |
| Workspace changes | Existing coding-harness workspace/repo behavior | Private Git worktrees and explicit promotion/validation boundary; workspace state is not the ledger |
| Main cost | Indexed queries and multi-row transactions are familiar; WAL checkpoint and DB lifecycle must be operated correctly | Simple sequential append path is attractive, but custom framing, checksums, rotation, replay/index rebuild, retention and native durability all need proof |

DwarfHarness's current design sets provisional record/segment limits and specifies hash-chain and flush ordering, but these are not a frozen schema or proven runtime. Its intent is one writer and multiple views, not a second database hidden alongside the ledger. See [Core §3.5](DESIGN-CORE-AND-LAUNCHERS.md#35-workspaces-private-scratch-per-work-context-and-fast-forward-promotion), [Core §13.1](DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3), [DECISIONS.md D26](DECISIONS.md#d26-one-core-and-one-ledger-per-colony), and the [contract status register](CONTRACTS.md).

## Performance and operational consequences

### SQLite/WAL: good local query behavior, with a checkpoint and backup contract

SQLite WAL allows readers alongside a writer, but still serializes writers. Writes append to the WAL and checkpoints later transfer pages into the main database. Automatic checkpoints normally trigger around 1000 pages; the commit that crosses the threshold can do checkpoint work and take much longer than ordinary commits. Long-lived readers can prevent checkpoint completion and WAL reset. This can create occasional commit latency and WAL growth even when average write time is low.

WAL is a same-host design, not a multi-machine shared-filesystem protocol. A live backup must use SQLite's backup/checkpoint facilities or otherwise coordinate a consistent DB/WAL copy: SQLite explicitly says the WAL is part of persistent database state, and separating it from the database can lose transactions that were already committed. OpenRig's local daemon architecture fits WAL's same-host boundary; using a shared network volume or copying only the `.sqlite` file would not.

Sources: [SQLite WAL: checkpointing/concurrency](https://www.sqlite.org/wal.html#checkpointing), [SQLite WAL limitations](https://www.sqlite.org/wal.html#overview), [WAL persistence and backups](https://www.sqlite.org/wal.html#the_wal_file).

### DwarfHarness: measured prototypes, not a production-vs-OpenRig result

The DwarfHarness design document records local prototype measurements on Windows. One prototype comparison used a ~650-byte event and reports ~0.52 ms for the custom Go log versus ~0.54 ms and ~0.85 ms for two SQLite Go bindings in that specific workload; a 20,000-event readback was ~29 ms. The same appendix explicitly frames these as prototypes, not a production benchmark. The measurements do not use OpenRig's Node/`better-sqlite3` code, schema, checkpoint patterns, real daemon traffic, or crash recovery, so they do not establish that DwarfHarness's chosen design is faster than OpenRig.

A separate measured cost is likely more consequential for DwarfHarness: worktree materialization and validation. The design notes a full fresh checkout/hash walk of roughly 305 seconds for a 20,000-file tree on the recorded Windows machine, while a stat-only walk was under a second. Per-work-context copies also consume disk. That can dominate ledger append performance in real use, so persistence benchmarks must measure the entire run and not just a single event append.

Source: [Core Appendix A.7–A.9](DESIGN-CORE-AND-LAUNCHERS.md#a7-git-worktrees-on-windows), [Core §3.5 worktree discussion](DESIGN-CORE-AND-LAUNCHERS.md#35-workspaces-private-scratch-per-work-context-and-fast-forward-promotion).

No OpenRig performance claim is made here: its test-layer guide describes correctness suites and scenario checks, not a published, comparable throughput/latency benchmark. See [OpenRig test layers](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/docs/as-built/test-layers.md).

## Security and data-lifecycle tradeoffs

- **Trust boundary:** OpenRig explicitly targets a user's own machine or trusted private network, assumes the machine/accounts are trusted, and does not support exposing an install to the open internet. It writes harness trust/configuration files and executable hooks as well as its own state. This is an honest, materially narrower threat model than treating the daemon as a multi-tenant security boundary. Source: [OpenRig security policy](https://github.com/mvschwarz/openrig/blob/5fab9ae8025e0a0279c988aaee55d01dbac4d709/SECURITY.md).
- **At-rest protection:** The inspected design does not identify application-level encryption for the SQLite database, transcript files, or snapshots. Treat them as protected by OS account/filesystem controls unless the deployment adds volume encryption. DwarfHarness similarly has a trust/authority model, not a selected encrypted ledger; a hash chain detects accidental/tampered history only if the attacker cannot rewrite the whole chain and its trusted head. Hashes are not encryption or authentication.
- **Deletion is not erasure:** SQLite `secure_delete` is normally off unless build-configured; WAL/checkpoints and filesystem behavior add further residue considerations. Deleting a row or pruning a snapshot is therefore not a secure-erasure promise. DwarfHarness's append-only operational history makes retention and body-GC policy an explicit governance issue; removing a body reference is not evidence that every filesystem/backup copy is gone. Source: [SQLite `secure_delete` pragma](https://www.sqlite.org/pragma.html#pragma_secure_delete).
- **Credentials and authority:** OpenRig's security policy discusses trust/configuration and local integrations; it should not be mistaken for per-agent isolation from a hostile process running as the same OS account. DwarfHarness's current design separates provider credential custody from agent-controlled tools and treats model/tool output as hostile, but that separation is an implementation requirement, not a present security proof. See [Core §13.9–13.10](DESIGN-CORE-AND-LAUNCHERS.md#139-execution-isolation-and-macoslinux-guests-b11-d87).
- **Durability level matters:** Process crash, OS crash, and sudden power loss are different fault models. SQLite documents that `synchronous=NORMAL` in WAL is transaction-consistent but may roll back recent commits after power loss; DwarfHarness's planned flush sequence must be tested against each target OS/filesystem and cannot be claimed from a passing process-kill test alone.

## Transferable design lessons

1. **Keep work ownership separate from delivery.** Commit the durable request/claim/handoff before sending a tmux keystroke or notification. A failed wake-up must be observable and retryable without erasing the accepted work. Do not equate sent, displayed, acknowledged, or terminal-pane-visible with completed and validated.
2. **Separate identity from process lifetime.** A seat/session is a stable logical target; its current process is replaceable. Bind delayed input to the intended occupant/generation and refuse ambiguous delivery. This is a strong design pattern, not a reason to copy OpenRig's domain model.
3. **Separate operational truth from convenience views.** Use immutable history for facts that need audit/recovery, and make indexes, snapshots, pane captures, and GUI state explicit projections with their own freshness/retention. A snapshot accelerates recovery; it is not itself proof that the underlying process or external workspace still matches it.
4. **Choose a durability contract before choosing a storage benchmark.** Decide whether the promised boundary is application crash, OS crash, or power loss; specify commit acknowledgment, flush ordering, segment rotation, backup/restore, and corruption behavior. “Append-only” and “SQLite transaction” each leave questions until these edges are stated.
5. **Do not confuse tamper evidence with access control.** A hash chain helps detect partial changes when its trusted head is protected. It does not hide sensitive content, prevent a same-user process from reading it, or stop a privileged writer from recomputing the chain.
6. **Measure the whole lifecycle.** Include startup/migration, index rebuild/replay, context/worktree materialization, checkpoint tail latency, recovery, backup, retention, and disk usage. A sub-millisecond append is not a fast harness if checkout or model-context preparation costs minutes.

These lessons align with DwarfHarness's already selected one-Core/one-ledger ownership and explicit promotion boundary; they do not authorize changing the current design or creating new contracts.

## What remains uncertain / measurement priorities

1. **OpenRig sync mode:** query the effective `PRAGMA synchronous` and SQLite compile options in the actual published runtime. The inspected connector sets WAL and foreign keys but not synchronous explicitly.
2. **OpenRig workload costs:** measure commit p50/p95/p99 with realistic event sizes, 1/2/8 seats, read-heavy UI load, queue transitions, and transcript writes; record database/WAL size and checkpoint duration. Include long-lived readers and process restart.
3. **DwarfHarness end-to-end storage:** compare the selected log design with SQLite using identical records, indexes, sync promises, retention, and host; include warm/cold replay and query mixes rather than only append microbenchmarks. These are design-validation measurements, not a demand to reopen D34 or other settled decisions.
4. **Crash matrix:** kill the process around append/flush/rotation/body publication; separately test OS-crash/power-loss claims where a credible test harness exists. Verify acknowledged commits, hash-chain continuity, orphan-body handling, index rebuild, and deterministic recovery.
5. **Workspace/context costs:** benchmark actual project sizes, Defender/indexer conditions, concurrent private worktrees, prompt/context loads, and disk amplification. This may be the dominant DwarfHarness cost.
6. **Backup, restore, and retention:** prove a coherent SQLite WAL backup/restore for OpenRig; for DwarfHarness, test ledger/body pairing, manifests, key/hash verification, and retention/GC. For both, specify what deletion means on backups, WAL, SSDs, and logs.
7. **Security reality:** inspect actual filesystem permissions and credential placement; test what a child agent/tool can read under the intended Windows/macOS/Linux launch boundary. Do not substitute a hash check or a UI-hidden file for OS-enforced isolation.

## Bottom line

OpenRig's SQLite/WAL control plane is a pragmatic fit for a local daemon that needs indexed mutable state, queues, snapshots, and transactional lifecycle updates. It deliberately leaves provider conversations and much workspace state to existing harnesses, and its trust model is for an owner-controlled machine/private network.

DwarfHarness's ledger design optimizes for a different explicit contract: one authoritative owner, append-only auditable work history, exact body identity, private contexts, and a controlled promotion path. That can make recovery and provenance easier to reason about, but only if replay, storage growth, cross-platform flush semantics, and host isolation are implemented and measured. The OpenRig source is useful as a concrete coordination/persistence reference—not a benchmark, drop-in architecture, or evidence that DwarfHarness's choices work.
