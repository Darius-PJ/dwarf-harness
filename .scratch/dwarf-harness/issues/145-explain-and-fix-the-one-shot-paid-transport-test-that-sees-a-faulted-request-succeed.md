# 145: Explain and fix the one-shot paid transport test that sees a faulted request succeed

Status: resolved
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

- [x] The cause of the three hosted failures is established by evidence: the hosted jobs' logs, obtained through the owner, or a reproduction. If it cannot be established, that is recorded explicitly. Proof: the counts-instrumented reproduction (177 failures in 10,000 `partial_header` leaves), `TestPaidHTTP1OneShotCloseWindow` and hosted diagnostic 42507895728 establish three possible signatures: a nil-error early answer, interrupted response drain, and “fault did not hit warm connection”. Run 42515734768 observes the first hosted decline labelled “warm connection not pooled”; the three earlier warm events remain identified by exclusion [INFERENCE]. Reproductions establish the mechanisms without the original hosted logs; their failure messages remain unobserved, and supplied logs are compared against all three signatures.
- [x] If the transport can send a faulted paid request twice, that is fixed in `internal/core/providers` and named in the Answer. If the test is at fault, the fix removes the cause. Either way the fix adds no retry, skip or quarantine and widens no timeout. Proof: no transport defect was found; product code is unchanged. S1 cuts the raw socket at the five fault closes. S2 detects an unpooled warm exchange through `httptrace.PutIdleConn` and fails before arming or dispatching the faulted attempt, with “warm connection not pooled”; that setup failure is not reported as a second send. Neither fix adds a retry, skip or quarantine or widens a timeout.
- [x] Where the cause allows one, a deterministic regression fails before the fix and passes after it. The test checks the dial count and the server's request count before it judges the faulted attempt's outcome, so a second send can never pass as a success. Proof: `TestPaidHTTP1OneShotCloseWindow/{anthropic,openai}/raw` was red with the old TLS-side closes and green with the raw cut; graceful controls passed throughout. Every faulted row checks counts first, and `TestPaidHTTP1OneShotCounts` detects its validator rule breaks. Forced decline on ec48c18 failed fourteen leaves at the count stage (2/2/2, no fault hit) and six earlier at their control checks; S2 instead labels all twenty as the warm precondition failure (1/1/1, before dispatch). S2's third rule break, functional `GetBody`, still fails through the count stage at 2/2/2 with reused=true and faultWrites=1, then passes after byte-identical restoration.
- [x] Every existing fault row of `TestPaidHTTP1OneShot` passes with its expected outcome and counts unchanged: `zero_write`, `read_eof`, `partial_header`, `partial_body` and `drop`, for `anthropic` and `openai`. The frozen F3-13 paid goldens replay unchanged. Proof: all ten original leaves retain their names and nonnil-error outcomes; dial/request/completion totals stay 1/1/1 (`zero_write`, `partial_header`, `drop`), 1/2/2 (`read_eof`) and 1/2/1 (`partial_body`). Focused, package plain/race and twentyfold race runs passed; every leaf passed twenty times. F3-13 and F3-14 fixture digests equal the base, and the unchanged agents and ledger consumer runs passed without regeneration, on S1; S2 changed only `transport_test.go`.
- [x] After the fix, hosted full-matrix runs of the final commit pass `TestPaidHTTP1OneShot` on Windows, Linux and macOS, in both the tests and race steps, using no paid minutes (D133 item 3; D146 item 3 makes the public repository's standard runners free). The design sets how many runs there are and how they are started. A failure of this test ends the series and returns the ticket to diagnosis, and a rerun never counts. Proof: F was declared as 2ca8675 and, after the toolchain move, became F' 326f5e82976a52e3119c7a3a3d2200f21d13929c under amendment 3 (v), with fresh Fable approval and no blocking findings. The two counted runs on F', by ordinal, are check 104 (run 44471427573) and check 103 (run 44471392438): each a push at attempt 1, classified as code, with tests and race passing on Windows, Linux and macOS and every native job and `Check result` successful. Old F's check 97 (run 42515714320) passed but is superseded by the rebase; check 99 (run 44443383210) failed only govulncheck, outside the series' scope, so neither counted nor ended it and is also superseded. A providers test failure ends the series; other failures neither count nor end it, and a replacement uses a fresh ref, never a rerun. This meets criterion 5, not a guarantee against future failures.

## Answer

The test read an early HTTP answer on the original connection as a success. The `partial_header` fault forwarded 8 bytes, then sent TLS `close_notify` before closing the socket; the loopback server answered the truncated line with 400, and the client could receive it, or lose its body mid-drain, before its own close completed. The reproduction found 177 failures in 10,000 `partial_header` leaf runs: a nil-error 400 or a response-drain failure, with one dial and one handler entry, not a second send. Core records such an answer as `unknown` with the hold kept; no product transport or money defect was found.

`partial_body` sends complete headers and zero body bytes and had a default-200 window: its body read fails, the handler returns without writing, and the server's default answer could reach the client before its own close completed. Unobserved in 2,000 reproduction leaves, it was observed on hosted runners: before-diagnostic 42507895728 had 25 nil-error `partial_body` failures in 12,000 leaves, with one dial and two handler entries, its normal counts. The status was not visible; default 200 follows from source [INFERENCE]. The same raw cut closes it: after-diagnostics 42509380879 and 42515734768 had none in as many.

A third, rarer base signature is “fault did not hit warm connection”: three of 6,000 before-diagnostic iterations, leaf and counts not visible. net/http waits at most 50 ms for the request-write result before pooling a connection and otherwise closes it (`wroteRequest`); Go's own tests widen that wait to avoid this flake. Under load the warm connection can be dropped and the next attempt sent once, unfaulted, on a new connection: no replay. Deterministic forced-decline reproduction made S1 fail at its count stage or earlier control checks. S2 reads the warm pooling decision through `httptrace.PutIdleConn` and fails before dispatch with “warm connection not pooled”. This is a test setup failure, at roughly five per hundred thousand hosted warm exchanges, not a second send; no retry, skip, quarantine or timeout widening is added. S2's after-diagnostic, run 42515734768, showed one such iteration and no other failure (macOS race step), the first hosted event labelled as the decline. The three base warm events remain identified by exclusion [INFERENCE].

S1 uses ordinary raw FIN at the five fault closes, leaves `read_eof` unchanged, checks counts before outcome, and adds `TestPaidHTTP1OneShotCloseWindow` and `TestPaidHTTP1OneShotCounts`. The [package documentation](../../../internal/core/providers/doc.go) adds two sentences on early answers and retained holds; S2 leaves it unchanged. The one-shot properties, product code, records, timeouts and frozen fixtures are unchanged. The original hosted failure lines remain unobserved: if supplied, compare them with the nil-error early 400, drain failure and “fault did not hit warm connection”; another signature reopens diagnosis.

Hosted stress is diagnostic, not acceptance: before, run 42507895728 failed on all three systems with nil-error answers, drain failures and three warm-condition failures; S1 after, run 42509380879 passed all three systems with no failure in 6,000 iterations (superseded); S2 after, run 42515734768 was green under amendment 9 with one labelled setup failure in macOS's race step, no other kind and no data race. No diagnostic SHA counts or lands. A providers failure still ends a counted series. The two exact-F' matrices meet criterion 5, not a guarantee. C3 status remains solely in its [Status line](../../../docs/ROADMAP-CORE.md#c3-agent-runtime) and [board row](../../../docs/ROADMAPS.md#status-board).

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

2026-11-08 — DesignerT145 supplied the brief; fresh Fable advisor RulingT145 approved it with eight amendments and no owner gate. S1 repairs the fixture and oracle; the ruling fixes the diagnostics, final-SHA series and hold on `main`. Clarification 1 below adds the S2 fix-up.

2026-11-08 — The project manager's counts-instrumented reproduction found 177 failures in 10,000 `partial_header` leaf runs: the early 400 and its interrupted drain, with one dial and one handler entry, not a second send. `TestPaidHTTP1OneShotCloseWindow` makes the window deterministic. The hosted jobs' failure messages were not needed and remain unobserved.

2026-11-08 — S1, reviewed commit ec48c182b45c07422407f5cb91f780ca1d27bd67, changes only `transport_test.go` and `doc.go`'s transport paragraph. ReviewT145S1: approved, no blocking findings. Its green push run 42509221277 and green after-diagnostic 42509380879 are superseded evidence, never counted; ec48c18 is not F.

2026-11-08 — Fresh Fable advisor ClarifyT145a ruled clarification 1 `change`, amendments 9–12, no owner gate. The before-diagnostic observed `partial_body`'s window and a third, warm-connection signature; deterministic reproduction proved the pooling-decline mechanism. S2 detects that setup precondition before dispatch, without retry.

2026-11-18 — S2, 2ca8675f611b0300099dc2249e54fa18fe42a177, adds only the warm-pool precondition to `transport_test.go`; forced-decline reproduction distinguishes setup failure from replay, and a real replay still fails at the count stage. ReviewT145S2 (fresh Fable): approved on 2026-11-18, no blocking findings; two nonblocking notes are retained in records.

2026-11-19 — F was declared as S2's reviewed commit 2ca8675f611b0300099dc2249e54fa18fe42a177 at 2026-11-18T21:19:11-05:00, after approval and after-2's amendment-9 green result. Run 44443383210's GO-2026-6859 govulncheck finding required the owner-approved go1.27.4 pin (D172), so the hold yielded to that commit and the patch-identical rebase became F' 326f5e82976a52e3119c7a3a3d2200f21d13929c under amendment 3 (v); the earlier local proof remains applicable because net/http's client path is unchanged. ReviewT145Fprime (fresh Fable) approved F' with no blocking findings; a new series started from zero, with `main` held from F' through its fast-forward, ticket 16's stacked S3 landing after it.

2026-11-19 — The two counted exact-F' matrices meet criterion 5, as listed above; no diagnostic counts. Integration: `main` 01ea48a → 326f5e82976a52e3119c7a3a3d2200f21d13929c, reflog 2026-11-19T02:11:56-05:00, pushed at 02:11:59; the hold ended then.

2026-11-19 — Closed with the five criteria's evidence above. Close review: approved by a fresh Fable advisor with no blocking findings: every commit, run, hash, count, criterion mapping and rule break checked against the notes, yields, hosted records, ruling and clarification; public text carries no private path, owner quote or private-record link; the PM then applied its three wording notes. RecheckT145Close (fresh advisor on Claude Fable 5.2) approved revision 4 with no blocking findings: every changed run, SHA, ref, attempt, per-system conclusion, duration, series ordinal, approval and merge time was checked against the notes, the F' approval, the hosted records and ruling amendments 3 and 4, and the public text carries no private path, owner quote or private-record link. Public ticket/paired-roadmap edits and private design/evidence records land separately (D145); no build-record entry, standing-rule change or contract-register edit is added (D161). The start and authority gates above stand.
