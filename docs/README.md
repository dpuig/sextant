# Documentation

Start with the [implementation plan](../Implementation%20Plan%20Multi-Cluster%20Control%20Plane.md): the milestones
(F0 Foundations through E6 and the frozen items), their exit gates and the risks.

| Document | What it is |
|---|---|
| [specs/vscode-extension.md](specs/vscode-extension.md) | Specification of milestone E1 (the VS Code extension, local-first); E2-E6 outlined |
| [specs/phase-0-foundations.md](specs/phase-0-foundations.md) | Specification of F0 (management plane, agent, tunnel) and the measured status of its gates |
| [adr/0001-api-server-shape.md](adr/0001-api-server-shape.md) | Why the API server is a small HTTP server over Postgres for now |
| [adr/0002-tunnel-approach.md](adr/0002-tunnel-approach.md) | Tunnel choice, measurements and the patches to the vendored library |
| [testing/e2e.md](testing/e2e.md) | What `make e2e` proves on real clusters, and what it does not |
| [publishing.md](publishing.md) | Pre-publication checklist for making the repository public |
| [third-party-licenses.md](third-party-licenses.md) | Licences of every dependency in the shipped binaries (generated: `make licenses`) |

At the repository root: [LICENSE](../LICENSE) and [NOTICE](../NOTICE) (Apache-2.0), [CONTRIBUTING](../CONTRIBUTING.md),
[SECURITY](../SECURITY.md) and [CODE_OF_CONDUCT](../CODE_OF_CONDUCT.md).

Conventions: documentation lives next to the code it describes; every non-trivial decision gets an ADR; every
specification states its boundaries (always / ask first / never) and testable success criteria; and a document that
reports a measurement also says what the measurement does not prove.
