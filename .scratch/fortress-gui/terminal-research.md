# Native terminal reuse research

Evidence checked **2026-10-06**. Source/document research only: no installation, build, native smoke, accessibility check or Core attachment was performed. The owner's preference is to embed an existing terminal, not implement another emulator. All six contracts remain **Not started** ([register](../../docs/CONTRACTS.md)); this report selects neither a dependency nor a new toolkit.

## Recommendation and decision readiness

**Investigate `fyne-io/terminal` as the first native reuse candidate, but do not adopt its inspected revision unchanged.** It supplies an actual embeddable Fyne widget, including terminal parsing, rendering and input—not merely a standalone application. Windows has real ConPTY code; Linux/macOS have PTY code. There is a credible route to run the installed `dwarf-harness` terminal view through its generic connection API without writing a terminal emulator.

**A factual prerequisite remains before an embedded-terminal product commitment:** prove native behavior with Fyne v2.8.1, resolve the concrete source hazards below, then exercise the real C7-bound launcher. Source inspection is enough to choose the next research experiment, not to promise an embedded CLI. Keep B11's native boundary and the existing external-terminal/manual-attach route while feasibility remains unproven. No browser or custom-emulator fallback is authorized.

## Inspected versions, licenses and repository status

Canonical repositories are linked below; API links allow independent metadata checks. `pushed_at` is repository activity, not evidence of correctness or maintenance guarantees. All five API responses reported `archived: false`; none redirected to a different canonical name.

