# 04: Load and identify a strict law independently of field execution

Status: claimed
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U1.L
Blocked by: 03

## What to build

Strict law parsing, exact-byte identity and validation parameters, independent of the field engine.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen ledger-format/ledger-reader prerequisites.
- Owner law-format freeze before any C4/U1.E consumer starts; loading does not activate policy.

Exposes U1.L law capability (ticket [04](04-load-and-identify-a-strict-law-independently-of-field-execution.md)) only after its actual contract gate and owner freeze; no U1.E engine capability (ticket [60](60-propagate-sources-with-bounded-replay-safe-best-arrivals.md)) or Core C2/C4 runtime dependency.

## Acceptance criteria

- [ ] A complete valid LAW-PARSER artifact loads into the typed six-kind/rule registry; every specified malformed, duplicate, unsupported, unsorted and boundary-exceeding artifact is rejected atomically under both finite profiles.
- [ ] Golden vectors distinguish byte-identical artifacts from whitespace/key-order/line-ending changes using exact immutable Git-blob SHA-256, with no semantic canonicalizer.
- [ ] The same contract supplies typed versioned D47 parameters and local-manifest bindings to a C4-facing consumer without importing or starting the field engine.
- [ ] Shared law exports exclude local workspace/check/environment policy, grants, credentials and activation time; parsing cannot authorize or execute activation.
- [ ] Additive/version compatibility, old evaluator semantics and unsupported versions are observable through the declared contract and golden samples; the contract record clearly separates implementation from owner freeze.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### LAW-PARSER

Strict typed UTF-8 JSON v1; exactly six registered kinds; no defaults, duplicate keys, unknown fields/enums/versions, decimal/exponent numeric tokens, invalid rule/mode/unit/coupling, unsorted/duplicate arrays or unsupported kernel. compact-v1: 65,536 bytes/depth 12/64 members/128 array elements/128 string bytes/2,048 tokens. standard-v1: 262,144 bytes/depth 16/128 members/256 elements/256 string bytes/8,192 tokens; host may tighten, not silently widen. Preserve every §13.4 numeric/rule bound. Identity is SHA-256 of exact immutable Git-blob bytes, not JSON canonicalization. Local D47 rational validation parameters and activation/check/environment identities remain in the existing local manifest chain, outside shared law bytes.

Source: Undercurrent design §13.4; Parent specification implementation 31; testing 5.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 80, 81.
- Implementation decisions: 1, 2, 3, 4, 30, 31.
- Testing decisions: 1, 2, 4, 5, 17, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U1
- Undercurrent design §13.4
- Contract register law-format
- Decision log D40/D47/B2/B12

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-10-12 — Claimed for the staged build by the project manager, on the owner's instruction of 2026-10-12 to take the next tickets once this was adjusted, each pausing for his approval before work moved on. A runner maps the requirements, a designer drafts the brief and the advisor rules on it. No code starts before the owner approves the design.
