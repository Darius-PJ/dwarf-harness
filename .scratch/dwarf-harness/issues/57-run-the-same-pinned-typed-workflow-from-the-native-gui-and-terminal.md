# 57: Run the same pinned typed workflow from the native GUI and terminal

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: GUI-C workflow surface and GUI-E functional parity input
Blocked by: 35, 36, 54

## What to build

Inspect and run the same pinned Core workflow as the terminal, without a second authority model.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen C9 launcher projections and real backend; no whole-C9 dependency.
- Native keyboard/privacy behavior belongs in this slice; supported-platform qualification follows.

## Acceptance criteria

- [ ] Terminal and GUI inspect/select/edit/run the same exact pinned graph/recipe revisions, branches, gate receipts, run state and loop counters through Core; the GUI introduces no graph model, execution engine, wallet or authority store.
- [ ] Changed/stale graph revisions and revoked edges produce the same Core refusal in both clients. Immutable duplicate commands retrieve receipts rather than rerun effects or silently adopt a new graph.
- [ ] Real required-branch failure, inaccessible attachment and duplicate completion cases yield the same authoritative branch/join result in both views; pending UI cannot imply task or check success.
- [ ] A real capped review loop reaches its escape and a waiting join releases the worker/context, with identical terminal/GUI counters, outcomes and bounded-budget state.
- [ ] Graph/list rendering remains bounded and keyboard/assistive-control friendly under updates, with stable selection and explicit non-color statuses. Search/export/notifications and link-like output cannot bypass current authorization or inert rendering.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 118, 121, 122, 124, 127, 128.
- Implementation decisions: 1, 2, 3, 4, 20, 52, 53, 54, 55, 56.
- Testing decisions: 1, 2, 7, 8, 15, 20.

Canonical sections (resolved through the parent specification’s reading map):
- GUI roadmap GUI-C/GUI-E
- Core design §13.8

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — Consolidation cross-link: Ticket [113](113-open-rpg-agent-and-corps-party-sheets-with-real-scoped-statistics.md) links fortress party sheets to the workflow panel delivered here. These extensions add no blocker or criterion to this ticket.
