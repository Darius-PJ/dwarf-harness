# DwarfHarness

DwarfHarness is a persistent colony of agents, inspected and operated through terminal and fortress views. It runs locally and is written in Go.

One authoritative Core per colony owns durable history, execution authority, workspaces, validated promotion, tasks, grants, accounting and communication. The Router prepares and prices model requests without becoming a second authority. Undercurrent derives a bounded, deterministic environmental field from authorized evidence and gives agents permission-scoped impressions, never hidden content or instructions. A terminal program and a native GUI control and inspect the same Core through one launcher protocol; in the GUI's fortress, each agent is drawn as a dwarf.

The aim is a colony that can deliver real work without giving up private context, explicit authority, bounded spending or trustworthy evidence that work passed. Parallel work must not turn into a shared mind, an unbounded queue of model calls, or claims that work passed because a model said so.

[ARCHITECTURE.md](ARCHITECTURE.md) describes the components, the package layers and the tests. [GLOSSARY.md](GLOSSARY.md) defines the domain terms.

## Status

DwarfHarness is pre-release. Nothing has been released. The `dwarf-harness` host and terminal program can create and configure a colony through explicit owner review and approval; the native GUI does not exist. This repository holds the contract packages, part of Core and of Undercurrent, the terminal program, a Go dependency resolver and the development tools, each tested on its own.

