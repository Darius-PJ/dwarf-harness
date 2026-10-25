# Architecture

This file describes DwarfHarness's components, the one-way import layers, the packages, the ledger and its contracts, and how the tests are organised. The design documents in `docs/` hold the full design, and each package's documentation states its exact behaviour. Statuses are not repeated here: each step's is on the [status board](docs/ROADMAPS.md#status-board), and each contract's version and status are in the [register](docs/CONTRACTS.md#register).

## Components

| Component | Role | In this repository |
|---|---|---|
| Core | One authoritative Core per colony. It owns durable history, execution authority, workspaces, validated promotion, tasks, grants, accounting and communication. | In part: the packages under `internal/core/` |
| Launchers | `dwarf-harness` is the colony's host process and terminal program. One host runs per active colony and is the sole writer of its ledger. The GUI, `dwarf-harness-GUI`, attaches to it as a display client and starts it first when no host is running. Views talk to the host through the launcher protocol. | Not yet: `cmd/dwarf-harness/` and `contract/launcher/` do not exist |
| Undercurrent | The environmental field layer. It derives a bounded, deterministic field from authorized evidence and gives agents permission-scoped impressions, never hidden content or instructions. | In part: the law format (`contract/law`) and the coupling graph (`internal/undercurrent/graph`, with `helpers/go-analyzer`). The field engine does not exist. |
| Routing layer | The Router prepares and prices model requests without becoming a second authority. It starts in shadow mode, observing requests without changing them. | Not yet: `internal/router/` and `contract/module/` do not exist |
| Native GUI | A native Fyne v2.8.1 display client with the fortress and RPG views. It sends only typed commands through the launcher protocol and never reads colony files. It will live in a nested `gui/` module, whose build may use cgo. | Not yet: `gui/` does not exist. `tools/artgen` generates its art sample. |

## Import layers

Imports run one way: `cmd` → `internal/core` → `internal/router` and `internal/undercurrent` → `contract` (D45). Step 5 of `tools/check` enforces the full table, which [`tools/check/boundary.go`](tools/check/boundary.go) encodes. Every package may also import the standard library and its own tree.

| Tree | May also import |
|---|---|
| `contract/...` | Nothing else |
| `internal/undercurrent/...` | `contract/...` |
| `internal/router/...` | `contract/...`; `golang.org/x/...` |
| `internal/core/...` | `contract/...`, `internal/router/...`, `internal/undercurrent/...`; `golang.org/x/...` |
| `cmd/dwarf-harness` | `internal/core/...`, `contract/...` |
| `tools/...` | `golang.org/x/...`, `honnef.co/go/tools/...`, `golang.org/x/vuln/...`: the tools that `go.mod` pins |
| `helpers/go-analyzer` | `golang.org/x/tools/go/packages` only (D132) |
| `gui/...` | `contract/...` |

The step also enforces three further rules:

- Only `internal/core/...`, `internal/router/...` and `internal/undercurrent/graph/...` may import `contract/ledger/appender` (D50).
- A package that is in no row of the table fails, and so does any file that uses cgo (D28).
- The module and its test variants are listed once for each supported target (windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64), because build constraints change which files each target sees.

No test-only dependency is recorded yet.

## Package map

| Package | What it is |
|---|---|
| `cmd/` | Not created yet. D45 reserves `cmd/dwarf-harness` for the host and terminal program. |
| `contract/ledger` | The `ledger-format` and `ledger-reader` contract: the on-disk encoding of a colony's history, the rules that verify it, and the read-only Reader through which every consumer replays it. |
| `contract/ledger/appender` | The `ledger-appender` contract (D50): the restricted way a producer adds records to one colony's ledger. Core's writer is its only implementation. |
| `contract/law` | Loads and identifies the shared field law and the local validation policy (D47), independently of field execution. |
| `internal/core/agents` | Core's identities, private state, scoped authority, tasks and non-paid work projections. The ledger selects state; directory contents and mutable Markdown never select a revision or confer authority. |
| `internal/core/durable` | Core's narrow adapter to the operating system's file locking and directory durability (B3). |
| `internal/core/graphfoundation` | The explicit Core-to-graph wiring boundary: its artifact reader reads Git objects through the workspace service, and its appender is the ledger store itself. |
| `internal/core/ledgerstore` | Core's store for one colony's ledger (D50): the colony directory and its `colony.json`, the writer lock, the sequencer, the writer and recovery behind `appender.Appender`, the body store with its pins and explicit collection, and command identities. |
| `internal/core/winexec` | Runs one authorized Windows executable in a fresh AppContainer with zero capabilities and in a non-breakaway Job. A Core-internal OS adapter; on other platforms it refuses without effects. |
| `internal/core/workspace` | Core's registered Git workspaces, bound private scratches, exact capture, bounded checkpoints and integration generations. It writes the F2-05 to F2-09 records through the colony store and accepts no promotion. |
| `internal/undercurrent/graph` | U3's artifact and module analysis, strict replay and explicitly driven durable worker. It depends on contracts, never on Core. |
| `tools/check` | The check (D42), run as `go run ./tools/check`. |
| `tools/artgen` | Renders the DwarfHarness art sample from text pixel maps. A development tool: it never ships and makes no model call. |
| `helpers/go-analyzer` | The separate, pinned Go dependency resolver (D132). The harness builds it; an analyzed repository or a test never does. |

## The ledger

Each colony has one durable history: one append-only log, written only through one Core-owned appender.

- **Records.** Each record is one line, `lowercase_hex(SHA-256(json)) TAB json LF`, where `json` is one compact UTF-8 JSON object. Its envelope carries the colony sequence, the commit's ID, the record's position in its commit and the commit's size, the previous record's hash, the event time, the colony ID, the record type and the type version. Readers check the bytes as stored and never re-marshal them.
- **Bodies.** A record names the bodies it references in the colony's body store in its optional `body_refs` key, a strictly ascending set of SHA-256 digests. That key holds the record's only body references.
- **Appending.** Callers hand drafts to the appender. The writer assigns sequence, hash, time, colony and commit, so a caller can neither write ledger files nor choose sequences. An append is acknowledged only after its commit is completely written and durably flushed; otherwise it fails with `ErrRefused`, `ErrPoisoned`, `ErrUncertain` or `ErrClosed`, each saying what became of the commit. `internal/core/ledgerstore` is the writer.
- **Durability.** `internal/core/durable` locks files and flushes directories. It makes no power-loss guarantee: it relies on the file system and the device honouring their flushes.

### Record contracts

A contract is a Go package under `contract/` that uses only the standard library, with golden samples for every version of every record or message type it defines, and an entry in the [register](docs/CONTRACTS.md#register). The register has six:

| Contract | Package |
|---|---|
| `ledger-format`, `ledger-reader` | `contract/ledger` |
| `ledger-appender` | `contract/ledger/appender` |
| `law-format` | `contract/law` |
| `launcher-protocol` | `contract/launcher`, not created yet |
| `context-module` | `contract/module`, not created yet |

For the contract packages, the package documentation is the format; see [`contract/ledger/doc.go`](contract/ledger/doc.go) and [`LEDGER-USER-NEEDS.md`](LEDGER-USER-NEEDS.md). The ledger's record catalogue grows in families: F0 holds the ledger's own records, F2 the workspace and promotion records, F3 the agent runtime's records and F-U3-61 the coupling graph's. Components use a family only after it is frozen, and consumers build only against frozen contracts. A frozen contract changes only by addition: new optional fields, new types, and new type versions with upgraders. A breaking change needs a decision entry that lists every step it affects.

## Tests

The specification's testing decisions shape the tests: they go through each producer's public boundary and assert behaviour, such as permissions, durable effects, state transitions, ordering, boundaries and recovery, not wiring. Where their effects are the subject, tests use real temporary stores, Git repositories and child processes. Time-dependent tests in `ledgerstore` and `workspace` use `testing/synctest`. The check runs every test with caching off (`-count=1`) and a 30-minute limit per package.

- **Goldens.** `contract/ledger/testdata/` holds golden logs for the F0, F2, F3 and U3 families, and `contract/law/testdata/` holds golden laws and validation policies. A test fails when an encoding differs from its stored golden. The ledger goldens are regenerated with `-update` only for an intended format change.
- **Fuzz targets.** Plain `go test` runs only their seed corpora; a fuzzing run uses `go test -fuzz`.

  | Package | What the fuzz targets cover |
  |---|---|
  | `contract/ledger` | Checking logs that are truncated, torn or corrupted; decoding each F2, F3 and U3 payload family |
  | `contract/law` | Loading field laws and validation policies |
  | `internal/core/agents` | Replay of private-state, scoped-authority and review records |
  | `internal/core/winexec` | Decoding the executor's persistent fence |
  | `internal/core/workspace` | Parsing Git's output and the capture inputs; CRLF streaming |
  | `internal/undercurrent/graph` | The SQL subset, graph-helper responses and replay prefixes |
  | `helpers/go-analyzer` | `go version` output and helper requests |

- **The race step.** Step 7 runs every test again with `-race`, `CGO_ENABLED=1` and `CC` set to the platform's C compiler: on Windows, the pinned clang that `DWARF_CC` names. A test can read the `race` build tag; `internal/core/graphfoundation` uses it to skip one production-scale test under the race detector, where a scaled test covers the same behaviour.
- **Windows-only tests.** Files named `*_windows_test.go` run only on Windows. They test the executor in `internal/core/winexec` and Windows behaviour in `agents`, `durable`, `graphfoundation`, `ledgerstore`, `workspace`, `internal/undercurrent/graph` and `helpers/go-analyzer`.
- **Native tools.** Tests that need the Go dependency resolver or installed Python, Node or TypeScript skip, with their reason, when the variables naming those tools are unset. The check's tests step builds `helpers/go-analyzer` and sets its variables; the [README](README.md#building-and-checking) lists the others.

## Design documents

| Area | Documents |
|---|---|
| Order, dependencies and status | [`docs/ROADMAPS.md`](docs/ROADMAPS.md), [`docs/CONTRACTS.md`](docs/CONTRACTS.md) |
| Whole system | [`.scratch/dwarf-harness/spec.md`](.scratch/dwarf-harness/spec.md), [`GLOSSARY.md`](GLOSSARY.md) |
| Core and launchers | [`docs/DESIGN-CORE-AND-LAUNCHERS.md`](docs/DESIGN-CORE-AND-LAUNCHERS.md), [`docs/ROADMAP-CORE.md`](docs/ROADMAP-CORE.md) |
| Validation | [`docs/RESEARCH-ADMISSIBLE-VALIDATION.md`](docs/RESEARCH-ADMISSIBLE-VALIDATION.md) |
| Routing layer | [`docs/DESIGN-ROUTING-LAYER.md`](docs/DESIGN-ROUTING-LAYER.md), [`docs/ROADMAP-ROUTER.md`](docs/ROADMAP-ROUTER.md) |
| Undercurrent | [`docs/DESIGN-UNDERCURRENT.md`](docs/DESIGN-UNDERCURRENT.md), [`docs/FIELD-PHILOSOPHY.md`](docs/FIELD-PHILOSOPHY.md), [`docs/ROADMAP-UNDERCURRENT.md`](docs/ROADMAP-UNDERCURRENT.md) |
| Native GUI | [`docs/ROADMAP-GUI.md`](docs/ROADMAP-GUI.md), [`.scratch/fortress-gui/spec.md`](.scratch/fortress-gui/spec.md), [`tools/artgen/STYLE.md`](tools/artgen/STYLE.md) |

The design documents record requirements and proposals, not what is built. The package documentation describes the code as it is.
