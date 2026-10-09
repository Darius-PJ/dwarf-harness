# 83: Exchange bounded authorized projections between independent sovereign Cores

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: U9 federation
Blocked by: 78

## What to build

Exchange authorized origin-preserving facts between separately governed Cores with revocation and bounded transport.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Each operator separately provisions and approves pinned peer keys/descriptors, exact shared law/protocol and bilateral source/assurance/domain/field/rate grants.
- Owner freeze of the producer-ready C1 external-fact/projection/cursor and peer protocol shapes before consumption; matching law bytes do not equate local policies.
- Applicable source-kind/assurance release gates at each receiving boundary.

Mandatory adopted U9 capability, not optional future work. Does not require C7 launcher capability (ticket [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md))/GUI, shared authority or all C10 platforms.

## Acceptance criteria

- [ ] Two independently administered actual Core processes complete explicit mutually authenticated exchange under FEDERATION limits while retaining separate ledgers, local activation/check policies, budgets and authority; no TOFU/discovery enrollment/global coordinator/raw-ledger export/transitive relay.
- [ ] Export contains only currently authorized typed coarse facts; imported overlap intersects locally with the recipient's authorized footprint without sending that footprint back, and shared pressure crosses only for an explicitly shared resource.
- [ ] Receiver atomically commits admitted facts plus import cursor before acknowledgment. Repeated origin identity/revision is idempotent, same identity/different digest is an integrity refusal, bounded gaps resume without unbounded buffering and replay contacts no peer.
- [ ] Facts retain origin, observed-versus-received time, law/grant and assurance provenance, never become independent local evidence or select a receiving agent. TLS/signature proves origin, not truth or assurance.
- [ ] Revocation blocks new projections and unseen backlog at current delivery checks without erasing already delivered history; incompatible law or partition pauses only that crossing and yields unknown coverage rather than zero pressure/no overlap/repaired hazard.
- [ ] Fair connection/batch/deadline/reconnect limits, peer failure, storage growth, replay and per-peer resource costs pass actual multi-Core/adversarial gates without blocking local work; each operator can independently leave.

## Bounded acceptance profiles

These are the source-selected initial limits or preregistration requirements with their original status, not measured results or newly approved defaults. Keep the canonical distinction between engineering calibration and hard gates; do not relax thresholds after observing a run.

### FEDERATION

Required sovereign bilateral capability; separately provisioned pinned peer keys, exact law hash/protocol and current bilateral source/assurance/domain/field/rate grants. Bounded Core-owned signed projection stream over mutually authenticated TLS 1.3: <=128 records/256 KiB per batch, one in-flight batch/peer/view, 8 active connections with fair rotation, 5-second connect/30-second long-poll deadlines, jittered reconnect <=60 seconds. Limits provisional pending actual qualification, not permission to omit them. No global ledger/coordinator, TOFU, automatic enrollment, transitive relay or account pooling.

Source: Undercurrent design §10.6; Decision log D81/B12/B14; Parent specification implementation 50.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 79, 80, 92, 103, 113, 114.
- Implementation decisions: 1, 2, 3, 4, 30, 31, 32, 42, 47, 50.
- Testing decisions: 1, 2, 4, 12, 13, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Undercurrent roadmap U9
- Undercurrent design §§10.6,13.4,14.1–14.2,15.1
- Field philosophy §§1,9–10
- Decision log D26/D80/D81/B12/B14

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
