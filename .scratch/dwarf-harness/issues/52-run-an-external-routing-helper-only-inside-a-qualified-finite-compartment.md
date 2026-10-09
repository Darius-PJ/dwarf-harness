# 52: Run an external routing helper only inside a qualified finite compartment

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: R5 external-module capability
Blocked by: 44, 46

## What to build

Run only authorized immutable input in a qualified finite compartment; unqualified helpers stay disabled.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- External execution additionally requires the applicable actually qualified C10 profile and an enforced complete finite wall/CPU/memory/process/input/output profile. Platform qualification alone does not authorize or enable a helper.
- D29 approval for each plugin/helper dependency and supported implementation
- Optional stages remain off until their own replay success/net-value/privacy/resource gates pass. Record required D29 dependency approvals. A failed value gate leaves the stage disabled, not represented as a demonstrated saving. Specific family/scope: for each enabled module
- Until whole C4 real-work gate (tickets [22](22-qualify-the-real-promotion-path-against-the-finite-v6-suite.md), [23](23-measure-every-change-delivery-under-steady-and-burst-load.md))/C4 is Done, every harness-agent workload, replay corpus and authorized live-provider drill is testing-only. No added C4 build dependency for R0/R1; no production-savings or real-delivery claim.

Windows-first actual demonstration; every other enabled profile separately needs the same actual finite-helper proof and remains disabled without it. No all-platform completion prerequisite.

## Conditional blockers

Only applicable conditions add blockers. An unrelated platform is not a prerequisite; an applicable configuration cannot be enabled or claimed qualified until its named ticket and external gates are satisfied.
- **Enabling a Linux helper profile.:** 48 (Qualify the native Linux runner profile for A2).
- **Enabling native macOS work on supported Apple Silicon.:** 50 (Qualify the one-slot Apple-Silicon native-macOS runner profile).
- **Enabling the separately qualified Linux-guest path on Intel/Apple-Silicon Macs; supplied by SocialGuiTickets, not interchangeable with native macOS.:** 51 (Qualify separately budgeted Linux guests on Intel and Apple-Silicon Macs).

## Scope boundaries

- Carry the bounded JSON-RPC external-module protocol only inside Core's existing C10 compartment; no second sandbox, daemon, generic host-query RPC or native Go plugin mechanism.
- Reuse the applicable qualified profile; per-platform enablement is conditional, not an all-platform prerequisite for first-party R5 stages.
- The complete declared support matrix must report each profile's actual gate; unqualified targets remain explicitly disabled, not silently claimed supported.

## Acceptance criteria

- [ ] A missing/unqualified/unenforceable compartment or partial resource declaration prevents enablement and execution. Each invocation has finite positive enforced wall time, CPU, memory, process count, input bytes and output bytes, directly or through one complete named profile; typical cost/latency is not a limit.
- [ ] Core materializes only the invocation's current-authorized immutable context-bound snapshot and one bounded result channel. Actual access attempts cannot reach Core private roots, credentials, general IPC or provider egress; the child receives no AuthorizedReader, provider adapter or mutable host transcript.
- [ ] An actual qualified helper invocation can return a useful valid result through the bounded protocol. Reject wrong binding/schema, malformed or oversize output and unauthorized provenance; discard late output rather than applying it to a changed turn/context.
- [ ] Timeout/cancellation/abnormal exit demonstrates positive stop of every descendant before compartment/resource reuse. A sent kill or helper self-report is insufficient; ambiguous cleanup keeps resources unavailable and output unusable.
- [ ] Remote/paid classification still executes exclusively through the Core adapter and separate CostBound admission, never through child egress. First-party in-process Go stages remain honestly described as trusted Core code, not isolated plugins.
- [ ] Exercise the real helper path on each enabled supported profile and retain its filesystem/IPC/egress/resource/stop evidence plus module value result. No Intel native-macOS A2 claim, platform-label substitute or unqualified-profile fallback is accepted.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 31, 66, 78.
- Implementation decisions: 1, 2, 3, 4, 11, 21, 29.
- Testing decisions: 1, 2, 11, 16, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Router roadmap R5
- Routing design §§6.5,12–14
- Core design §13.9
- Decision log B11,D87

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
