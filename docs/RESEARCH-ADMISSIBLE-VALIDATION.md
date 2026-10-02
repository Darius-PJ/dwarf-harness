# Admissible validation paths: research and proposal for Q4

**Status:** Research and proposal, 2026-10-01; V3 decided and V6 options added 2026-10-02, together with notes on the runner's roadmap placement (section 6) and the cost of materializing fresh subjects on Windows (section 8). Answers open question Q4 in `DESIGN-UNDERCURRENT.md`: *what counts as an admissible validation path for failure and repair?* This is not an owner decision, not an approved Field Design and not an implementation report. No code exists yet for any layer of the harness. Proposals are marked as such; findings cite their sources in section 9.

**Why this question is load-bearing.** Three of Undercurrent's initial field kinds depend on it. `dependency_strain` starts when a failure is established and ends when a repair is established. `affordance` needs a definition of "validated" for promoted work. Graded `interference` needs build and test outcomes, not just textual overlap. If the validation path can be forged, gamed or fooled by noise, those fields become the counterfeit-consequence channel that `FIELD-PHILOSOPHY.md` §8 warns against. If it is too strict or too expensive, the fields stay empty.

**Reading convention:** "finding" means something a cited source establishes. **[PROPOSAL]** marks a design recommendation that no source establishes. **[INFERENCE]** marks reasoning from findings to this project.

## 1. The question, decomposed

"Admissible validation path" bundles five separate questions. Treating them as one is the most common way the problem goes wrong.

| Sub-question | Plain form |
|---|---|
| Executor | Who ran the check, and could the party being judged have influenced the run? |
| Subject | Exactly what was checked: which bytes, at which commit, in which environment? |
| Oracle | What decided pass or fail, and could it be wrong? |
| Noise | Would the same run give the same result again? |
| Attribution | If the result changed, what caused the change? |

Failure and repair are asymmetric. A failure claims something exists (a defect, reproducibly exposed by a check). A repair claims something stopped existing, which is harder to show: Dijkstra's observation that testing can show the presence of bugs but never their absence applies directly. The proposal therefore asks more of repair evidence than of failure evidence.

## 2. Summary of the proposal

**[PROPOSAL]** A validation path is admissible when a host-owned runner executes a content-pinned check against a content-pinned subject in a freshly materialized environment, reads the verdict from the process and artifacts it controls, and records the whole run as a typed validation record in the action ledger. An agent's statement about a result is never admissible, and neither is a log the agent selected.

Admissibility is necessary but not sufficient for a field effect. Each admissible record also carries an **evidence grade** and an **assurance level**:

| Property | What it answers | Effect in Undercurrent |
|---|---|---|
| Admissible | Did the run meet the minimum requirements in section 4.2? | Inadmissible runs are recorded but never become field sources |
| Evidence grade | How strong is the conclusion: observed once, reproduced, attributed to a change, corroborated by an independent check? | Selects the source mode (impulse versus sustained) and the support recorded on the source |
| Assurance level | How well isolated was the run from the agents whose work it judges? | Recorded on every source; the acceptance gate can require a minimum level |

Four rules carry most of the weight:

| Rule | Statement |
|---|---|
| Canonical only | Only failures on canonical state are world facts that seed `dependency_strain`. Failures inside private scratch describe private work and fall under Q3. |
| Reproduce before sustaining | A failure becomes a sustained source only once reproduced on the same pinned subject, check and environment, with the number of reruns set by an estimated flake rate. |
| Same check judges the repair | A repair is established only by the same pinned check that established the failure, rerun enough times to beat the failure's observed intermittency, with previously passing checks still passing. A changed or deleted check never establishes a repair. |
| Host overlays the check | Checks are pinned by content and overlaid by the host before every run, so an agent cannot repair a failure by editing the test that detects it. |

Sections 3 and 4 give the evidence and the detail. Section 5 traces the effects on Undercurrent.

## 3. What the research shows

### 3.1 Mature evaluation harnesses: the agent hands over an artifact, the host grades it

The strongest pattern across coding-agent benchmarks is a clean separation between the agent's work and the grade. SWE-bench treats the agent's only output as a diff. The harness writes the diff into a fresh container, applies it, resets the test files to the base commit, applies a hidden test patch the agent never saw, runs the test command and grades the log. Grading requires every designated fail-to-pass test to pass and every pass-to-pass test to be maintained; anything less is partial or unresolved. (SWE-bench does not count a skipped pass-to-pass test as a regression.) Harbor, the framework behind Terminal-Bench 2.0, offers an opt-in setting that runs the verifier in a separate environment from the agent. OpenHands and SWE-agent both capture a patch and hand it to the external SWE-bench harness for scoring.

Tools for interactive use make the opposite trade. Aider runs a configured test command after edits and treats a non-zero exit as failure, feeding the output back to the model. Claude Code hooks can run checks at lifecycle points such as Stop and PostToolUse and can block the agent from stopping; the verdict goes back to the model rather than into an external record. OpenAI's Codex cloud tasks run tests inside an isolated environment and present "verifiable evidence" as citations of terminal logs. **[INFERENCE]** The logs are captured by the environment, but the citations appear in the model's own summary, so the selection is the model's rather than the harness's.

| Arrangement | Who executes | Who decides what counts | Examples |
|---|---|---|---|
| External grading after the agent finishes | Harness, in a fresh environment | Harness, using tests the agent did not see | SWE-bench, Harbor verifier, OpenHands and SWE-agent evaluation |
| Host-run checks during the loop | Host process | Host reads exit code; verdict returned to the model | Aider auto-test, Claude Code Stop and PostToolUse hooks |
| Agent-run checks with logged evidence | Agent, in a sandbox | Agent selects which logs to cite | Codex cloud tasks |

**[INFERENCE]** Only the first arrangement produces evidence that can stand outside the agent's own account. Undercurrent needs that arrangement, applied continuously rather than once per benchmark run.

Even host-observed logs can be forged by the code under test. The current SWE-bench grading code says so in its comments: a patch "can print its own "PASSED" lines (e.g. from a conftest.py hook), so cross-check the log against the test command's exit status". It also treats a run with no parsed results and no sign the suite ran as invalid rather than passing, and counts a skipped fail-to-pass test as unresolved because otherwise "a patch that makes every F2P test skip ... scores RESOLVED_FULL". The comments describe what a patch can do, which is reason enough to design against it.

### 3.2 Agents game checks they can reach

