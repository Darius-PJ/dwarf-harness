# DwarfHarness: Rust or Go?

**A decision guide for the project owner · 2026-09-29**

**Status:** Comparison and recommendation, not a new owner decision. The current designs record Go as selected; this document reconsiders that choice at the owner's request without changing it. No implementation changes or new benchmarks were performed. The superseded design was neither read nor changed.

## 1. The answer first

**My recommendation is Go for DwarfHarness as currently envisioned—not merely for a prototype, but for the intended full product.** [INFERENCE]

The defining challenge is coordinating agents, their private memories, permissions, durable messages and model calls correctly while the organization and interaction rules continue to evolve. Go offers a comparatively straightforward way to build and maintain that coordination machinery. The existing measurements show a real resource penalty versus Rust, but do not establish that Go would miss any requirement of the planned application.

**Rust is the stronger choice if your overriding priority is tighter resource use and more compiler-enforced correctness, and you accept greater implementation complexity to obtain them.** Its benefits are substantive, especially for concurrent state and long-lived software. They are not necessary to enable the colony vision, and they do not automatically solve its hardest privacy and recovery problems. [INFERENCE]

The actual trade is:

> **Go:** easier to construct and revise; more responsibility on runtime validation, concurrency discipline and verification.
>
> **Rust:** more mistakes rejected before the program runs; more effort expressing ownership and asynchronous work; a lower footprint in the existing small benchmarks.

**Neither choice makes the agents smarter, improves their culture, or lowers the model provider's token prices.** Those outcomes depend on the models, prompts, context selection, routing and interaction policies.

### An important correction to the old comparison

Go was originally selected partly to avoid a C toolchain. You now accept that toolchain for the native GUI. **That former restriction no longer rules Rust out.** Nor is this a StarNet port: neither language owes compatibility to an inherited JavaScript runtime.

This recommendation therefore rests on the current product's engineering tradeoffs, not on preserving an obsolete constraint or defending a past decision.

## 2. What stays the same in both versions

The current [core design](DESIGN-CORE-AND-LAUNCHERS.md) and [routing design](DESIGN-ROUTING-LAYER.md) describe essentially the same product in either language:

- A Colony with specialized Corps, Orchestrators, Griots, Diplomats, workers and the Grand roles.
- Distinct identities and private, directional relationship memories: A's memory of B is not B's memory of A.
- The specified communication rules: worker peers use their Corps board; Griots reach workers through story publications; only Diplomats have lateral cross-Corps direct channels.
- Independent clones with optional selected memory copies, deliberate drift, and superior approval before birth. Unresolved birth policies and Grand mandates remain unresolved.
- `dwarf-harness` as the plain terminal entry point and `dwarf-harness-GUI` as the native-window entry point, with identical harness capabilities and simultaneous views of the same live session.
- One GUI instance per colony; Dwarf Fortress styling in that GUI only.
- Harness-owned records, permissions, tool execution, approvals, usage accounting and cache-aware routing. Anthropic and OpenAI first; no automatic provider switching in v1, but user-chosen handovers must work.
- Recipes, executable workflow graphs, host-checked acceptance, lazily loaded skills and child-process extensions.

The proposed **one execution session per agent, many private contexts, embedded durable broker and colony core host** are viable in both languages. They remain proposals, not choices made by this comparison.

**Networking analogy:** language choice is closer to choosing the implementation platform for a controller than choosing your VLAN plan or ACL policy. Rust and Go can implement the same topology. Neither discovers the correct segmentation rules for you. Logical context separation also is not, by itself, an OS-level security boundary.

## 3. What the two implementations would look like

Under the proposed colony-host arrangement, the shape is the same:

```text
Plain terminal --------\
                        > Colony core authority --> Model providers
Native GUI ------------/          |
                                  +--> Private contexts and durable messages
                                  +--> Authorized tools and child-process plugins
```

These are **two views of one owner**, not two copies of the agent engine. Agents and relationships are logical records and scheduled work—not an OS process per agent or a network socket per friendship.

The following are implementation judgments, not selected new mechanisms. [INFERENCE]

