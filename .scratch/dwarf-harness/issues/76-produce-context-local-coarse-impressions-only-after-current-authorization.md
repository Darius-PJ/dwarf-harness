# 76: Produce context-local coarse impressions only after current authorization

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U6 projection capability
Blocked by: 72, 75

## What to build

Authorize before aggregation/ranking and keep coarse impressions and salience context-local.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Frozen bound-context/grant and producer projection inputs; D80 policy, not rank-based disclosure.

No prompts yet. Later renderer consumes only this authorized projected index.

## Acceptance criteria

- [ ] The bound context's host-derived footprint and current origin/source/audience/scope grants filter every candidate before coarsening, aggregation, counting, ranking, ambient/wakeup/mood eligibility or index insertion.
- [ ] Allowed shared-resource output contains only granted coarse band/freshness; private overlap contains only possible_overlap true/false/unknown and an already-authorized coarse region, never actor/task/path/hash/content/amount/account/source handles.
- [ ] Denied or revoked candidates cannot change visible membership, order, counts, timing or metadata; current-grant rechecks withdraw queued delivery and fail closed on incomplete policy/coverage.
- [ ] PROJECTION-RENDER smoothing, jitter, hysteresis and host-confirmed experience remain context-local and presentation-only; unknown is not averaged into healthy zero and adaptation never changes source amplitude or permission.
- [ ] Adversarial cross-context/reassignment/revocation tests and capacity measurements explicitly state D80's accepted existence/timing leakage without claiming anonymity or a new authority channel.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### PROJECTION-RENDER

At most six complete rows: four situated, one strength-biased ambient, one reserved exploration. Fixed ASCII framing and ordered ref/kind/region/signal/freshness/support fields, enums and safe-label grammar; no free text. Estimate min(384,TokenAllowance) using ceil(UTF-8 bytes/4); 4,096-byte hard whole-block cap. Exact §10.3 SHA-256 preimages and checked uint64 weights, at most 64 rejection evaluations, no biased fallback/counter wrap. Exploration at most one cyclic authorized-catalogue scan; skip non-fitting rows unseen, cursor advances only on whole emission; no-fit/exhaustion returns zero-block/zero-estimate no-op. Local EWMA candidate 5 minutes; entry 500,000 +/-50,000 ppm jitter clamped 250,000–750,000; exit gap 100,000; host-evidenced experience update 0.05(1-e), 7-day half-life and at most 100,000e entry adjustment. Shadow retained 7 days or 64 MiB/context, whichever first, Core-private owner-only audited access.

Source: Undercurrent design §§10.3–10.5, 11.1, 14.4; Parent specification implementation 42–46.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 80, 89, 103, 106.
- Implementation decisions: 1, 2, 3, 4, 30, 42, 45.
- Testing decisions: 1, 2, 6, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U6
- Undercurrent design §§10.1–10.5,14.1–14.2
- Field philosophy §9
- Decision log D80/B13

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
