# Issue tracker: Local Markdown

Issues and specs for this repo live as Markdown files in `.scratch/`.

## Conventions

- One feature per directory: `.scratch/<feature-slug>/`.
- The spec is `.scratch/<feature-slug>/spec.md`.
- Implementation issues are separate files at `.scratch/<feature-slug>/issues/<NN>-<slug>.md`, numbered from `01`; never combine tickets in one file.
- Record triage state in a `Status:` line near the top of each issue or spec, using [the triage labels](triage-labels.md).
- Append comments and conversation history under a `## Comments` heading.

## Publication and lookup

When a skill says to publish, create the appropriate file under `.scratch/<feature-slug>/`; preserve any existing content. When fetching a ticket, read its referenced path or resolve its number within the named feature.

Publication and triage state do not grant owner approval, freeze contracts or advance roadmap status. Execution must respect the current [roadmap gates](../ROADMAPS.md) and [contract status](../CONTRACTS.md).

## Wayfinding operations

- Map: `.scratch/<effort>/map.md`, containing Notes, Decisions-so-far and Fog.
- Child ticket: `.scratch/<effort>/issues/<NN>-<slug>.md`, with the question in its body. `Type:` is `research`, `prototype`, `grilling` or `task`; `Status:` records `claimed` or `resolved` while working.
- Dependencies: `Blocked by: NN, NN`; a ticket is unblocked only when every listed ticket is resolved.
- Frontier: select the lowest-numbered open, unblocked, unclaimed ticket.
- Claim: save `Status: claimed` before work.
- Resolve: append an `## Answer`, set `Status: resolved`, and append a gist plus child link to the map’s Decisions-so-far.