| Area | Written in Go | Written in Rust | Consequence for the vision |
|---|---|---|---|
| Concurrent work | Goroutines: lightweight activities managed by Go's runtime; queues and locks coordinate them. | Asynchronous tasks, commonly using an executor such as Tokio; queues and locks coordinate them. | Both can wait on many providers/tools without one OS thread per waiting operation. Neither supplies durable mailboxes automatically. |
| Core state | Ordinary records, explicit owners and validation functions; mutable sharing must be synchronized correctly. | Owned records and controlled sharing; the compiler checks many lifetime and cross-thread rules. | Rust provides stronger guardrails around how shared state is accessed; both still need the right authority model. |
| Router and workflow states | Explicit state values and checks for legal combinations and transitions. | Enums can represent distinct alternatives and require handling every case. | Rust can catch more missing state-handling code when the design changes. Go can implement the same behavior with more explicit checks. |
| Provider adapters | The standard library already supplies HTTP/HTTPS; thin streaming adapters fit the current recommendation. | Typically library dependencies for HTTP, serialization and async execution. | Both can control request bytes, usage parsing, cache behavior and handovers. Neither requires an official provider SDK. |
| Persistent state | A chosen durable store, transactions/checkpoints and recovery logic. | The same responsibilities, using Rust libraries. | Disk formats, retention and crash correctness are design choices, not language features. |
| Desktop and terminal | A Go core and thin clients; keep GUI dependencies out of the terminal path. | A Rust core and thin clients; the same dependency separation. | No language requires sacrificing either interface or duplicating the engine. |

Go's HTTP capability is documented in its [standard library](https://go.dev/src/net/http/doc.go). Rust's state-handling and thread-sharing guarantees are discussed in section 5.

### A concrete example: a worker changes counterpart

A worker finishes speaking with its Orchestrator, then speaks with its Diplomat. In **both** implementations the core must:

1. Finish or yield the old turn.
2. Bind the new turn to the Diplomat relationship.
3. Load only that context's authorized memory and history.
4. Keep the Orchestrator's approvals, provider continuations and pending results with the original context.
5. Publish the same resulting events to the terminal and GUI without running the turn twice.

Rust can help make the data objects and transitions harder to misuse. But if both relationships have valid IDs and the programmer selects the wrong one, a perfectly memory-safe Rust program can still disclose private information. Go faces that same business-rule problem. **The context-isolation scenario must be verified in either version.** [INFERENCE]

## 4. Lightweight operation: where Rust genuinely wins, and where it does not

### What the project has actually measured

