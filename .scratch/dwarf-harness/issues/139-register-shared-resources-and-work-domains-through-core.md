# 139: Register shared resources and work domains through Core

Status: resolved
Type: task
Published: 2026-10-24
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 registered shared resources and owner-registered work domains (B32)
Blocked by: 11

## What to build

Let the owner register the colony's shared resources and work domains in Core, so that the coupling graph can show them as nodes and link them to the work that uses them. [B32](../../../docs/DECISIONS.md#b32-c3-scoped-grants-and-atomic-task-ownership-ticket-11), item 4, assigned this family to C3: registered shared resources are the resources F3-11's grants name, and work domains are the owner's, registered like his root grants and capacity limits, with Core as the registrar. Ticket [61](61-turn-bounded-artifact-and-usage-evidence-into-atomic-causal-graphs.md)'s resource and work-domain nodes and its positive resource-to-consumer usage wait for these records. Undercurrent §§8.2–8.3 set their rules: a resource identity is an opaque host registration, never a secret-bearing URL or account name, and a declaration is scoped and versioned, never a permission.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Consumes the frozen F3-10 and F3-11 families ([D139](../../../docs/DECISIONS.md#d139-f3-10-private-state-records-frozen), [D140](../../../docs/DECISIONS.md#d140-f3-11-scoped-authority-task-and-non-paid-work-records-frozen)) and changes them only by addition (D40).
- A ruled design settles the family's records, commands and slices before code (D117). Once the build advisor approves the contract slice with its freeze record, the project manager freezes the family under [D125](../../../docs/DECISIONS.md#d125-completed-contract-slices-are-frozen-without-the-owners-approval); only a breaking change to a frozen contract (D40), a D52 approval, a new dependency (D29) or another change that touches an owner decision (D41) waits for the owner.
- D112 approved the D52 boundary before C3 implementation; registrations serve test agents only, and nothing here admits colony work (D52).
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); no provider credentials are needed.

## Scope boundaries

- C3's producer and records only. Graph nodes, edges and usage are ticket 61's, and host-bound actual use comes from ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md)'s action facts.
- A registration is not a grant: F3-11's grants still decide every access.
- No terminal, C7 or GUI view; a view of registrations needs its own ticket after this producer.

## Acceptance criteria

- [x] The owner registers and revises shared resources and work domains through producer-owned typed Core transitions that bind actor, operation and target, check the expected version and current authority, and commit one replay-complete group ending in its receipt before acknowledging; an agent cannot register, revise or widen a registration.
- [x] A registered shared resource is one that F3-11's grants can name, by resource kind and key. Each registration's identity is opaque and non-secret, never a URL, account name or credential.
- [x] A registration, and any declaration linking one to a task or context, is scoped and versioned: a revision supersedes the earlier version without rewriting history, and replay at any cursor sees the version then current. A registration or declaration is evidence for the graph, never a permission.
- [x] The family is specified with its real payloads, permitted caller, consumers, privacy and replay-essential fields and a golden sample, and is frozen by addition to the ledger format before its producer writes it; F3-10's and F3-11's records, rules and goldens are unchanged.
- [x] At restart the service rebuilds every registration from the ledger alone and appends nothing; a crash before or after the commit leaves only committed registrations, and a same-command retry returns the original receipt.
- [x] Ticket 61 can build its resource and work-domain nodes from the frozen records without stand-in data: the golden holds every registration and declaration shape a node or declared edge needs.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 27, 86.
- Implementation decisions: 1, 2, 3, 4, 11, 38.
- Testing decisions: 1, 2, 4, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3
- Undercurrent design §§8.2–8.3
- Undercurrent roadmap U3 node definitions, Q1 and non-code nodes
- Decision log B32/D40/D125/D140

## Comments

2026-10-24 — Published at the owner's word, which made the family ticket its own ticket, 139, and with his approval of the breakdown ([D142](../../../docs/DECISIONS.md#d142-tickets-139-and-140-published-with-three-blocker-changes)). This adopts the ticket for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-28 — Claimed for the ruled three-slice Core registration build under D117/D125/D152.

2026-10-29 — Resolution: the three-slice build supplies owner-only CAS registration/declaration transitions, exact opaque F3-11 resource addresses, scoped versioned evidence, historical/pinned queries and original command receipts (B34). The complete additive F3-139 family was frozen before its producer (D154); see the [ledger catalogue](../../../docs/CONTRACTS.md) and [Core package documentation](../../../internal/core/agents/doc.go). Real process-interruption and injected torn-tail recovery prove ledger-only reconstruction without Core replay appends and retries without duplicate effects; they do not establish power-loss durability. Registrations remain evidence, not permission. Ticket61 receives real record shapes, not implemented graph nodes/edges; later action facts, provider execution and views are outside this ticket. D52's testing-only boundary, C4 admission and separate whole-ticket Fable verification remain unchanged.
