# 86: Run and report the concurrent controlled usefulness trial without overstating results

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U10 completion
Blocked by: 85

## What to build

Run the concurrent cluster-randomized trial and report all endpoints, separate cost gates and honest inconclusive outcomes.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Exact treatment remains released; owner-approved fixed numeric sample/budget/horizon and demonstrated capacity remain valid before randomization.

Only this actual prospective trial can support U10 usefulness claims; neither an implementation test nor synthetic simulation can close it.

## Acceptance criteria

- [ ] Randomize each whole component once within frozen strata to concurrent field-visible versus field-withheld arms, prohibit crossover, hold/block tools/checks/provider/model/access/allocation as preregistered and retain all assignments intention-to-treat.
- [ ] Cross-arm component bridges or >5% exposure, missing assigned outcomes/denominators, unreached sample, budget/capacity stop or permission incident trigger the exact STUDY-TRIAL stop/inconclusive rules without retrospective cluster repair or sample reduction.
- [ ] Report all four registered task-weighted endpoints, denominators and Bonferroni bounds; a benefit finding requires at least one lower bound above its margin, never a replay/simulation or favored-endpoint substitute.
- [ ] Independently pass both provider-USD and validation-CPU/task upper cost bounds with full evidence; do not combine currencies with CPU or claim benefit from degenerate/missing cost intervals.
- [ ] Report attention, tokens/calls, wakeups, context fairness, privacy/newly exposed information, capacity and the two deduplicated descriptive promotion secondaries separately.
- [ ] Publish only the prespecified benefit/no-minimum-worthwhile-effect/inconclusive classification with all evidence and limits; historical synthetic arithmetic is never a baseline, approved sample or empirical efficacy result.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### STUDY-TRIAL

Indivisible interference components share agents/contexts/workspaces/resources/permitted collaboration; cluster-randomize once within frozen strata, no crossover, intention-to-treat. A component bridging arms or >5% eligible-task cross-arm exposure stops efficacy interpretation. Missing assigned outcomes/denominators, unreached fixed sample, capacity/budget stop or exhausted planning limit => inconclusive. At least one .0125 one-sided Bonferroni lower bound above its margin required for benefit; report all four. Independently for provider USD and validation CPU seconds/task, 95% upper Welch/delta bound on mu_T-1.10*mu_C must be <0, with variance v_T+1.10^2*v_C; missing/degenerate cost evidence blocks benefit. Any confirmed permission incident stops. No passing endpoint plus all four valid upper bounds below margins => no minimum worthwhile effect detected; otherwise inconclusive. Preserve descriptive request-ID-deduplicated refused/busy and durable conflict-blocked secondaries without replacing benefit gates.

Source: Undercurrent design §14.5; Parent specification implementation 51; testing 19.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 93, 115, 116.
- Implementation decisions: 1, 2, 3, 4, 51.
- Testing decisions: 1, 2, 18, 19, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U10
- Undercurrent design §14.5

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
