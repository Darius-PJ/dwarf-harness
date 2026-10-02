# Implementation language research

**Status:** Research report with a 2026-09-29 decision and relationship-context proposal addendum. **Go** remains selected; design decisions and proposals are not implementation claims.
**Date:** Research and measurements: 2026-09-28. Current-decision update: 2026-09-29.

**Fresh session:** begin at [the current core-design entry point](DESIGN-CORE-AND-LAUNCHERS.md#start-here-for-a-fresh-session), which owns reading order, authority and the proposed next design step. This report supplies historical evidence, not an implementation plan. Core §2 owns current owner decisions; core §§3–4 and §7 distinguish proposed process/context/messaging mechanisms, and core §10 records open choices. This report does not approve those proposals.

**Question:** Which implementation language fits DwarfHarness's aims? Those are a short system prompt, a small default tool set, and low use of both model tokens and device resources (CPU, memory, disk, battery, extra processes).
**Inputs:**

- Measurements on the development machine (§3 and Appendix A).
- A survey of what existing harnesses are written in, and why (§4).
- Ecosystem checks (§5).
- Published evidence on runtime costs (§2).

Sources were checked on 2026-09-28. The 2026-09-29 addenda clarify design consequences without refreshing the historical sources or measurements.

**Reconciliation scope:** The 2026-09-29 incorporation is limited to the three owner-authorized idea sections recorded in `DESIGN-CORE-AND-LAUNCHERS.md` §11 and §12. The original is archived as `superceeded-Design.md` in the archive folder `../dwarfharness/`, outside this repository; its remaining material is not reconciled. The historical survey relied on skill descriptions of existing JavaScript/port tooling without opening it; this update neither inspects nor changes legacy code or data.

Conventions:

- **[INFERENCE]** marks reasoning that no cited source establishes.
- Measured numbers come from one Windows machine. Read them as ratios between languages, not as absolute values.
- Memory is in MiB. File sizes are in MB.

## Decision

**2026-09-28 decision — historical rationale.** The owner chose **Go** under the then-current requirement to avoid a C toolchain. The recommendation in §1 and the language benchmarks remain the research record, not measurements of the native GUI.

- **The deciding cost at the time.** On Windows, Rust needed MinGW-w64, a 274 MB download, to link its HTTPS client (§3.3); Microsoft's C++ Build Tools, the alternative, weren't tried. Go needed only its 78.9 MB toolchain zip.
- **Costs accepted with Go.**
  - Garbage-collector headroom: 58 MiB committed against Rust's 5 MiB on the router workload (§3.2). Lowering `GOGC` didn't pay; see `DESIGN-CORE-AND-LAUNCHERS.md` §8.4.
  - Go's native `plugin` package doesn't work on Windows. Child-process plugins were selected for isolation and language independence; WASM is another portable option (§5).
  - Under the no-cgo constraint, the researched ONNX bindings could not run in the core. That constraint is superseded below; helper processes remain preferred for isolation and lazy resource costs.
- **Historical interface proposal.** A plain terminal and an external-browser launcher were selected on 2026-09-28. That browser proposal is superseded by the native launcher below.

**2026-09-29 addendum — current decisions and relationship-context proposal**

- **Language and toolchain.** Go remains selected. A C toolchain will be acquired, so blanket `CGO_ENABLED=0` is no longer a product constraint. Dependency and packaging costs must be evaluated for the chosen native GUI and any in-process native integrations (`DESIGN-CORE-AND-LAUNCHERS.md` §8.1).
- **One product, two entry points.** The program and plain terminal entry point are `dwarf-harness`; the native window launcher is `dwarf-harness-GUI`. The owner requires identical harness functionality and simultaneous views of the same live session; core §3 and §4 propose shared-core ownership and separate event cursors to realize that requirement. This is a design contract, not implemented launcher behavior.
- **Native GUI scope.** At most one GUI instance serves a colony; launching the same colony reuses/focuses it, while other colonies may have separate instances. The GUI can spawn agent sessions within core ownership limits. Dwarf Fortress styling belongs only to the GUI, with visual design deferred. The user's default browser belongs only to the superseded proposal (`DESIGN-CORE-AND-LAUNCHERS.md` §5 and §6).
- **Independent product.** DwarfHarness takes inspiration from ideas, not a StarNet fork, port or addon. There is no StarNet runtime/compiler, JavaScript/goja reuse, compatibility or parity contract. Capabilities, recipes/workflows and skills are independently designed (`DESIGN-CORE-AND-LAUNCHERS.md` §11 and §12).
- **Relationship-context proposal.** One execution session per agent owns lifecycle and scheduling, not one pooled model conversation. Private memory/handoff, routing state, provider continuations, render history and cache ledgers are independently owned by `(agent_id, context_id)` for general work, directed relationships and board participation. Bound context/turn approvals remain distinct from explicit agent/account budgets (§8; `DESIGN-CORE-AND-LAUNCHERS.md` §7.3). [INFERENCE: proposed mechanism, not a newly approved owner implementation choice]
- **Organization and communication.** Colony Grand Orchestrator/Griot/Diplomat roles sit above specialized Corps, each retaining its own Orchestrator, Griot, Diplomat and workers. The owner-defined paths and role purposes are centralized in `DESIGN-CORE-AND-LAUNCHERS.md` §7.1 and §7.2. Logical conversations do not imply one process, session or socket per relationship; the broker and local IPC are implementation proposals (core design §7.4 and §3.4), not language benchmarks. [INFERENCE: transport distinction]

Research findings in §§1–7 and Appendix A retain their 2026-09-28 measurement/source date. Explicit references to the revised cgo/native-GUI policy and context model in those sections are 2026-09-29 design annotations, not new research results. The original Rust recommendation, no-C constraint, browser proposal and JavaScript-reuse comparison remain historical; Go, the native launcher and standalone independence follow current core decisions. Native toolkit selection remains open.

## 1. Summary

**Historical recommendation (superseded by the Go decision above):** Write the harness core in **Rust**, shipped as one native binary, and run plugins out of process by default. The preference ordering below records the original research recommendation; it does not reopen the language decision or settle current executable packaging.

- **Go** is a close second, and it's the right choice if you'd rather not learn and review Rust.
- **TypeScript** (on Node, Bun or Deno) and **Python** don't fit the resource goal.

What the evidence shows:

- **Language shows up in memory, startup and install size, not in per-call speed.** A harness is I/O-bound. Preparing each request for a conversation of about 230,000 tokens took a median of 5–17 ms in every runtime tested (§3).
- **Native binaries need a fraction of the memory.** Across three tests, the peak working set was:

  | Runtime | Peak working set |
  |---|---|
  | Rust | 3.5–8.6 MiB |
  | Go | 4.6–18.0 MiB |
  | Python | 9.4–22.1 MiB |
  | JavaScript runtimes and compiled executables | 13.6–77.0 MiB |

- **Native binaries are 0.9–6.9 MB.** The JavaScript runtimes, and executables compiled from JavaScript, are 80–98 MB. The Python installation on this machine is 291 MB, not counting third-party packages.
- **The Rust–Go gap is small next to the gap between compiled and interpreted runtimes:**
  - Both start in about 15 ms.
  - Go used 1.3–2.4× Rust's working set, and far more committed memory under load.
  - Go's binaries were 2–2.7× the size.
  - Go rebuilt a one-line change in 1.3 s against Rust's 3.5 s, and it needed no C toolchain on Windows.
- **Precedent favors a native core for efficiency.**
  - OpenAI rewrote Codex CLI from TypeScript to Rust. Its reasons included a zero-dependency install and "no runtime garbage collection, resulting in lower memory consumption".
  - Goose, Zed and aichat are Rust; Crush and Plandex are Go.
  - Anthropic chose the other path for Claude Code: a Bun executable. It also acquired Bun.
- **Language sets the floor, not the ceiling.** Users have reported multi-gigabyte memory growth or pegged CPUs in TypeScript harnesses and in Codex (Rust) alike (§4.3). The rules in §7 apply whatever the language.

## 2. What "resource-efficient" means for a harness

A harness spends most of its time waiting: for the user, for the model, for tools. The costs that matter are these:

| Cost | Why it matters here | How much the language decides |
|---|---|---|
| Resident memory | Held for the whole session, by every agent process and every helper | A lot: runtime baseline, object overhead and garbage-collector headroom (§3) |
| Startup | Paid at every launch, and by every helper process (plugins, subagents) | A lot: about 15 ms native, against 29–100 ms for JavaScript and Python here |
| Install size | Download, disk and update size | A lot: 0.9–6.9 MB native, against 80–291 MB |
| Idle wakeups | Battery life. Apple: "Timers prevent the CPU from going to or staying in the idle state" ([Energy Efficiency Guide](https://developer.apple.com/library/archive/documentation/Performance/Conceptual/EnergyGuide-iOS/MinimizeTimerUse.html)) | Little. The application and its UI library decide this (§7) |
| Per-call CPU | Building, hashing and parsing requests | Little at this scale (§3.3) |
| Child processes | Tools, MCP servers, plugins | Each helper pays its own runtime's baseline |

**Energy rankings.** The widely cited ranking comes from Pereira et al. It measured CPU-bound benchmark programs. From its Table 4 ([Science of Computer Programming, 2021](https://haslab.github.io/SAFER/scp21.pdf)):

| Language | Energy (× C) | Time (× C) | Peak memory (× Pascal) |
|---|---|---|---|
| C | 1.00 | 1.00 | 1.17 |
| Rust | 1.03 | 1.04 | 1.54 |
| Go | 3.23 | 2.83 | 1.05 |
| JavaScript | 4.45 | 6.52 | 4.59 |
| TypeScript | 21.50 | 46.20 | 4.69 |
| Python | 75.88 | 71.90 | 2.80 |

These rankings don't transfer to a harness:

- **Energy follows busy time, not language.** A re-analysis of that work found that "the choice of programming language implementation has no significant impact on energy consumption beyond execution time" ([van Kempen et al., ASE 2025](https://arxiv.org/abs/2410.05460)). A process that mostly waits uses energy according to how long its CPU is busy and how often it wakes. Its rank in a compute benchmark doesn't decide that. [INFERENCE]
- **The TypeScript row reflects the benchmark programs, not the language.** TypeScript compiles to JavaScript and runs on the same runtime, so its 5× gap from JavaScript must come from how the programs were written. [INFERENCE]

## 3. Measurements on this machine

### 3.1 Setup

- **Machine:** one Windows development machine.
- **Versions:** Rust 1.98.1, Go 1.27.1, Node 24.18.0, Bun 1.4.2, Deno 2.9.7, Python 3.13.15.
- **Programs.** There are three programs per language, each written idiomatically: typed structs in Rust and Go, plain objects in JavaScript and Python. Appendix A has the details.
  1. **Start:** print a line and exit. This is the floor for any process: the harness itself, a plugin, a helper.
  2. **Router workload:** load a 0.92 MB transcript (180 messages; about 230,000 tokens at 4 characters per token). Then, 100 times:
     - append a user turn;
     - serialize the full request;
     - take its SHA-256;
     - estimate its tokens;
     - evaluate 20 rules.

     The workload re-serializes and re-hashes everything on every call, so its cost is an upper bound. An append-only implementation would hash only the new content.
  3. **HTTPS request:** one GET with each language's standard or most common client. This loads the TLS stack that a harness always carries.
- **Metrics:**
  - wall time;
  - peak working set, which is resident memory;
  - peak private bytes, which is committed memory;
  - CPU time. Windows counts it in 15.6 ms ticks, so small values are coarse.
- **Runs:** 15 for Start and 5 for the other programs. Warm-up runs came first and were discarded, and the order rotated across runtimes. Tables show medians.

### 3.2 Results

**Start** (print and exit)

| Runtime | Wall, ms (range) | Peak working set, MiB | Peak private, MiB |
|---|---|---|---|
| Rust | 13 (10–20) | 3.5 | 0.7 |
| Go | 15 (10–23) | 4.6 | 11.6 |
| Python | 43 (34–67) | 9.4 | 5.2 |
| Bun, compiled executable | 29 (23–38) | 13.6 | 45.9 |
| Bun | 37 (29–48) | 15.2 | 55.9 |
| Deno, compiled executable | 54 (39–72) | 23.6 | 11.6 |
| Deno | 74 (57–132) | 27.2 | 13.0 |
| Node | 100 (74–120) | 32.9 | 19.2 |

**Router workload** (0.92 MB transcript, 100 calls)

| Runtime | Per call, median ms | Per call, p95 ms | Load and parse, ms | Peak working set, MiB | Peak private, MiB | Total CPU, ms |
|---|---|---|---|---|---|---|
| Rust | 8.5 | 12.1 | 2.2 | 7.4 | 5.1 | 891 |
| Go | 7.3 | 16.1 | 3.3 | 18.0 | 57.7 | 797 |
| Python | 16.5 | 30.8 | 7.1 | 18.5 | 12.8 | 1,750 |
| Bun, compiled executable | 5.4 | 8.5 | 2.2 | 43.1 | 107.0 | 672 |
| Bun | 5.3 | 9.5 | 3.3 | 44.3 | 110.9 | 656 |
| Node | 8.4 | 12.4 | 5.6 | 60.4 | 61.5 | 906 |
| Deno, compiled executable | 9.0 | 12.9 | 5.7 | 72.5 | 63.1 | 1,031 |
| Deno | 10.9 | 17.0 | 7.8 | 77.0 | 64.3 | 1,281 |

**HTTPS request** (one GET, including process start)

| Runtime and client | Wall, ms | Peak working set, MiB | Peak private, MiB |
|---|---|---|---|
| Rust, reqwest (SChannel TLS) | 56 | 8.6 | 2.1 |
| Go, net/http | 70 | 11.8 | 46.5 |
| Bun, fetch | 49 | 19.9 | 70.3 |
| Python, urllib | 202 | 22.1 | 11.9 |
| Deno, fetch | 147 | 33.3 | 15.6 |
| Node, fetch | 175 | 47.3 | 61.2 |

**Size on disk**

| | Start | Router workload | HTTPS request |
|---|---|---|---|
| Rust binary (stripped) | 0.85 MB | 1.11 MB | 3.25 MB |
| Go binary (`-ldflags="-s -w"`) | 1.67 MB | 2.94 MB | 6.87 MB |
| Bun `--compile` executable | 86.1 MB | 86.1 MB | — |
| `deno compile` executable | 79.8 MB | 79.8 MB | — |

The runtimes themselves:

| Runtime | Size |
|---|---|
| `node.exe` | 92.5 MB |
| `bun.exe` | 86.1 MB |
| `deno.exe` | 97.5 MB |
| Python installation (python.org layout, no third-party packages; includes 63 MB of documentation) | 290.7 MB |

**Build times on this machine**

| | Go | Rust |
|---|---|---|
| First build | 10–47 s per program (the first build also compiles the standard library) | HTTPS program's dependency tree: 137 s (debug) and 205 s (release), even though an earlier attempt had already compiled some shared crates |
| One-line change, median of 4 runs | 1.3 s | 3.5 s (release), 4.0 s (debug) |

The first Go rebuild took 27.9 s and the rest took 0.4–1.3 s. For comparison, `bun build --compile` took about 1 s and `deno compile` 15–18 s.

### 3.3 What the numbers say

**Compiled versus interpreted runtimes**

- The JavaScript runtimes used 2.3–10.4× Rust's working set, and Python 2.5–2.7×.
- They started 2.2–7.4× slower.
- They ship as, or depend on, runtimes of 80–291 MB, against native binaries of 0.9–6.9 MB.

**Rust versus Go**

- **Startup:** the same.
- **Working set:** Go's was 1.3–2.4× Rust's.
- **Committed memory under load:** Go used 57.7 MiB against Rust's 5.1 MiB in the router workload. The [Go GC guide](https://go.dev/doc/gc-guide) explains the headroom: with the default `GOGC=100`, the heap may grow to about twice the live heap before the next collection.

**Per-call CPU**

- Every runtime needed 5–17 ms, which doesn't matter next to a model round trip. [INFERENCE]
- Rust wasn't the fastest here. The likely reason is that its pure-Rust SHA-256 crate competed against assembly-optimized SHA-256 in the other runtimes. [INFERENCE; not profiled]

**Compiled JavaScript executables**

- They start faster than their runtimes: Bun 29 ms against 37 ms, and Deno 54 ms against 74 ms.
- But they embed the whole runtime, so even a one-line program is 80–86 MB.

**Windows toolchains**

- Go needed only its 79 MB zip.
- Rust's GNU toolchain linked the Start and router programs on its own. The HTTPS program's dependencies (`windows-sys` 0.61) also needed a full MinGW-w64, a 274 MB download.
- The MSVC route needs Visual Studio Build Tools instead. It wasn't tried.

**Limits**

- One machine.
- Short-lived processes: memory growth over hours wasn't measured.
- No UI.
- No vendor SDKs loaded.

## 4. What existing harnesses use, and why

### 4.1 Languages and packaging

| Harness | Main language (share of code) | Ships as |
|---|---|---|
| [Codex CLI](https://github.com/openai/codex) | Rust (97%) | Native binary; the npm package wraps it |
| [Goose](https://github.com/aaif-goose/goose) | Rust (72%), TypeScript (23%) | Native CLI binary; Electron desktop app |
| [aichat](https://github.com/sigoden/aichat) | Rust (82%) | Native binary |
| [Zed](https://github.com/zed-industries/zed) (editor with agent) | Rust | Native app |
| [Crush](https://github.com/charmbracelet/crush) | Go (98.5%) | Native binary |
| [Plandex](https://github.com/plandex-ai/plandex) | Go (93%) | Native binary; Docker for the self-hosted server |
| Claude Code | TypeScript (closed source) | Bun single-file executable |
| [OpenCode](https://github.com/anomalyco/opencode) | TypeScript (74%) | Bundled Bun, with its own terminal UI library, [OpenTUI](https://github.com/anomalyco/opentui), which has a Zig core |
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | TypeScript (97%) | npm package on Node |
| [Pi](https://github.com/earendil-works/pi) | TypeScript (96%) | npm package on Node |
| [Aider](https://github.com/Aider-AI/aider), [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent), [llm](https://github.com/simonw/llm) | Python | pip, pipx or uv |
| [StarNet](https://github.com/androoAGI/starnet) | JavaScript (92%), Rust (0.6%) | Tauri desktop app with a Node sidecar |

Language shares come from the GitHub languages API, retrieved on 2026-09-28. Goose, OpenCode and Pi have moved to the repositories linked above.

### 4.2 Stated reasons

**Codex CLI (TypeScript → Rust).** OpenAI's announcement ([discussion #1174](https://github.com/openai/codex/discussions/1174)) listed four goals, each of which matches a DwarfHarness aim:

- "Zero-dependency Install": the TypeScript version required Node.
- "Native Security Bindings": they already shipped Rust for Linux sandboxing.
- "Optimized Performance — no runtime garbage collection, resulting in lower memory consumption."
- "Extensible Protocol": a wire protocol, so the agent can be extended in other languages.

**Goose (rewritten in Rust).** The team planned to "rewrite goose in rust" so they could ship the CLI as a binary, and for portability ([discussion #230](https://github.com/block/goose/discussions/230)).

**Crush and OpenCode.** These went separate ways from one Go codebase.

- Charm: "Crush started out as OpenCode" ([discussion #360](https://github.com/charmbracelet/crush/discussions/360)). It continued in Go with the original author.
- The OpenCode fork is now TypeScript on Bun.

**Claude Code (TypeScript on Bun).**

- Bun's announcement says "Claude Code ships as a Bun executable to millions of users" ([Bun blog](https://bun.com/blog/bun-joins-anthropic)).
- Anthropic acquired Bun, and said the collaboration "directly drove the recent launch of Claude Code's native installer" ([Anthropic](https://www.anthropic.com/news/anthropic-acquires-bun-as-claude-code-reaches-usd1b-milestone)).

### 4.3 Resource reports

These are single user reports from issue trackers. They aren't controlled comparisons.

| Harness | Report |
|---|---|
| Claude Code | Growth past 120 GB of memory ([#4953](https://github.com/anthropics/claude-code/issues/4953)) |
| Gemini CLI | "High memory usage detected: 7.77 GB" ([#14713](https://github.com/google-gemini/gemini-cli/issues/14713)) |
| OpenCode | Intermittent 24–28 GB memory exhaustion in its TUI ([#51761](https://github.com/anomalyco/opencode/issues/51761)) |
| Codex CLI (Rust) | 50–60% CPU while idle from background subprocess churn, with a 565 MB peak ([#15620](https://github.com/openai/codex/issues/15620)) |

What this shows: every ecosystem has reports like these, including the Rust one. The causes described are architectural: render loops, leaks and child processes. The language sets a lower floor; it doesn't prevent those failures. §7 covers them.

## 5. Ecosystem fit

| Need | Rust | Go | TypeScript (Bun, Node, Deno) | Python |
|---|---|---|---|---|
| Official Anthropic SDK ([list](https://platform.claude.com/docs/en/cli-sdks-libraries/overview)) | No | Yes | Yes | Yes |
| Official OpenAI SDK ([list](https://developers.openai.com/api/docs/libraries)) | No. OpenAI lists the community crate `async-openai` | Yes (beta) | Yes | Yes |
| MCP SDK ([tiers](https://modelcontextprotocol.io/docs/sdk)) | Tier 1 (`rmcp`) | Tier 1 | Tier 1 | Tier 1 |
| Terminal UI | [Ratatui](https://github.com/ratatui/ratatui) (MIT) | [Bubble Tea](https://github.com/charmbracelet/bubbletea) (MIT) | Ink (MIT); OpenTUI (MIT) | [Textual](https://github.com/Textualize/textual) (MIT) |
| Sandboxed in-process plugins | WASM through [Wasmtime](https://github.com/bytecodealliance/wasmtime) or [Extism](https://github.com/extism/extism) | WASM through [wazero](https://github.com/wazero/wazero), which is pure Go, or Extism | Not sandboxed: `import()` runs with the harness's own permissions | Not sandboxed: `importlib` runs with the harness's own permissions |
| Native plugins | Dynamic libraries go through the C ABI (`cdylib`) ([reference](https://doc.rust-lang.org/reference/linkage.html)) | Go's `plugin` package is "supported only on Linux, FreeBSD, and macOS" ([source](https://github.com/golang/go/blob/master/src/plugin/plugin.go)), so not on Windows | — | — |
| Out-of-process plugins | JSON-RPC over stdio, as MCP and LSP do | Same | Same | Same |
| Optional local classifier | `ort` (ONNX Runtime) or candle, both in-process | [onnxruntime_go](https://github.com/yalue/onnxruntime_go) requires cgo; allowed by the revised toolchain policy, though a lazy helper remains preferred for isolation | onnxruntime-node, transformers.js | ONNX Runtime, transformers |
| Distribution | Single native binary | Pure-Go dependencies can support a static `CGO_ENABLED=0` build; native-GUI and cgo dependency packaging must be evaluated separately | npm on a runtime, or an 80–86 MB compiled executable. Node's single-executable feature is "Stability: 1.1 - Active development" ([docs](https://nodejs.org/api/single-executable-applications.html)) | Interpreter plus packages |
| Windows build toolchain | MSVC Build Tools, or MinGW-w64 (§3.3) | Go zip for the measured pure-Go baseline; a C toolchain for dependencies requiring cgo | The runtime only | The interpreter only |

The table leaves out two things that don't separate the candidates:

- **Tokenizers:** OpenAI's tiktoken has a Rust core. Anthropic's documented way to count Claude tokens is an API endpoint that returns an estimate ([token counting](https://platform.claude.com/docs/en/build-with-claude/token-counting)), so offline Claude counts are estimates in every language. [INFERENCE]
- **OS sandboxing:** Codex's Linux sandbox is written in Rust ([README](https://github.com/openai/codex/blob/main/codex-rs/linux-sandbox/README.md)). Rust and Go both reach the operating-system sandbox APIs directly.

**Official SDKs matter less here than the table suggests.** The routing design has the harness own its provider adapters. An adapter must control:

- the exact request bytes, for cache matching;
- the cache breakpoints;
- the beta headers;
- the raw usage fields.

A thin HTTP-and-SSE adapter gives that control directly, in any language. [INFERENCE]

**Two notes on plugins:**

- Out-of-process plugins keep a crashing or misbehaving plugin from corrupting the harness's state, and they let advanced users write plugins in any language. That matches the aim of keeping control and trust in the harness. [INFERENCE]
- Go's native `plugin` package doesn't support Windows. That does not rule out portable plugins: child processes are the selected default, and sandboxed WASM is another option.

## 6. The candidates: what each adds, and what it costs

| Candidate | Adds | Costs |
|---|---|---|
| **Rust** | <ul><li>The smallest footprint in every test.</li><li>No garbage collector.</li><li>The closest precedent: Codex's reasons match this project's.</li><li>In-process ONNX, candle and Wasmtime, with no C toolchain at run time.</li><li>Enums with exhaustive matching, which suit the routing contracts (`Needs`, `Plan`, invalidation classes, content blocks). [INFERENCE]</li><li>Codex's Apache-2.0 sandbox code to learn from.</li></ul> | <ul><li>The steepest learning curve of the four, which also makes the code harder for you to review.</li><li>Builds: minutes for a cold dependency tree, and about 3.5 s for a one-line change here.</li><li>A heavier Windows toolchain.</li><li>No official provider SDKs, though the design doesn't need them.</li></ul> |
| **Go** | <ul><li>A footprint close to Rust's.</li><li>The simplest measured toolchain and the fastest builds (1.3 s).</li><li>Pure-Go builds can cross-compile without a C toolchain; this is not a native-GUI packaging guarantee.</li><li>Official SDKs from both providers.</li><li>Easy to read and audit.</li><li>Precedent: Crush and Plandex.</li></ul> | <ul><li>Garbage-collector headroom: 58 MiB committed against Rust's 5 MiB under load.</li><li>No sum types, so the contracts need validation code.</li><li>The researched in-process ONNX bindings need cgo, now permitted rather than excluded.</li><li>Bubble Tea's default renderer wakes 60 times a second (§7).</li></ul> |
| **TypeScript on Bun** | <ul><li>The fastest per-call times measured.</li><li>Any plugin loads with `import()`.</li><li>Historical only: StarNet/existing JavaScript reuse was considered in the 2026-09-28 survey; the 2026-09-29 independent-product decision removes that reuse requirement.</li><li>Anthropic's own choice for Claude Code.</li></ul> | <ul><li>2.3–6× Rust's working set, and 107–111 MiB committed under load.</li><li>An 86 MB executable.</li><li>In-process plugins get full privileges.</li></ul> |
| **TypeScript on Node or Deno** | The largest ecosystem. | <ul><li>The highest memory measured: 24–77 MiB working set, including compiled Deno executables.</li><li>The slowest starts: 54–100 ms.</li><li>A 92–98 MB runtime.</li></ul> |
| **Python** | <ul><li>The best ML ecosystem.</li><li>Simple to write.</li><li>Good for evaluation and replay tooling.</li></ul> | <ul><li>1.5–3× the per-call time of the other runtimes.</li><li>A 291 MB installation.</li><li>Packaging tools that can't cross-compile.</li><li>A realistic harness would import far more than these tests did; that wasn't measured.</li></ul> |

Zig and C# were ruled out in the 2026-09-28 survey:

- **Zig** is pre-1.0, and has no official provider SDK or MCP SDK.
- **C#** compiled with NativeAOT needs the MSVC linker on Windows and cannot dynamically load managed assemblies ([docs](https://learn.microsoft.com/en-us/dotnet/core/deploying/native-aot/)). This limits that form of in-process extension; it does not rule out child-process plugins. The former no-C-toolchain requirement is no longer current.

## 7. Architecture rules that matter more than the language

These are research recommendations targeting the failure modes in §4.3, with later context-isolation annotations. Their imperative wording is not independent owner approval of a process/session architecture: current ownership and lifecycle contracts belong to core §§3–4 and §7, and the one-session/many-context model remains proposed.

1. **Redraw on events, never on a timer.** When idle, block waiting for input or network data.
   - Bubble Tea's renderer runs a ticker at 60 frames per second by default, for as long as the renderer runs ([source](https://github.com/charmbracelet/bubbletea/blob/main/tea.go)), so the process wakes 60 times a second even when nothing changes.
   - Ratatui leaves the loop to the application, and its docs show redraws driven by `event::read()` ([event handling](https://ratatui.rs/concepts/event-handling/)).
2. **Run agents as tasks in one process, not as one process each.** A crew of ten agents in separate Node processes would hold ten times the 33–60 MiB that one Node process used here. In one Rust or Go process they share a single runtime and HTTP client. [INFERENCE]
   - Sharing a runtime does not merge private contexts. The colony core owns each agent's one execution session; terminal and native GUI are views of it. General work, directed relationships and board participation retain separate private context IDs and routing/provider/cache state even inside that same session (`DESIGN-CORE-AND-LAUNCHERS.md` §3, §4 and §7.3). Dormant contexts need no dedicated process or model call. [INFERENCE]
3. **Tie every child process to the execution session.** Tools, MCP servers and plugins should be killed when that session ends, and polling subprocesses shouldn't run while idle. Tool dispatch, results and approvals remain bound to the initiating agent/context/turn; sharing a process lifetime must not transfer private state or authority when the agent changes counterpart. [INFERENCE] Codex's idle-CPU report was subprocess churn (§4.3).
4. **Keep the transcript on disk.** Hold in memory only what the next request needs.
5. **Set resource budgets and check them automatically:** idle wakeups, idle working set, per-call time, and the number of child processes. [INFERENCE]

## 8. Consequences for the other design documents

**2026-09-29 design-consequence addendum, subordinate to the current designs.** The routing document owns routing contracts; the core document owns product decisions and process/context/messaging proposals. The summaries below are not additional approvals or an implementation checklist.

**Routing layer** ([`DESIGN-ROUTING-LAYER.md`](DESIGN-ROUTING-LAYER.md)):

- Classifier and context-module plugins run out of process over JSON-RPC on stdio, with a declared cost that includes resident memory.
- Provider adapters are compiled into the core.
- The optional local embedding classifier remains a preferred lazy helper for isolation, with no running helper while disabled. It is not mandatory merely because ONNX bindings need cgo: the blanket ban has been lifted, and an in-process integration may be evaluated on its actual costs.

**Independent product, execution and context ownership** (current proposal as of 2026-09-29):

- The core owns independently designed capabilities, recipes/workflows and skills (`DESIGN-CORE-AND-LAUNCHERS.md` §11 and §12). Historical StarNet research is not a requirement to port its compiler, embed goja, reuse its drawing code or retain parity tooling.
- An agent owns its identity/folder with `Agent.md`; general `Memory.md` holds deliberately retained general lessons, not automatically shared or pooled relationship memory. The uniform proposal gives every agent one execution session, including Orchestrators, Griots, Diplomats and their Grand counterparts; session count is not the mechanism for preserving relationship memory. [INFERENCE]
- Each `(agent_id, context_id)` privately owns memory/handoff, rendered history, compaction, provider response/tool continuations and `ContextRouteState`. Relationship memory is directional: A's attitudes toward B are not B's attitudes toward A. Only the active context plus explicitly authorized shared records is rendered for the model; the execution-wide audit and common agent files must not silently pool relationships (`DESIGN-CORE-AND-LAUNCHERS.md` §7.3; `DESIGN-ROUTING-LAYER.md` §5.1 and §6.4). [INFERENCE]
- A worker switching from its Orchestrator to its Diplomat must compose a new context-bound turn, without the prior counterpart's history, response IDs, tool results or grants. Board participation is another context; private observations about peers learned there do not authorize worker-to-worker DMs. The permitted Grand/Corps hierarchy and channels stay in `DESIGN-CORE-AND-LAUNCHERS.md` §7.1, §7.2 and §7.5 rather than a second matrix here. [INFERENCE: isolation mechanism]
- Agent base policy and explicit agent/account budgets are shared constraints, not pooled private state. Approvals bind to agent/context/turn; a reusable relationship grant says **this context**, with any wider grant explicitly selected. A session's lifecycle identity alone cannot isolate or authorize a relationship. [INFERENCE]
- Logical conversations pass through the proposed colony-owned core broker; agents in that core use method/queue calls. External launchers/processes use colony-scoped local IPC rather than a socket or server per agent/relationship. See `DESIGN-CORE-AND-LAUNCHERS.md` §7.4 and §3.4 for the proposal and its unresolved implementation details. This adds no separate broker service requirement and changes none of the historical runtime measurements. [INFERENCE]
- Cloning creates a new agent identity/folder with copied `Agent.md` and an optional independent memory copy, never an alias or automatic inheritance of private peer files, mailboxes, credentials or channel grants. Reassignment does not transfer private history by role title. Distinct memory and deliberate drift remain features; birth still requires superior permission, with selection policy/process/protocol left open (`DESIGN-CORE-AND-LAUNCHERS.md` §7.6). [INFERENCE: isolation and clone mechanism]

## 9. Decision questions (updated on 2026-09-29)

These are resolved questions from the language comparison, retained with their current answers—not the next-session agenda. For unresolved mechanisms and the proposed next design step, use core §10 and [its fresh-session entry point](DESIGN-CORE-AND-LAUNCHERS.md#start-here-for-a-fresh-session).

1. **Will you write and review Rust yourself?** The owner chose Go on 2026-09-28 and retains it after agreeing to acquire a C toolchain.
2. **Does DwarfHarness need StarNet's JavaScript modules at run time?** No. The product is independent, with no runtime/compiler reuse or compatibility/parity contract. Historical survey note from 2026-09-28: StarNet's `pipeline.js` had 1,437 lines at commit `fbddbf9`; that is not a porting target.
3. **Is the Dwarf Fortress-style interface a terminal UI or a graphical window?** A native graphical window launched by `dwarf-harness-GUI`; `dwarf-harness` remains the plain terminal entry point. Both expose the same live core-owned sessions simultaneously. Visual design and unresolved native mechanisms belong to the core design (`DESIGN-CORE-AND-LAUNCHERS.md` §6 and §10), not the superseded browser proposal.

## Appendix A — Measurement details

**Fixture.** Seeded random ASCII text:

- 60 turns, each made of:
  - a user text of 200–2,000 characters;
  - an assistant text of 100–800 characters, plus a `tool_use` block;
  - a `tool_result` of 1,000–25,000 characters.
- 8 tools with JSON schemas.
- A 2,000-character system prompt.
- 0.92 MB in total.

**Router workload.** Parse the fixture once. Then, per call:

1. Append `{"role":"user","content":[{"type":"text","text":"Follow-up request number i: …"}]}`.
2. Serialize `{model, max_tokens, system, tools, messages}` compactly.
3. Hex-encode its SHA-256.
4. Estimate tokens as bytes ÷ 4.
5. Evaluate 20 comparisons that set a floor value.
6. Fold the hash prefix into a checksum, so that no work is optimized away.

**HTTPS request.** A GET to `https://www.gstatic.com/generate_204`, which returns status 204.

**Builds and runtimes**

| Runtime | Build or invocation | Libraries |
|---|---|---|
| Rust | Edition 2021; release profile with `strip = true`; target `x86_64-pc-windows-gnu`. The HTTPS program was linked with MinGW-w64 GCC 16.2.0 | serde 1, serde_json 1, sha2 0.10; reqwest 0.12 with default features off plus `blocking` and `native-tls` (SChannel on Windows) |
| Go | `go build -ldflags="-s -w"` with `CGO_ENABLED=0` | Standard library only: `encoding/json` with typed structs, and `json.RawMessage` for schemas and tool inputs |
| Node, Bun, Deno | One `.mjs` file each, with `node:fs`, `node:crypto` and `fetch`. Deno ran with `--allow-read` or `--allow-net`. Compiled with `bun build --compile` and `deno compile --allow-read` | — |
| Python | — | Standard library: `json`, `hashlib`, `urllib` |

**Measurement.**

- **Launch and memory.** A Python script started each program with `subprocess.Popen`. It kept the process handle after exit, and read memory with `GetProcessMemoryInfo` (`PeakWorkingSetSize`, `PeakPagefileUsage`) and CPU time with `GetProcessTimes`.
- **Wall time** was measured around the whole process lifetime.
- **Order:** warm-up runs first (2 for Start, 1 for the others), then the measured runs, rotating through the runtimes round-robin.

**Toolchain downloads**

| Toolchain | Download |
|---|---|
| Go | 78.9 MB |
| Bun | 39.8 MB |
| Deno | 42.6 MB |
| rustup-init (the toolchain it fetched wasn't recorded) | 12.7 MB |
| MinGW-w64 | 274 MB |

All were installed into a temporary folder and deleted after the measurements. None was added to `PATH`.

## Appendix B — Sources

Harnesses and rationale:

- Codex CLI rewrite announcement — <https://github.com/openai/codex/discussions/1174>
- Goose Rust rewrite discussion — <https://github.com/block/goose/discussions/230>
- Crush and OpenCode history — <https://github.com/charmbracelet/crush/discussions/360>
- Bun joins Anthropic — <https://bun.com/blog/bun-joins-anthropic>
- Anthropic acquires Bun — <https://www.anthropic.com/news/anthropic-acquires-bun-as-claude-code-reaches-usd1b-milestone>
- Resource reports — <https://github.com/anthropics/claude-code/issues/4953>, <https://github.com/google-gemini/gemini-cli/issues/14713>, <https://github.com/anomalyco/opencode/issues/51761>, <https://github.com/openai/codex/issues/15620>

Runtime evidence:

- Pereira et al., "Ranking programming languages by energy efficiency" — <https://haslab.github.io/SAFER/scp21.pdf>
- van Kempen et al., "It's Not Easy Being Green" — <https://arxiv.org/abs/2410.05460>
- Go garbage collector guide — <https://go.dev/doc/gc-guide>
- Apple Energy Efficiency Guide, timers — <https://developer.apple.com/library/archive/documentation/Performance/Conceptual/EnergyGuide-iOS/MinimizeTimerUse.html>

Ecosystem:

- Anthropic client SDKs — <https://platform.claude.com/docs/en/cli-sdks-libraries/overview>
- OpenAI libraries — <https://developers.openai.com/api/docs/libraries>
- MCP SDKs and tiers — <https://modelcontextprotocol.io/docs/sdk>
- Go `plugin` package — <https://github.com/golang/go/blob/master/src/plugin/plugin.go>
- Rust linkage reference — <https://doc.rust-lang.org/reference/linkage.html>
- Node single executable applications — <https://nodejs.org/api/single-executable-applications.html>
- .NET Native AOT — <https://learn.microsoft.com/en-us/dotnet/core/deploying/native-aot/>
- onnxruntime_go — <https://github.com/yalue/onnxruntime_go>
- Codex Linux sandbox — <https://github.com/openai/codex/blob/main/codex-rs/linux-sandbox/README.md>
- Anthropic token counting — <https://platform.claude.com/docs/en/build-with-claude/token-counting>
- Bubble Tea renderer loop — <https://github.com/charmbracelet/bubbletea/blob/main/tea.go>
- Ratatui event handling — <https://ratatui.rs/concepts/event-handling/>
