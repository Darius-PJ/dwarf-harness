# 96: Save current fortress presentation atomically with bounded retries

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C7 staged presentation persistence with C1 catalogue support (D90, D93, D96)
Blocked by: 92

## What to build

Specify and build Core's bounded, replaceable store for current fortress presentation so that layout, appearance, settings, plans, reviewed baseline and Build TODO progress save atomically, survive restart and refuse stale or expired retries.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Specify the replaceable bounded current-state snapshot/checkpoint mechanism, finite retry-receipt retention and expiry, crash recovery and size limits with a D78 workload argument, then obtain the owner's C7/C1 family freeze before any GUI consumer depends on it.
- Use Core's existing single-writer storage discipline: no GUI database, direct colony-file write, independent appender or permanent per-edit ledger history.

## Acceptance criteria

- [ ] Typed C7 commands save and load owner-scoped current presentation: colony/presentation IDs and revision, tileset and exact appearance selections, applied map, room, object and rail geometry, unfinished blueprints, manual-mode and construction-standard settings, change groups with stable TODO item identities and dispositions, the reviewed topology baseline and item completion bindings.
- [ ] A save binds current authorization, expected presentation and topology revisions and an immutable command ID; Core commits the affected layout objects and their TODO progress atomically before acknowledging.
- [ ] Within the declared retry window a same-ID retry returns the one saved result; after expiry a stale command is refused and the caller reconciles current state; changed content under a reused ID is a mismatch.
- [ ] A crash before commit leaves the previous durable state, and a crash after commit but before the reply recovers exactly one result; storage failure, stale revision, unauthorized binding and oversize edits (initially above 512 KiB) fail without marking anything saved.
- [ ] An unsupported presentation schema fails read-only with an explanation instead of resetting state; operational birth, alignment, Purpose, approval, publication and succession history is untouched.
- [ ] Presentation saves and reads change no membership, memory, permission, execution, delivery or field input and make no model call; undo history is never persisted.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 6, 122, 148, 151, 152.
- Implementation decisions: 1, 2, 3, 4, 6, 17, 60, 62.
- Testing decisions: 1, 2, 4, 7, 18, 20, 21.
- Fortress acceptance: FG10, FG13.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C7 staged presentation rows and "D89–D96 staged producer acceptance"
- Core design §§13.1, 13.7
- Fortress GUI spec §8 persistence and crash semantics
- Contract register fortress and shared-leadership additions

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
