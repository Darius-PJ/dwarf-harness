# 77: Render bounded exploration-aware impressions into private shadow diagnostics

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U6 completion; reusable U9 ambient mechanism
Blocked by: 76

## What to build

Render bounded sanitized exploration-aware blocks into owner-only shadow diagnostics.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen R1 context-module; actual tokenizer/provider cost adapter and authorized shadow workload.

Completes U6 shadow capability (ticket [77](77-render-bounded-exploration-aware-impressions-into-private-shadow-diagnostics.md)), not visibility. Implements ambient/exploration once; U9 later qualifies their live-discovery behavior rather than rewriting a second selector.

## Acceptance criteria

- [ ] The real context-module returns zero or one complete sanitized PROJECTION-RENDER block from the current bound projected index, respecting six-row quotas, exact grammar/order/enums/labels, 384 estimate and 4,096-byte cap without truncation or free text.
- [ ] Situated sorting and ambient SHA selection use exact deterministic keys/preimages, checked uint64 arithmetic and 64-rejection cap; overflow/rejection exhaustion/counter exhaustion returns unknown/no row without biased fallback.
- [ ] Reserved exploration makes at most one authorized-catalogue scan, skips oversized rows without marking seen, emits a later fitting whole row and advances only on emission; no fit or budget exhaustion returns valid empty output with cursor unchanged.
- [ ] Revocation between snapshot/render/dispatch forces fresh authorized projection or whole-block omission; hidden rows do not influence omission counts, continuation, cursor, timing or prompt metadata.
- [ ] Actual real-activity shadow measures tokenizer volume/provider cost and privacy/fairness with renderable-pair denominators and separate unrenderable count/duration. Nothing reaches an agent/provider prompt.
- [ ] Core-private owner-only audited diagnostics use the selected seven-day/64-MiB retention and authorized fields only; failure marks evidence incomplete without creating a source or blocking work.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### PROJECTION-RENDER

At most six complete rows: four situated, one strength-biased ambient, one reserved exploration. Fixed ASCII framing and ordered ref/kind/region/signal/freshness/support fields, enums and safe-label grammar; no free text. Estimate min(384,TokenAllowance) using ceil(UTF-8 bytes/4); 4,096-byte hard whole-block cap. Exact §10.3 SHA-256 preimages and checked uint64 weights, at most 64 rejection evaluations, no biased fallback/counter wrap. Exploration at most one cyclic authorized-catalogue scan; skip non-fitting rows unseen, cursor advances only on whole emission; no-fit/exhaustion returns zero-block/zero-estimate no-op. Local EWMA candidate 5 minutes; entry 500,000 +/-50,000 ppm jitter clamped 250,000–750,000; exit gap 100,000; host-evidenced experience update 0.05(1-e), 7-day half-life and at most 100,000e entry adjustment. Shadow retained 7 days or 64 MiB/context, whichever first, Core-private owner-only audited access.

Source: Undercurrent design §§10.3–10.5, 11.1, 14.4; Parent specification implementation 42–46.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 89, 90, 104, 105, 107.
- Implementation decisions: 1, 2, 3, 4, 42, 43, 44, 46.
- Testing decisions: 1, 2, 6, 12, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U6/U9
- Undercurrent design §§10.3,11.1,14.3–14.4
- Contract register context-module
- Decision log B13

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
