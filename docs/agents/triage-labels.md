# Triage labels

| Canonical role | Local Status value | Meaning |
|---|---|---|
| `needs-triage` | `needs-triage` | Maintainer evaluation needed |
| `needs-info` | `needs-info` | Waiting for more information |
| `ready-for-agent` | `ready-for-agent` | Fully specified for an agent, within the declared approval and dependency gates |
| `ready-for-human` | `ready-for-human` | Human implementation required |
| `wontfix` | `wontfix` | Will not be actioned |

When a skill names a triage role, use its mapped value in the local tracker’s `Status:` line. A readiness label never overrides owner approvals, contract freezes or roadmap dependencies.
