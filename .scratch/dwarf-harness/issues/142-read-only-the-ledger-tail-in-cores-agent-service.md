# 142: Read only the ledger tail in Core's agent service

Status: ready-for-agent
Type: task
Published: 2026-10-31
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 agent-service scan and reopening cost; ticket 15's fix ruling, answer 5, and its matrix rerun
Blocked by: None (can start immediately)

## What to build

Keep Core's agent service from rescanning the whole ledger on every operation. Ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md)'s labelled diagnostic profiled a loopback run of 12 agents with 20 dormant contexts each and one active turn: 69% of CPU went to whole-history scans, because each scan opens the ledger at the segment start and decodes and hashes every earlier commit before skipping it. The per-wake `Since` page took 47% of CPU, global command lookups from sequence 0 for pre-minted IDs 34% and projection refreshes 10%. The work ran serialized under the service's lock (about 2.9 CPU cores busy, about 16 goroutines waiting on average), and loopback turns took 80–128 s from command to final answer. At two agents the same scripts took 2.4–7.7 s; the cost grows with history size and is multiplied by the number of concurrent turns. Ticket 15's conditional submit adds up to two more full-scan refreshes per refused write.

Reopening is quadratic in the same workload. `acceptCommitLocked`'s reused-command-ID check scans from sequence 0 for every commit whose command ID is below the running maximum, the normal case with concurrent producers. In ticket 15's matrix rerun, the third cell's fresh `Open` had not finished at about 9,400 records when the run's four-hour bound ended it, so the 20×20×3 size never ran.

Use the three remedies the diagnostic names:

- checkpointed tail reads, reusing the ledger reader's checkpoint or an in-memory tail cursor, so that refresh, `Since` and tail scans read only new commits;
- an incarnation command-ID index, so that old-ID checks stop rescanning from sequence 0;
- cold scans moved out of the agent service's lock.

The same command-ID index must remove the quadratic reopening cost. The measurement also reports exchange-token and slot waits read from ledger record times after a cell, not from an observer, as ticket 15's S6 review asked.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Every cursor, checkpoint or index is rebuilt from the ledger alone and is discardable, as ticket [140](140-bound-the-private-state-services-replay-and-lookup-cost.md) requires of its indexes.
- No record type, frozen rule, encoding or golden changes.

## Acceptance criteria

- [ ] A ruled design chooses among the three remedies before code.
- [ ] Every cursor, checkpoint or index the fix adds is rebuilt from the ledger alone: deleting it, or finding it stale or corrupt, loses no committed state, appends nothing and dispatches nothing.
- [ ] Reopening a colony grows no faster than the bound the design states: the reused-command-ID check no longer scans from sequence 0 for each commit.
- [ ] Behaviour is unchanged: authority, busy and denied answers, paid admission and settlement, turn-loop journaling, reads and restart recovery give the same results; the frozen goldens replay unchanged.
- [ ] A benchmark-style measurement row, preregistered in the design before code, records per-operation scan cost against history size for refresh, `Since`, the command lookup and the tail scans, and shows each growing no faster than the bound the design states. It also reports exchange-token and slot waits read from ledger record times after each cell. Ticket 15's full criterion-8 matrix, including 20×20×3, then runs on the fixed tree. The output keeps its label and claims no capacity.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Comments

2026-10-31 — Published with ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md)'s close, from its fix ruling's answer 5. Ticket 15 records its latency figures as measured under this cost profile and changes no latency itself. By the owner's decision this ticket goes before ticket [16](16-prove-concurrent-private-runtime-restart-and-child-shutdown.md), which it now blocks. Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.
