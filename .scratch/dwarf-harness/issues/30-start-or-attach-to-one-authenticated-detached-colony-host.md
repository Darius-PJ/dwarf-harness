# 30: Start or attach to one authenticated detached colony host

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C7 host/IPC/typed-command path
Blocked by: 16

## What to build

Authenticate local Windows/Unix clients to one detached Core; closing a view never shuts down work.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- C1 required reader/appender/locking contracts and relevant command families frozen.
- Real Windows/Linux/macOS IPC hosts available; D41 dependency approval where required for go-winio.
- C7 owns the protocol draft. It stays provisional, with no owner freeze, while terminal views use it (this ticket and ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md)); ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md) requests its freeze before the desktop app consumes it (D182).

## Acceptance criteria

- [ ] Concurrent terminal starts produce one lock-owning Core and one set of executions; later terminals attach. Stale run/host.json is treated as discovery only, never identity or permission.
- [ ] Detached --host --no-terminal exposes authenticated booting/recovering/ready/quiescing/stopped state. Closing or crashing the last view leaves the Core and turn alive; explicit shutdown remains a separate authorized control.
- [ ] Windows uses intended-user restricted local named pipes with remote-client rejection; Linux/macOS use private-directory Unix sockets and restrictive endpoint permissions. Unauthorized local principals and incompatible versions cannot attach.
- [ ] Strict four-byte big-endian framed JSON enforces the provisional 1-MiB cap and rejects malformed lengths, duplicate keys, unknown required variants and out-of-order commands before effects.
- [ ] Typed commands bind authenticated colony/session/agent/context, UUIDv7 ID and expected revision. Same immutable retry has one durable effect; changed-ID bindings/stale conflicts fail. Views receive no provider keys and cannot independently execute tools or mutate permissions.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 50, 52.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 17.
- Testing decisions: 1, 2, 4, 7, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C7
- Core design §§3.1–3.4,13.7

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-20 — Ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md) is resolved, so all listed blockers are clear. The criteria are unchanged. No runtime, production-delivery or other start-gate approval is inferred.

2026-11-27 — Release tier rework (D182, D186): Release 1. Slimmed: the launcher protocol stays a provisional draft, with no owner freeze, while terminal views use it; ticket [54](54-attach-one-native-gui-to-a-live-core-and-recover-its-own-view-safely.md) requests its freeze when the desktop app needs it. The transparency criterion is added. This grants no implementation, contract-freeze or release approval.
