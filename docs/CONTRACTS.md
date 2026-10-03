# Contracts between roadmaps

**Status:** 2026-10-02. All contracts are *Not started*. This register implements D40. A contract is the boundary between two roadmaps: the producing step owns it, and consuming steps build against it only once it is frozen.

## What a contract is

- A Go package that uses only the standard library, under `contract/` in the root module (D45): `contract/ledger` holds `ledger-format` and `ledger-reader`, `contract/launcher` holds `launcher-protocol`, `contract/module` holds `context-module`, and `contract/law` holds `law-format`. Golden samples live in `contract/<name>/testdata/`.
- Golden sample files for every version of every record or message type it defines. Tests check the package against them, so a change made without a decision entry fails the check.
- An entry in the register below.

## Rules

| State | Who changes it | How |
|---|---|---|
| *Draft* | The owning step | Freely |
| *Frozen* | The owner freezes it | By addition only: new optional fields, new types, new type versions with upgraders |
| Breaking change to a frozen contract | Owner | A decision entry listing every step it affects; consuming roadmaps update those steps' status |

Freezing is the moment another roadmap's agent may start building against the contract (D20). Record each freeze as a decision entry and update the version and status columns below.

## Register

| Contract | Defines | Owning step | Consumed by | Version | Status |
|---|---|---|---|---|---|
| `ledger-format` | Record envelope, framing, hash chain, the catalogue of event types and their type versions (D35–D37); includes every non-[PROPOSAL] event in `DESIGN-UNDERCURRENT.md` §15.1 and routing design rule R11. The [PROPOSAL] events in §15.1 (graph delta, law activation, interference check, and the scratch lifecycle rows C2 adds: base advanced, closed) join by addition when their owning steps define them | C1 | Every component that writes or reads the ledger: C2–C10, R1, U1–U10 | — | Not started |
| `ledger-reader` | Reading by cursor from a sequence number, declared consumed types, skip and stop behaviour (D35), replay after restart | C1 | U1 (engine), C6 (broker), C7 (launcher views), R1 | — | Not started |
| `launcher-protocol` | Versioned, length-framed command and event protocol between the colony host and its views (core §3.4, §4.2); version check on attach (D39) | C7 | Terminal, GUI roadmap | — | Not started |
| `context-module` | A prompt-tail module with a declared token cost, bound to an agent, context and turn (routing R3, §6.5) | R1 | C3 (other-active-contexts note, D25), U6 (impression renderer) | — | Not started |
| `law-format` | Versioned, hashed field-law files: kinds, source rules, parameters, effective ledger position (Undercurrent §13.4) | U1 | U1–U10; R1 if headroom buckets are versioned with the law (Undercurrent Q28) | — | Not started |

## Open questions that touch contracts

- **Undercurrent Q24:** JSON or YAML for law files. JSON keeps Undercurrent standard-library only (D29).
- **Undercurrent Q28 and routing open question 7:** whether headroom buckets live in router configuration or in law files. The answer decides whether R1 consumes `law-format`.
- **C3 before R1:** the runtime renders the D25 note with a fixed tail section. R1 freezes `context-module` and converts that note into a module (`ROADMAP-ROUTER.md` R1).
