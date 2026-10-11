# Ledger contracts: user needs

**Date:** 2026-10-10. Written at the owner's request in his answer to OG2, recorded in [D111](docs/DECISIONS.md#d111-f0-ledger-contracts-frozen-at-version-1). This note explains the three frozen ledger contracts. It changes none of them and freezes nothing. Where it and the package documentation differ, the package documentation governs: "this documentation is the format".

## 1. The contracts and their users

The colony ledger is the colony's one durable history: one append-only log per colony, written through one Core-owned appender ([D26](docs/DECISIONS.md#d26-one-core-and-one-ledger-per-colony), [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log), [D50](docs/DECISIONS.md#d50-one-core-owned-appender-serializes-colony-writes-q28), [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage)). Three contracts define it:

- **`ledger-format`** (package `contract/ledger`): how history is written. It covers the record line and its envelope, commits, the hash chain, the size caps, segment files, the `body_refs` key, and the F0 catalogue of four record types that Core's store writes.
- **`ledger-reader`** (also package `contract/ledger`): how history is checked and read back. It covers `Check`, the `Reader` and its cursor, the `Through` and `ToEnd` bounds, `ReadOptions.AllTypes` and `ParseSegmentName`.
- **`ledger-appender`** (package `contract/ledger/appender`): how a component adds records. It covers the `Appender` interface, its errors and its refusals.

Their text is the package documentation in [`contract/ledger/doc.go`](contract/ledger/doc.go) and [`contract/ledger/appender/appender.go`](contract/ledger/appender/appender.go), decided in [B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) (ticket 02) and [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) (ticket 03). The [freeze packet](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md) prints both in full in its §2.1 and §2.2.

