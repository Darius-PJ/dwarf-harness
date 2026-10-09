# 26: Exchange private relationship messages and recover a two-agent crash

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C6 early operational social/restart milestone
Blocked by: 16

## What to build

Deliver durable private messages and distinct receipts; prove two-agent crash recovery, revocation and retired-history holes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Relevant C1 reader/appender and C6 broker/relationship/receipt families owner-frozen.
- Provider credentials/authorization only for the separately labelled provider-backed portion; no whole-C4 or C5 wait.

## Acceptance criteria

- [ ] Two actual test agents use one authorized relationship and canonical exchanged-message history while retaining distinct private memory, handoff, transcript, continuation and approvals in the real Core context store.
- [ ] Sender identity and destination come from the bound context; current role/relationship, audience, membership epoch and attachment/reference access are checked before capacity or inbox insertion. Guessed/spoofed identities and forbidden routes fail without disclosure.
- [ ] Acceptance follows durable append. Same sender-scoped ID and immutable content return the original receipt even after queues fill; changed binding/content is a mismatch. Initial direct-message/body/attachment limits fail visibly rather than silently dropping accepted work.
- [ ] Accepted, delivered, processed and replied remain distinct. A processing checkpoint commits the context revision and outgoing envelopes together; asynchronous send/yield releases execution instead of blocking on a peer response.
- [ ] A crash after durable send but before processing preserves private separation, exact delivered/processed positions and the original receipt on restart, with no duplicate message or automatic repeat of interrupted model/tool effects. Preserve actual commands, receipts and observations.
- [ ] Revocation before a later read prevents newly unauthorized body/count/existence disclosure. Retirement retains only already-delivered read-only history and private memory, with holes preserved and no unseen or successor access.
- [ ] An explicit social wait checkpoints through the single continuation owner, yields and releases the worker and active-turn slot, and resumes only on the matching authorized reply or an explicit command, never through polling model calls; message, inbox and processing state change only through compulsory typed Core transitions.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 42, 44, 45, 46, 48.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 15, 16.
- Testing decisions: 1, 2, 4, 8, 14, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 early milestone
- Core design §§7.2–7.4,13.6

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-08 — D88 rework: the owner chose to include the deferred D88 rework in this consolidation and approved the plan. Criteria now cover social waits under the single continuation owner and compulsory typed transitions. Number and blockers are unchanged; this grants no separate implementation, contract-freeze or release approval.
