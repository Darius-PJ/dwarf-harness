# 150: Set up a colony from the terminal

Status: ready-for-agent
Type: task
Published: 2026-11-27
Spec: [DwarfHarness system specification](../spec.md)
Roadmap capability: Release 1 colony setup from the terminal, over C1's colony creation, C2's workspace registration and policy activation, and C3's agents, contexts, routes, credentials and spend (D182, D186)
Blocked by: None (can start immediately)

## What to build

Let the owner set up a colony on a repository from the terminal: create the colony, register the repository and its required checks, create agents and their work contexts with his approval, bind provider credentials, configure provider routes and set spend ceilings, then run a `doctor` command that checks the setup and names what is wrong. Today the `dwarf-harness` program opens only a colony that already exists, takes its agents, contexts and credential bindings from a settings file the owner writes by hand, and never initializes a colony or installs a provider, grant, spend term or capacity. Core already has an owner operation for each step: colony creation, workspace inspection and registration, ticket [09](09-control-exact-one-off-policy-activation.md)'s policy activation, agent registration, context creation, and paid-route, credential and spend configuration. The program calls only credential binding, at startup, for the bindings in the settings file; only tests reach the others. This ticket gives them all to the owner through the terminal.

## Start and authority gates

Publication and triage do not grant D52 approval, freeze a contract, advance roadmap status, or establish runtime, isolation, accessibility, capacity or usefulness evidence. Use the current roadmap gates and producer-owned specifications through the parent specification’s canonical reading map.

Respect the current status of all six contracts. Their owners may draft and build their own boundary as the roadmap permits; consumers and later producer families wait for the specifically required owner freezes. C1 completes with its F0 freeze request, not an assumed approval. No placeholder records, permissive substitutes, second authority stores or new contract boundaries are authorized.

Harness workloads remain testing-only until Release 1's exit test passes (D182). Later containment, per-kind release and study approvals remain independent gates.

- Testing-only harness workloads until Release 1's exit test passes (D182); a colony, agent or context created here runs only test work until then.
- A design brief and a ruling come before code (D117). They map each command to Core's existing owner operation; a new Core operation or record family is frozen under D125 before code that writes it reaches `main`.
- Adds no new dependency without its D29 record.
- Each code slice's branch commit passes the three-OS CI (D133) before `main` moves to it (D153).
- No C6, C7, C8, C9 or GUI dependency.

## Scope boundaries

- Agents created here are the owner's, with no parent, Corps or role. Births an agent proposes, clones, Corps, Purpose and the leadership triad stay with C8 (tickets [32](32-create-one-independently-owned-child-from-an-exactly-approved-birth-manifest.md) and [99](99-finalize-an-exactly-reviewed-scratch-or-clone-creation-batch.md)), in Release 3's teams layer (D182).
- Running the registered checks and landing work belong to tickets [17](17-run-admissible-required-go-checks-on-exact-candidates.md), [18](18-validate-python-candidates-with-reconciled-test-evidence.md) and [20](20-publish-only-the-validated-cumulative-prefix.md). This ticket registers the checks and changes nothing about how they run.
- Credentials stay where the owner keeps them: he names each credential's source, as the settings file's credential bindings do today, and Core reads it only through those bindings.
- Windows first (D138); code that is not Windows-specific stays portable.

## Acceptance criteria

- [ ] The owner creates a new colony from the terminal. Creating one where a colony already exists is refused, a crash during creation leaves either no colony or a complete one, and the `dwarf-harness` program then opens the new colony as it opens one today.
- [ ] The owner registers a repository after reviewing what Core observed of its Git setup, and names its required checks, which take effect through ticket 09's owner-approved policy activation. A later change to the check list uses the same activation: it is revision-checked and held until earlier finalization is resolved.
- [ ] The owner creates agents and their work contexts, each from an exact review he approves: the agent's name and starting instructions, and each context's title, task binding, provider route and turn limits. Nothing is created before he approves it, and the terminal program then runs them without his copying their identifiers into the settings file.
- [ ] The owner binds each provider credential by naming its source, as the settings file's credential bindings do today. The secret never enters the ledger, a context, terminal output or the `doctor` report, and a missing or unusable credential refuses paid dispatch with its cause.
- [ ] The owner configures each provider route a context uses (provider, model and verified price profile) and sets spend ceilings. Each ceiling is a finite amount or an explicit unlimited choice, with its period and an optional warning. Without a ceiling, paid dispatch is refused with that cause; nothing defaults to unlimited.
- [ ] Every setup command is a revision-checked Core command with a durable receipt: a repeated command returns its original receipt, a stale one is refused, and a crash leaves each step either done or not done.
- [ ] `doctor` checks the colony, each registered repository and its checks, each agent, context, route, credential binding and spend ceiling, and each prerequisite the terminal program checks at startup. It names each problem with the change that fixes it, changes no colony state, and makes no model call or paid request.
- [ ] On a test repository, the setup for Release 1's exit test runs from the terminal alone: a new colony with the repository and its required checks, two agents with their contexts, a bound credential, routes and spend ceilings. `doctor` reports the setup sound, and the terminal program opens it.
- [ ] Every failure this ticket introduces reaches the terminal with a cause the owner can act on (D182).

## Verification boundary

Exercise the actual consumer-visible boundary and effects named above. Preserve deterministic regressions for plausible permission, amount, ordering, identity, recovery and evidence failures. Mocks, source-text assertions, historical design experiments and passing builds do not establish runtime, native-platform, isolation or empirical acceptance.

## Specification coverage

- User stories: 3, 19, 20, 23, 27, 33.
- Implementation decisions: 1, 2, 3, 4, 6, 7, 9, 10, 11, 12.
- Testing decisions: 1, 2, 4, 10, 20.

Canonical sections (resolved through the parent specification’s reading map):
- Core roadmap C1, C2 and C3
- Core design §4.2
- Decision log D52/D138/D182

## Comments

2026-11-27 — Published by the owner's decision. He adopted release tiers whose Release 1 includes a ticket for colony setup from the terminal (D182), and he approved its publication (D186). Release tier: Release 1. This grants no implementation, contract-freeze or release approval; the start and authority gates above stand.