**The freeze.** On 2026-10-10 the owner froze all three at version 1, at commit c8b1d93 ([D111](docs/DECISIONS.md#d111-f0-ledger-contracts-frozen-at-version-1)). His answer to OG2 also asked for a markdown at the repository root on the three parts' user needs, and for work to hold. This note is that markdown. From now on the three contracts change only by addition: new optional fields, new types, and new type versions with upgraders. A breaking change needs an owner decision entry that lists every step it affects ([D40](docs/DECISIONS.md#d40-contracts-between-roadmaps)).

D111 does not freeze Core's store policy: the 64 MiB body cap, the body layout, the collection policy, the command-index and pin bounds, the 5-minute command-ID bound and the segment target. It does not freeze the privacy-class wording or any later record family either. Where Core's store, not a contract, meets part of a need, the need says so under **Limits**.

### Users

**People and roles**, in the terms of the [glossary](GLOSSARY.md):

- **The colony owner** decides, approves and audits. He may act as owner stand-in for an officer in service loss ([D99](docs/DECISIONS.md#d99-officer-service-loss-outage-work-rules-owner-stand-in-and-office-handover) item 5). As the authenticated colony administrator he may inspect every colony record (story 164; "the owner as administrator sees them all", [D101](docs/DECISIONS.md#d101-officer-service-loss-procedure-confirmed-for-consolidation)).
- **Agents** act in their own private contexts. They are the officers (each Corps' Orchestrator, Griot and Diplomat, and their Grand counterparts) and the workers.
- Every record names the kind of party it is attributed to: `owner`, `agent`, `core` or `runner` ([D35](docs/DECISIONS.md#d35-record-format-and-evolution)).

**Software steps** build against a contract only once the owner has frozen it ([D20](docs/DECISIONS.md#d20-core-first-undercurrent-only-against-frozen-contracts), [D40](docs/DECISIONS.md#d40-contracts-between-roadmaps)). The freeze alone does not start them: "C2 still needs D52's approval, R1 needs R0, and U3's foundation needs C2" ([D111](docs/DECISIONS.md#d111-f0-ledger-contracts-frozen-at-version-1)).

| Step | In the owner's terms | Parts it uses | Sources |
|---|---|---|---|
| C2 | Workspaces and promotion | Format and appender, its C1 dependency; reader | [Status board](docs/ROADMAPS.md#status-board); [C2](docs/ROADMAP-CORE.md#c2-workspaces-observation-promotion-temporary-scratch); [register](docs/CONTRACTS.md#register) |
| C3 | Agent runtime, which wires Core's store into the host | All three; its prerequisites name "frozen ledger contracts", now met | [C3](docs/ROADMAP-CORE.md#c3-agent-runtime); [register](docs/CONTRACTS.md#register); [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities), Limits |
| C4–C6, C8, C9 | Validation, check governance, broker, Corps, workflows | Reader; their records go through Core's appender | [Register](docs/CONTRACTS.md#register); [D50](docs/DECISIONS.md#d50-one-core-owned-appender-serializes-colony-writes-q28) |
| C7 and the GUI views | Colony host with its terminal and GUI views | Reader and appender; the GUI reaches the ledger only through C7 | [C7](docs/ROADMAP-CORE.md#c7-colony-host-local-ipc-and-launcher-protocol); [D49](docs/DECISIONS.md#d49-durable-cursors-and-live-text-use-separate-streams-q27); [B11](docs/DECISIONS.md#b11-isolate-native-clients-and-operating-system-adapters); [spec decision 52](.scratch/dwarf-harness/spec.md#implementation-decisions) |
| R1 | Router events (R11) | All three; its R11 emission uses format and appender | [Status board](docs/ROADMAPS.md#status-board); [R1](docs/ROADMAP-ROUTER.md#r1-safe-per-turn-settings-provider-awareness-r11-events) |
| U1 (U1.L, U1.E) | Undercurrent law and engine | Format and reader | [Status board](docs/ROADMAPS.md#status-board); [U1](docs/ROADMAP-UNDERCURRENT.md#u1-law-and-engine) |
| U3 | Undercurrent coupling graph | Appender for its foundation; reader for its own frozen families | [Status board](docs/ROADMAPS.md#status-board); [register](docs/CONTRACTS.md#register) |

### How each need is written

Each need gives the **Need** in its users' terms; **Who** needs it; its **Sources**, as decision IDs and story numbers from the [system specification's user stories](.scratch/dwarf-harness/spec.md#user-stories); **Met by**, the frozen rule or API with its B18 or B21 item or package-doc wording; and **Limits**, what version 1 does not cover. Measurements come from one Windows machine against an assumed workload ([Core Appendix A.10](docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03)). None is a threshold, a pass mark or a capacity claim.

## 2. `ledger-format`

### F-1. Every record checks itself

- **Need:** Whoever reads history must be able to tell whether a record's bytes changed after they were written.
- **Who:** the owner and the administrator; every consumer that replays history (U1, R1, C2–C9).
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution); [D36](docs/DECISIONS.md#d36-integrity-and-retention); [stories 4, 83](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** a record is one line: `lowercase_hex(SHA-256(json))`, a TAB, `json`, LF. `json` is one compact UTF-8 object, and the hash covers exactly those bytes. "Readers check the bytes as stored and never re-marshal" ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 1; [package doc](contract/ledger/doc.go), "Records").
- **Limits:** damage confined to the final commit cannot be told from an interrupted write (F-4, X-2).

### F-2. One gapless order for the colony

- **Need:** Every consumer must see the colony's history in the same order with no gaps, so that two replays agree and a position in history means the same to everyone.
- **Who:** U1's engine; the terminal and GUI views, through their durable cursors; Core's projections; the owner.
- **Sources:** [D37](docs/DECISIONS.md#d37-ids-and-time) ("Each colony has a gapless integer sequence, the only order replay uses"); [D49](docs/DECISIONS.md#d49-durable-cursors-and-live-text-use-separate-streams-q27); [stories 4, 51, 82, 83](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** the required key `seq`, which starts at 1. A commit's records have consecutive `seq`. The writer assigns it; callers cannot (A-2). `Check` and the reader verify every record's sequence ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) items 1–2; [package doc](contract/ledger/doc.go), "Reading").
- **Limits:** the order is per colony. Ledgers of different colonies can be combined by colony ID ([D26](docs/DECISIONS.md#d26-one-core-and-one-ledger-per-colony)), but no order across colonies exists.

### F-3. Related records land together

- **Need:** When one change takes several records, consumers must see all of them or none, never half a change.
- **Who:** C2 (promotion records); C3 (a command with its effect; task transitions); C6 (a processing checkpoint with its outgoing messages); U3 (one logical graph version).
- **Sources:** [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [D35](docs/DECISIONS.md#d35-record-format-and-evolution) ("the last record of each commit is marked"); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage) ("One sequencer commits related records together"); [stories 46, 96](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** a commit's records share `commit_id`, `commit_count`, `event_time_ns` and `clock_held`. Their `commit_index` runs from 0 to `commit_count-1`, and the record with `commit_index == commit_count-1` ends the commit ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 2; [packet Q3](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q3-commit-marker)). Readers return whole commits only (R-3), and producers write a group as one commit (A-4).
- **Limits:** a commit holds at most 16 MiB (F-9). A commit is all-or-nothing inside the ledger only. It does not make a Git update or another outside effect atomic with its record ([B5](docs/DECISIONS.md#b5-order-cumulative-validation-durable-acceptance-and-disclosure), Limits).

### F-4. Lost, reordered or altered history is detectable

- **Need:** The owner must be able to detect a record removed, reordered or changed anywhere in history, not only damage inside one record.
- **Who:** the owner and the administrator; Core when it opens a colony; U1.
- **Sources:** [D36](docs/DECISIONS.md#d36-integrity-and-retention) ("Each record carries the previous record's hash from the start"); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage); [stories 4, 83](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** each record's `prev_hash` is the hash of the record before it, across commits and segments. The record with `seq` 1 carries `ZeroHash`, 64 zeros ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 2). `Check` and the reader verify the chain of every record they read (R-1, R-3).
- **Limits:** damage confined to the final commit, up to 16 MiB, cannot be told from an interrupted write until the latest hash is anchored outside the log. D36 defers that anchoring to assurance level A3, and the roadmaps name no step that delivers A3 ([ROADMAPS](docs/ROADMAPS.md#dependency-graph), notes on the edges).

### F-5. Honest time

- **Need:** Consumers that reason about time need event times that never go backwards. They also need to be told when Core held the time because the system clock moved back.
- **Who:** U1's engine, which evaluates only at recorded event times; cooldowns and limits that must survive clock rollback; the owner reading history.
- **Sources:** [D37](docs/DECISIONS.md#d37-ids-and-time); [stories 82, 112](.scratch/dwarf-harness/spec.md#user-stories); [spec decision 32](.scratch/dwarf-harness/spec.md#implementation-decisions).
- **Met by:** the required `event_time_ns`: UTC Unix nanoseconds, above 0, never decreasing. The writer stamps one time per commit. If the clock reads earlier than the previous commit's time, the writer reuses that time and sets `clock_held` on every record of the commit; `clock_held` is never on `seq` 1 ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 2; [package doc](contract/ledger/doc.go), "Commits and the chain").
- **Limits:** the event time is when Core wrote the commit. "Producers keep their own observed times in their payloads" (B18 item 2). Durations come from the monotonic clock, not from event times (D37).

### F-6. Who acted, in which context, and how it was seen

- **Need:** Each record must say which kind of party it is attributed to and, where it applies, which agent, session, context, turn and operation it belongs to. It must also say how the host observed the fact, so that history is never built from an agent's report alone.
- **Who:** the owner and the administrator; C3, which binds work to contexts and marks interrupted operations; U1, whose sources must come from host-observed effects.
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution); [D37](docs/DECISIONS.md#d37-ids-and-time) ("In-flight work is a start and an end sharing an operation ID"); [ROADMAP-CORE C1](docs/ROADMAP-CORE.md#c1-action-ledger-and-body-store) (typed deltas, "never from an agent's report alone", D15; the measurement path "distinguishes host observation from report"); [stories 26, 87](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** the required `actor`, one of `owner`, `agent`, `core` and `runner`: "The closed set of actors (D35). Adding one is a format change." The optional `agent_id`, `execution_session_id`, `context_id`, `turn_id` and `operation_id`, which "links a start and its end". The optional `measurement_path`, whose vocabulary each record family owns ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 1; [package doc](contract/ledger/doc.go), "Records").
- **Limits:** these keys are optional in the envelope. Each record family's rules say which it requires, and no family beyond F0 is frozen yet. Core checks a draft's actor, context, operation and target against the current grant before it appends ([B7](docs/DECISIONS.md#b7-use-bound-core-transitions-for-contexts-tasks-grants-and-broker-state)); the contract does not. Writing an `interrupted` end for every unmatched start after a crash is C3's work ([C3](docs/ROADMAP-CORE.md#c3-agent-runtime)). D35 names `runner` among the actor kinds and no document defines it; this note does not interpret it.

### F-7. Which colony

- **Need:** Each record must say which colony it belongs to, so that ledgers of different colonies can be combined for the owner's experiments and a reader never mixes colonies.
- **Who:** the owner; every reader.
- **Sources:** [D26](docs/DECISIONS.md#d26-one-core-and-one-ledger-per-colony); [D37](docs/DECISIONS.md#d37-ids-and-time).
- **Met by:** the required `colony_id`, a UUIDv7, on every record. `Check` and `OpenReader` take the colony's ID, and a record of another colony fails closed. A colony ID that is not a UUIDv7 is refused with `ErrInvalid` ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) items 1, 3 and 4; `ReadOptions.Colony`: "required; every record must carry it").
- **Limits:** version 1 has no tool that combines ledgers.

### F-8. Room to grow without rewriting history

- **Need:** Later steps must be able to add keys, record types and type versions without rewriting a stored record. An old reader must stop clearly on what it cannot read, not misread it.
- **Who:** every later producer (C2–C9, R1, U3); every consumer built on version 1; the owner.
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution) ("a written record is never rewritten. New fields are optional, and a reader treats a missing field as absent, never as a default"); [D40](docs/DECISIONS.md#d40-contracts-between-roadmaps); [stories 4, 83](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:**
  - `format` is always the first key. A correctly hashed record of a later format stops a reader with `*UnsupportedFormatError` instead of failing as corruption ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 1; [packet Q2](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q2-envelope-keys-order-and-encodings)).
  - `type` and `type_version` name each record's family and version.
  - Keys this version does not know are ignored, "so the envelope can gain optional keys by addition"; `Record.JSON` keeps them. An absent optional key reads as absent: "no default is ever guessed" ([package doc](contract/ledger/doc.go), "Records").
  - `body_refs` was added this way: the two golden segments of ticket 02 did not change ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 1).
- **Limits:** each family writes its own upgraders when it adds a type version (D35); every F0 type is at version 1. Some changes break the format and need an owner decision entry: changing a cap, adding an actor, renaming envelope keys, or recognising compressed segments ([packet §3.4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#34-capacity-reasoning-d78); [packet Q5](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q5-strictness-of-the-envelope)).

### F-9. Bounded sizes

- **Need:** A reader on a modest laptop needs a known limit on how much one record or commit can make it hold. Producers need a clear rule that large content goes elsewhere.
- **Who:** every reader (U1, Core at startup, the views through C7); producers of large content (C2's diffs, C4's run logs, C6's message bodies).
- **Sources:** [D79](docs/DECISIONS.md#d79-literature-grounded-authority-guidance-and-modest-laptop-efficiency) (modest laptops); [D33](docs/DECISIONS.md#d33-what-the-colony-log-holds) ("Message bodies, diffs and run logs go in the body store"); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("A record/commit over a cap is rejected before publication; large content belongs in the body store").
- **Met by:** `MaxRecordBytes` is 1 MiB per line, counting the hash, TAB and LF. `MaxCommitBytes` is 16 MiB per commit. A line or commit of exactly the cap is accepted. `EncodeCommit` refuses anything larger with `ErrTooLarge`, and `Check` and the reader fail closed on it, "which bounds a reader's memory to one commit" ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 2; [packet Q4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q4-caps)).
- **Limits:** Core §13.1 calls these caps provisional, to be calibrated; version 1 fixes them. Raising either now breaks the format, because older readers reject larger records, so it needs an owner decision entry (B18 item 2; D40). The 64 MiB segment target is writer policy, not a format rule.

### F-10. Predictable segment files and an honest torn tail

- **Need:** The log must be split into files that any tool can list and verify in order. A write cut off by a crash must be recognisable as exactly that, so that recovery removes it without ever accepting damage elsewhere.
- **Who:** Core, which checks the log and removes a torn tail when it opens a colony; the owner; U1; tools that list segments (R-8).
- **Sources:** [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log) (segments named by the sequence of their first record; "Recovery truncates a half-written final commit"); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3); [stories 3, 4](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:**
  - A segment's name is its first `seq` as 20 zero-padded digits plus `.jsonl`. The first is `00000000000000000001.jsonl`; each later one is named by the previous segment's last `seq` plus 1. A segment holds whole commits, and only the newest may be empty or end in a torn commit. Names beginning with a dot are ignored; any other entry that is not a segment file fails closed ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 3).
  - Trailing bytes count as a torn final commit only if they total at most 16 MiB and every correctly hashed line among them fits the one commit that could have been in flight. "A correctly hashed line after damaged bytes therefore fails closed" ([package doc](contract/ledger/doc.go), "Torn tails and failing closed").
- **Limits:** damage confined to the final commit cannot be told from an interrupted write (F-4). Compressed segments, which [D36](docs/DECISIONS.md#d36-integrity-and-retention) allows for old segments, are not recognised: they need a name rule and an owner decision entry (B18 item 3; [packet §3.4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#34-capacity-reasoning-d78)). Core's store, not a contract, truncates the torn tail when it opens a colony (B18 item 6).

### F-11. Large content by reference

- **Need:** A record must be able to point to large content, such as a diff, a run log or a message body, by its content hash. The pointers must sit in one place that every tool can find, so that publication can be checked and every body's references found.
- **Who:** C2 (diffs), C4 (run logs), C6 (message bodies); Core's publication check and its collection; the owner, who orders collection.
- **Sources:** [D33](docs/DECISIONS.md#d33-what-the-colony-log-holds); [D35](docs/DECISIONS.md#d35-record-format-and-evolution) (body references in the envelope); [D36](docs/DECISIONS.md#d36-integrity-and-retention) (SHA-256 for bodies); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3); [story 5](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** the optional envelope key `body_refs`, between `measurement_path` and `payload`. It is a non-empty, strictly ascending list of SHA-256 digests in lowercase hex, none of them zero, so each set has one byte form. `EncodeCommit` refuses an unsorted, repeated or zero digest. Readers fail closed on `[]`, `null`, uppercase or short hex, a zero digest, and an unsorted or repeated list. These are the record's only body references: "a hash inside a payload, such as a private-content hash, a check digest or a Git object ID, is not one" ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 1; [package doc](contract/ledger/doc.go), "Records"; [packet Q8](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q8-what-body_refs-means)).
- **Limits:** readers do not check payload interiors. A family whose payload names a stored body must also list it in `body_refs` and say so in its own payload rules, and each later family's freeze must do this. The body store itself, with its layout, its 64 MiB body cap and the check of each body on read, is Core's store and not frozen (B21 items 4–5; D111).

### F-12. Keeping bodies that are still needed

- **Need:** A producer that still needs a body, for an unfinished finalization, a failure under investigation or an owner's hold, must be able to keep it from collection. The hold must survive a restart and show in history.
- **Who:** C4 (finalization and failure obligations); the owner (holds); Core's collection.
- **Sources:** [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) (collection is "an explicit operator policy over reachability, active pins and legal/operational retention"); [D36](docs/DECISIONS.md#d36-integrity-and-retention); [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 6.
- **Met by:** `ledger.body_pin` v1 holds `pin_id`, a UUIDv7, and pins the bodies its `body_refs` lists until a `ledger.body_unpin` v1 with the same `pin_id`. Record rules: a pin lists bodies, all published when it was written, under a pin ID not already active. An unpin lists no bodies and names an active pin. A released pin ID may start a new pin (B21 item 2; [package doc](contract/ledger/doc.go), "F0 record types"). The permitted appender callers set pins through `Append`, which refuses a pin or unpin that breaks a rule (A-7).
- **Limits:** who may release whose pin is C3's (B21 item 6), and what C4 pins for finalization is C4's (B21, Limits). The bound of 65,536 active (pin, body) pairs is Core policy, not frozen (B21 item 10). One rule cannot be checked when a log opens: that a pin's bodies were published when it was written. Core's own writers cannot break it (B21 item 10).

### F-13. Removed bodies stay in history

- **Need:** When bodies are removed to save disk, history must still say which bodies went, how large they were, who ordered it and under which policy. A removed body must then read as removed, never as damage, and its records must still stand.
- **Who:** the owner, who orders collection; Core's body reads; C4's `artifact_unavailable`; any view that shows bodies.
- **Sources:** [D36](docs/DECISIONS.md#d36-integrity-and-retention) ("Bodies may be garbage-collected under an operator policy"); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("An absent pruned body is recorded as unavailable, not as though its event never existed"); [story 5](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `ledger.bodies_collected` v1 holds `before_ns`, the policy's age cutoff; `keep_bytes`, the policy's optional byte target; and `bodies`, a non-empty list of `{"sha256", "bytes"}` objects, strictly ascending by digest. Record rules: no `body_refs`; the actor is `owner` or `core`; it lists only bodies that an earlier record referenced and that no active pin held. Only Core's store writes it, during an explicit collection, and `Append` refuses it ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 2; [package doc](contract/ledger/doc.go), "F0 record types").
- **Limits:** when and what to collect is Core's store policy, not frozen. "Nothing is collected unless the owner or Core asks, with an explicit policy: an actor, an age cutoff and an optional byte target" ([packet Q11](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q11-for-information-not-frozen-the-body-store-and-core-policy); B21 item 7). Telling a collected body from a missing one on read is Core's store too (`ReadBody`, B21 item 5). C4 decides which bodies are diagnostic, their age and byte policy, and the `artifact_unavailable` projection (B21, Limits). One rule cannot be checked when a log opens: that a collected body was referenced by an earlier record (B21 item 10).

### F-14. One record per command

- **Need:** A caller who repeats a command, after a crash, a timeout or a reconnect, must get the original outcome and never a second effect, however old the command. A repeat that changed the request under the same ID must be refused.
- **Who:** agents and the owner issuing commands; the terminal and GUI views that retry them; C2's promotion requests; C3's command receipts.
- **Sources:** [D58](docs/DECISIONS.md#d58-deduplicate-promotion-submissions-by-request-id-not-content-q35); [D37](docs/DECISIONS.md#d37-ids-and-time); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage) ("D58 deduplication from the authoritative segment catalogue on a hot-cache miss"); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("An evicted in-memory index is never proof that an old command ID is absent"); [stories 6, 46, 50, 122](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `ledger.command` v1, D58's identity tombstone. It holds `command_id`, the caller's UUIDv7, and `submission_sha256`, the SHA-256 of the caller's canonical submission bytes, and no request content. Record rules: it is the record at `commit_index` 0 of the commit that holds the command's effect; a commit holds at most one, and a log at most one per `command_id`; it has no `body_refs`. Only Core's store writes it, and `Append` refuses it ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) items 2, 8 and 9; [package doc](contract/ledger/doc.go), "F0 record types"). The freeze fixes two of Q9's settlements as record rules: a log holds at most one record per `command_id`, and only the digest is compared. That a recorded ID resolves for the life of the log is Core's store behaviour, which follows from a log that is never compacted (B21 items 8 and 9; [packet Q9](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q9-command-identities-what-c1-settles-of-d58)).
- **Limits:**
  - "Only the digest is compared", so the caller's digest must cover the request's content, its candidate and its context. Otherwise D58's mismatch rule is hollow (package doc).
  - The canonical submission bytes and fields, the request schema, result delivery, and who may retrieve a duplicate's state are C3's (D58, "Still open"; B21 item 9).
  - The lookup belongs to Core's store: `Submit` looks an ID up in an in-memory index of 65,536 IDs, then in the segments. That, and the 5-minute bound on how far an ID may be dated ahead of its commit, are Core policy, not frozen (D111). Measured on one machine: repeating a first-day command in a 7-day, 20-context colony took 465 ms at the median and 1,020 ms at most ([A.10](docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03), "Command-ID lookups").
  - One rule cannot be checked when a log opens: that an ID older than the in-memory index appears only once. Core's own writers cannot break it (B21 item 10).

### F-15. One catalogue, reserved names and golden samples

- **Need:** Producers and consumers need one list of the record types a writer may append, type names that cannot collide, and stored samples that prove the bytes have not changed.
- **Who:** later producers adding families (C2–C9, R1, U3); consumers testing against version 1; the owner, whose freeze the samples protect.
- **Sources:** [D40](docs/DECISIONS.md#d40-contracts-between-roadmaps) (golden samples for every type version; "the golden tests catch one made without it"); [B2](docs/DECISIONS.md#b2-stage-producer-ready-contracts-and-split-u1-capabilities) (what each catalogue entry records).
- **Met by:**
  - `Defined()` returns the F0 catalogue: `ledger.command`, `ledger.body_pin`, `ledger.body_unpin` and `ledger.bodies_collected`, each at version 1. `NewCatalogue` refuses a malformed type, a version below 1 and a repeated entry ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 5; [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 2).
  - The namespace `ledger.` is reserved for F0 types, and `fixture.` for the contract's own fixtures, which `Defined()` never contains ([package doc](contract/ledger/doc.go), "Catalogue").
  - The register's [F0 catalogue](docs/CONTRACTS.md#producer-ready-family-freezes-b2) gives each type B2's attributes: payload owner, producer, permitted appender caller, consumers, privacy class, replay-essential fields and golden sample.
  - Three golden files, recorded with their SHA-256 digests in D111 ([packet §2.4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#24-golden-files)), are rebuilt byte for byte by `TestGoldenF0` (packet §3.1, V2).
- **Limits:** every later family joins through its own additive catalogue freeze once its producer specifies it (B2; D111). Core's store refuses types outside the catalogue it is given, and production callers must pass `ledger.Defined()`. The check cannot enforce that rule, so C3's review verifies it (B18 item 5).

### F-16. Synthetic ledgers without Core

- **Need:** Undercurrent's early work and every step's tests need to build valid ledgers without running Core, so that the field engine can be proven on synthetic history before real data exists.
- **Who:** U1 and U2; test authors in every step; the golden generator; the workload generator of A.10.
- **Sources:** [D20](docs/DECISIONS.md#d20-core-first-undercurrent-only-against-frozen-contracts) (Undercurrent's early work uses synthetic ledgers); [D41](docs/DECISIONS.md#d41-several-agents-in-one-repository) ("tests may use stand-in data only against a frozen contract"); [U2](docs/ROADMAP-UNDERCURRENT.md#u2-synthetic-ledgers-simulation-adversarial-suite) ("Synthetic ledgers are stand-in data against the frozen `ledger-format`").
- **Met by:** `EncodeCommit(after, header, drafts)` is "the one encoder" and a pure function. It assigns `seq`, `prev_hash` and the commit fields, returns the lines and the new chain position, and refuses any commit a reader would reject. `Genesis()` gives an empty log's position, and `SegmentName` names the files ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 2; [package doc](contract/ledger/doc.go), "Commits and the chain"). A.10's generator wrote each of its colonies through it, the largest with 17.4 GiB of log ([A.10](docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03), "Colonies").
- **Limits:** `EncodeCommit`, `Check` and the reader do not apply F0's record rules; Core's `Open` does ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) items 2 and 10). A synthetic log can therefore pass `Check` and still be refused by Core. Writing and flushing the files is the test's own job.

## 3. `ledger-reader`

### R-1. Check a whole log before trusting it

- **Need:** Before a log is used, at Core's start or by the owner offline, someone must be able to verify all of it, learn exactly where it ends, and be told where any damage lies.
- **Who:** Core, which runs `Check` before it changes any byte when it opens a colony ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 6); the owner and the administrator; U1.
- **Sources:** [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("corruption or a gap before the tail fails closed"); [stories 3, 4](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `Check(logDir, colony)` verifies the whole log read-only: "It never writes, truncates or locks." It returns `CheckResult{Last, Segment, Size, TornBytes}`: where the last complete commit ends, and how many torn bytes follow it. It fails closed with `*CorruptError{Segment, Offset, Reason}`, or with `*UnsupportedFormatError`, on anything but complete, correctly hashed and chained commits in order, followed by at most one torn final commit (B18 item 3; [package doc](contract/ledger/doc.go)).
- **Limits:** `Check` reads the whole log, so its time grows with history: 219 s for the 12.1 GiB log of 7 days at 60 contexts, from the file cache ([A.10](docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03), "Recovery, warm"). It does not apply F0's record rules ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 2). It cannot tell final-commit damage from an interrupted write (F-4).

### R-2. Replay with no effects, from outside Core

- **Need:** A consumer must be able to replay history without changing anything, without Core's lock or code, and while Core keeps writing.
- **Who:** U1's engine, which is a standalone, standard-library consumer of the ledger; C5's check registry, derived from ledger events; Core's projections; the owner's offline tools.
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution); [D40](docs/DECISIONS.md#d40-contracts-between-roadmaps) (contracts use only the standard library); [spec decision 32](.scratch/dwarf-harness/spec.md#implementation-decisions); [spec testing decision 4](.scratch/dwarf-harness/spec.md#testing-decisions) ("replay does not issue execution, provider or external-tool work"); [stories 82, 83](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** "A Reader opens files read-only and never writes, truncates, renames or locks, so replay has no effects and may run while a writer holds the colony" ([package doc](contract/ledger/doc.go), "Reading"; [B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 4). `contract/ledger` imports only the standard library ([packet §3.1](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#31-checks), V6).
- **Limits:** a `Reader` is "not safe for concurrent use", so each concurrent consumer opens its own. The reader causes no effects, but each consumer must still not act on what it replays (spec testing decision 4).

### R-3. Whole commits, every record verified

- **Need:** A consumer must see only whole commits. Every record it reads, used or skipped, must have been verified, so that it never builds state on half a change or on damaged bytes.
- **Who:** every consumer that replays history; U1 and U3, which need whole graph versions; Core's projections.
- **Sources:** [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("publishing a durable cursor only after a whole commit"); [stories 4, 96](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `Next` returns whole commits only, "verifying every record it reads, consumed or not: hash, chain, sequence, commit framing, colony and time". A torn final commit is never returned, and "Errors are final" ([package doc](contract/ledger/doc.go)). Memory is never sized from `commit_count`, and the caps bound it to one commit ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) items 2 and 4).
- **Limits:** a reader that resumes verifies only the link into its first segment: "Whole-log verification is Check's job" (package doc). The reader does not apply F0's record rules.

### R-4. Read what I understand, skip the rest, stop clearly

- **Need:** Each consumer must be able to name the record types and versions it understands and skip all others safely. On a version it does not know, it must stop with a clear error, never guess.
- **Who:** every reader the register names: C2–C9, R1, U1, and U3 for its own families.
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution) ("Each reader declares the types it consumes and skips the rest. A consumed type that a reader cannot decode stops it with a clear error"); [story 4](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `ReadOptions.Consume` maps each consumed type to the versions the reader decodes. Records of other types are skipped, and `Cursor` still moves past them. A consumed type at an undeclared version stops the reader with `*UnsupportedVersionError{Seq, Type, Version, Supported}` ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 4; [package doc](contract/ledger/doc.go), "Reading").
- **Limits:** the reader returns payloads undecoded. F0's decoders are in the package; each later family brings its own decoders and upgraders (D35).

### R-5. Resume where I stopped

- **Need:** A view, the router or the engine must be able to save its place in history and, after a restart, carry on from exactly there without missing or repeating a commit.
- **Who:** the terminal and GUI views, through C7; R1; U1; Core's projections.
- **Sources:** [D49](docs/DECISIONS.md#d49-durable-cursors-and-live-text-use-separate-streams-q27) ("Durable view cursors use the colony-wide ledger sequence"); [ROADMAP-CORE C1](docs/ROADMAP-CORE.md#c1-action-ledger-and-body-store) ("launchers, router consumers and Undercurrent each hold their own" durable cursor); [stories 51, 119](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `Cursor()` returns the last sequence of the last commit fully processed, whether skipped or returned, and stays there on any error; "A consumer may persist it as its next After". A reader resuming after a cursor starts in the segment that holds the next record, and first verifies the link from the previous segment's last line. A bound that falls inside a commit returns `ErrCursor` ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 4; [package doc](contract/ledger/doc.go)).
- **Limits:** storing the cursor is the consumer's job. The views get theirs through C7's launcher protocol, which recovers a gap from a fresh authorized snapshot ([spec decision 53](.scratch/dwarf-harness/spec.md#implementation-decisions)). Live model text is not in the ledger and cannot be resumed from it (D49).

### R-6. Never read an unacknowledged commit

- **Need:** Inside a running colony, nothing may be shown or acted on before the writer has acknowledged it, because an unacknowledged commit can still disappear.
- **Who:** the views (C7 and the GUI); Core's projections; the owner and agents, who rely on receipts.
- **Sources:** [story 3](.scratch/dwarf-harness/spec.md#user-stories); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("expose its sequence/cursor only after durability succeeds"); [D49](docs/DECISIONS.md#d49-durable-cursors-and-live-text-use-separate-streams-q27).
- **Met by:** the bound is explicit. "Inside a running Core, ReadOptions.Through must be the writer's durable mark." `ToEnd` "is for offline reading: while a writer is live it may expose a commit the writer has not yet acknowledged" ([package doc](contract/ledger/doc.go); [B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 4; [packet Q7](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q7-the-readers-bound-and-its-two-additions)).
- **Limits:** the contract cannot enforce this rule. A consumer inside Core must pass the durable mark, which Core's store provides as `Durable()` (B18 item 4); that method is not part of the frozen contracts. C3 wires the store into the host ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities), Limits).

### R-7. See every record's envelope (`AllTypes`)

- **Need:** Some consumers must see envelope facts of every record, above all which bodies it references, without decoding the payloads of families they do not own.
- **Who:** Core's collection, which reads the log with `AllTypes` to find every reference before it removes a body ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 7); any consumer that needs envelope fields of records it does not decode.
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution) (readers skip what they do not consume); [D36](docs/DECISIONS.md#d36-integrity-and-retention); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) (collection is a policy "over reachability"); B21 items 3 and 7.
- **Met by:** with `ReadOptions.AllTypes`, the reader also returns the records of types absent from `Consume`, "for their envelope fields, such as body_refs. Their versions are not checked, and their payloads are not the consumer's to decode". Types in `Consume` are still version-checked (B21 item 3; [package doc](contract/ledger/doc.go), "Reading").
- **Limits:** a consumer may rely only on the envelope of a record it does not consume. It decodes only the payloads of the versions it consumes, "so that skipping an unknown type stays safe" (package doc).

### R-8. Name segments as the reader does

- **Need:** A program that lists the log directory must map each segment file to its first sequence by exactly the reader's rule, so that it never disagrees with the reader about which file holds which records.
- **Who:** Core's store, which lists the log directory for its segment catalogue of old command IDs and for classifying missing bodies ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) items 5 and 8); offline tools.
- **Sources:** [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 3; B21 items 3, 5 and 8.
- **Met by:** `ParseSegmentName(name) (first uint64, ok bool)`, "the inverse of SegmentName". It returns false for a name that is not 20 decimal digits followed by `.jsonl`, or whose digits exceed `uint64` (B21 item 3; [package doc](contract/ledger/doc.go)).
- **Limits:** compressed segment names are not recognised (F-10).

## 4. `ledger-appender`

### A-1. Acknowledged means durable

- **Need:** A producer, and anyone who reads its receipt, must be able to rely on an acknowledged write surviving a crash or a restart.
- **Who:** every producer (C2, C3, R1, U3 and Core itself); the owner and agents, who rely on receipts; C3's command receipts, which cite the ledger sequence ([C3](docs/ROADMAP-CORE.md#c3-agent-runtime)).
- **Sources:** [story 3](.scratch/dwarf-harness/spec.md#user-stories); [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log) (each commit "is one write followed by a flush"); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3); [spec decision 6](.scratch/dwarf-harness/spec.md#implementation-decisions) ("committed durability before acknowledgment").
- **Met by:** "An Append is acknowledged only after its commit is completely written and durably flushed." `Append` returns `Committed{First, Last}`, the sequence range of the acknowledged commit ([appender package doc](contract/ledger/appender/appender.go)). Core's writer advances its chain and durable mark only after the flush succeeds ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 6).
- **Limits:** this is not a power-loss guarantee. It relies on the file system and the device honouring their flushes ([B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage)). It is verified on Windows and NTFS on one machine. Linux and macOS are compiled and vetted only until C3's CI exists ([D42](docs/DECISIONS.md#d42-check-command-and-ci)), and other file systems and network shares are unverified ([packet §3.2](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#32-platform-status-and-limits)).

### A-2. Callers cannot choose order, time or colony, or touch the files

- **Need:** No component may choose its own sequence number, time, colony or place in the chain, or write ledger files directly, so that no component can bend history.
- **Who:** the owner; every consumer that relies on order and time (F-2, F-5).
- **Sources:** [D50](docs/DECISIONS.md#d50-one-core-owned-appender-serializes-colony-writes-q28) (the appender "assigns the colony sequence"); [D26](docs/DECISIONS.md#d26-one-core-and-one-ledger-per-colony) (Core is "the sole writer of that colony's ledger"); [spec testing decision 4](.scratch/dwarf-harness/spec.md#testing-decisions) ("callers cannot choose sequence numbers").
- **Met by:** "A caller supplies ledger.Draft values, which carry no sequence, hash, time, colony or commit field; the writer assigns all of them, so a caller can neither write ledger files nor choose sequences" ([appender package doc](contract/ledger/appender/appender.go)).
- **Limits:** a draft's actor, IDs and payload come from the caller. Core checks actor, context, operation and target against the current grant before it appends ([B7](docs/DECISIONS.md#b7-use-bound-core-transitions-for-contexts-tasks-grants-and-broker-state)). The exclusive colony lock that keeps a second writer out belongs to Core's store, not to the contract ([B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 6).

### A-3. A clear answer when a write fails

- **Need:** When a write fails, the producer must know whether anything was written and whether a repeat can help. Otherwise it may lose a real effect or repeat one.
- **Who:** every producer; C2, which reconciles interrupted promotion work without running it again; C3, which marks interrupted operations; the owner.
- **Sources:** [stories 16, 26](.scratch/dwarf-harness/spec.md#user-stories); [D55](docs/DECISIONS.md#d55-after-a-crash-reconcile-but-do-not-restart-promotion-execution-q32); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) ("A short write, failed flush or uncertain publication poisons that appender incarnation"); [packet Q6](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q6-the-appenders-errors).
- **Met by:** every failure says what became of the commit ([appender package doc](contract/ledger/appender/appender.go); [B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 6):
  - `ErrRefused`: nothing was written, and the same request fails again. A refusal for size also matches `ledger.ErrTooLarge`.
  - `ErrPoisoned`: nothing was written; the writer needs recovery.
  - `ErrUncertain`: the commit may or may not survive, and the writer is now poisoned. "A caller that needs certainty looks the commit up after recovery."
  - `ErrClosed`: nothing was written.
  - The context's error: nothing was written. The context "bounds only that wait: once writing starts, the commit completes or fails whatever ctx does."
  - A refused, poisoned, closed or cancelled call uses no sequence number (B18 item 6).
- **Limits:** "the same request fails again" holds for the same store state. A refusal for an unpublished body or an inactive pin ends when that state changes (packet Q6). Recovery is not in the contract: `Recover` is a method of Core's store, and C3 decides when to call it (B18, Limits). The lookup after `ErrUncertain` uses the reader.

### A-4. Several records in one commit

- **Need:** A producer must be able to write a group of related records as one commit.
- **Who:** C2; C3 (a command with its effect); C6 (a checkpoint with its outgoing messages); U3 (a graph version).
- **Sources:** [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage); [B7](docs/DECISIONS.md#b7-use-bound-core-transitions-for-contexts-tasks-grants-and-broker-state) (Core "durably appends the bounded event group"); [spec decision 16](.scratch/dwarf-harness/spec.md#implementation-decisions); [story 96](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** `Append(ctx, drafts)` "commits drafts, in order, as one commit", acknowledged only whole ([appender package doc](contract/ledger/appender/appender.go)). The format frames the group (F-3).
- **Limits:** a commit holds at most 16 MiB (F-9). A command's tombstone and its effect are written together by Core's `Submit`, a store method outside the contract ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 8).

### A-5. One writer, one order

- **Need:** Many components write at once. Their commits must go into one order with no gaps, and each producer must know what is promised about when its turn comes, and what is not.
- **Who:** every producer; the owner; every consumer (F-2).
- **Sources:** [D50](docs/DECISIONS.md#d50-one-core-owned-appender-serializes-colony-writes-q28) ("One in-process appender per colony serializes appends and assigns the colony sequence"); [D37](docs/DECISIONS.md#d37-ids-and-time); [story 49](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** "Core's writer is its only implementation." "The writer serializes commits and promises no fairness among waiting callers" ([appender package doc](contract/ledger/appender/appender.go)).
- **Limits:** no fairness, wait time or priority is promised. Priority for control commits, such as a shutdown or a grant revocation under heavy traffic (AC06), goes on Core's writer "as a separate method or interface, so that this contract does not change for it" ([appender package doc](contract/ledger/appender/appender.go)); that work is [ticket 28](.scratch/dwarf-harness/issues/28-keep-shutdown-and-revocation-admissible-under-saturated-social-traffic.md).

### A-6. References only to published bodies

- **Need:** No record may reference a body that is not there, so that every reference can be followed.
- **Who:** producers of bodies (C2, C4, C6); readers of bodies; the owner.
- **Sources:** [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3) (bodies are published "before any ledger event can reference them"); [D36](docs/DECISIONS.md#d36-integrity-and-retention); [story 5](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** "A draft's BodyRefs may name only bodies already published to the colony's body store. The writer refuses, with ErrRefused, a draft that names an unpublished body […]" ([appender package doc](contract/ledger/appender/appender.go); [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 3).
- **Limits:** publishing a body (`PublishBody`) is a method of Core's store and is not frozen (B21 item 4). The check is for existence only: publication hashed the body, and every read verifies it (B21 item 6). A body collected after its producer published it, but before its `Append`, is refused; the producer publishes it again and retries (B21 item 6).

### A-7. Store-owned records stay store-owned

- **Need:** The records that prove deduplication and body removal must come only from Core's store, and every pin and release must keep its rules, so that their history can be trusted.
- **Who:** the owner; the consumers of command identity and of collection history (F-13, F-14).
- **Sources:** [D58](docs/DECISIONS.md#d58-deduplicate-promotion-submissions-by-request-id-not-content-q35); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3); [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) items 2, 3 and 6.
- **Met by:** the writer refuses, with `ErrRefused`, a draft that "breaks a rule of its record type, including the F0 types that only the writer itself produces (ledger.command and ledger.bodies_collected)" ([appender package doc](contract/ledger/appender/appender.go)).
- **Limits:** `Submit` and `Collect`, the store methods that write those two types, are outside the contract and not frozen (B21 items 7 and 8). Who may release whose pin is C3's (B21 item 6).

### A-8. Only permitted components append

- **Need:** Only Core, the router and Undercurrent's graph service may add records. Agents and other components may not.
- **Who:** the owner; agents, whose actions Core records for them.
- **Sources:** [D50](docs/DECISIONS.md#d50-one-core-owned-appender-serializes-colony-writes-q28) (only `internal/core/...`, `internal/router/...` and `internal/undercurrent/graph/...` "may import/use that appender contract"); [spec testing decision 4](.scratch/dwarf-harness/spec.md#testing-decisions) ("only permitted components can append").
- **Met by:** the import-boundary step of `go run ./tools/check` fails any other package that imports `contract/ledger/appender` ([B17](docs/DECISIONS.md#b17-c0-tool-pins-check-steps-and-import-table-encoding) item 4). The [register](docs/CONTRACTS.md#register) names those three trees as the appender's only users.
- **Limits:** an import rule constrains this repository's code; it does not stop a process from writing the files. Protecting the ledger from hostile child processes is C10's boundary, which is not built yet ([C10](docs/ROADMAP-CORE.md#c10-isolation-boundary-and-assurance-a2)).

## 5. Shared needs

### X-1. Durability and crash recovery

- **Need:** After a crash or a killed process, everything acknowledged must still be there. A cut-off final write must be removed, and nothing else may be dropped or guessed.
- **Who:** the owner; every producer and consumer; spending holds that must survive a restart; allowances, limits and cooldowns that must survive a crash.
- **Parts:** format (the torn-tail rule, F-10), reader (`Check`, R-1), appender (acknowledgement after the flush and clear failures, A-1 and A-3).
- **Sources:** [stories 3, 4, 26, 69, 95, 112](.scratch/dwarf-harness/spec.md#user-stories); [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3).
- **Met by:** F-10, R-1, A-1 and A-3. Evidence at c8b1d93, 20 runs each: a killed writer releases its lock and keeps every acknowledged commit, and commands submitted around a crash are recorded once ([packet §3.1](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#31-checks), V4). In A.10's kill run the writer had reported sequence 3,264,644 as acknowledged. After `Open` the log ended at sequence 3,264,948, and no torn bytes had to be truncated ([A.10](docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03), "Recovery after a kill").
- **Limits:** there is no power-loss guarantee (B3), and A.10's restart was a clean shutdown, not a power loss. Only Windows and NTFS on one machine are verified; the two killed-child tests run there only ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 12). Linux and macOS are compiled and vetted only until C3's CI exists ([D42](docs/DECISIONS.md#d42-check-command-and-ci)). Other file systems and network shares are unverified.

### X-2. Integrity: append-only and tamper-evident

- **Need:** The owner must be able to trust that history is complete, in order and unaltered, and that a written record is never rewritten. A correction is a new record.
- **Who:** the owner and the administrator; reviewers and human oracles, whose corrections must not erase the original; every consumer.
- **Parts:** format (the record hash and the chain), reader (`Check`; every record read is verified), appender (append only).
- **Sources:** [D35](docs/DECISIONS.md#d35-record-format-and-evolution) ("a written record is never rewritten"); [D36](docs/DECISIONS.md#d36-integrity-and-retention) ("The log is never deleted or compacted"); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage) ("Preserve exact record bytes, hash-chain validation"); [stories 4, 41, 83, 114](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** F-1, F-4, R-1 and R-3. The appender's only operation is `Append`. The fuzz targets `FuzzCheckTruncation`, `FuzzCheckTornTail` and `FuzzCheckCorruption` ran 120 s each with no failure ([packet §3.1](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#31-checks), V3).
- **Limits:** damage confined to the final commit, up to 16 MiB, cannot be told from an interrupted write until the latest hash is anchored outside the log (D36, assurance A3; see F-4). `Check` and the reader do not apply F0's record rules; Core's `Open` checks the ones the records show ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) items 2 and 10). Bodies are verified on read by Core's store (B21 item 5), not by the contracts.

### X-3. Compatibility under D40

- **Need:** Steps that build against the contracts need them to stay compatible: what works on version 1 must keep working as the contracts grow.
- **Who:** C2, C3, R1, U1, U3 and every later consumer; the owner, who alone approves a breaking change.
- **Parts:** all three.
- **Sources:** [D40](docs/DECISIONS.md#d40-contracts-between-roadmaps); [D20](docs/DECISIONS.md#d20-core-first-undercurrent-only-against-frozen-contracts); [D35](docs/DECISIONS.md#d35-record-format-and-evolution); [D111](docs/DECISIONS.md#d111-f0-ledger-contracts-frozen-at-version-1); [CONTRACTS.md, Rules](docs/CONTRACTS.md#rules); [B2](docs/DECISIONS.md#b2-stage-producer-ready-contracts-and-split-u1-capabilities).
- **Met by:** the freeze at version 1, at c8b1d93 (D111). From now on the contracts change only by addition: "new optional fields, new types, and new type versions with upgraders". A breaking change needs an owner decision entry that lists every step it affects, and the golden tests catch a change made without one (D40; CONTRACTS.md, Rules). The goldens were rebuilt byte for byte at c8b1d93 ([packet §3.1](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#31-checks), V2).
- **Limits:** later families join through their own additive catalogue freezes (B2); this freeze covers none of them. Known breaking changes are changing a cap, adding an actor, renaming envelope keys and recognising compressed segments ([packet §3.4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#34-capacity-reasoning-d78); [packet Q5](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q5-strictness-of-the-envelope)). Core's store policy can change without a format change (D111).

### X-4. Privacy

- **Need:** Agents' private content must stay out of the shared history, and each record must reveal no more than its purpose needs.
- **Who:** agents with private contexts; the owner; the administrator, who may inspect every record.
- **Parts:** format (what records hold); reader and appender (no access control of their own).
- **Sources:** [D33](docs/DECISIONS.md#d33-what-the-colony-log-holds) ("An agent's private context content stays in its directory, and each change is recorded in the log by hash, never by content"); [B2](docs/DECISIONS.md#b2-stage-producer-ready-contracts-and-split-u1-capabilities) (a privacy class for each catalogue entry); [packet Q10](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q10-privacy-class-wording); [stories 5, 23, 44, 164](.scratch/dwarf-harness/spec.md#user-stories).
- **Met by:** the F0 records hold identifiers, digests, sizes and policy values, and no content. The register describes `ledger.command` as "identifiers and a digest; no submission content", the pin as "identifiers and body digests; no content", the unpin as "identifiers only" and the collection record as "digests, sizes and policy values; no content" ([F0 catalogue](docs/CONTRACTS.md#producer-ready-family-freezes-b2)). `body_refs` holds digests only, and a private-content hash inside a payload is not a body reference ([B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 1).
- **Limits:**
  - No privacy-class vocabulary exists. The owner's answer asked for none, and D111 leaves the descriptive wording as register text, not frozen (Q10); a vocabulary can replace it later without a format change.
  - The reader and the appender have no access control. Core decides who sees what: C3 authorizes body reads (B21 item 5); the views receive only Core's authorized projections through C7 ([spec decisions 52–54](.scratch/dwarf-harness/spec.md#implementation-decisions)); C10 is to protect the colony's files from hostile child processes ([C10](docs/ROADMAP-CORE.md#c10-isolation-boundary-and-assurance-a2)).
  - Each later family states its own privacy class at its freeze (B2).

### X-5. Capacity reasoning (D78)

- **Need:** Before relying on the contracts, the owner needs to know how the ledger behaves under the intended workload, with assumptions, calculations and measurements kept apart ([D78](docs/DECISIONS.md#d78-performance-feasibility-is-part-of-architecture-decisions)).
- **Who:** the owner; every step that sizes its own load.
- **Parts:** all three, with Core's store.
- **Sources:** D78; [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage); [A.10](docs/DESIGN-CORE-AND-LAUNCHERS.md#a10-colony-ledger-and-body-store-c1-ticket-03); [packet §3.4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#34-capacity-reasoning-d78).
- **Assumption.** A.10's workload model, an assumption whose population and traffic the owner set at OG3: 12, 20 and 60 contexts, 60 being the larger population; each change runs a 41-commit script twice, doubling the traffic. Per context-hour that is 492 commits, 960 records, 96 commands and 96 bodies of 3.61 MiB.
- **Measurements**, on one Windows machine (A.10):
  - a cold `Open` of 7 days at 60 contexts, 12.1 GiB of log: 431 s, with a peak working set of 91.4 MiB;
  - steady load at 60 contexts, from arrival to acknowledgement: 3.84 ms at the median and 15.9 ms at p99, with no failed calls;
  - commits per second with no waits at 60 contexts: 917;
  - the longest append during an explicit collection on a 1-day, 20-context colony: 677 ms;
  - repeating an old command, looked up in the segments: 465 ms at the median.
- **Calculations from the measurements, not measurements** (packet §3.4):
  - headroom at 60 contexts is 112: the no-wait commit rate over the model's rate. It is not accepted throughput under real producers;
  - at 60 contexts the log grows by about 1.73 GiB a day, and bodies by about 5.08 GiB a day before any collection;
  - a cold `Open` grows by about 62 s for each day of history at 60 contexts, if the measured scaling holds.
- **What the freeze fixes.** The framing of every record, with its hash, TAB and envelope keys, at 1,350 to 1,360 bytes per record on disk in the model, payload included (A.10, "Generation"); and the verification of every record read. Snapshots and indexes are rebuildable acceleration outside the log, so adding them needs no format change (D34; B3; packet §3.4).
- **Limits:** no budget, threshold, SLA or pass mark is set, and no capacity claim is made. The figures come from one machine, and the model is an assumption; Linux and macOS are unmeasured. The log is never compacted, so it grows without bound ([D36](docs/DECISIONS.md#d36-integrity-and-retention)). D34 adds snapshots or an index only when startup replay time or index memory exceeds a budget the owner sets; none is set.

### X-6. Replay needs only the records

- **Need:** Removing bodies must never break replay. Everything a consumer needs to rebuild state must be in the records themselves.
- **Who:** Undercurrent, which "reads nothing but the ledger" and may not recover workspace facts from bodies ([ROADMAP-UNDERCURRENT §2](docs/ROADMAP-UNDERCURRENT.md#2-what-undercurrent-needs-from-other-roadmaps)); Core's projections; the owner, who orders collection.
- **Parts:** format and reader, with Core's collection.
- **Sources:** [D36](docs/DECISIONS.md#d36-integrity-and-retention) ("everything replay needs lives in the record itself: changed paths and their hashes, check digests, outcomes, measurements"); [B2](docs/DECISIONS.md#b2-stage-producer-ready-contracts-and-split-u1-capabilities) (replay-essential fields for each catalogue entry); [Core §13.1](docs/DESIGN-CORE-AND-LAUNCHERS.md#131-ledger-bounded-replay-and-producer-ready-freezes-b2b3).
- **Met by:** the F0 catalogue names each type's replay-essential fields ([F0 catalogue](docs/CONTRACTS.md#producer-ready-family-freezes-b2)). `ledger.bodies_collected` keeps the history of removed bodies (F-13). The log is never compacted (D36).
- **Limits:** each later family must name its replay-essential fields at its own freeze (B2). The contracts cannot check that a family put them in its records.

## 6. Needs not met by version 1

| Need not met by version 1 | Goes to | Sources |
|---|---|---|
| The canonical submission bytes and fields, the request and operation schema, result delivery, and who may retrieve a duplicate's state (stories 6, 18) | C3 | [D58](docs/DECISIONS.md#d58-deduplicate-promotion-submissions-by-request-id-not-content-q35), "Still open"; [B21](docs/DECISIONS.md#b21-c1-body-store-body-references-f0-records-and-command-identities) item 9 |
| Command, result, context, task, turn, approval and accounting record families (F3) | C3 | [B2](docs/DECISIONS.md#b2-stage-producer-ready-contracts-and-split-u1-capabilities); [C1](docs/ROADMAP-CORE.md#c1-action-ledger-and-body-store) |
| Wiring Core's store into the host; calling `Recover`, `PublishBody`, `Submit` and `Collect` from owner and agent commands; authorizing body reads and pin releases; the default colony location of D38 | C3 | B21, Limits |
| An `interrupted` end for every unmatched start after a crash (story 26) | C3 | [D37](docs/DECISIONS.md#d37-ids-and-time); [C3](docs/ROADMAP-CORE.md#c3-agent-runtime) |
| Test runs on native Linux and macOS | C3's CI | [D42](docs/DECISIONS.md#d42-check-command-and-ci); B21, Limits |
| Promotion and scratch record families, with D23's fields (F2) | C2, after the owner's D52 approval | B2; B21, Limits; [D52](docs/DECISIONS.md#d52-path-a-testing-only-agents-until-validated-promotion-is-proven) |
| Check-evidence families (F4); which bodies are diagnostic, and their age and byte policy; the `artifact_unavailable` projection; finalization pins; body reservations in `CapacityProfile` | C4 | B21, Limits |
| Governance record families (F5) | C5 | B2; C1 |
| Broker and message families (F6); message caps; sender scoping beyond a colony-wide command ID | C6 | B2; B21, Limits |
| Durable history for the terminal and GUI views (stories 51, 119) | C7, through `launcher-protocol` | [D49](docs/DECISIONS.md#d49-durable-cursors-and-live-text-use-separate-streams-q27); [C7](docs/ROADMAP-CORE.md#c7-colony-host-local-ipc-and-launcher-protocol) |
| Expiry of cosmetic command retries, and snapshots of the fortress presentation (stories 122, 152) | C7 | B21, Limits |
| Router measurement families (R11) | R1 | B2; [R1](docs/ROADMAP-ROUTER.md#r1-safe-per-turn-settings-provider-awareness-r11-events) |
| The graph-delta family | U3 | B2 |
| Priority for shutdown and grant-revocation commits under heavy traffic (story 49) | Ticket 28 | [Ticket 28](.scratch/dwarf-harness/issues/28-keep-shutdown-and-revocation-admissible-under-saturated-social-traffic.md); [appender package doc](contract/ledger/appender/appender.go) |
| Upgraders between type versions | Each family, when it adds a version | [D35](docs/DECISIONS.md#d35-record-format-and-evolution) |
| Calibrating the record and commit caps, which Core §13.1 calls provisional | An owner decision entry, as a breaking change | [B18](docs/DECISIONS.md#b18-c1-ledger-format-v1-encoding-colony-store-and-os-adapter) item 2; [D40](docs/DECISIONS.md#d40-contracts-between-roadmaps) |
| Snapshots, an on-disk or SQLite index, an old-ID lookup outside the writer turn, one startup pass instead of two | Only on a measured trigger: a startup-time or index-memory budget that the owner sets | [D34](docs/DECISIONS.md#d34-ledger-storage-our-own-append-only-log); [B3](docs/DECISIONS.md#b3-one-authoritative-durable-ledger-and-bounded-platform-storage); B21, Limits; [packet §3.4](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#34-capacity-reasoning-d78) |
| Compressed old segments | Only on a measured trigger, as a format addition with a name rule and an owner decision entry | [D36](docs/DECISIONS.md#d36-integrity-and-retention); B18 item 3; B21, Limits |
| Telling damage in the final commit from a cut-off write | External anchoring of the latest hash at assurance A3, which no step delivers yet | D36; [ROADMAPS](docs/ROADMAPS.md#dependency-graph) |
| A guarantee against power loss | Not promised | B3 |
| A privacy-class vocabulary | The owner, if he wants one; a register change, not a format change | [Packet Q10](.scratch/dwarf-harness/c1-evidence/og2-f0-freeze-packet.md#q10-privacy-class-wording); [D111](docs/DECISIONS.md#d111-f0-ledger-contracts-frozen-at-version-1) |
| Access control on reading records and bodies | C3 (body reads), C7 (views), C10 (the colony's files) | B21 item 5; [spec decisions 52–54](.scratch/dwarf-harness/spec.md#implementation-decisions); [C10](docs/ROADMAP-CORE.md#c10-isolation-boundary-and-assurance-a2) |
| A disk, startup-time or memory budget, and any capacity claim | The owner | [D78](docs/DECISIONS.md#d78-performance-feasibility-is-part-of-architecture-decisions); D34 |
| Checking three record rules when a log opens: a pin's bodies were published when it was written; a collected body was referenced earlier; an ID older than the in-memory index appears only once | No step: Core's own writers cannot break them | B21 item 10 |
