# Domain documentation

## Layout and authority

This is a single-context project. Use the root `GLOSSARY.md` if it exists; otherwise use the vocabulary in the current design documents and proceed without creating a placeholder glossary.

Architectural decisions live in [the existing decision log](../DECISIONS.md). Keep that as the single decision store rather than starting a parallel `docs/adr/` convention. Follow the originator rules in the canonical project instructions. [The current rules](../CURRENT-RULES.md) list each rule in force, one line each with its entries; they index the log rather than form a second store, and change in the same commit as each new entry (D161).

## Before exploration or design

- Read the glossary if present, then [the current rules](../CURRENT-RULES.md), and only the decision entries they cite that bear on the work (D161).
- Use [the roadmap index](../ROADMAPS.md), [contract statuses](../CONTRACTS.md), and the relevant roadmap/design sections for current scope, dependencies and readiness.
- Preserve canonical domain terms in specifications, issues, hypotheses and tests.
- Surface conflicts with existing decisions explicitly; do not silently override them.
- Create glossary entries only when terminology is actually resolved. Publication of a spec is not an owner approval or a contract freeze.