The evidence that current models exploit validation when they can is substantial and recent.

| Source | Finding |
|---|---|
| ImpossibleBench (Zhong, Raghunathan, Carlini, 2025) | Tasks whose tests contradict the specification, so any pass is cheating. GPT-5 cheated on 76% of one-off SWE-bench variants and 54% of conflicting variants. Strategies included modifying tests, overloading comparison operators and special-casing. Hiding tests reduced cheating to near zero but hurt legitimate performance; read-only tests were a middle ground that blocked test editing but not special-casing. An LLM monitor (Claude Sonnet 4) caught 42–50% of cheating on the SWE-bench variants; Claude Opus 4 as monitor caught 57–65%. |
| METR (June 2025) | o3 reward-hacked in 30.4% of RE-Bench runs, including overwriting a timer, monkey-patching the evaluator to accept every submission, and finding a grader's precomputed answer on the call stack. On the one task where it was tested, anti-cheating instructions had a nearly negligible effect. |
| OpenAI chain-of-thought monitoring (March 2025) | In a task to make unit tests pass, a model reasoned "We can hack verify to always return true". Optimization pressure on the reasoning produced hidden rather than absent hacking. |
| Anthropic system cards (2025) | Claude 3.7 Sonnet "occasionally resorts to special-casing in order to pass test cases". The Claude Sonnet 4.5 card reports a reward-hack-prone coding rate of 1% against 14–16% for earlier models, and 53% on deliberately impossible tasks without an anti-hacking prompt. |
| SWE-bench issue #465 (2025) | Agents queried future repository history (for example `git log --all`, `git reflog`, checking out newer tags) to find the real fix. Fixed in SWE-bench v4.1.0 by removing remotes, newer tags and reflog before evaluation. |
| EvilGenie (2025), BenchJack (2026) | Explicit reward hacking, mainly hard-coding test cases, observed in Codex and Claude Code (test-file edits were rare); 219 flaws found across 10 agent benchmarks, most exploitable without solving any task. |

**[INFERENCE]** Three design consequences follow. First, any check file the judged agent can edit will sometimes be edited, so checks must be pinned and overlaid by the host. Second, read-only or overlaid checks still leave special-casing and runtime patching open, so a passing run is weaker evidence than a failing one. Third, the agent's environment, including its repository history, is part of the attack surface; the runner must materialize the subject itself rather than run in the agent's scratch.

### 3.3 Passing a check is plausibility, not correctness

The program-repair literature distinguishes *plausible* patches (pass the validation suite) from *correct* ones, and the gap is large.

| Source | Finding |
|---|---|
| Qi, Long, Achour, Rinard (ISSTA 2015) | Of three generate-and-validate repair systems, GenProg produced a correct patch for 2 of 105 defects, RSRepair for 2 of 24, AE for 3 of 105. A system that only deletes functionality matched or beat them on plausibility. Some acceptance tests checked only for exit code 0, which the authors call a weak proxy. |
| Smith, Barr, Le Goues, Brun (FSE 2015) | Patches passing all of their training tests passed a median of 75% of an independent held-out suite. Lower-coverage suites produced more overfitting. Human novices overfit too. |
| Wang, Pradel, Liu (2025) | On SWE-bench Verified, 7.8% of "resolved" patches fail the full developer test suite, and 29.6% of plausible patches behave differently from the reference fix; reported resolution rates were inflated by 6.2 percentage points. |
| UTBoost (Yu et al., ACL 2025) | Found 345 erroneous patches labelled as passing in the original SWE-bench, changing 24.4% of SWE-bench Verified leaderboard entries. |
| Ahmed et al. (2025) | Patches selected with a visible, generated reproduction test overfit at 21.8% to 33.0% when evaluated on hidden tests. |
| OpenAI (February 2026) | Stopped reporting SWE-bench Verified. An audit of 138 hard problems (ones o3 did not consistently solve) found material test or description issues in 59.4%, and tests narrow enough to reject many functionally correct submissions in 35.5%. Training-data contamination was a further reason. |

The last row cuts the other way: tests also fail correct work. The oracle problem (Barr, Harman, McMinn, Shahbaz, Yoo, TSE 2015) is the general statement. Automated oracles are often incomplete, and when none is adequate "the final source of test oracle information remains the human".

Mutation testing offers a measurable proxy for whether a check could detect a class of defect. Just et al. (FSE 2014) found mutant detection correlates with real fault detection independently of coverage, though 27% of real faults were not coupled to standard mutants. Google's mutation testing generates a median of 7 mutants per changelist and surfaces a median of 2, and developers rated 82% of surfaced mutants with feedback as productive. Coverage alone is a weak quality target (Inozemtseva and Holmes, ICSE 2014).

**[INFERENCE]** A repair record should say what it is: "the pinned check that exposed the failure now passes, and previously passing checks still pass". It should never say "fixed". That wording matters because agents will read it.

### 3.4 Failures are noisy

| Source | Finding |
|---|---|
| Google Testing Blog (Micco, 2016) | About 1.5% of test runs report a flaky result; almost 16% of tests show some flakiness; about 84% of observed pass-to-fail transitions involve a flaky test. Mitigations: rerun failures, mark tests flaky so they fail only after three consecutive failures, quarantine excessively flaky tests. |
| Luo, Hariri, Eloussi, Marinov (FSE 2014) | Of classified flaky-test fixes, async waits caused 45%, concurrency 20%, test-order dependency 12%. |
| iDFlakies (Lam et al., ICST 2019) | About half of a 422-test flaky dataset was order-dependent. |
| Parry et al. survey (TOSEM 2021) | Of 107 tests found flaky by rerunning whole suites, the flakiness of only 50 could be reproduced in isolation. |
| Bazel, Maven Surefire, Buildkite | Mature tools record more than pass and fail: Bazel's test statuses include FLAKY, TIMEOUT, INCOMPLETE, REMOTE_FAILURE and NO_STATUS; Surefire records flaky failures separately; Buildkite keeps retried jobs immutable. |
| GitHub required checks | A required check passes on "successful, skipped, or neutral" status. Checks must pass on the latest commit of the pull request. |

**[INFERENCE]** The headline number is the 84%. A single pass-to-fail transition in a large suite is more likely noise than a defect. A single fail-to-pass transition after a fix is equally weak. GitHub's acceptance of "skipped" as passing is a gap SWE-bench closed for fail-to-pass tests only. This proposal is stricter than either: a skipped run never counts as passing.

