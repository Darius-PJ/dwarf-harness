# 93: Inspect every colony record as administrator without widening agent evidence

Status: ready-for-agent
Type: task
Published: 2026-10-08
Spec: [DwarfHarness system specification](../spec.md); feature: [fortress GUI specification](../../fortress-gui/spec.md)
Roadmap capability: C6 administrator inspection and scoped memories with C7 projection (D90–D91)
Blocked by: 26, 31

## What to build

Let an authenticated colony administrator read every colony record, including agent-private memories and both relationship perspectives, through projections that never become agent evidence or expose provider credentials.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Owner-frozen C6 memory and relationship families and the C7 administrator and agent projection additions.
- The administrator principal is authenticated through C7; there is no impersonation path.

## Acceptance criteria

- [ ] General, task and each direction's relationship memory, exchanged history and, once ticket [27](27-use-the-complete-role-graph-and-communal-boards-without-context-leakage.md) provides boards, board memory are separately addressable records with owner agent/context, counterpart or task, version, provenance and freshness; general memory is never a union of private memories.
- [ ] The administrator projection returns all of them, including both A-about-B and B-about-A, as the administrator's own action; it never impersonates the agent, changes recipients, or enters any agent's context, prompt, memory or classifier input.
- [ ] Other principals see only what their actual grants allow; D83 limits a retired agent to its already-delivered read-only history and private memories with holes preserved, with no unseen backlog or successor content.
- [ ] Provider credentials never appear in any projection, export or error, for any principal.
- [ ] Search, counts, snippets, existence checks, exports and notification text are filtered for the requesting principal; switching or revoking a principal mid-read leaves no forbidden content, count or existence disclosure.
- [ ] Permitted revision history is readable as inert text; reading never starts a turn, injects memory into a prompt or copies it elsewhere, and edits use their existing authorized Core commands.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 141, 164.
- Implementation decisions: 1, 2, 3, 4, 15, 16, 17, 54, 59.
- Testing decisions: 1, 2, 7, 8, 20, 21.
- Fortress acceptance: FG06, FG08.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C6 "D89–D95 social handoff" and "Reassignment, retirement and administrator"
- Core design §§7.3, 13.6
- Fortress GUI spec §4 memory browser and §10 privacy gates

## Comments

2026-10-08 — Published from the [fortress GUI specification](../../fortress-gui/spec.md) after the owner approved publishing tickets 87–129 in the consolidated breakdown review. These tickets join 01–86 in this tracker so one team works a single frontier. This adopts the decomposition for local publication only; it does not grant any separate implementation, contract-freeze, release or study approval.

2026-11-27 — Release tier rework (D182, D186): Release 2, the full administrator view; terminal basics arrive earlier with ticket [151](151-see-and-troubleshoot-from-the-terminal.md). Criterion 1's board memory now appears once ticket [27](27-use-the-complete-role-graph-and-communal-boards-without-context-leakage.md) provides boards. The transparency criterion is added. Its blockers were 27 and 31; they are now 26 and 31. Its memory and relationship records come from ticket [26](26-exchange-private-relationship-messages-and-recover-a-two-agent-crash.md)'s private messages and relationships, and it does not need boards to start. This grants no implementation, contract-freeze or release approval.