Everything here is for testing only. Do not use it for real work: real assignments begin only after Release 1's exit test passes (see the [release tiers](docs/ROADMAPS.md#release-tiers)). Each step's status is on the [status board](docs/ROADMAPS.md#status-board).

Setup and terminal turns are separate operations. Setup never starts a turn or sends a model request. Its turns have run only against local loopback providers. One owner-authorized live OpenAI drill ran one tool turn through Core's Session in a test driver, not through this program (`gpt-6-luna`; two calls; one tool); Anthropic has run only against loopback. The program proves neither step C3's exit nor any capacity, latency or isolation figure.

The first release needs only Windows. The code stays portable, and the check also runs on Linux and macOS.

## Colony setup

Build the program with `GOTOOLCHAIN=local CGO_ENABLED=0 go build ./cmd/dwarf-harness`.
Always supply `--colony-dir` before the command. Setup is offline owner administration:
finish the current terminal with `/quit` before changing its configuration. A held
colony refuses `colony_in_use`; setup never takes over its owner.

Every operation has two separate invocations:

```text
dwarf-harness --colony-dir C colony create --input create.json --review-out create.review.json
dwarf-harness --colony-dir C colony create --apply create.review.json --approve SHA256
```

For colony creation, `create.json` contains `{}` and the review file must be
**outside** the new colony directory. The first invocation captures the exact
operation, generated identities, current revisions and artifact bytes. Read its
full review, then supply its `review_sha256` to the second invocation. A review
does not apply the requested operation. Existing review files are not overwritten.
The same approved file can be reapplied to recover its original receipt; it does
not create another agent, context or credential version.

Reviews contain private instructions, host file contents and credential source
locators, but never credential values. Keep input and review files outside the
repository; on Windows, use an owner-only directory with appropriate access
permissions. Human review prints the complete document and quoted exact artifact
text, including line endings. `--json` instead prints safe identities, digests and
receipts, without private artifact text or source locators. Refusals report a code,
target identity, member path and a fix entrance, not raw native errors or input
path values.

### Input forms

Inputs are strict JSON objects: no unknown or duplicate members, `null`, trailing
values or literal credentials. Input JSON is limited to 1 MiB. Named artifact
paths are strings, resolved relative to the input file's directory; the command
captures their exact bytes before review. Apply never rereads those paths.
Instructions are limited to 1 MiB and provider profiles to 32 KiB.

| Command | Input members |
|---|---|
| `colony create` | `{}`; the directory is the global flag |
| `workspace register` | `repository` (also relative to the input directory) |
| `workspace inputs` | `repository`; resolves its applied registration, or use `--workspace UUID` / `workspace_id` |
| `agent create` | `name`, `instructions_file`, `general` |
| `context create` | `agent_id` or `--agent`, `title`, context settings below |
| `context configure` | `context_id` or `--context`, `title`, complete replacement context settings |
| `task create` | `description_base64`, `acceptance_sha256`, `dependencies`, `audience`; optional native `task_id`, `origin`, `assignment`, or `--context` |
| `grant issue` | `origin`, `spec`: exact native grant ID, grantee, operations, typed resources, data class, recipients, limits, delegation depth, lifetime and optional parent/task binding |
| `capacity set` | `origin`, `scope`, `limits`: `turn_slots`, `local_slots`, `buffer_bytes`, `local_units`, `max_operation_bytes` |
| `route set` | `enabled`, `profile_file`; `--context` or explicit `origin`, `route_id`, `credential_scope_id` |
| `credential bind` | `provider`, `source_kind`, `source_locator`; `--context` or explicit `origin`, `scope_id`; optional `revoke_previous_version` |
| `credential revoke` | `origin`, `scope_id`, `credential_version` |
| `spend set` | `origin`, `scope`, `enabled`, `mode`, `usd`, `period_kind`; optional `warning_ppm` |

Native identities in these objects are UUIDs, `origin` is
`{"agent_id":"UUID","context_id":"UUID"}`, and scopes use their native `kind` and
optional `id`. Grant resources retain the native kind-specific object shape, not
wildcard strings. `capacity set` reviews the native `capacity configure` operation.
Money is explicit: `mode` is `finite` with decimal `usd`, or `unlimited` with an
empty `usd`; `period_kind` is `lifetime`, `utc_day` or `utc_month`. Configure the
colony's terms first; subsequent scopes must use its period. No grant, capacity
or spend ceiling is implied by creation.

For example, an `agent create` input needs no hand-copied identities:

```json
{
  "name": "Scout",
  "instructions_file": "instructions.md",
  "general": {"max_output_tokens": 4096}
}
```

`general` accepts `route_id`, `expected_route_version`, `credential_scope_id`,
`max_output_tokens` and `turn_policy`. Its title remains `General work` and it has
no task binding. Missing route/scope IDs request fresh UUIDv7 identities, with
initial expected route version 1. The review shows those identities and every
expanded finite turn-policy field; it does not fabricate a route or credential.

Work-context settings use the same fields at the input object's top level, plus
`title` and an optional `task_binding` triple (`task_id`, `assignment_id`,
`generation`). Creation can mint the triple at generation 1; configuration is a
complete replacement, not a patch. Omitted `turn_policy` selects the finite
testing template shown in review; it does not retain an old custom policy.

```text
dwarf-harness --colony-dir C context create --agent Scout --input work.json --review-out work.review.json
dwarf-harness --colony-dir C route set --context "Scout/General work" --input route.json --review-out route.review.json
dwarf-harness --colony-dir C credential bind --context "Scout/General work" --input source.json --review-out source.review.json
dwarf-harness --colony-dir C task create --context Scout/Research --input task.json --review-out task.review.json
```

`--agent` accepts an exact registered name or UUID; a supplied `agent_id` must
agree. `--context` selects an exact registered agent name/UUID followed by `/`
and the configured context title/UUID. Unknown or ambiguous names are refused,
never guessed. It is accepted by route setting, credential binding, task creation
and context configuration. Task creation resolves the reviewed work context's
intended assignment. Apply uses only the approved review, without either selector.

For `workspace inputs`, the repository's inspected common directory must match
one applied registration exactly. Alternatively, select an existing workspace
with `--workspace UUID`; an input `workspace_id`, if also supplied, must agree.
Unknown or ambiguous registrations are refused. Registration allocates its UUID
during apply, so use the applied state or receipt, never the registration review,
when explicitly selecting that workspace later. Selectors are review-only flags.

Credential sources are only `environment` and `windows_credential_manager`.
Supply the variable or Credential Manager entry name, never its value. Binding
authorizes a fresh native bind on each owner start from the newest active source
selection for each used scope. Startup replaces only that selected version,
preserving other explicitly active versions. A missing or invalid selected source
refuses startup with the scope identity; there is no fallback to an older source.
Setup commands do not perform this startup rebinding.

### Managed and legacy startup

```text
dwarf-harness --colony-dir C
```

The initial applied `colony.create` record selects managed mode. Context settings
then come only from the ledger. Supplying `--settings`, even an empty file, refuses
`mixed_configuration`: nothing is imported, merged or generated. A managed colony
without a configured context refuses `setup_required`. Missing routes, tasks,
grants, capacities and spend terms remain turn prerequisites, not implicit grants
or reasons to invent configuration at startup. Validation is disabled in this
build; check setup, `doctor` and `startup set` are not exposed yet.

Populated colonies without that initial setup record remain explicit legacy mode:

```text
dwarf-harness --colony-dir C --settings legacy.json
```

The legacy file remains strict nonsecret JSON as documented by
`internal/core/terminal`; it is never imported into managed setup. Omitting it
refuses `settings_required`, and setup mutations refuse `legacy_configuration`.
An empty unfinished colony is `creation_incomplete`, not an automatic migration.

## Building and checking

You need:

- Go 1.27.4, the toolchain that `go.mod` pins, on `PATH`. Run every Go command with `GOTOOLCHAIN=local`, so that no toolchain is downloaded, and with `CGO_ENABLED` set explicitly. Core and the terminal program never use cgo.
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

The workflow [`.github/workflows/check.yml`](.github/workflows/check.yml) runs the full check on Windows, Linux and macOS runners, with Go archives and, on Windows, the llvm-mingw compiler verified against pinned SHA-256 digests. It runs on every push to a work branch (`build/`, `docs/` or `fix/`) and on manual dispatch. A push reports a pass without running the full check when nothing but Markdown files has changed since the branch's merge base with `main`; manual dispatch always runs the full check. A ruleset on `main` accepts only commits whose `Check result` job has passed.

## Repository map

| Path | Contents |
|---|---|
| `contract/` | Standard-library-only contract packages: the ledger format and reader, the ledger appender, and the law format |
| `cmd/dwarf-harness/` | The terminal program: it opens an initialized colony's Core and runs a plain line terminal over its Sessions |
| `internal/core/` | Core: agents, workspaces, the ledger store, durability, the Windows executor and the graph wiring |
| `internal/core/session/` | Core's in-process Session seam: typed commands, durable-sequence results, events, live text and the durable final answer |
| `internal/core/terminal/` | The terminal program's settings, commands and labelled live and final display |
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
