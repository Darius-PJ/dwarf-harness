# 37: Observe bound turns without changing the C3 request

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R0 complete
Blocked by: 16

## What to build

Observe each bound turn without changing its static request or leaking another context’s cache history.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Explicit configured provider/model and Core-controlled credential; current source-verified finite endpoint/tariff profile; current model-call authority; owner-selected finite ceiling(s) or explicit unlimited. Missing credentials/authorization means no paid dispatch. Live-provider evidence requires the owner's credentials and authorization. Specific family/scope: for live calibration
- Consent to use replay conversations and labelled reruns
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

## Scope boundaries

- Run beginTurn/compile/observe in shadow with the default command, continuation, deterministic-rule and heuristic cascade; finish the versioned capability file with sources and verification dates.
- Maintain bounded per-context route/cache state and a separate D51 tools-plus-system prefix record. Diagnostic JSONL and counterfactual shadow cache state are not the authoritative money ledger.
- Include R0's actual cache calibration and labelled-subset evidence. Role-specific relationship/board drills remain the C6 C6 role-route capability (ticket [27](27-use-the-complete-role-graph-and-communal-boards-without-context-leakage.md)) integration, not an R0/R1 prerequisite.

## Acceptance criteria

- [ ] For each bound call, shadow routing records a proposed decision but the provider receives C3's static request unchanged; disabling shadow observation cannot disable paid holds, intent or settlement. R0 emits no R11.
- [ ] General/task switches and between-agent/concurrent turns preserve their own Needs, Plan, overrides, continuations, artifacts and cache history. Follow-up inheritance and fallback never use the last-focused foreign context; a binding mismatch blocks rather than falling back.
- [ ] Default classification adds no model call/download: only authenticated explicit metadata can invoke a command; new-turn rules merge needs upward, reject malformed configuration and treat missing keys as false. Tool continuations bypass classification; rules still inspect new short-follow-up content.
- [ ] Shared-prefix observations can price identical tools/system bytes only. Private-history timestamps/hits never refresh through another context. Enforce at most 16 configured cache entries per context, four priced segments, bounded pending state and fixed-size statistics; eviction means cold and dormant state is lazy-loaded.
- [ ] Consented replay preserves protected bindings and measures classifier agreement against labelled cheapest-successful-rung outcomes. Authorized live tests calibrate both per-context and shared-prefix prediction against observed usage, retaining unknown segment attribution where providers supply no evidence.
- [ ] Record task success, net cost, escalation, under/over-provisioning, cache calibration and p50/p95 routing overhead on stated hardware. Prompt text remains protected debug-only data; logs normally retain hashes/signals. No prewarm, staggered starts, production-savings claim or C6-dependent completion shortcut.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 65, 66.
- Implementation decisions: 1, 2, 3, 4, 10, 22, 23, 27.
- Testing decisions: 1, 2, 11, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R0
- Routing design §§5.1–5.3,6.1–6.4,7.1–7.5,7.7,9.1–9.4,13–14
- Decision log D21–D25,D33,D51

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
