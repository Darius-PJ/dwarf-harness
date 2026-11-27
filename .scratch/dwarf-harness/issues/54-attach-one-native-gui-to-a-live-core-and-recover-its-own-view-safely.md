# 54: Attach one native GUI to a live Core and recover its own view safely

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: GUI-A/GUI-B plus security/accessibility foundations at first surface
Blocked by: 31

## What to build

Attach/reopen a real native client with independent recovery, durable receipts and no duplicate Core.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C7's launcher-protocol, provisional until now, is owner-frozen with producer-ready golden samples before this client consumes it (D182); Fyne v2.8.1 in isolated gui/ module and pinned native toolchain.
- Actual native application/Core runtime available; no browser or mock-view acceptance.
- GUI-A/B may start after C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md)) alone; no C9 backend capability (tickets [35](35-execute-a-pinned-branching-workflow-with-nonblocking-joins-and-bounded-review-escape.md), [36](36-load-and-update-shared-format-skills-lazily-through-core.md)) or whole-C9 prerequisite.

## Acceptance criteria

- [ ] A real Fyne native window starts/attaches through authenticated detached-host readiness, negotiates protocol and reuses one GUI instance per colony. Close/crash/reopen or a second invocation cannot stop Core or duplicate a session/turn.
- [ ] The view uses its own authorized durable snapshot/through_seq recovery and separate ephemeral live-text snapshot/high-water stream. Actual host restart distinguishes missing live text from durable history; disconnect clears transient/private buffers when authority cannot be re-established.
- [ ] Commands carry immutable UUIDv7 keys and expected revisions. Same-command retry shows the existing durable receipt; changed commands use new identities, and busy/unavailable/denied/stale/interrupted/disconnected are real Core states, not optimistic success or automatic replay.
- [ ] The client only uses frozen contracts and authenticated typed commands; no direct colony/Git/context/artifact/keychain access, provider calls or credentials. Displaying the same events causes no model call.
- [ ] All model/tool/skill/story text is inert native text from the first surface: no HTML, generated controls, remote resources or shell links. A link requires a separately typed authorized safe-URI action.
- [ ] Initial transcripts/live buffers are bounded and keyboard usable with meaningful control names, visible focus, non-color status and stable selection. Security, bounded rendering and usable native controls are not deferred to polish.
- [ ] Before the client consumes it, request the owner's D40 freeze of C7's `launcher-protocol` as tickets [30](30-start-or-attach-to-one-authenticated-detached-colony-host.md) and [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md) built it, with producer-ready golden samples; the client builds only against the frozen version (moved from ticket 31's gates, D182).
- [ ] With a terminal view and the app attached, competing approval/start/cancel commands execute once or return the real stale/busy result in every view. Turn cancellation targets the shared turn and never implicitly withdraws its promotions; promotion cancellation remains request-specific (moved from ticket 31, D182).
- [ ] Every failure this ticket introduces reaches the terminal or the app with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 119, 120, 122, 123, 128.
- Implementation decisions: 1, 2, 3, 4, 17, 52, 53, 54, 55.
- Testing decisions: 1, 2, 7, 15, 20.

Canonical sections (resolved through the parent specification’s reading map):
- GUI roadmap GUI-A/GUI-B, interaction/reconnect contract
- Core design §§6,13.7

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: GUI-F fortress work starts from this client with ticket [107](107-compose-layered-dwarves-and-tiles-from-bounded-passive-asset-packs.md) (asset pipeline) and ticket [123](123-qualify-a-reusable-native-terminal-within-the-effort-allowance-or-choose-the-external-fallback.md) (native-terminal qualification). These extensions add no blocker or criterion to this ticket.

2026-11-27 — Release tier rework (D182, D186): Release 2. Before the app consumes the launcher protocol, this ticket requests its owner freeze, with producer-ready golden samples. It also takes ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md)'s former criterion 4 on competing commands from several open views, now a terminal and the app. The transparency criterion is added. This grants no implementation, contract-freeze or release approval.
