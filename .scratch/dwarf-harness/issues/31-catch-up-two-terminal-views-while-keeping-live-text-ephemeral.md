# 31: Catch up two terminal views while keeping live text ephemeral

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C7 independent durable/live client semantics; completes C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))
Blocked by: 30

## What to build

Recover independent durable cursors and separate ephemeral snapshot/delta streams for multiple terminal views.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen launcher-protocol and producer-ready golden samples before C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md)) handoff.
- No broker, GUI or whole-C4 dependency; pre-C4 promotion requests remain unavailable, never simulated accepted work.

## Acceptance criteria

- [ ] Two real terminal views retain independent colony-sequence cursors. A gap yields a currently authorized snapshot with through_seq and only subsequent events; moving one cursor neither moves the other nor acknowledges message/task completion.
- [ ] Live attachment atomically receives per-turn text plus delta high-water, then only higher indexed deltas. A delta gap requests a new snapshot; text is not persisted or reconstructed from durable event cursors.
- [ ] Actual view reconnect catches up without duplicate execution; actual host restart preserves durable results while clearly losing the former ephemeral stream rather than presenting it as recovered history.
- [ ] Competing approval/start/cancel commands execute once or return the real stale/busy result in both views. Turn cancellation targets the shared turn and never implicitly withdraws its promotions; promotion cancellation remains request-specific.
- [ ] Requests/results retain original context and immutable submission identity across view changes and retries. Show pending, too-late and actual terminal outcomes without treating a ref move or speculative pass as acceptance; unavailable validation remains unavailable.
- [ ] Concurrent terminal output identifies originating agent/context/turn, selected owner input remains explicit, and completion/status delivery creates no model wakeup. Terminal Ctrl+C distinguishes active-turn cancellation from empty-prompt detach and preserves piped/paste/non-color behavior.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 50, 51, 52.
- Implementation decisions: 1, 2, 3, 4, 6, 8, 10, 17.
- Testing decisions: 1, 2, 7, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C7
- Core design §§4–5,13.7
- Decision log D49/D53/D57–D59/D63

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Tickets [92](92-publish-revisioned-topology-and-authorized-edge-snapshots.md), [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md), [96](96-save-current-fortress-presentation-atomically-with-bounded-retries.md) and [124](124-attach-open-cli-to-the-selected-contexts-existing-session.md) stage C7 topology projections, administrator inspection, current fortress presentation persistence and existing-session terminal attach on this launcher capability; base C7 is complete without them. These extensions add no blocker or criterion to this ticket.
