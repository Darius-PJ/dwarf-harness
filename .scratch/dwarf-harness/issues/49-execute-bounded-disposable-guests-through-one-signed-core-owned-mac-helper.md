# 49: Execute bounded disposable guests through one signed Core-owned Mac helper

Status: ready-for-agent
Type: task
Published: 2026-10-06
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: C10 shared macOS guest executor; exports Mac guest executor (ticket [49](49-execute-bounded-disposable-guests-through-one-signed-core-owned-mac-helper.md))
Blocked by: 07, 09

## What to build

Start bounded disposable guests through a signed Core-owned helper and prove stop before reuse.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until every C4 end-to-end, adversarial and declared-workload gate passes. Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen B11 helper/guest receipt interface; narrow signed Swift/Objective-C Virtualization.framework helper, not a seventh contract or third launcher.
- Actual applicable Apple-silicon/Intel Macs, supported host/guest architectures/APIs, guest images, SDK/signing identity and verified applicable Apple licensing.
- Real native-macOS A2 and Linux-guest A2 remain separate later gates.

## Acceptance criteria

- [ ] Core launches the narrow signed executor through its private inherited channel and obtains a real bounded guest result tied to run/attempt/generation, boot nonce and immutable inputs; helper/guest owns no ledger, provider, wallet or task authority and children never inherit the Core channel.
- [ ] Copied exchange accepts only bounded authorized regular files and run-specific channels. No host home/workspace/common Git/keychain/provider credentials, writable cross-run cache, clipboard/device or arbitrary host path is exposed; disabled-network profiles omit NICs.
- [ ] Every protection domain starts from an immutable clean base plus fresh private disk/auxiliary state. A dirty guest never becomes the next baseline, and outputs remain host-private rather than an agent-visible transport.
- [ ] Exactly one demand-started native macOS slot exists on Apple Silicon and queues excess native work; Intel explicitly rejects native-macOS A2 work. Linux-declared guests have distinct admission/budget state rather than consuming a fictional native equivalent.
- [ ] Core/helper/channel/guest loss fences the boot nonce, interrupts the attempt and positively stops the VM; delayed receipts cannot publish and no slot/private state is reused before positive stop completion. Never auto-reexecute.
- [ ] Run real positive round-trip/stop drills on the applicable host paths and record startup/stop/CPU/RAM/disk costs and fidelity. The helper stays independently packaged from the GUI and cgo-free Core; this executor proof alone is not A2 qualification.

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 62, 63, 64.
- Implementation decisions: 1, 2, 3, 4, 21.
- Testing decisions: 1, 2, 16, 18, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C10
- Core design §13.9
- Decision log D87/B11

## Comments

2026-10-06 — The owner approved publishing the 86 tickets in the breakdown review. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.
