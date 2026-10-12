# DwarfHarness build process audit, 2026-10-12

Source: every agent transcript in the session directory (148 subagents plus the PM session), 2026-10-08 23:08 UTC to 2026-10-12 19:42 UTC. Costs are the harness's own per-message figures. The `openai-codex` figures are the harness's estimates; actual billing may differ.

## Spend: $1,247 over four working days
| Role (model, effort) | Agents | Cost | Share |
|---|---|---|---|
| advisor (Fable 5.1, max) | 17 | $311 | 25% |
| coder (Opus/Sonnet earlier; GPT-6-astra since 10-12) | 15 | $311 | 25% |
| designer (Opus 5.5, max; xhigh before 10-09 15:01) | 17 | $299 | 24% |
| PM (Opus 5.5) | 1 | $267 | 21% |
| runner (Sonnet before 10-12, Haiku since) | 78 | $47 | 4% |
| docwriter, other | 21 | $13 | 1% |

Ticket 05, subagents only: about $243 (designers $74, advisors $81, coders $88, runners under $1), plus a share of the PM's time.

## Runners: Haiku vs Sonnet
- Haiku: 24 runs, $1.03 in all (~$0.04 a run). Sonnet: 54 runs, $46 (~$0.85 a run).
- Haiku defects that reached the PM, all in read-and-report work:
  - RunnerT04R1 dropped one token line from a probe report; the PM caught it in the raw output.
  - RunnerT05Git read its request with the default viewer, which cut a long environment line; part of the probe stayed unverified.
  - RunnerT04Map had truncation gaps, for the same reason.
  - RunnerT05S3Api summarized struct fields instead of quoting them, and had no write tool (a role gap).
- Haiku command and evidence runs: 7 evidence runs, no defect reported. RunnerT05S1bEv had one harmless protocol slip.
- Sonnet: one significant error in 54 runs: `du` inside a colony folder blocked a measurement run for 25 minutes.

## Designers: max vs xhigh
- The owner set max for the designer roles on 2026-10-09; it was not a response to a quality problem.
- Choices the advisor modified, by brief:

  | Brief | Effort | Choices modified |
  |---|---|---|
  | C1 | xhigh | 4 of 19 (21%) |
  | ticket 03 | max | 4 of 23 (17%) |
  | ticket 04 | max | 3 of 26 (12%) |
  | ticket 05 | max | 9 of 33 (27%) |

  This shows no measurable quality gain from max.
- Cost:
  - C1 brief (xhigh): $7, 35 active minutes.
  - ticket 03 brief (max): $20, 65 minutes.
  - ticket 04 (max): $50, 144 minutes, with its freeze packet.
  - ticket 05 (max): $39, 128 minutes, with OG-B and its freeze packet.
- About 48% of designer spend ($142 of $298) went to documents: edit files, B entries, packets and status edits, not designs.

## Machine time on 2026-10-12
- 9 full checks, about 122 minutes.
- 1 false failure: `TestBodiesConcurrentUse` failed under the owner's game. There was another false failure at d4e3032, under three parallel jobs.
- Per slice: the coder's full check, then the evidence full check, then the pre-merge full check, plus 16 minutes of fuzzing even when no parser changed.

## Weak links
1. The load-sensitive race test: forces quiet windows, serializes all work, and has failed falsely twice.
2. Redundant full checks and fuzzing per slice.
3. Document fan-out:
   - C2's status sits in 13 places across 4 files, and each change needed a designer edit file and an advisor review (S4 review: 1 hour).
   - B entries of 92–112 lines restate doc.go and the ruling. Per-slice entries go stale: B24 had to amend B23.
4. Owner stops on routine merges: the recommended option was taken in 10 of 11 gates on 2026-10-12.
5. Haiku in read-and-summarize roles.
6. The PM as a single point of failure for fills and edits. Asserting scripts catch most slips; the advisor caught a fill-site slip in the OG5 packet.
7. Serial pipeline: the next ticket's design waits for the current ticket's merges.

## Savings that keep quality
| Change | Saves | Quality risk |
|---|---|---|
| Fix the race test's wall-clock bound (ticket 03 follow-up) | quiet windows, reruns; lets two tickets run at once | none |
| One full check per slice; coders run package tests only; fuzz only changed targets | ~30–45 min machine time a slice | none |
| Documents once per ticket, not per slice; B entries short with pointers; status only on the board and in the step's roadmap | ~$30–40 and ~1.5 h a ticket | low; needs his conventions |
| Designer: max for briefs; xhigh "drafter" for edit files, B entries, packets | ~half the document spend (~$70 a ticket at ticket 05's rate) | low |
| Runner: Haiku for scripted commands; Sonnet "reader" for maps and digests; `:raw` reads; raw outputs to files | errors, not dollars (+~$1 a ticket) | improves |
| Evidence first, then one advisor review of code, evidence and docs together | 2–4 advisor round trips a slice | none |
| Pre-approval of routine slice merges (advisor-approved, check passed, no owner decision, frozen contract or dependency) | 1–3 owner stops a ticket | his call |
| Owner-facing freeze packet of 1–2 pages; edit appendix in a PM file | designer and advisor time | low |
| Design the next ticket while the current one is coded | calendar time | none |

## Keep
- Advisor at max:
  - It caught the major findings in ticket 05's design (junction-safe removal) and in S3 (the capture-ref sweep).
  - It caught the S1a CommonDir defect that would have broken S3's records.
- One advisor per ticket: context reuse makes each later item cheap.
- Evidence on the exact commit.
- Owner design approvals and freezes.
- GPT-6-astra coders: clean questions, honored holds, few findings per thousand lines.
