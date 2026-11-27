# 149: Keep a ledger reader's checked position across reads

Status: ready-for-agent
Type: task
Published: 2026-11-22
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 agent-service ledger read cost, through an amendment to C1's frozen `ledger-reader` checkpoint rule (D157, D171, D180)
Blocked by: None (can start immediately)

## What to build

Within one process incarnation, let Core's agent service, `internal/core/agents`, keep a ledger reader's verified position, its checkpoint or prefix proof, so that a later read from that position neither re-opens the ledger nor re-hashes the prefix it has already verified. Integrity holds as it does today, except that the signed-off amendment may move the moment a change to verified bytes is caught:

- a change to bytes the reader has already verified is still detected, though not necessarily by the next read of its segment as today; the ruled amendment states which read detects it and when;
- crash recovery and `Open`'s full replay are unchanged;
- any retained copy of history stays within the agent service's bounded history cache, whose limits D158 held fixed and the package documentation states, unless the design asks the owner to change them.

Today every ledger read the service makes opens a fresh reader through `ledger-reader`'s frozen checkpoint capability, reads its range and closes it: the acknowledgement after a successful submit, a refresh to the durable frontier, a committed-range readback, a `Since` page, a command-directory rebuild, a cold history read and `Open`'s replay. A kept checkpoint spares decoding the prefix, but D157's rule requires each checkpointed open to hash every byte of the containing segment's prefix afresh and to check the preceding segment's chain. A read with no usable checkpoint decodes and verifies every earlier commit in its segment, and a read from sequence 0 decodes the whole history. Ticket [142](142-read-only-the-ledger-tail-in-cores-agent-service.md)'s limits recorded that every warm read rehashes its whole containing segment prefix, and left a reader capability that removes the rehash to a separate ticket.

Ticket [147](147-cut-the-agents-packages-test-time.md)'s diagnosis and rulings found this read path to be the largest cost left in the package's race profile, and one that ticket could not cut, because keeping a reader's position changes D157's rule for a frozen contract. Its diagnostic count found the service reading ledger ranges far more often than it opened stores, many of those reads starting from sequence 0. Ticket 147 met its targets on its own tree, where the package's Windows race step took 1,115.467 s (run 44958796872). After ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s S2 landed, the same step took 1,591.157 s in run 44985772136, above D127's 25-minute report line and inside its 30-minute limit; ticket 91's remaining slices run against that line. Under D171 the cost is fixed in the product. The owner approved this ticket and, in principle, a change to D157's rule (D180).

To build:

1. A design brief and a ruling choose the mechanism before code (D117). They state the exact amendment to D157's rule, how each integrity guarantee above is kept, the rule breaks and the hosted target, and they list every owner gate.
2. The owner signs off the exact amendment before `main` moves (D41), and reassesses this ticket at the pause that follows ticket 91's close.
3. Implement the ruled design with its rule breaks, and measure the package's hosted race step.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Start gate: implementation begins only after the owner reassesses this ticket at the pause that follows ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s close. The design brief and its ruling may proceed before then.
- A design brief and a ruling come before code (D117).
- `ledger-reader` is a frozen contract. The exact amendment to D157's rule needs the owner's sign-off before `main` moves (D41); his approval at publication (D180) is in principle only and approves no mechanism.
- The limits of the agent service's bounded history cache, which D158 held fixed, are the owner's decision too: a larger cache, or a second retained copy of decoded history beside it, needs his explicit answer.
- The cost is fixed in the product: no hosted runner is reconfigured and no limit is raised (D171).
- Adds no new dependency without its D29 record.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); a red check is neither rerun nor skipped.

## Scope boundaries

- Only the ledger read path. The other large costs ticket 147's rulings recorded, directory checks each time a store opens, the flushes the crash rules require and Git children, keep their own decisions and are outside this ticket.
- The kept position lives within one process incarnation; nothing about it is persisted.

## Acceptance criteria

- [ ] A ruled design chooses the mechanism and states the exact amendment to D157's rule, and the owner has signed off that amendment before `main` moves to any change that touches the frozen contract or relies on the amendment (D41).
- [ ] Rule breaks prove each integrity guarantee, each failing at its named assertion and passing after byte-identical restoration: a change to an already verified prefix is detected by the read, and at the moment, that the signed-off amendment names; a torn final commit is still handled by the torn-tail rule; and a reopen after a crash replays as before.
- [ ] No frozen record, encoding or golden changes, and no frozen contract changes beyond the amended rule the owner signed off.
- [ ] The package's race step on the hosted windows-amd64 runner takes less time than before this ticket, by at least the target the ruled design sets, read from the job log of the final commit's hosted check. The aim is to bring it back under D127's 25-minute report line with ticket 91's slices included.
- [ ] No test is skipped, moved behind a flag or taken out of the hosted check.
- [ ] The final commit's hosted check passes on Windows, Linux and macOS, in both the tests and race steps, before `main` moves to it (D153).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 2, 4, 26.
- Implementation decisions: 1, 3, 4, 5, 6.
- Testing decisions: 1, 2, 3, 4.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C1 and C3
- Contract register: `ledger-reader`
- Decision log D127/D157/D158/D171/D180

## Comments

2026-11-22 — Published at the project manager's proposal. Ticket [147](147-cut-the-agents-packages-test-time.md)'s diagnosis and floor rulings found the cost. The owner approved this ticket, choosing the project manager's recommended option: he approved in principle a product change to D157's rule, and asked to reassess the ticket at the pause that follows ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s close (D180). Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.

2026-11-27 — Release tier (D182): On trigger. Schedule it when history reads are slow in real use; the workflow engineer may take it earlier for test time, and the change to D157's rule still needs the owner's sign-off (D180). When it is triggered, its design ruling restates criterion 2 under D181, resets criterion 4's baseline to check 140 or later, and has the entry amending D157 list the affected steps and registered `ledger-reader` consumers (D40). This changes no criterion or blocker.