| Canonical repository | Inspected pin / purpose | License evidence | API `pushed_at` date |
|---|---|---|---|
| [fyne-io/terminal](https://github.com/fyne-io/terminal) | [`c8f30fa130e35b342233cf2ee3171d3a161039bd`](https://github.com/fyne-io/terminal/commit/c8f30fa130e35b342233cf2ee3171d3a161039bd), master at inspection; widget and app | [LICENSE][terminal-license] contains BSD-3-Clause terms; API says `NOASSERTION`, not “unlicensed” | [2026-09-27](https://api.github.com/repos/fyne-io/terminal) |
| [ActiveState/termtest](https://github.com/ActiveState/termtest) | `conpty/v0.5.0`, [`df18a134061144fbf9a2ae79a3a650f44e3e0ee1`](https://github.com/ActiveState/termtest/tree/df18a134061144fbf9a2ae79a3a650f44e3e0ee1/conpty); terminal's Windows dependency | [BSD-3-Clause](https://raw.githubusercontent.com/ActiveState/termtest/df18a134061144fbf9a2ae79a3a650f44e3e0ee1/LICENSE) | [2025-08-20](https://api.github.com/repos/ActiveState/termtest) |
| [creack/pty](https://github.com/creack/pty) | `v1.1.21`; terminal's Unix dependency | [MIT](https://raw.githubusercontent.com/creack/pty/v1.1.21/LICENSE) | [2026-06-01](https://api.github.com/repos/creack/pty) |
| [fyne-io/fyne](https://github.com/fyne-io/fyne) | `v2.8.1`; already-selected GUI toolkit | [BSD-3-Clause](https://raw.githubusercontent.com/fyne-io/fyne/v2.8.1/LICENSE) | [2026-10-06](https://api.github.com/repos/fyne-io/fyne) |
| [xtermjs/xterm.js](https://github.com/xtermjs/xterm.js) | `c58ea3637f3968e0e6e79cd92cf9aace7ef89ee2`; boundary comparison only | MIT reported by API | [2026-09-13](https://api.github.com/repos/xtermjs/xterm.js) |

The terminal [tags API](https://api.github.com/repos/fyne-io/terminal/tags?per_page=10) returned an empty list. Pin a reviewed commit, not `@latest`. Its inspected [README][terminal-readme] has no deprecation notice, but its feature TODO is not a reliable completeness checklist: it still lists tabs while the [standalone application][terminal-app] already implements them. License notices must accompany redistribution; this is not a completed transitive/license audit.

## What exists, versus required integration

### Embedding API, not application embedding

The library documents `terminal.New()` as Fyne window content; [`Terminal`][terminal-api] embeds `widget.BaseWidget`. `RunLocalShell()` and `RunWithConnection(in io.WriteCloser, out io.Reader)` are blocking operations used from a goroutine. The latter accepts generic byte streams despite the README's SSH example—it does not require SSH. `AddListener` / `RemoveListener` expose `Config.Rows` and `Config.Columns`. The separate `cmd/fyneterm` owns its own app/window/tabs and calls `RunLocalShell`; embed the **library widget**, not that application or its window.

`RunLocalShell` cannot select an arbitrary executable/argv through its public API: [Windows][terminal-windows] hardcodes `C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`; [Unix][terminal-unix] selects `$SHELL`, falling back to `bash`. `SetStartDir` only selects the initial directory. Do not send a constructed shell command into either shell to launch DwarfHarness.

### Platform evidence

| Platform | Existing implementation | Glue needed for the real launcher |
|---|---|---|
| Windows | `term_windows.go` uses `ActiveState/termtest/conpty` v0.5.0: `New`, `Spawn`, input/output pipes and `Resize`. Its [syscall layer][conpty-syscalls] calls Windows `CreatePseudoConsole`/`ResizePseudoConsole`/`ClosePseudoConsole`; this is not a README-only support claim. | Own a ConPTY for the **terminal-view subprocess**, call [Spawn][conpty-api] with the resolved installed executable and an argument vector, and bridge its pipes through `RunWithConnection`. `Spawn` builds a Windows command line with [`windows.EscapeArg`][conpty-args], not shell evaluation. Supply the executable as argv[0] as well as the application path; qualify spaces, Unicode, quoting, environment and working directory. Own the returned process handle, wait/exit status and cleanup. |
| Linux / macOS | `term_unix.go` uses `creack/pty` v1.1.21. [`StartWithSize`][pty-start] accepts an `*exec.Cmd`, connects stdin/stdout/stderr and creates an OS session/controlling terminal. [Linux](https://raw.githubusercontent.com/creack/pty/v1.1.21/pty_linux.go) opens `/dev/ptmx` plus `/dev/pts/...`; [macOS](https://raw.githubusercontent.com/creack/pty/v1.1.21/pty_darwin.go) uses `/dev/ptmx` with Darwin PTY ioctls. | Start `exec.Command(resolvedExecutable, frozenArgs...)` directly with the existing PTY library, bridge the PTY file through `RunWithConnection`, propagate dimensions with `pty.Setsize`, and own process wait/close. A new **OS process session** is not a new Core execution session. |

Microsoft specifies [Windows 10 version 1809 / Server 2019 minimum](https://learn.microsoft.com/en-us/windows/console/createpseudoconsole). That establishes an OS API floor, not successful runtime on our Windows/Fyne/toolchain combination. The terminal's pinned [platform-test workflow][terminal-ci] runs Ubuntu and macOS, **not Windows**, and uses `-tags ci`; it is not native desktop or accessibility evidence.

### How this would attach to Core

The proposed path is **Fyne widget ↔ PTY/ConPTY ↔ newly launched real terminal-view process ↔ authenticated C7 connection ↔ existing Core session**. It does not import Core into the widget, pass C7 protocol frames to the terminal parser, or take over an existing OS terminal window.

C7 must supply the authorized colony/agent/execution-session/context binding and actual frozen launcher syntax. Resolve the installed binary, use IDs/argv rather than names/shell interpolation, and keep provider keys out of the GUI and launch arguments. The terminal view performs the normal authenticated attachment; attaching creates neither a model loop nor provider continuation. These are [existing requirements](spec.md#3-clicks-navigation-and-agent-cli), **not functionality provided by fyne-io/terminal**. No usable installed launcher or frozen attach command is established by this research.

The Core host must remain detached from this pseudoconsole. Closing a pane may terminate its terminal-view subprocess, but must not stop Core or its turns ([host/view lifecycle](../../docs/DESIGN-CORE-AND-LAUNCHERS.md#31-starting-and-attaching)). Never start the host inside the embedded PTY or treat Ctrl-C/EOF/console closure as colony shutdown.

## Blocking hazards and unverified behavior

1. **Not a side-effect-free widget at this pin.** [`osc.go`][terminal-osc] handles OSC 7 by calling process-wide `os.Chdir`; output can change the GUI's working directory. That must be removed/disabled through a reviewed library-level fix before adoption. Untrusted model/tool text must remain inert under the launcher's output contract; terminal control parsing is an additional boundary, not automatically safe because the renderer is native. Do not build an ad-hoc ANSI sanitizer as a second terminal implementation.
2. **Windows failure/lifetime behavior needs correction and proof.** The local-shell wrapper calls `log.Fatalf` on a process-wait error. More importantly, even the reused [ConPTY dependency][conpty-api] calls `log.Fatalf` on pipe-creation failures, which can exit the entire GUI. Resolve that dependency behavior through a reviewed upstream/fork fix or separately researched existing backend. Its `Close` calls `ClosePseudoConsole` before closing output. Microsoft's [shutdown documentation][conpty-close] requires closing or continuing to drain output; before Windows 11 24H2, incorrect sequencing can block indefinitely. This matters on Windows 10. Do not label pane close as proven detach.
3. **External-connection lifecycle is caller-owned.** `RunWithConnection` does not populate the library's private `cmd`/`pty`: automatic PTY resize, process wait and useful `ExitCode()` do not come with that route. `Close()` closes its input writer, not an independently supplied output reader; the adapter must manage all resources and unblock readers safely. A closed `Terminal` cannot be run again. Reattach needs a fresh widget and no retained unauthorized terminal buffer. Config sends are nonblocking and can be dropped, so prove delivery of the final size under resize bursts rather than assuming a listener guarantees it.
4. **Input, history and accessibility are not qualified.** [Keyboard handling][terminal-input] encodes runes/control/function keys; [selection/paste][terminal-select] implements clipboard and bracketed paste. IME, AltGr, international layouts, combining/wide characters, multiline paste, interrupt behavior and host-shortcut conflicts remain untested. `AcceptsTab()` consumes Tab: keyboard focus must have a reliable way out. The [grid][terminal-grid] disables scrolling and [scrolling code][terminal-output] replaces rows; do not promise a normal scrollback history. Screen-reader name/role/value, live text announcements, focus, contrast, scaling and reduced motion require actual OS evidence—not inference from `BaseWidget` or native rendering. If missing behavior would require us to build a terminal, stop and revisit the reuse choice.
5. **Resource/threading compatibility remains unmeasured.** The renderer uses `fyne.Do` / `DoAndWait`, but [Fyne's threading requirements](https://docs.fyne.io/started/goroutines/) and the widget's native behavior still need qualification. Output/parser buffers, sustained output, hidden/closed-pane activity, resizing and repeated attach/detach need bounded resource evidence. No binary-size, latency, memory or handle-leak result is claimed.

## Go/Fyne and dependency boundary

The terminal [go.mod][terminal-mod] declares Go 1.19 and Fyne `v2.7.5-0.20260529084154-f5f48d2ab76e`, plus ConPTY v0.5.0, creack/pty v1.1.21, fancyfs v0.0.1 and testify v1.11.1. ConPTY [declares Go 1.12](https://raw.githubusercontent.com/ActiveState/termtest/df18a134061144fbf9a2ae79a3a650f44e3e0ee1/conpty/go.mod), requiring Azure/go-ansiterm and x/sys; pty [declares Go 1.13](https://raw.githubusercontent.com/creack/pty/v1.1.21/go.mod). Fyne v2.8.1 [declares Go 1.22.0](https://raw.githubusercontent.com/fyne-io/fyne/v2.8.1/go.mod). No declared floor here exceeds D28's Go 1.27.1, but that is not a compilation result.

An explicit Fyne v2.8.1 requirement would select the newer same-module version under [Go minimal version selection](https://go.dev/ref/mod#minimal-version-selection), absent another higher requirement/override. **Compatibility is plausible, unverified**, especially with the selected modern x/sys and native graphics graph. Do not downgrade Fyne to match the widget's metadata. Keep widget and adapters solely inside `gui/`; Core/terminal stay `CGO_ENABLED=0` and GUI imports of DwarfHarness remain launcher-contract-only. D29 still requires the actual selected module/package graph, notices, native linked libraries and measured binary-size delta. Removal means deleting the widget/adapter dependency while retaining the real launcher/C7 attachment, not retaining a homegrown emulator.

## Alternatives and required native experiment

[xterm.js at the inspected commit][xterm-readme] is an actual embeddable **browser** terminal whose visual API opens a DOM element and uses CSS. Its headless variant would not supply a Fyne renderer. Using the browser renderer conflicts with B11 absent an explicit amendment; it is not a native fallback. A full terminal application's popularity or screenshots likewise do not establish a Fyne embedding API. No other toolkit or terminal application is selected here.

Before adoption, authorize a disposable native experiment—not product implementation:

1. Pin the inspected widget/necessary reviewed fixes, Fyne v2.8.1, Go 1.27.1 and actual platform toolchains. First run a real installed console executable through direct PTY/ConPTY argv (including paths with spaces/Unicode), not through an injected shell line. Observe native rendering, input, resize and exit/error cleanup. This preliminary step proves only transport/widget behavior, **not FG05**.
2. Once the real C7 implementation and frozen attach interface exist, run the actual installed `dwarf-harness` launcher against an existing Core session/context. Compare authoritative IDs and turn/provider-call receipts across GUI, embedded terminal and a simultaneous external terminal. Opening/reopening must not create a session or paid turn; no invented attach flags or fake console can satisfy this step.
3. Exercise burst/final resize, minimize/restore, EOF, Ctrl-C, pane close, GUI close/crash, launcher failure and reattach during a real turn. Observe that Core stays alive, terminal-view processes/handles are reclaimed, close does not hang, failures are reported without exiting the GUI, and revoked/disconnected private output is no longer accessible. Include the actual Windows 10 target, then native macOS/Linux paths; Windows 11 success alone does not prove older ConPTY shutdown behavior.
4. Run keyboard-only focus in/out, copy/paste and representative Unicode/IME inputs; inspect actual Windows screen-reader/accessibility behavior, then each supported OS. Record output/history limitations, accessibility findings, scaling/reduced-motion behavior and bounded sustained-output resource costs. Retain exact OS/build/dependency versions and observed traces/screenshots; distinguish pass, fail and unrun.

**Adoption gate:** credible reuse route exists, but unresolved source hazards, native usability/accessibility and real Core attachment remain factual prerequisites. No contract freeze, dependency approval, runtime gate or product implementation is claimed.

[terminal-license]: https://raw.githubusercontent.com/fyne-io/terminal/c8f30fa130e35b342233cf2ee3171d3a161039bd/LICENSE
[terminal-readme]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/README.md#library
[terminal-api]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/term.go
[terminal-app]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/cmd/fyneterm/main.go
[terminal-windows]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/term_windows.go
[terminal-unix]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/term_unix.go
[terminal-mod]: https://raw.githubusercontent.com/fyne-io/terminal/c8f30fa130e35b342233cf2ee3171d3a161039bd/go.mod
[terminal-ci]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/.github/workflows/platform_tests.yml
[terminal-osc]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/osc.go
[terminal-input]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/input.go
[terminal-select]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/select.go
[terminal-grid]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/internal/widget/termgrid.go
[terminal-output]: https://github.com/fyne-io/terminal/blob/c8f30fa130e35b342233cf2ee3171d3a161039bd/output.go
[conpty-api]: https://github.com/ActiveState/termtest/blob/df18a134061144fbf9a2ae79a3a650f44e3e0ee1/conpty/conpty_windows.go
[conpty-args]: https://github.com/ActiveState/termtest/blob/df18a134061144fbf9a2ae79a3a650f44e3e0ee1/conpty/exec_windows.go
[conpty-syscalls]: https://github.com/ActiveState/termtest/blob/df18a134061144fbf9a2ae79a3a650f44e3e0ee1/conpty/syscall_windows.go
[conpty-close]: https://learn.microsoft.com/en-us/windows/console/closepseudoconsole
[pty-start]: https://github.com/creack/pty/blob/v1.1.21/start.go
[xterm-readme]: https://github.com/xtermjs/xterm.js/blob/c58ea3637f3968e0e6e79cd92cf9aace7ef89ee2/README.md