These figures come from [the existing language research, section 3](RESEARCH-IMPLEMENTATION-LANGUAGE.md#3-measurements-on-this-machine), measured on one Windows machine on **2026-09-28**. They are historical small-program results, not a finished DwarfHarness comparison.

| Measurement | Rust | Go | Plain-language meaning |
|---|---:|---:|---|
| Print-and-exit startup, median | 13 ms | 15 ms | No meaningful user-facing startup advantage established here. |
| Router workload, peak resident memory | 7.4 MiB | 18.0 MiB | Rust used less actual RAM in this workload. |
| Router workload, peak private committed memory | 5.1 MiB | 57.7 MiB | Go reserved substantially more committed backing for private memory; this is not the same as RAM resident at that instant. |
| Router preparation per call, median | 8.5 ms | 7.3 ms | Rust was not faster in every measurement. |
| HTTPS test executable, stripped | 3.25 MB | 6.87 MB | Rust's test executable was smaller; neither number predicts the final installer. |
| One-line rebuild, median | 3.5 s, release | 1.3 s | Go had the shorter edit/build loop in this experiment. |

The router workload repeatedly serialized and hashed a roughly 0.92 MB transcript. The study used Rust 1.98.1 and Go 1.27.1; its exact setup and dependencies remain in the source report. MiB measures memory here; MB measures file sizes.

**Do not multiply these process measurements by the number of agents.** A shared colony host pays its runtime baseline per host, not per relationship. More agents still add active requests, records and buffers. More independent colony hosts add more process baselines. Neither total has been measured.

### Why the difference exists

**Go uses garbage collection:** its runtime finds memory the program no longer needs and reclaims it. That reduces manual lifetime work, but needs CPU time and memory headroom. The collector does most of its work concurrently; this is not a claim that Go routinely freezes the whole application for long pauses. Its [GC guide](https://go.dev/doc/gc-guide) explains the CPU–memory tradeoff.

**Rust normally uses ownership and automatic cleanup without a tracing garbage collector.** The programmer expresses who owns data and how it may be shared; the compiler checks the rules. That offers tighter lifetime control, not an absence of allocation, cleanup work or memory leaks.

**Implication [INFERENCE]:** Rust is attractive for an always-running, tightly budgeted host. But neither the present design nor the measurements establish a hard memory or latency target that Go cannot meet. Neither language guarantees hard real-time responsiveness.

### Separate three different bills

| Bill | What mainly determines it | Does choosing Rust directly reduce it? |
|---|---|---|
| Model bill | Selected models, tokens, retries, summaries, Diplomat relays, Griot cadence and cache reuse. | **No.** Equivalent requests receive the same provider pricing. |
| Device bill | Core runtime plus active context, queues, GUI renderer, tools and optional services. | **Potentially the core portion.** A smaller core does not guarantee a smaller whole application. |
| Development bill | Implementation, review, debugging, builds and long-term maintenance. | **Not established.** Stronger checking can prevent debugging; expressing those checks takes work. |

There is no measured maximum colony size, battery comparison, long-running memory comparison or native-GUI comparison. A claim that Rust supports a particular multiple of Go's agent count would be invented.

## 5. Reliability and security: Rust's strongest argument

The important distinction is **which mistakes are prevented before shipping**, not “safe Rust versus unsafe Go.”

- **Memory safety:** ordinary Go avoids manual memory freeing; safe Rust checks ownership and lifetimes. Rust's guarantees rely on sound implementations underneath safe interfaces. Native/unsafe code needs care in either ecosystem.
- **Data races:** two concurrent activities touching shared memory unsafely. Safe Rust prevents these through ownership and thread-sharing rules such as [Send and Sync](https://doc.rust-lang.org/book/ch16-04-extensible-concurrency-sync-and-send.html). Go requires correct synchronization; its [race detector](https://go.dev/doc/articles/race_detector) finds races on paths actually exercised, not every possible path. Go's [memory model](https://go.dev/ref/mem) warns that races can still cause corruption.
- **Missing states:** Rust's [enums and exhaustive matching](https://doc.rust-lang.org/book/ch06-02-match.html) can force code to account for a newly added alternative—provided developers do not hide it behind a catch-all branch. That fits states such as accepted, processed, failed or awaiting approval. It does not prove the transition itself is correct.

**Rust's benefit is particularly relevant to a colony that runs for hours and receives events from many sources.** Go's simpler syntax does not cancel that advantage. [INFERENCE]

However, neither language automatically prevents:

- two valid GUI/terminal commands approving the same action twice;
- an authorized agent reading the wrong relationship's history;
- two agents waiting forever for each other's replies;
- losing an “accepted” message because it was not committed to storage;
- repeating a paid call or external side effect after an ambiguous crash;
- an unrestricted shell reading another agent's files as the same OS user;
- a Griot story being treated as evidence or authority when it is only content.

**Think of Rust's compiler as an additional engineering safety inspector, not the author of your ACLs.** A race-free program can still make the wrong decision in the wrong order.

Cancellation also needs design in both. Go activities must cooperate with cancellation signals; in Tokio, simply dropping a task handle [does not stop its task](https://docs.rs/tokio/latest/tokio/task/struct.JoinHandle.html). Tool descendants still require OS process-lifetime controls. Neither language makes “close the GUI,” “cancel this turn” and “shut down the colony” interchangeable.

## 6. The GUI is a separate decision—not a Rust advantage by default

“Native” can mean three things:

1. **An application-owned desktop window:** the current requirement.
2. **OS-native controls:** Windows itself supplies the buttons and other widgets.
3. **No browser renderer:** the toolkit draws the interface instead of using HTML/CSS in an embedded webview.

The current design requires the first, but leaves the renderer open. An embedded webview is not the superseded external-browser launcher: it remains inside an application-owned window. It still has browser-engine resource and security implications.

| Approach | Go examples | Rust examples | What you are choosing |
|---|---|---|---|
| Toolkit-drawn interface | Fyne or Gio | egui with eframe | Custom-drawn controls and graphics without an HTML webview; renderer and platform dependencies still exist. |
| Embedded web interface | Wails | Tauri | HTML/CSS/JavaScript inside a desktop window; an additional frontend layer and a restricted bridge to native code. |

These are examples, not selected dependencies or a ranking. Primary documentation: [Fyne setup](https://docs.fyne.io/started/quick/), [Gio platform requirements](https://gioui.org/doc/install), [egui](https://github.com/emilk/egui), [Wails architecture](https://wails.io/docs/introduction/) and [Tauri prerequisites](https://v2.tauri.app/start/prerequisites/).

**Both languages can deliver the glyph map, rooms, agents, boards and workflow display.** Dwarf Fortress styling is a presentation requirement, not proof that a game engine or Rust is necessary. Conversely, Go does not confine you to a utilitarian-looking interface. [INFERENCE]

Important consequences:

- **Toolkit choice may outweigh the small-core footprint difference.** Measure the whole process tree, including any webview helpers; a small executable does not imply small running memory. [INFERENCE]
- **A native framework's built-in frontend/backend bridge is not the colony broker.** Both launchers still need the same core-owned command/event contract.
- **Idle efficiency needs verification.** An event-driven design should not redraw or animate continuously when nothing changes. No framework's description proves this application's idle behavior.
- **C tools are principally a build-machine concern, not something every user must install.** Actual runtime libraries and installers depend on the selected stack. Fyne documents a C compiler requirement; Gio documents no cgo requirement on Windows. Tauri's Windows build needs Microsoft C++ Build Tools, and its UI uses WebView2. Avoid universal “Go needs no native tools” or “Rust ships everything alone” claims.

The remaining GUI questions—rendering, text handling, accessibility, packaging, startup and idle use—must be evaluated against the desired interface in either language.

## 7. Maintenance, extensions and the cost of changing your mind

### Owning the project six months from now

**Go's likely advantage [INFERENCE]:** fewer language concepts are needed to follow ordinary coordination code, and the measured rebuild cycle was shorter. This helps when hierarchy, birth policies, recovery and UI behavior are still being refined. It is a maintainability argument, not a promise that a non-programmer can audit the security of Go code.

**Rust's likely advantage [INFERENCE]:** deliberate ownership and well-modeled states can make later changes safer because the compiler identifies affected uses. An experienced Rust maintainer can benefit substantially. The tradeoff is that asynchronous ownership, shared state and compiler constraints demand more expertise during implementation and review.

If AI writes much of the code, that tradeoff does not disappear. Rust's compiler can reject some AI-generated mistakes; Go can make the resulting code easier to inspect. Neither compilation nor a persuasive explanation proves the intended permissions, recovery or spending behavior. There is no project-specific evidence that either language costs fewer AI-development tokens.

Both have language-stability policies: [Go's compatibility promise](https://go.dev/doc/go1compat) and [Rust's opt-in editions](https://doc.rust-lang.org/edition-guide/editions/index.html). Those do not freeze third-party GUI libraries, native SDKs or provider APIs. Dependency selection matters in both.

### Extensions do not lock everyone into the core language

The selected child-process plugin boundary allows extensions in other languages. Recipes, skill files and declarative routing rules need not become Go or Rust programs. A Go core does not prevent a future Rust helper; a Rust core does not prevent a Python classifier.

Separate processes isolate memory address spaces, **not permissions to files or networks**. Input validation, restricted access, cancellation and cleanup remain necessary. Optional local models should be charged their own CPU/RAM/weights cost; neither core language makes inference inexpensive.

### Changing later is possible, but not free

Language-neutral message formats, persistent schemas and the launcher protocol preserve options. They do not make a core rewrite cheap: scheduling, adapters, permissions, recovery and packaging would still need reimplementation and verification. No audited implementation inventory exists here, so this document gives no rewrite estimate.

**Do not build a mixed Rust/Go core merely to postpone the choice.** That adds two toolchains and another boundary without an identified requirement. A measured future bottleneck can justify a focused helper; it does not justify designing one now. [INFERENCE]

## 8. Final decision: what you gain and what you accept

| Priority | Better fit for this project | Reason |
|---|---|---|
| Deliver and evolve the complete colony design with straightforward coordination code | **Go** | Simpler implementation model; the hard social, authority and recovery policies remain application work either way. [INFERENCE] |
| Minimize the core's memory and executable footprint | **Rust** | Clear advantage in the existing small measurements; final application advantage remains unmeasured. |
| Reject more concurrency and state-model errors before running | **Rust** | Stronger ownership/thread-sharing checks and exhaustive alternatives. |
| Rich native GUI and Dwarf Fortress presentation | **No language winner** | Toolkit and interaction design decide; neither current implementation has been measured. |
| Smarter agents, meaningful culture and lower inference cost | **No language winner** | Models, context policy, routing and evaluation decide. |
| Reliable private memories and durable messages | **Both require deliberate engineering** | Rust adds useful guardrails but neither language supplies this application's policy or crash semantics. |

### Recommended choice: Go

**Choose Go because the current vision is primarily a coordination system whose policies are still developing—not because Rust is unnecessary complexity in every project.** Accept its measured memory overhead and the greater burden of explicitly verifying shared-state behavior. Do not accept weakened context isolation, durability or runaway resource use as the price of simpler code. [INFERENCE]

### When I would choose Rust instead

I would reverse the recommendation if:

- you make a strict, measured core footprint or latency budget a defining product requirement and representative Go measurements miss it; **or**
- you deliberately prioritize compiler-enforced ownership and concurrency checks over implementation simplicity, with reliable Rust maintenance expertise available.

You do not need a benchmark failure to prefer Rust on principle. But that would be an explicit choice to pay for stronger engineering constraints—not a requirement imposed by the colony concept.

### Evidence the eventual implementation must provide, whichever you select

Use the same acceptance scenarios, not language-specific demonstrations:

1. **Private memory:** switch one worker between Orchestrator, Diplomat and board; no history, approval or late result leaks across contexts.
2. **Two live views:** concurrent terminal/GUI commands cause one execution and one resolution; reopening the same colony reuses its GUI.
3. **Durability:** accepted messages survive restart; uncertain external effects are surfaced rather than silently repeated.
4. **Resource behavior:** measure terminal plus host, then host plus GUI, over sustained representative colony work, including dormant contexts and child processes.

These are future acceptance requirements, **not checks claimed to have passed**. No native toolkit or colony-scale performance winner is established by the existing evidence.

**Bottom line: Go is my recommendation for realizing and maintaining this particular vision. Rust buys a lower measured core footprint and stronger pre-run safeguards—not a more capable colony. Either can be the permanent foundation; choose which engineering cost you want to carry.** [INFERENCE]

## Basis and evidence limits

Read for this comparison:

- [Core and launchers design](DESIGN-CORE-AND-LAUNCHERS.md), Draft 0.4: all current product requirements, mechanisms, open choices and retained measurements.
- [Routing layer design](DESIGN-ROUTING-LAYER.md), Draft 0.5: context-bound routing, provider independence, handover, permissions, costs and extensions.
- [Implementation-language research](RESEARCH-IMPLEMENTATION-LANGUAGE.md): historical experiments and changed toolchain assumptions.
- [Open-source harness research](RESEARCH-OPEN-SOURCE-HARNESSES.md): minimum-core philosophy and the distinction between model and device costs; not a fresh ecosystem ranking.

External language/runtime/framework documentation linked above was consulted for this comparison. Existing provider-specific prices, version inventories and historical benchmarks were not re-measured or comprehensively refreshed. No superseded design, legacy implementation or third-party project codebase was audited. **[INFERENCE]** labels recommendations and project-specific deductions rather than measured outcomes.
