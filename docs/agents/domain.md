# Domain documentation

## Layout and authority

This is a single-context project. Use the root `GLOSSARY.md` if it exists; otherwise use the vocabulary in the current design documents and proceed without creating a placeholder glossary.

Architectural decisions live in [the existing decision log](../DECISIONS.md). Keep that as the single decision store rather than starting a parallel `docs/adr/` convention. Follow the originator rules in the canonical project instructions.

## Before exploration or design

- Read the glossary if present, then the relevant decisions and their amendments.
- Use [the roadmap index](../ROADMAPS.md), [contract statuses](../CONTRACTS.md), and the relevant roadmap/design sections for current scope, dependencies and readiness.
- Preserve canonical domain terms in specifications, issues, hypotheses and tests.
- Surface conflicts with existing decisions explicitly; do not silently override them.
- Create glossary entries only when terminology is actually resolved. Publication of a spec is not an owner approval or a contract freeze.
