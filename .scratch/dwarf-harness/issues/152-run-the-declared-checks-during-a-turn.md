# 152: Run the declared checks during a turn

Status: ready-for-agent
Type: task
Published: 2026-11-27
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C4 validation runner as one mid-turn agent tool, through C10's Windows executor, for Release 1 (D182, D185, D186)
Blocked by: 17, 45

## What to build

Give agents one tool that runs their workspace's declared checks on the agent's own scratch during a turn and returns the results inside that turn, so that an agent can find and fix a failing check before it submits work. The run goes through ticket [17](17-run-admissible-required-go-checks-on-exact-candidates.md)'s runner and ticket [45](45-run-and-positively-close-a-restricted-windows-process-tree.md)'s Windows executor. It lands nothing and grants no new authority: only the landing gate's fresh run counts toward a landing (D185).

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Testing-only harness workloads until Release 1's exit test passes (D182); any A2 claim additionally needs actual per-profile C10 qualification.
- Uses ticket 17's runner, ticket 45's executor and the workspace's declared checks as built; no second runner, executor or check list.
- A design brief and a ruling come before code (D117). The ruling states the per-turn bounds and how landings stay first on the runner's lanes; a new Core operation or record family is frozen under D125 before code that writes it reaches `main`.
- Adds no new dependency without its D29 record.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153).
- No C6, C7, C8, C9 or GUI dependency.

## Scope boundaries

- Windows only (D138): on other systems the tool is unavailable and says so.
- Known limit, not a criterion: ticket [46](46-qualify-the-real-windows-runner-profile-for-a2.md)'s A2 qualification of the Windows executor stays On trigger (D182), so a mid-turn run claims no A2 assurance.
- Ticket [151](151-see-and-troubleshoot-from-the-terminal.md)'s timeline shows each run; whichever of the two tickets lands second makes it so.

## Acceptance criteria

- [ ] During a turn, an agent calls one tool that runs its workspace's declared checks against the current content of that context's own scratch, through ticket 17's runner and ticket 45's Windows executor. The turn receives each check's outcome and bounded output before it continues.
- [ ] Only declared checks run: the tool takes no command, shell text, path or environment from the model, and any argument only names declared checks of that workspace. Another context's scratch, canonical main and other workspaces are out of its reach.
- [ ] A mid-turn run lands nothing and grants nothing. Its pass counts toward no landing, approval or check change, the landing gate's fresh run alone counts, and the tool gives the agent no process, shell or network capability.
- [ ] Each turn's mid-turn runs are bounded in number and total time, and a landing's validation is admitted ahead of any waiting mid-turn run, so landings come first on the runner's lanes under the owner's declared workload (D185): about five agents, at most three paid model exchanges at once, one or two repositories. A turn at its bound gets a refusal that names the bound.
- [ ] Every run is recorded with its turn, its context, the scratch content it ran on, its checks, their outcomes and its timing, and the record survives restart. An interrupted run is recorded as interrupted and is never rerun automatically.
- [ ] Results reach the model only as a tool result within the existing output rules of ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md)'s turn loop: the context's turn limits bound the result's size, and no output reaches the model any other way.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 7, 34, 35, 62, 63.
- Implementation decisions: 1, 2, 3, 4, 10, 11, 13, 21.
- Testing decisions: 1, 2, 9, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C4 and C10
- Decision log D138/D182/D185

## Comments

2026-11-27 — Published by the owner's decision. Under the release tiers (D182) he chose to have agents run their declared checks during a turn as a Release 1 ticket (D185), and he approved its publication (D186). Release tier: Release 1. This grants no implementation, contract-freeze or release approval; the start and authority gates above stand.
