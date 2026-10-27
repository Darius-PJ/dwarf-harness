# DwarfHarness

DwarfHarness is a persistent colony of agents, inspected and operated through terminal and fortress views. It runs locally and is written in Go.

One authoritative Core per colony owns durable history, execution authority, workspaces, validated promotion, tasks, grants, accounting and communication. The Router prepares and prices model requests without becoming a second authority. Undercurrent derives a bounded, deterministic environmental field from authorized evidence and gives agents permission-scoped impressions, never hidden content or instructions. A terminal program and a native GUI control and inspect the same Core through one launcher protocol; in the GUI's fortress, each agent is drawn as a dwarf.

The aim is a colony that can deliver real work without giving up private context, explicit authority, bounded spending or trustworthy evidence that work passed. Parallel work must not turn into a shared mind, an unbounded queue of model calls, or claims that work passed because a model said so.

[ARCHITECTURE.md](ARCHITECTURE.md) describes the components, the package layers and the tests. [GLOSSARY.md](GLOSSARY.md) defines the domain terms.

## Status

DwarfHarness is pre-release. Nothing has been released, and there is no program to run yet: the `dwarf-harness` host and terminal program and the native GUI do not exist. This repository holds the contract packages, part of Core and of Undercurrent, a Go dependency resolver and the development tools, each tested on its own.

Everything here is for testing only. Do not use it for real work: real assignments begin only after the validation runner and promotion gate (step C4) pass their end-to-end gate. Each step's status is on the [status board](docs/ROADMAPS.md#status-board).

The first release needs only Windows. The code stays portable, and the check also runs on Linux and macOS.

## Building and checking

You need:

- Go 1.27.3, the toolchain that `go.mod` pins, on `PATH`. Run every Go command with `GOTOOLCHAIN=local`, so that no toolchain is downloaded, and with `CGO_ENABLED` set explicitly. Core and the terminal program never use cgo.
- Git 2.45 or later, which the workspace package requires.
- Network access, for Go modules and for govulncheck's database at `https://vuln.go.dev`.
- For the race step only, a C compiler. On Windows, set `DWARF_CC` to the absolute path of the pinned llvm-mingw 20260922 clang, whose `--version` reports `clang version 23.1.2` and `Target: x86_64-w64-windows-gnu`. On Linux the step uses `/usr/bin/gcc`, and on macOS the clang that `xcrun --find clang` reports.

`tools/check` is the project's one gate. Without the race step:

```sh
GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/check -race=false
```

In PowerShell:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
go run ./tools/check -race=false
```

The check runs nine steps, numbered 0 to 8: toolchain, gofmt, vet, staticcheck, govulncheck, import boundary, tests, race and cross builds. Vet, staticcheck, govulncheck and the import boundary cover every supported target: windows/amd64, linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64. The steps run in order, even after a failure; the check prints each step's commands and its PASS, FAIL or SKIP verdict, and exits non-zero if any step failed. Every Go command it starts sets `GOTOOLCHAIN=local` and `CGO_ENABLED` itself.

`-race=false` skips step 7, the race step, and does not need `DWARF_CC`. The full check runs it. On Linux and macOS:

```sh
GOTOOLCHAIN=local CGO_ENABLED=0 go run ./tools/check
```

On Windows, in the same PowerShell session:

```powershell
$env:DWARF_CC = '<absolute path of bin\clang.exe in llvm-mingw-20260922-ucrt-x86_64>'
go run ./tools/check
```

To test one package:

```sh
GOTOOLCHAIN=local CGO_ENABLED=0 go test ./contract/ledger/...
```

Some tests need native tools. Tests of the Go dependency resolver skip, with their reason, unless `DWARF_GRAPH_GO_HELPER`, `DWARF_GRAPH_GO` and `DWARF_GRAPH_GOROOT` are set; the check's tests step builds `helpers/go-analyzer` and sets them itself. Tests of installed language tools skip unless `DWARF_GRAPH_PYTHON` (Python 3.13.15), `DWARF_GRAPH_NODE` (Node v24.18.0) and `DWARF_GRAPH_TYPESCRIPT` (the `lib/typescript.js` of TypeScript 6.0.3) hold absolute paths.

The workflow [`.github/workflows/check.yml`](.github/workflows/check.yml) runs the full check on Windows, Linux and macOS runners, with Go archives and, on Windows, the llvm-mingw compiler verified against pinned SHA-256 digests. It runs on every push to a work branch (`build/`, `docs/` or `fix/`) and on manual dispatch, and reports a pass without the full check when a push changes only Markdown. A ruleset on `main` accepts only commits whose `Check result` job has passed.

## Repository map

| Path | Contents |
|---|---|
| `contract/` | Standard-library-only contract packages: the ledger format and reader, the ledger appender, and the law format |
| `internal/core/` | Core: agents, workspaces, the ledger store, durability, the Windows executor and the graph wiring |
| `internal/undercurrent/` | Undercurrent's coupling graph service |
| `helpers/go-analyzer/` | The separately built Go dependency resolver that the coupling graph uses |
| `tools/check/` | The check |
| `tools/artgen/` | The generator of the fortress art sample, from text pixel maps |
| `docs/` | Designs, roadmaps, the contract register and research |
| `.scratch/dwarf-harness/` | The consolidated specification (`spec.md`) and the tickets (`issues/`) |
| `.scratch/fortress-gui/` | The fortress GUI's feature specification and research |
| `GLOSSARY.md` | Domain terms |
| `LEDGER-USER-NEEDS.md` | Who uses the three ledger contracts, and for what |
| `ARCHITECTURE.md` | Components, layers, packages, ledger and tests |
| `SECURITY.md` | How to report a vulnerability |

## Documentation

- [`docs/ROADMAPS.md`](docs/ROADMAPS.md) is the entry point: the reading order, the dependencies between steps, the status board and the working rules.
- [`docs/CONTRACTS.md`](docs/CONTRACTS.md) is the register of the interfaces between roadmaps, with each contract's version and status.
- The designs: [`DESIGN-CORE-AND-LAUNCHERS.md`](docs/DESIGN-CORE-AND-LAUNCHERS.md), [`DESIGN-ROUTING-LAYER.md`](docs/DESIGN-ROUTING-LAYER.md), [`DESIGN-UNDERCURRENT.md`](docs/DESIGN-UNDERCURRENT.md), [`RESEARCH-ADMISSIBLE-VALIDATION.md`](docs/RESEARCH-ADMISSIBLE-VALIDATION.md) and [`FIELD-PHILOSOPHY.md`](docs/FIELD-PHILOSOPHY.md). They record requirements and proposals, not what is built.
- The roadmaps: [`ROADMAP-CORE.md`](docs/ROADMAP-CORE.md), [`ROADMAP-ROUTER.md`](docs/ROADMAP-ROUTER.md), [`ROADMAP-UNDERCURRENT.md`](docs/ROADMAP-UNDERCURRENT.md) and [`ROADMAP-GUI.md`](docs/ROADMAP-GUI.md).
- Each package's documentation, its package comment, usually in `doc.go`, states what the package does. For the contract packages, the package documentation is the format.
- Most other files in `docs/` are research behind the design.

## Private records

Documents and code comments cite decisions by D-number, such as D45, and build decisions by B-number, such as B3. They also link to `docs/DECISIONS.md` and to evidence under `.scratch/dwarf-harness/*-evidence/`. These refer to the project's private records, which are not published, so those links do not resolve here.

## Support and contributions

The project offers no support and does not accept outside contributions for now. To report a vulnerability, follow [SECURITY.md](SECURITY.md).

## Licence

DwarfHarness is licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
