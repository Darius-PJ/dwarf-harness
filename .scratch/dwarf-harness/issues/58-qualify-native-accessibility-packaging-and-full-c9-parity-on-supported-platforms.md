# 58: Qualify native accessibility, packaging and full C9 parity on supported platforms

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: GUI-D/GUI-E actual release evidence; completes native GUI parity gate (ticket [58](58-qualify-native-accessibility-packaging-and-full-c9-parity-on-supported-platforms.md))
Blocked by: 55, 56, 57

## What to build

Prove keyboard/screen-reader access, bounded live rendering, packaging and whole-C9 parity on actual platforms.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Actual supported Windows/Linux/macOS machines, screen readers and accessibility inspectors; native SDK/compiler/runtime prerequisites and signing/notarization identities.
- Declare package/runtime/performance acceptance bounds before measuring. Failure stays blocked; toolkit APIs, browser tests or cross-builds do not establish support.
- Any shipped Mac executor is separately signed/versioned and needs Mac guest executor (ticket [49](49-execute-bounded-disposable-guests-through-one-signed-core-owned-mac-helper.md)); the GUI/Core-only package does not acquire an A2 or mandatory VM claim.

## Acceptance criteria

- [ ] On every declared supported OS, complete actual keyboard-only task, conversation and workflow paths with visible focus, stable tab order, usable shortcuts, non-destructive defaults and explicit confirmations for irreversible/scope-changing actions.
- [ ] Use actual screen readers and native accessibility inspectors to verify names, roles, values, relationships and state announcements without focus theft, including virtualized lists, reconnect and denied-content announcements. Record OS, Fyne/runtime and assistive-technology versions.
- [ ] Real native checks establish non-color status, readable contrast/scaling/zoom/system text size, no essential hover-only information, reduced-motion behavior and no continuous idle/autoplay animation.
- [ ] Under long histories and high update/reconnect load, row/byte/page limits, focus/selection/read position and authorization still hold. Measure cold start, idle/high-update CPU/RAM/latency and absence of duplicate provider work against the declared bounds.
- [ ] Install separately pinned Core and GUI artifacts on real targets: Windows executables; signed/notarized Mac GUI and separately signed helper when shipped; documented Linux Fyne/graphics dependencies. Headless Core/terminal remain cgo-free without GUI dependencies or runtime compiler downloads, with Fyne BSD and applicable linked-toolchain licence notices.
- [ ] Freeze the actual native/terminal parity evidence for stale graph, revoked edge, inaccessible attachment, required branch failure, capped-loop escape and duplicate completion. Only these completed native gates supply native GUI parity gate (ticket [58](58-qualify-native-accessibility-packaging-and-full-c9-parity-on-supported-platforms.md)) to whole C9; packaging or toolkit selection alone never does.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 120, 121, 124, 127, 129, 130.
- Implementation decisions: 1, 2, 3, 4, 5, 52, 54, 55, 56.
- Testing decisions: 1, 2, 3, 15, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- GUI roadmap GUI-D/GUI-E and acceptance-evidence section
- Decision log D28/D39/D44/B11

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [129](129-qualify-the-complete-fortress-on-supported-native-platforms.md) reapplies these accessibility and packaging gates with the fortress enabled; GUI-E parity remains independently deliverable. These extensions add no blocker or criterion to this ticket.
