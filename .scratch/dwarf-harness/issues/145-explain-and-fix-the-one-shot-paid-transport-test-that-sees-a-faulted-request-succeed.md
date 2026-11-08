# 145: Explain and fix the one-shot paid transport test that sees a faulted request succeed

Status: ready-for-agent
Type: task
Published: 2026-11-08
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C3 paid-attempt one-shot transport (B9, B36), as tickets 13 and 14 built it; found by three hosted checks
Blocked by: None (can start immediately)

## What to build

Explain why `TestPaidHTTP1OneShot`, the paid transport's one-send test in `internal/core/providers`, failed in three hosted checks, and remove the cause. Each failure was in a `partial_header` row, on a commit that changed only the Windows executor's tests and nothing in `internal/core/providers`:

- run 42409795650 on cc63d0b (2026-11-07), macOS job 128515276984, tests step: `TestPaidHTTP1OneShot/anthropic/partial_header`;
- run 42462309289 on b9efa89 (2026-11-08), Linux job 128674055961, race step: `TestPaidHTTP1OneShot/openai/partial_header`;
- run 42478930439 on b9efa89 (2026-11-08), Linux job 128726425292, tests step: `TestPaidHTTP1OneShot/openai/partial_header`.

Both commits are ticket [144](144-make-the-windows-executors-race-step-tests-reliable-on-hosted-runners.md)'s, and the two runs on b9efa89 were in its hosted series, where a failure in another package neither counted nor ended the series. The failure messages are unobserved: the job logs need a signed-in account, and the annotations carry only the failing test's line. Ticket [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md)'s first slice already fixed one race in this test: 64c4afb makes the transport fault tests observe the server connection's final state before closing, and it changed only the test.

The paid transport is meant to send each paid request exactly once (B9). Core records its dispatch intent and spends a one-use send permit before each send, and the transport then makes exactly one HTTP client call, a POST. Per the package documentation, the one-send rule rests on four properties of the transport:

- The body cannot be rewound and declares a positive length, which keeps Go's HTTP client off its replay path for a request that wrote nothing on a reused connection.
- The transport keeps its connections alive in its own HTTP/1.1 pool, so a paid request may go out on a warm, reused connection. A failure there is an unknown outcome, never evidence of a free request.
- There is no retry layer, redirect or proxy: a redirect response is returned, not followed, and HTTP/2, which has its own retry path, is never negotiated.
- The request carries no idempotency header.

Each fault row first completes one exchange on a real, verified TLS connection. It then faults the next exchange on that same connection and expects the attempt to fail, with no new connection and a fixed number of requests at the server. A faulted attempt that succeeds instead could be a second paid send. Until the failures are explained, they therefore count as a possible money defect, not a test flake.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Builds on the one-shot paid transport as tickets [13](13-dispatch-and-settle-a-sealed-static-anthropic-text-attempt-through-core.md) and [14](14-add-openai-responses-to-the-same-safe-static-paid-attempt-path.md) left it. B9 and B36 state the one-send rule, and the package documentation states the transport's profile.
- A ruled design chooses the remedy before code (D117). The cause is open; this ticket ranks no explanation or fix.
- The hosted job logs need a signed-in account; the project manager asks the owner for them.
- Adds no record type and changes no frozen encoding or golden (D148, D163, D164); a change that proves necessary waits for the owner.
- Local TLS fixtures exercise the transport: no live provider call, credential request or new paid authorization is needed.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153); a red check is neither rerun nor skipped.

## Acceptance criteria

- [ ] The cause of the three hosted failures is established by evidence: the hosted jobs' logs, obtained through the owner, or a reproduction. If it cannot be established, that is recorded explicitly.
- [ ] If the transport can send a faulted paid request twice, that is fixed in `internal/core/providers` and named in the Answer. If the test is at fault, the fix removes the cause. Either way the fix adds no retry, skip or quarantine and widens no timeout.
- [ ] Where the cause allows one, a deterministic regression fails before the fix and passes after it. The test checks the dial count and the server's request count before it judges the faulted attempt's outcome, so a second send can never pass as a success.
- [ ] Every existing fault row of `TestPaidHTTP1OneShot` passes with its expected outcome and counts unchanged: `zero_write`, `read_eof`, `partial_header`, `partial_body` and `drop`, for `anthropic` and `openai`. The frozen F3-13 paid goldens replay unchanged.
- [ ] After the fix, hosted full-matrix runs of the final commit pass `TestPaidHTTP1OneShot` on Windows, Linux and macOS, in both the tests and race steps, using no paid minutes (D133 item 3; D146 item 3 makes the public repository's standard runners free). The design sets how many runs there are and how they are started. A failure of this test ends the series and returns the ticket to diagnosis, and a rerun never counts.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 32, 69.
- Implementation decisions: 12, 24.
- Testing decisions: 2, 3, 11.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 paid-attempt dispatch and recovery
- Core design §8.3
- Routing design §6.5 and §10: the one-shot transport profile

## Comments

2026-11-08 — Published at the project manager's proposal; the owner approved publishing it and starting it at once (D170). Publication grants no separate implementation, contract-freeze, release or study approval; the start and authority gates above stand.
