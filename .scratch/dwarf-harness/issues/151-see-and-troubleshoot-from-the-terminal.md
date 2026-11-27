# 151: See and troubleshoot from the terminal

Status: ready-for-agent
Type: task
Published: 2026-11-27
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: Release 1 terminal inspection and troubleshooting over C3's and C4's existing records (D182, D186)
Blocked by: 20

## What to build

Let the owner see what the colony did, what it cost and why something failed, from the terminal alone. He gets a timeline for each agent of its turns, tool calls, files changed and landing results, with the failing check's output; spend by agent, provider and day, with open holds; task states; outages; and the recorded cause behind any refusal. He can export a slice of the ledger for a bug report, and an owner-only diagnostic mode shows the local paths and values that ordinary output withholds. Today the terminal prints a refusal's fixed error class, never its path, value or locator, and only service status and outages have commands of their own. Everything here reads existing records; nothing changes colony state.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Reads only records Core already writes: the ledger; the turn and tool records of ticket [15](15-run-bounded-context-bound-model-and-tool-turns.md); check evidence and landing results from tickets [17](17-run-admissible-required-go-checks-on-exact-candidates.md) and [20](20-publish-only-the-validated-cumulative-prefix.md); ticket [89](89-report-waiting-ready-waiting-blocked-and-active-task-facts.md)'s task queries; ticket [91](91-record-service-loss-and-restoration-for-every-agent.md)'s service and outage records; and Core's paid reservations and settlements. A record Core does not write yet comes from its producing ticket, never from a second store.
- Credentials never appear, in any mode or export.
- A design brief and a ruling come before code (D117).
- Adds no new dependency without its D29 record.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153).
- No C6, C7, C8, C9 or GUI dependency.

## Scope boundaries

- Read-only: no view, export or diagnostic changes colony state, starts a turn, or makes a model call or paid request.
- Terminal basics only. The administrator view of every record (ticket [93](93-inspect-every-colony-record-as-administrator-without-widening-agent-evidence.md)) and the desktop app's views (ticket [55](55-inspect-authorized-operations-and-mutate-a-task-through-the-real-core.md)) stay in Release 2. The background host's reattaching views (tickets [30](30-start-or-attach-to-one-authenticated-detached-colony-host.md) and [31](31-catch-up-two-terminal-views-while-keeping-live-text-ephemeral.md)) may show the same records later.
- Ticket [152](152-run-the-declared-checks-during-a-turn.md)'s mid-turn check runs belong in the timeline; whichever of the two tickets lands second makes them appear.
- No persistent index or snapshot is added to read faster; faster history reading is ticket [149](149-keep-a-ledger-readers-checked-position-across-reads.md)'s, On trigger (D182).

## Acceptance criteria

- [ ] For a chosen agent and time range, the terminal shows a timeline of its turns, tool calls, files changed and landing results in ledger order, each line naming its context and turn. A refused landing shows the failing check and its output, bounded, with the rest reachable page by page.
- [ ] Spend is shown by agent, provider and day from Core's reservations and settlements, with open holds listed separately, including unknown paid results held in full; totals agree with the settled records.
- [ ] Task states come from ticket 89's queries (waiting-ready, waiting-blocked with its reasons, and active), shown beside ticket 91's `/service` and `/outages`, which keep their behavior.
- [ ] For any refusal the owner can see (at startup, or of a turn, tool call, paid request, landing or setup command), the terminal shows the recorded cause behind its error class: for example the failed check, the exhausted budget scope, the missing grant or credential, or the outage.
- [ ] The owner exports a ledger slice (an agent and time range, or one turn, task or landing) to a file for a bug report. It holds the records and whichever referenced bodies he chooses to include, never a credential, and in ordinary mode it carries no local path, value or locator.
- [ ] An owner-only diagnostic mode, off by default, shows the paths, values and locators that ordinary output withholds. Only the owner's own terminal session can turn it on, no agent or other view receives its output, and it never shows a credential.
- [ ] Every view and export reads in bounded pages and leaves the ledger and every projection unchanged.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 3, 26, 31, 32, 34, 35, 69, 70, 165, 166.
- Implementation decisions: 1, 2, 3, 4, 6, 10, 11, 12, 13, 24, 64.
- Testing decisions: 1, 2, 4, 9, 11, 20, 23.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C3 and C4
- Core design §§4.2,13.11
- Decision log D33/D182

## Comments

2026-11-27 — Published by the owner's decision. He adopted release tiers whose Release 1 includes a ticket for inspection and troubleshooting from the terminal (D182), and he approved its publication (D186). Release tier: Release 1. This grants no implementation, contract-freeze or release approval; the start and authority gates above stand.