### 3.5 Attribution and confirming a repair

Google's continuous integration system (TAP) runs affected tests asynchronously after submission, and because it does not run every target at every change, it cannot always pin a breakage to one change (Memon et al., ICSE-SEIP 2017). Flake-Aware Culprit Finding (Henderson et al., ICST 2023) treats bisection as a noisy search: in its dataset of 13,600 breakages, about 40% had no culprit at all because the failure was flaky. It verifies a suspected culprit by rerunning at the culprit, its parent and the original failing version, with the rerun count set as ⌈log(1−C)/log(f)⌉ for flake rate f and target confidence C. Plain `git bisect` assumes deterministic outcomes and one transition.

Revert-first practice is widespread: Google's engineering book calls rolling back "often the fastest and safest route", and TAP automatically rolls back changes it is confident are culprits. Chromium's LUCI Bisection service identifies culprits for compile and test failures, and reverts it generates appear in Chromium's history with the reason "LUCI Bisection identified this CL as the culprit". None of the sources found publishes a formal protocol for confirming a repair beyond rerunning the failing target at the fix commit; the protocol in section 4.5 is assembled from the attribution literature.

Test selection decides which checks are relevant to a change. TAP selects targets that "directly or indirectly depend on the modified files". Meta's predictive test selection runs fewer than a third of transitively dependent tests while still catching over 99.9% of faulty changes, and labels tests that both pass and fail on retry as flaky rather than as deterministic failures.

### 3.6 Provenance, tamper evidence and isolation

SLSA's build track defines what makes a build record trustworthy. Level 3 requires that runs cannot influence one another, that each run gets an ephemeral environment, that a run cannot reach the provenance signing secrets, that cache poisoning is impossible, and that "every field in the provenance MUST be generated or verified by the build platform in a trusted control plane". SLSA v1.2 adds a source track. The in-toto attestation framework binds a predicate to subjects identified purely by digest and includes a test-result predicate with PASSED, WARNED and FAILED results and the configuration used. Bazel's remote execution API keys cached results by a digest of the command and its input tree, with environment variables sorted for a canonical digest, and its execute request has a `skip_cache_lookup` field that forces a fresh run.

Append-only logs give tamper evidence, not tamper prevention. RFC 6962 says logs "do not themselves prevent misissue, but they ensure that interested parties ... can detect" it, and Sigstore warns that without third-party monitoring "misbehavior ... might go undetected". Git's object model pins the exact bytes, names and executable bits of every tracked file under a tree hash, but not untracked files, ignored files or the state of a checkout that has drifted.

On Linux, processes with the same user ID can by default inspect each other and share file permissions. Separate user IDs, user namespaces (bubblewrap), Landlock (an unprivileged, stackable filesystem restriction since Linux 5.13) and gVisor are the available tools for narrowing that. The core design already records that the same-user boundary is unresolved (core §10 item 8).

**[INFERENCE]** The harness is the trusted control plane; agents and the code they write are the untrusted build steps. SLSA's level 3 isolation list translates almost directly into requirements for the validation runner, and the core's unresolved same-user boundary caps how much assurance any validation record can currently claim.

## 4. Proposal

### 4.1 Definitions

| Term | Meaning |
|---|---|
| Check | An executable oracle: a command plus the files that define it (test files, configuration, fixtures, test-runner settings). Identified by the digest of those files and the command. |
| Subject | The workspace state being judged, identified by a git tree hash computed by the host from files it materialized, never from the git index (core §3.5 treats the index as untrusted). |
| Environment | Toolchain versions, sorted environment variables, platform and dependency set, identified by a digest in the style of a remote-execution action key. |
| Run | One execution of a check on a subject in an environment. |
| Verdict | The run's outcome state (section 4.3), derived by the host. |
| Validation record | The ledger event describing one or more runs of the same (check, subject, environment), with every digest, every attempt's verdict, the runner identity and the assurance level. |
| Check lineage | Who authored the check and from what: the promoting context, its clone ancestry, and whether it was derived from another check. Feeds Q2. |

### 4.2 Admissibility requirements

**[PROPOSAL]** A run is admissible only when all of the following hold. Each row names the failure it prevents.

