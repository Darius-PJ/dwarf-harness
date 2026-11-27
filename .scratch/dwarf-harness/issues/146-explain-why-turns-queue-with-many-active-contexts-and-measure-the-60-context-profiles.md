# 146: Explain why turns queue with many active contexts and measure the 60-context profiles

Status: ready-for-agent
Type: task
Published: 2026-11-20
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 declared runtime resource profiles, carried from ticket 16 (D173)
Blocked by: None (can start immediately)

## What to build

Find why bound turns queue and colony throughput falls as active contexts grow, decide whether Core's paid-exchange limits fit many active contexts, and then measure the two declared profiles that ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md)'s matrix did not reach within its preregistered 12-hour cap: runtime-20x20x3 (20 agents, each with 20 dormant contexts and 3 active turns) and actual-60-contexts (20 agents, each with its general and two work contexts, all 60 active). By the owner's decision ticket 16 closed with its matrix stopped at that cap, and these two cells moved here (D173).

Ticket 16's matrix ran each active context's five scripts (private CAS, scoped file loop, clean temporary merge, conflict merge, and a private continuation with a passive next turn) twice, against loopback TLS servers with fixture credentials. Each cell is a single observation, with no bound, comparison or capacity claim. It showed:

- runtime-12x20x1 (12 active contexts) and runtime-12x20x3 (36) completed; runtime-20x20x1 (20) completed 180 of its 200 samples before the cap.
- In every cell, at most three model exchanges ran at once for the whole colony. Core's `PaidLimits.MaxActivePaid` defaults to 3, at `main` a4ee351: `paid_types.go:33` (the [package documentation](../../../internal/core/agents/doc.go) states it at line 1215); it is checked at `loop_runtime.go:241` and `paid_dispatch.go:100` and passed to the transport at `paid_live.go:66`, all in `internal/core/agents`. The other turns waited as `waiting_resource`: at the first repetition's barrier 3 ran and 32 waited of 36, and 3 ran and 16 waited of 20.
- From 12 to 36 active contexts, the median time to a turn's durable final, over the samples that recorded one, rose 2–5× (scoped file loop 28.7 → 72.4 s, clean temporary merge 52.1 → 116.8 s, conflict merge 24.7 → 130.2 s); the passive next turn rose 1.2× (27.5 → 32.2 s) and the held private-CAS turns are described below; throughput to the cell's end fell from about 79 turns an hour to about 44.
- At 36 contexts the driver labelled 39 of 360 turns failed: 15 private-CAS turns whose exchange, held by design until an overlap barrier, passed the 120-s `DispatchTimeout` (`unresolved_result`), 10 more private-CAS turns at their deadline, and 14 conflicting merges, also `unresolved_result`. It labelled 70 more as unfinished at its watchdog (the 600-s turn timeout plus 60 s): of those, 24 clean temporary merges had reached a durable finished ending 81.7–166.3 s after their command, 11 had failed with `unresolved_result`, and 35 private-CAS turns had no ending recorded. The native endings were 275 finished, 50 failed and 35 none. CPU stayed low throughout.

To build:

1. Establish the cause of the queueing and the throughput fall with counted or profiled evidence: how much comes from the colony-wide `MaxActivePaid`, from exchanges that keep a place while held, from the dispatch clock, and from anything else found.
2. Decide whether the `MaxActivePaid` default fits many active contexts, and whether an exchange's dispatch clock, which `paid_dispatch.go:136` starts, should run while the exchange only waits for a place. No decision entry sets `PaidLimits` or their defaults, and D148 lists them as not frozen; a change that touches an owner decision waits for the owner.
3. Then, once the driver's outcome labels are corrected, measure runtime-20x20x3 and actual-60-contexts once each, with ticket 16's driver or a successor, under a preregistered cap sized from a timed run.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Builds on ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md)'s runtime, its measurement driver and analyzer, and its matrix figures.
- A ruled design chooses the method and any remedy before code (D117).
- The declared workloads, their traffic and the quiet-machine eligibility stay as ticket 16's ruling set them unless the owner changes them.
- Adds no record type and changes no frozen encoding or golden; a change that proves necessary waits for the owner.
- Local TLS fixtures exercise the runtime: no live provider call, credential request or new paid authorization is needed.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); a red check is neither rerun nor skipped.
- Each measurement instance runs through the quiet-machine runner, one at a time and never with race (D160 items 7–8).

## Acceptance criteria

- [ ] The cause of the queueing and of the throughput fall as active contexts grow is established by counted or profiled evidence, with the share of `MaxActivePaid`, of held exchanges, of the dispatch clock and of any other cause found. If a cause cannot be established, that is recorded explicitly.
- [ ] A ruled decision states whether the `MaxActivePaid` default fits many active contexts and whether an exchange's dispatch clock should run while it only waits for a place. Any change keeps one send per paid request (B9) and leaves holds and settlement unchanged, and a deterministic regression fails before it and passes after it; a change that touches an owner decision waits for the owner.
- [ ] After the driver's outcome labels are corrected, runtime-20x20x3 and actual-60-contexts each run once, with ticket 16's driver or a successor, on a hosted-checked commit, through the quiet-machine runner, under a cap preregistered before the run and sized from a timed run. The figures are reported as figures-only single observations, with no bound unless the owner sets one and no capacity claim; a held exchange that passes the dispatch bound is reported as failed with `unresolved_result`, never retried.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 18, 23, 24, 25, 26, 63.
- Implementation decisions: 1, 2, 3, 4, 8, 10, 11, 12.
- Testing decisions: 1, 2, 8, 9, 14, 16, 18, 20.

Canonical sections: Core roadmap C3; its cited controlling Core design and latest decision amendments.

## Comments

2026-11-20 — Published at the project manager's proposal: the owner chose to stop ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md)'s matrix at its preregistered 12-hour cap and carry its two 60-context cells here (D173). Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.

2026-11-27 — Release tier (D182): On trigger. Schedule it when turns queue at the owner's real agent count, after first raising Core's paid-call concurrency limit (default 3); C3 closed with ticket [91](91-record-service-loss-and-restoration-for-every-agent.md) without it, deferring the measurement D173 assigned here. This changes no criterion or blocker.