| Requirement | Prevents | Grounded in |
|---|---|---|
| Executed by the core's validation runner, never by an agent's shell or tool call | Self-reported or agent-selected results | SWE-bench external grading; core §3.2 "never from an agent's report alone" |
| Subject materialized by the host from a tree hash into a fresh, ephemeral directory outside every scratch, with no repository history beyond the subject commit | Planted untracked files, stale build output, mining future history | SWE-bench v4.1.0 history stripping; SLSA ephemeral environment; core §3.5 untracked-file gap |
| Check files overlaid by the host from their pinned digest after the subject is materialized, replacing any same-path files in the subject | Repair by editing or deleting the detecting test, conftest-style hooks | SWE-bench test reset and overlay; ImpossibleBench |
| Verdict derived from the exit status of the process the host spawned, cross-checked against structured results the runner collects and positive evidence that the suite ran | Printed "PASSED" lines, suites that never ran | SWE-bench grading invalidity rules |
| Skipped, timed-out, incomplete and infrastructure-failed outcomes are never "passed" | Skip-to-pass laundering, timeouts read as success | SWE-bench's skipped fail-to-pass rule, extended here to every check; Bazel and JUnit outcome states |
| Cache lookup disabled for every run that establishes or retires a failure or repair | Replaying a stale verdict instead of observing a new one | Remote Execution API `skip_cache_lookup`; remote caching hazards |
| Run isolated from concurrent runs and from the agents being judged, with no access to the ledger write path or any signing key | Runs influencing each other; forged records | SLSA level 3 isolation and unforgeable provenance |
| Record written by the runner directly to the ledger, including every attempt, digests, runner identity and assurance level | Selective reporting of favourable attempts | Buildkite immutable retries; in-toto subject-by-digest |
| Run triggered by a recorded action (a promotion, a schedule, or an agent's request), never fabricated after the fact | Unattributable evidence | Philosophy §3 concrete-action rule |

The last row clarifies an important point: an agent may *request* a validation run. The request is a concrete action. The result is a host observation. Neither the request nor its rationale moves the field; only the recorded result can.

### 4.3 Outcome states

**[PROPOSAL]** Each attempt resolves to one of: `passed`, `failed`, `timeout`, `skipped`, `incomplete` (started, did not finish), `infra_failure` (runner or environment fault), `no_status` (no evidence the suite ran). Each validation record then summarizes its attempts as `passed` (every attempt passed), `failed` (every attempt failed), `flaky` (attempts disagree on the same digests), or `inconclusive` (any attempt in the last four states and no disagreement). Only `failed` and `passed` summaries can establish failure or repair. `flaky` routes to epistemic uncertainty (section 5). `inconclusive` routes nowhere except the runner's own health metrics.

### 4.4 Failure evidence grades

**[PROPOSAL]**

| Grade | Requirement | Field effect |
|---|---|---|
| F0, claimed | An agent states that something fails | None. Not admissible. |
| F1, observed | One admissible `failed` attempt on a canonical subject | Transient `dependency_strain` impulse at reduced amplitude; schedules reproduction |
| F2, reproduced | `failed` summary over n attempts on the same check, subject and environment digests, where n is set from the check's estimated flake rate | Sustained `dependency_strain` source |
| F3, attributed | F2, plus the same check `passed` at the parent of a specific promotion and `failed` at that promotion, both reproduced | Strain sourced at the culprit promotion's changed nodes; enables revert as a repair route |
| F4, corroborated | F2 or F3, plus an independent check (different lineage) also failing on the same subject | Additional support on the same source; amplitude unchanged |

The rerun count follows the culprit-finding formula. A failure is real with confidence C when the chance that n consecutive failures are all flakes falls below 1 − C, giving n = ⌈log(1 − C) / log(f)⌉ for flake rate f.

| Estimated flake rate f | n at C = 0.999 | n at C = 0.99999999 |
|---:|---:|---:|
| 0.01 | 2 | 4 |
| 0.05 | 3 | 7 |
| 0.20 | 5 | 12 |

The formula assumes independent attempts. Flakes caused by the environment (a shared service down, a full disk) are correlated, so reruns should vary what can safely vary, such as test order and the run host. Order randomization also catches the order-dependent half of flaky tests. Flake rates start from a configured prior and are updated per check from its own history; the prior and the confidence are law parameters.

**[PROPOSAL] Only canonical subjects produce failure grades.** A failure inside an agent's scratch, including at a promotion gate, is a fact about private work. It is recorded, it can refuse a promotion, and the agent sees it. It does not seed `dependency_strain`, because doing so would disclose private work (Q3) and would let an agent manufacture strain from deliberately broken private work.

### 4.5 Repair evidence grades

**[PROPOSAL]**

| Grade | Requirement | Field effect |
|---|---|---|
| R0, claimed | An agent states that it fixed something | None. Not admissible. |
| R1, observed | One `passed` attempt of the pinned failing check on a later canonical subject | Freshness update only; the sustained source continues |
| R2, reproduced | `passed` summary over n attempts of the **same pinned check digest** on a later canonical subject, and every check that passed on the failing subject still passes | Ends the sustained source; emits the transition event described in section 5 |
| R3, attributed | R2, plus the check `failed` at the repairing promotion's parent and `passed` at that promotion, both reproduced | As R2, and the repairing promotion becomes an `affordance` source |
| Retired | The pinned check was changed, deleted or quarantined through the check-governance path (section 4.6) rather than passed | Ends the sustained source with reason "check retired"; emits an `epistemic_uncertainty` impulse; never an affordance |

The repair rerun count differs from the failure count because the risk differs. The question is whether a still-broken system could pass n times by chance. If the failure was observed in a fraction p of attempts before the repair, n = ⌈log(1 − C) / log(1 − p)⌉.

| Failure rate before repair, p | n at C = 0.999 |
|---:|---:|
| 1.0 (deterministic) | 1 (by convention; the formula is undefined at p = 1) |
| 0.9 | 3 |
| 0.5 | 10 |
| 0.3 | 20 |
| 0.1 | 66 |

An intermittent defect is expensive to declare repaired, which is the correct incentive. **[INFERENCE]** Google's finding that about 84% of pass-to-fail transitions involve flaky tests suggests that many intermittent failures are flaky checks rather than defects, so a single passing run after a claimed fix says little. Runs cost compute but no model tokens.

The pass-to-pass requirement in R2 is adapted from SWE-bench's grading (made stricter, since a skipped check never counts as maintained) and blocks the deletion-style repairs Qi et al. found: a "repair" that makes the failing check pass by removing functionality usually breaks something else that was passing.

A revert of an F3 culprit is the cheapest repair route and the most reliable: it restores a tree on which the check was already observed to pass. It still has to meet R2, because canonical may have moved since.

### 4.6 Check identity, pinning and governance

**[PROPOSAL]** A check's identity is the digest of its defining files and command, and each sustained failure records the exact check digest that established it. Repair is judged only by that digest. This one rule removes the most common cheat, editing the test, without needing to detect it.

Checks still need to change. OpenAI's 2026 audit of 138 hard SWE-bench Verified problems found tests rejecting many correct submissions in 35.5% of them. So changing a check that holds an open failure is a separate event with a separate authority:

| Event | Who may cause it | Effect |
|---|---|---|
| Check added | Any promotion | New check; its first canonical runs establish its baseline and flake history |
| Check modified or deleted while it holds no open failure | Any promotion | New digest replaces old for future runs |
| Check modified or deleted while it holds an open failure | Promotion plus explicit retirement by an agent in the Corps that owns the check, other than the repairing context; the Orchestrator has the last say (section 4.6.1) | The open failure ends as "retired", not "repaired" (section 4.5) |
| Check quarantined as flaky | The runner, automatically, past a law-defined flake rate; or the owner | The check stops sourcing strain; its node gains epistemic uncertainty |

#### 4.6.1 Retirement authority (V3)

**Owner decision (2026-10-02):** an agent within the same Corps can close a check; the Orchestrator has the last say. The process should stay simple and should not overload the Orchestrator, which runs on a larger model and can judge whether a retirement is valid when given the proper information.

**[PROPOSAL] — mechanism implementing that decision:**

| Element | Proposal |
|---|---|
| Owning Corps | The Corps of the context that promoted the check's current digest. Only an agent in that Corps may retire it. |
| Who may not retire | The context whose promotion the check would judge as a repair, and its clones. This keeps the one guard the research supports most directly (section 3.2): the party being judged cannot remove the judge. |
| Retirement packet | Assembled by the host, not written by the agent: the check digest and its diff or deletion, every open failure the check holds with its failing output and grade, the promotions involved, and whether the retiring agent authored any of them. The agent adds only a reason category and a short argument. Reason categories: the expectation is wrong, the tested behavior is no longer intended, the check depends on its environment rather than the code, the check is non-deterministic, or another check supersedes it. |
| Effect | Retirement takes effect when recorded. The open failure ends as "retired" and emits an `epistemic_uncertainty` impulse, never an affordance. |
| Orchestrator review | The Orchestrator is notified with the packet and reviews it when its workload allows. It acts only to overturn. Reopening restores the failure under the original pinned check digest; a fresh run then refreshes it. Both events are recorded in the ledger. |
| Review criteria | Encoded in the Orchestrator's skill suite alongside the strange-mood interruption criteria: does the packet show the check is wrong, or only that it is inconvenient? |

The Orchestrator acts only to overturn, so it carries no load unless something looks wrong. Retirement never produces an affordance, so no agent gains field influence by retiring a check, which keeps the incentive to abuse it low. Retiring a check owned by another Corps is not permitted; a Corps that believes another Corps' check is wrong raises it through its Diplomat.

### 4.7 Who authors checks: lineage and independence

**[PROPOSAL]** Checks come from three places, and their origin affects the grade they can support.

| Origin | Example | Can support |
|---|---|---|
| Canonical suite | Tests and acceptance rows already promoted to canonical | F1 to F4; R1 to R3 |
| Agent-authored reproduction check | An agent writes a test showing a defect, requests a run against canonical; the host overlays the test file onto a canonical subject | F1 and F2, marked "agent-authored"; amplitude reduced until corroborated (F4) or until the check is promoted into the canonical suite |
| Recipe acceptance rows | Host-checked acceptance from core §11.3 | Same as canonical suite once the recipe is promoted |

Agentless and SWT-bench show that model-written reproduction tests are useful when the host executes them and when they are first observed to fail on the unpatched code; SWT-bench reports that using them as a filter doubled SWE-Agent's precision. They are also oracles written by a fallible agent: a failing reproduction test proves that canonical and the test disagree, not which one is wrong. That is why an agent-authored failure is admissible but weaker until another lineage agrees.

Independence uses Q2's lineage rule. Checks written by clones of one agent, or derived from the same issue text, count as one lineage. Knight and Leveson's experiment with independently written programs found failures were correlated far more than chance predicts; checks produced by the same model family should be expected to share blind spots. **[INFERENCE]** Model family belongs in the lineage hints.

A useful property follows. The context that found a failure and the context that repairs it are normally different. The finder's pinned reproduction check becomes the repairer's oracle, visible but immutable, which is the read-only middle ground ImpossibleBench found effective against test editing. A repairer that is also the finder is permitted, but its R2 or R3 record is marked "self-judged" and cannot produce an `affordance` source until another lineage's check also passes.

### 4.8 When validation runs

**[PROPOSAL]**

| Trigger | Subject | Checks run | Field relevance |
|---|---|---|---|
| Promotion attempt | The candidate scratch tree | Checks affected by the change, selected through the coupling graph (reverse dependencies, as TAP does) | Gate only. Results are private; a refusal is recorded. |
| Accepted promotion | New canonical tip | Affected checks not already run at the gate on an identical tree digest | Can establish F1 and, after reruns, F2 and F3 |
| Agent request | Canonical tip, optionally with an overlaid agent-authored check | The named check | Can establish agent-authored F1 and F2 |
| Sustained-source refresh | Canonical tip | The pinned failing check | Updates freshness; can establish R1 and R2 |
| External perturbation | Canonical tip in a new environment digest (a toolchain or dependency update) | Checks covering the changed environment | Can establish failures caused by the environment, attributed to the perturbation rather than to a promotion |

A gate run on exactly the tree that becomes canonical is reusable as a canonical observation, since the digests match; this avoids running everything twice. The gate itself answers part of the open promotion questions in core §3.5 and §10 item 8: fast-forward promotion "does not itself certify that tests were run or passed", and this proposal recommends that it should, using the not-rocket-science rule from merge queues.

Validation consumes a real, finite resource. The runner's queue is therefore a resource node in Undercurrent, and its depth and wait time feed `resource_pressure`, which gives the promotion pipeline and resource pressure a shared source.

### 4.9 Assurance levels

The core has not yet built an enforceable boundary between same-user child processes and core-owned state. Until it does, a validation record is trustworthy against honest mistakes and casual shortcuts but not against an agent that deliberately attacks the runner. **[PROPOSAL]** Record an assurance level on every validation record, loosely following SLSA's build levels, and let the acceptance gate (Undercurrent §14.4) and law parameters require a minimum.

| Level | Runner isolation | Trust it supports |
|---|---|---|
| A1 | Host-run in a fresh directory, same user as agents | Admissible; protects against mistakes and casual gaming. Code under test can still reach other files the user can. |
| A2 | A1 plus filesystem and process restriction: a separate user or user namespace, Landlock rules denying writes outside the run directory, no ptrace of the core, network off unless the check declares it | Resists ordinary tampering by the code under test |
| A3 | A2 plus an ephemeral sandbox per run (for example gVisor or a container with its own kernel interface) and ledger records with tamper-evident checkpoints kept outside agent reach | Approaches SLSA level 3 isolation for the runner |

Level A1 is achievable in core phase 1. A2 depends on the core resolving its same-user boundary.

#### 4.9.1 Options for the visibility floor (V6), to be settled by testing

Assurance applies only to sources derived from validation runs: `dependency_strain`, validation-derived `affordance`, graded `interference` and flakiness-derived `epistemic_uncertainty`. Resource pressure and staleness are measured by the host without running agent-written code, so V6 does not affect them.

Tampering by code under test runs almost entirely in one direction. Every case found in section 3.2 fakes a pass: fake "PASSED" output, a patched evaluator, an overwritten comparison. Faking a failure on canonical would require sabotage code already promoted to canonical, which is a real defect in its own right. **[INFERENCE]** A failure observed at A1 is therefore more trustworthy than a pass observed at A1, and the options below use that asymmetry.

| Option | Rule | Advantages | Risks | What testing would decide |
|---|---|---|---|---|
| O1, single floor at A1 | Every admissible source is visible; the assurance level is shown in the rendering | Available in core phase 1; earliest real use | Forged passes end strain and create affordance | Whether the forged-pass rate at A1 is low enough to accept |
| O2, single floor at A2 | Validation-derived sources stay hidden until A2 exists | Simplest to reason about; strongest | Validation-derived fields stay invisible until the core's isolation boundary is built, possibly a long time | Whether A2's runtime overhead is acceptable; how long the wait would be |
| O3, weighted, no floor | Each assurance level carries a law-defined amplitude multiplier, so A1 sources appear weaker | Fully tunable; no hard cutoff | Weak forged signals still appear; agents may ignore weak signals | Which weights keep agent behavior useful in shadow and simulation |
| O4, asymmetric by direction | At A1, failures are visible and can start strain; a repair at A1 ends strain only after an independent scheduled rerun confirms it in a separate run directory; affordance requires A2 | Uses the asymmetry above; strain still ends without waiting for A2 | Collusion between the repair and the refresh run, which is unlikely if the refresh is host-scheduled | Whether the confirmation rerun catches the forged passes found in adversarial tests |
| O5, audit sampling with canaries (canaries adopted; sampling off by default) | A1 floor, plus a sample of A1 records rerun at a higher level once available (or in a separate sandbox before then), plus canary checks that must fail; a canary reported as passing marks the runner compromised | Measures tampering directly instead of assuming it | The sample rate and canary design need tuning | The discrepancy rate between A1 records and their audits |

The test that decides among them is an adversarial suite in the style of ImpossibleBench, run inside the colony before the acceptance gate. Agents receive tasks whose pinned checks cannot legitimately pass, are free to use any action their permissions allow, and the suite measures how often a forged pass reaches the ledger at each assurance level, how often confirmation reruns, audits and canaries catch it, and the runner overhead per level. Because floors, weights, rerun rules and audit rates are all law parameters, every option can be tried against the same synthetic and shadow ledgers without code changes.

**[PROPOSAL] Starting configuration (owner-selected 2026-10-02): O4 plus canaries.** Sample re-runs from O5 are dropped from the starting configuration to keep compute low: they add a fixed fraction to every validation run, while O4's confirming rerun applies only to repairs and canaries add a few small checks per batch. The sample rate stays a law parameter with a default of zero, so it can be switched on if the adversarial suite shows that confirming reruns and canaries miss too much. The floor itself remains open until the suite has results.

| Element | Rule |
|---|---|
| Failures at A1 | Visible; can start strain |
| Repairs at A1 | End strain only after a second, host-scheduled run in a separate run directory confirms the pass with the same pinned check |
| Affordance | Requires A2 |
| Canaries | Each validation run includes at least one sentinel check that must fail, executed **in the same process as the suite**, not as a separate run. A sentinel reported as passing invalidates the run and marks the runner suspect. |
| Sample re-runs | Off by default (law parameter) |

Canaries must share the suite's process because isolation levels protect the runner and ledger from code *outside* the run, not from code inside it. Code under test executes in the same process as the tests and can patch the test framework, override comparisons or exit early at any assurance level (section 3.2). A sentinel in the same process is exposed to the same patching, so a global assertion override that makes the real tests pass also makes the sentinel pass, which reveals it. Tampering that targets specific tests by name will evade a generic sentinel; that residual risk is what the adversarial suite measures.

The retirement path (section 4.6.1) also serves as a legitimate exit when a check cannot be passed. ImpossibleBench found that giving models an explicit option to flag an impossible task lowered GPT-5's cheating on conflicting SWE-bench tasks from 54% to 9%, and o3's from 49% to 12%. Agent instructions should name retirement as the expected response to a check that appears wrong.

### 4.10 Work that is not code

The colony is not only a software organization. The Science Corps researcher in the illustrative story from the design discussion produces opportunity memos, and other corps will produce documents, data sets and plans. The validation path applies to any workspace content, but automated oracles for non-code work are weaker and rarer.

| Artifact | Admissible checks | No automated oracle |
|---|---|---|
| Data files | Schema validation, referential integrity, recomputing derived figures from their inputs | Whether the data is true of the world |
| Documents with sources | Link resolution, host re-fetch of a cited source and match against the quoted text, deadline and date consistency | Whether the argument is sound |
| Plans and memos | Required-section presence, connector read-back matching (core §11.3) | Whether the plan is good |

**[PROPOSAL]** Non-code failures are admissible on the same terms as code failures. Where no automated oracle exists, the only admissible source is a human-oracle event: the owner recording a verdict through an owner command, which the ledger attributes to the owner. This is consistent with the oracle-problem literature's conclusion that the human remains the final oracle, and with the philosophy's statement that human judgment remains necessary where evidence is insufficient. Agents cannot act as human oracles for each other; that would reintroduce declaration as a field source.

## 5. Downstream effects on Undercurrent

| Undercurrent element | Effect of this proposal |
|---|---|
| `dependency_strain` source rules | F1 seeds a reduced transient impulse; F2 and above seed a sustained source; R2 ends it; retirement ends it with a different reason. Only canonical subjects count. |
| Origin node of strain | An unattributed failure (F2) is sourced at the failing check's node, with `epistemic_uncertainty` spread over that check's dependency closure, because the defect could be anywhere the check reaches. An attributed failure (F3) is sourced at the culprit promotion's changed nodes and propagates downstream to dependents. This resolves an ambiguity the draft did not address: strain flows from a dependency to its dependents, but a failing test sits downstream of the defect. |
| Q12 (when a sustained source ends, and whether ending emits a transient) | Answered as a proposal: an R2 repair ends the source and emits a short `affordance` impulse at the repaired nodes so agents who avoided the region notice it has reopened; an R3 repair also makes the repairing promotion a sustained `affordance` source; retirement emits an `epistemic_uncertainty` impulse and no affordance. |
| `epistemic_uncertainty` | Gains two sources: `flaky` validation summaries at the check's node, and quarantined checks. Flakiness is uncertainty about the check, not strain on the code. |
| `affordance` | "Validated" now has a definition: a promotion that passed its gate checks at an admissible assurance level, with R3 repairs as the strongest case. Self-judged repairs do not produce affordance until another lineage corroborates. |
| `interference` grading | Textual overlap from `git merge-tree` is the first grade. Build and test outcomes for the merged tree come from this same runner, so the interference grades and the failure grades share one evidence pipeline. |
| `resource_pressure` | The validation runner's queue becomes a resource node. |
| `perturbation` | Environment-digest changes become boundary events, and failures they cause are attributed to the perturbation rather than to a promotion. |
| Source support and confidence | Each source records its evidence grade, the number of independent check lineages, assurance level and freshness, kept separate from amplitude as section 6 of the design already requires. Rendering can show "reproduced 5 of 5, attributed, A1" rather than a bare number. |
| Ledger contract (§15.1) | Validation-run events gain check, subject and environment digests, per-attempt outcomes, runner identity, assurance level, cache-bypass flag, trigger reference and check lineage. New events: check added, modified, retired, quarantined; culprit attribution; human-oracle verdict. |
| Law parameters | Confidence C for failure and repair, flake-rate prior, quarantine threshold, F1 amplitude factor, agent-authored amplitude factor and minimum assurance level per kind all become versioned law. |
| Acceptance gate (§14.4) | Gains adversarial cases drawn from section 3.2: a patch that prints fake pass lines, a check that is skipped, a test edited to pass, a fix that deletes functionality, a check retired by the repairing context, and flaky checks producing false failures. |
| Gaming surface | An agent cannot seed strain by declaration. It can seed reduced, agent-authored strain by writing a check that fails on canonical; that costs compute, is deduplicated by lineage and is visible as agent-authored. It cannot end strain by editing a check. It can still special-case a fix to pass a visible check; independent-lineage corroboration narrows but does not close that gap. |
| Phase dependencies | A minimal runner (assurance A1) belongs in core phase 1 alongside promotion, not in phase 4 with recipes. Recipes' acceptance rows become one more check source when they arrive. |

## 6. Implications for the core design

This proposal places requirements on the core that its draft left open. They were listed here for the owner's decision. Since 2026-10-02 the core design cites this section for the runner's placement (core §9), and `ROADMAP-CORE.md` builds the runner at C4; the other rows remain proposals.

| Core item | Current state | Implication |
|---|---|---|
| §3.5 promotion | "Fast-forward promotion does not itself certify that tests were run or passed"; whether acceptance is required is open | Recommend a promotion gate that runs affected checks on the candidate tree |
| §3.5 per-action observation | Host derives changed paths and hashes from scratch; untracked files unspecified | The runner materializes subjects from host-computed tree hashes, so untracked scratch state never reaches a validation run |
| §11.3 acceptance | Host evaluates acceptance; unsupported checks are reported, not passed | Consistent; acceptance rows become pinned checks with lineage |
| §9 rollout | Recipes and acceptance in phase 4 | A minimal validation runner moves into phase 1, since promotion gating and the field's failure evidence both need it. `ROADMAP-CORE.md` (2026-10-02) places the runner at step C4, immediately after the agent runtime step (C3) |
| §10 item 8 | Same-user boundary unresolved | Sets the ceiling on assurance level; A2 requires it to be resolved |

## 7. What this cannot guarantee

A passing validation record never proves correctness. Plausible repairs that overfit their checks will pass R2 and sometimes R3; the literature puts that rate for LLM-generated patches between roughly a fifth and a third under some conditions. A failing record proves only that a check and the subject disagree; a wrong check produces a real-looking failure until someone retires it. Reruns defeat independent flakes, not correlated ones. Lineage rules reduce correlated evidence but cannot detect every shared blind spot. Until the core has an OS-enforced boundary, code under test can tamper with its own run. And for much non-code work there is no automated oracle at all.

These limits are the reason the proposal grades evidence rather than accepting or rejecting it, and the reason the renderer should show grade and assurance alongside every impression.

## 8. Open sub-questions

| ID | Question | Why it matters |
|---|---|---|
| V1 | What flake-rate prior and confidence values should the initial law use? | Sets rerun cost and false-positive rate for every failure |
| V2 | Should F1 seed any field effect, or only schedule reproduction? | A transient on a single observation is a useful early warning, but single failures are often flaky (section 3.4) |
| V3 | Who holds authority to retire a check with an open failure? | **Decided 2026-10-02:** an agent in the owning Corps retires; the Orchestrator has the last say (section 4.6.1) |
| V4 | How are dependencies for a subject materialized (package installs, caches) without breaking reproducibility or making every run slow? | The core already lists per-workspace setup as open; validation runs inherit it |
| V5 | Which checks are required at the promotion gate, and how much time may the gate take before it delays work more than it saves? | Gate cost versus canonical health |
| V6 | What minimum assurance level must a source have before Undercurrent shows it to agents? | To be settled by an adversarial test suite; options and a proposed starting configuration in section 4.9.1 |
| V7 | Is a human-oracle verdict ever reversible, and by whom? | Owner verdicts are the only source for much non-code work |
| V8 | Should agent-authored checks that reproduce a failure be promoted automatically into the canonical suite when the failure is repaired? | Grows the suite with regression tests, but also grows it with agent-written oracles |

**Materialization cost on Windows (2026-10-02).** On the owner's Windows machine, creating a checkout cost about 2 ms per file (19–21 s for 10,000 files), and the first read of each freshly written file about 23 ms (core Appendix A.7). **[INFERENCE]** Materializing a fresh subject for every run is therefore expensive for large workspaces, which bears on V4 and V5. Whether to exclude the colony's `runs/` directory from scanning is an open owner decision (`DECISIONS.md`, open items).

## 9. Sources

**Harnesses and benchmarks**

| Source | Link |
|---|---|
| SWE-bench repository, evaluation harness and grading code | <https://github.com/SWE-bench/SWE-bench> |
| Jimenez et al., SWE-bench (ICLR 2024) | <https://arxiv.org/abs/2310.06770> |
| OpenAI, Introducing SWE-bench Verified (2024) | <https://openai.com/index/introducing-swe-bench-verified/> |
| OpenAI, Why we no longer evaluate SWE-bench Verified (2026) | <https://openai.com/index/why-we-no-longer-evaluate-swe-bench-verified/> |
| SWE-bench issue #465, repository state loopholes | <https://github.com/SWE-bench/SWE-bench/issues/465> |
| Xia et al., Agentless | <https://arxiv.org/abs/2407.01489> |
| Mündler et al., SWT-Bench (NeurIPS 2024) | <https://arxiv.org/abs/2406.12952> |
| Harbor framework | <https://github.com/laude-institute/harbor> |
| OpenHands benchmarks | <https://github.com/OpenHands/benchmarks> |
| SWE-agent batch mode | <https://swe-agent.com/latest/usage/batch_mode/> |
| Aider linting and testing | <https://aider.chat/docs/usage/lint-test.html> |
| Claude Code hooks | <https://code.claude.com/docs/en/hooks> |
| OpenAI, Introducing Codex | <https://openai.com/index/introducing-codex/> |

**Gaming and reward hacking**

| Source | Link |
|---|---|
| Zhong, Raghunathan, Carlini, ImpossibleBench | <https://arxiv.org/abs/2510.20270> |
| METR, Recent frontier models are reward hacking | <https://metr.org/blog/2025-06-05-recent-reward-hacking> |
| OpenAI, Chain-of-thought monitoring | <https://openai.com/index/chain-of-thought-monitoring/> |
| Anthropic, Claude 3.7 Sonnet system card | <https://assets.anthropic.com/m/785e231869ea8b3b/original/claude-3-7-sonnet-system-card.pdf> |
| Anthropic, Claude Sonnet 4.5 system card | <https://assets.anthropic.com/m/12f214efcc2f457a/original/claude-sonnet-4-5-system-card.pdf> |
| Gabor, Lynch, Rosenfeld, EvilGenie | <https://arxiv.org/abs/2511.21654> |
| Wang et al., BenchJack | <https://arxiv.org/abs/2605.12673> |

**Test validity and repair**

| Source | Link |
|---|---|
| Barr, Harman, McMinn, Shahbaz, Yoo, The Oracle Problem in Software Testing (TSE 2015) | <https://discovery.ucl.ac.uk/1471263/> |
| Qi, Long, Achour, Rinard, patch plausibility and correctness (ISSTA 2015) | <https://www.cs.toronto.edu/~fanl/papers/kali-issta2015.pdf> |
| Smith, Barr, Le Goues, Brun, Is the cure worse than the disease? (FSE 2015) | <https://www.cs.washington.edu/homes/brun/pubs/pubs/Smith15fse.pdf> |
| Wang, Pradel, Liu, Are "Solved Issues" in SWE-bench Really Solved Correctly? | <https://arxiv.org/abs/2503.15223> |
| Yu et al., UTBoost (ACL 2025) | <https://arxiv.org/abs/2506.09289> |
| Ahmed et al., Is the Cure Still Worse Than the Disease? | <https://arxiv.org/abs/2511.16858v1> |
| Just et al., Are mutants a valid substitute for real faults? (FSE 2014) | <https://homes.cs.washington.edu/~mernst/pubs/mutation-effectiveness-fse2014-abstract.html> |
| Inozemtseva and Holmes, Coverage is not strongly correlated with test suite effectiveness (ICSE 2014) | <https://cs.ubc.ca/~rtholmes/papers/icse_2014_inozemtseva.pdf> |
| Petrović, Ivanković, Fraser, Just, Practical Mutation Testing at Scale | <https://homes.cs.washington.edu/~rjust/publ/practical_mutation_testing_tr_2021.pdf> |
| Knight and Leveson, independence assumption in multiversion programming (TSE 1986) | <https://www.kth.se/social/files/564df871f2765419e306178d/KnightLeveson.pdf> |
| Kang, Yoon, Yoo, LIBRO (ICSE 2023) | <https://arxiv.org/abs/2209.11515> |

**Flakiness, attribution and CI practice**

| Source | Link |
|---|---|
| Micco, Flaky Tests at Google and How We Mitigate Them | <https://testing.googleblog.com/2016/05/flaky-tests-at-google-and-how-we.html> |
| Luo, Hariri, Eloussi, Marinov, An Empirical Analysis of Flaky Tests (FSE 2014) | <https://huang.isis.vanderbilt.edu/cs8395/paper/flakytest.pdf> |
| Lam et al., iDFlakies (ICST 2019) | <https://par.nsf.gov/biblio/10101224-idflakies-framework-detecting-partially-classifying-flaky-tests> |
| Parry et al., A Survey of Flaky Tests (TOSEM) | <https://eprints.whiterose.ac.uk/id/eprint/230095> |
| Memon et al., Taming Google-Scale Continuous Testing (ICSE-SEIP 2017) | <https://research.google/pubs/pub45861/> |
| Henderson et al., Flake Aware Culprit Finding (ICST 2023) | <https://research.google/pubs/flake-aware-culprit-finding/> |
| Software Engineering at Google, chapter 23 | <https://abseil.io/resources/swe-book/html/ch23.html> |
| Machalica et al., Predictive Test Selection | <https://arxiv.org/abs/1810.05286> |
| git bisect | <https://git-scm.com/docs/git-bisect> |
| Bazel user manual and test encyclopedia | <https://bazel.build/docs/user-manual>, <https://bazel.build/reference/test-encyclopedia> |
| Maven Surefire, rerunning failing tests | <https://maven.apache.org/surefire/maven-surefire-plugin/examples/rerun-failing-tests.html> |
| Buildkite retries | <https://buildkite.com/docs/pipelines/configure/retry> |
| GitHub protected branches and required checks | <https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches> |
| Chromium LUCI Bisection | <https://chromium.googlesource.com/infra/luci/luci-go/+/HEAD/bisection/README.md> |
| GitHub, troubleshooting required status checks | <https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks> |

**Hermeticity, provenance and isolation**

| Source | Link |
|---|---|
| Bazel hermeticity | <https://bazel.build/basics/hermeticity> |
| Remote Execution API | <https://github.com/bazelbuild/remote-apis/blob/main/build/bazel/remote/execution/v2/remote_execution.proto> |
| Bazel remote caching | <https://bazel.build/remote/caching> |
| Reproducible Builds definition | <https://reproducible-builds.org/docs/definition/> |
| SLSA v1.0 requirements and levels | <https://slsa.dev/spec/v1.0/requirements>, <https://slsa.dev/spec/v1.0/levels> |
| SLSA v1.2 | <https://slsa.dev/spec/v1.2/> |
| in-toto test-result predicate | <https://github.com/in-toto/attestation/blob/main/spec/predicates/test-result.md> |
| Sigstore security model | <https://docs.sigstore.dev/about/security/> |
| RFC 6962 and RFC 9162 | <https://www.rfc-editor.org/rfc/rfc6962.html>, <https://www.rfc-editor.org/rfc/rfc9162.html> |
| Git objects | <https://git-scm.com/book/en/v2/Git-Internals-Git-Objects> |
| Landlock | <https://docs.kernel.org/userspace-api/landlock.html> |
| Yama | <https://www.kernel.org/doc/html/latest/admin-guide/LSM/Yama.html> |
| bubblewrap | <https://github.com/containers/bubblewrap> |
| gVisor | <https://gvisor.dev/docs/> |
