<div align="center">

# Sextant

**One control plane for every Kubernetes cluster you run: brokered, short-lived, audited access first; AI and lifecycle on top.**

[![CI](https://github.com/dpuig/sextant/actions/workflows/ci.yml/badge.svg)](https://github.com/dpuig/sextant/actions/workflows/ci.yml)
![Status](https://img.shields.io/badge/status-Phase%200%20(foundations)-orange)
![Go](https://img.shields.io/badge/go-1.26-00ADD8)
![License](https://img.shields.io/badge/license-proprietary-lightgrey)

</div>

Platform teams running tens to hundreds of clusters across EKS, AKS, GKE and
on-prem hand out kubeconfigs over chat, cannot say who has admin on prod right
now, and burn hours on access tickets and offboarding. Sextant puts one
outbound-only agent in each cluster and one management plane in front of them,
so engineers sign in once and reach any cluster they are entitled to without a
secret on their laptop.

> **Status: Phase 0 (foundations), in progress.** The management plane, agent
> tunnel, enrollment, Helm charts and an end-to-end test on real clusters work.
> Identity (SSO), the inventory graph, the `kx` CLI and everything AI-related are
> later phases. See [Roadmap](#roadmap) and [What is not built yet](#what-is-not-built-yet).

## How it works

```mermaid
flowchart LR
  subgraph mp["Management plane"]
    API["API + proxy<br/>/apis/sextant.andean.io/v1alpha1"]
    ENR["Enrollment<br/>/v1/enroll, /v1/renew"]
    TUN["Tunnel endpoint<br/>/connect (mTLS)"]
    PG[("PostgreSQL<br/>row-level security")]
    API --- PG
    ENR --- PG
  end
  subgraph wl["Customer cluster (no inbound ports)"]
    AG["sextant-agent"]
    KAPI["kube-apiserver"]
    AG -->|"service account"| KAPI
  end
  U["kubectl / API client"] -->|"HTTPS"| API
  API -->|"dials through the tunnel"| TUN
  AG ==>|"outbound mTLS WebSocket"| TUN
  AG -.->|"one-time token, then short-lived cert"| ENR
```

1. An operator creates a `Cluster` and mints a **single-use registration token**.
2. The agent redeems it for a **24-hour client certificate** issued from a
   per-tenant, name-constrained intermediate CA, stores it in a Secret, and
   renews it at half-life with a fresh key.
3. The agent dials **out** over mutual TLS. The management plane never connects in.
4. Requests to `.../clusters/{name}/proxy/...` travel back down that tunnel; the
   agent forwards them to the local kube-apiserver **as its own service account**,
   discarding any caller-supplied `Authorization` or `Impersonate-*` headers.

### Design principles

| Principle | In the code today |
|---|---|
| Outbound-only connectivity | The agent opens no ports and creates no Service; it may dial exactly one virtual address |
| Tenant isolation at every layer | Tenant in every API path and storage row; Postgres RLS (`FORCE`) beneath application checks; tunnel session keys embed the tenant |
| No long-lived secrets on endpoints | 24 h certificates, rotated automatically; registration tokens are single-use and stored only as a hash |
| Deny by default | The API authorizer defaults to deny-all; plaintext HTTP and dev auth are refused off loopback |
| Declarative, versioned API | Kubernetes-style resources: `Organization`, `Workspace`, `Environment`, `Cluster`, `AccessGrant` |
| Integrate before you build | Tunnel is a vendored, patched [Rancher remotedialer](third_party/remotedialer/UPSTREAM.md) |

## Quick start

Requires Go 1.26+. Docker or Podman is needed for the integration tests and images.

```bash
make build                 # binaries in ./bin
make test                  # unit tests with the race detector
make lint                  # golangci-lint
make test-integration      # starts Postgres in a container, runs the storage tests
```

Try the API locally (development authentication only):

```bash
make pg-up                                                     # Postgres on :54329
export SEXTANT_OWNER_DATABASE_URL=postgres://postgres:test@localhost:54329/postgres
./bin/apiserver migrate
# the app role is created NOLOGIN by the migration; enable it out of band:
psql "$SEXTANT_OWNER_DATABASE_URL" -c "ALTER ROLE sextant_app LOGIN PASSWORD 'app'"

export SEXTANT_DATABASE_URL=postgres://sextant_app:app@localhost:54329/postgres
export SEXTANT_DEV_TOKEN=local-dev-token-0123456789            # >= 16 bytes
./bin/apiserver serve --dev-tenant acme &

B=http://127.0.0.1:8443/apis/sextant.andean.io/v1alpha1/organizations/acme
curl -H "Authorization: Bearer $SEXTANT_DEV_TOKEN" -X POST $B/clusters \
     -d '{"metadata":{"name":"prod-eu"},"spec":{"environment":"prod"}}'
curl -H "Authorization: Bearer $SEXTANT_DEV_TOKEN" $B/clusters
```

> The dev token grants full access to one tenant. It exists until the identity
> broker lands (Phase 2) and is refused on non-loopback addresses unless TLS **and**
> `--dev-insecure-allow-remote` are set.

### End-to-end on real clusters

```bash
make e2e                   # two kind clusters, Postgres, both Helm charts, kubectl through the tunnel
SEXTANT_E2E_KEEP=1 make e2e    # leave the clusters up to poke at
```

Needs `kind`, `helm`, `kubectl` and a container runtime (`CONTAINER=podman|docker`).
What it asserts, and what it deliberately does not, is in
[docs/testing/e2e.md](docs/testing/e2e.md).

### Deploying

Two charts in [deploy/charts](deploy/charts):

| Chart | Installs | Notable |
|---|---|---|
| `sextant` | Management plane | Bring-your-own Postgres; migrations run as a pre-install/upgrade hook with the schema-owner DSN while the server connects as the non-owner app role so RLS applies; hardened pods; secrets only by reference |
| `sextant-agent` | One agent per cluster | RBAC limited to get/update on its own credentials Secret; access to the cluster is a binding to an existing ClusterRole (default `view`) |

```bash
helm install sextant-agent deploy/charts/sextant-agent -n sextant-system \
  --set managementURL=https://sextant.example.com \
  --set registration.token=<one-time token> \
  --set-file serverCA=ca.pem        # omit to trust system roots
```

Production authentication, a Vault-backed CA and a managed Postgres are not wired
up yet; see below.

## Repository layout

```
cmd/apiserver        management plane: migrate | serve | pki init
cmd/agent            in-cluster agent
cmd/controllers      reconcilers (scaffold)
cmd/kx               CLI (scaffold)
pkg/apis/v1alpha1    API types and validation
pkg/registry         typed layer over storage; server-owned fields, status subresource
pkg/storage          tenant-scoped Postgres, migrations, registration tokens
pkg/server           REST API, cluster proxy, token endpoint
pkg/enroll           token + CSR -> certificate; mTLS renewal
pkg/pki              per-tenant intermediates, agent identity, key policy
pkg/tunnel           mTLS tunnel server and agent over the vendored remotedialer
pkg/agent            credentials, rotation, kube-apiserver proxy, Secret store
pkg/controllers      Cluster connectivity status
pkg/tenancy          tenant identity carried in context
third_party/         vendored forks, each with an UPSTREAM.md of local patches
deploy/              Helm charts, Dockerfiles, chart tests
test/e2e             kind-based exit-gate tests
docs/                specs, ADRs, testing notes
```

## Quality bar

- **Security-relevant behavior is mutation-tested.** For each guard (allow-list,
  revocation, identity parsing, single-use tokens, header stripping, RLS, chart
  hardening) the guarding test was shown to fail when the guard is removed.
- **`-race` clean**, including the vendored library, where we fixed two data races and an event-ordering bug.
- **Real dependencies in tests:** Postgres for storage and the API, kind for
  end-to-end. The e2e suite has already caught two deployment bugs unit tests could not.
- Every non-trivial decision is recorded: [ADR 0001](docs/adr/0001-api-server-shape.md)
  (API server shape), [ADR 0002](docs/adr/0002-tunnel-approach.md) (tunnel and its measurements).

## Roadmap

Each phase ends at an exit gate, not a date. The full plan is in
[Implementation Plan AI-Native Multi-Cluster Control Plane.md](Implementation%20Plan%20AI-Native%20Multi-Cluster%20Control%20Plane.md);
the Phase 0 spec and gate status are in [docs/specs/phase-0-foundations.md](docs/specs/phase-0-foundations.md).

| Phase | Goal | State |
|---|---|---|
| 0 | Foundations: management plane, agent tunnel, API model | **In progress** |
| 1 | Cluster import and inventory graph | Planned |
| 2 | SSO identity broker, brokered access, `kx` CLI | Planned |
| 3 | Kubeconfig migration, guardrails, JIT elevation | Planned (revenue gate) |
| 4 | Read-only AI copilot, model gateway, MCP server | Planned |
| 5 | AI decision fabric | Planned |
| 6 | Cluster lifecycle and GitOps | Planned |
| 7 | Containers and microVMs | Planned |

### Phase 0 exit gates

| Gate | Status |
|---|---|
| G1 one `helm install` connects an agent | Met on kind (about 1 s to connected); real NAT not yet exercised |
| G2 survives management-plane restarts | Met on kind: longest data-plane gap about 0.15 s; silent partitions detected in 15 s |
| G3 added p95 latency under 50 ms | Protocol overhead about 0.5 ms same-host; **real-region measurement still open** |
| G4 tenant isolation suite | Partial: storage, registry, HTTP and tunnel isolation tested; generated per-resource suite pending |
| G5 agent identity | Met in tests: single-use tokens, rotation, revocation hook |
| G6 signed images and SBOM | Not started |
| G7 OpenTelemetry | Not started |
| G8 hygiene | Lint, vet and race clean; threat model pending |

## What is not built yet

Stated plainly so nothing here is mistaken for more than it is:

- **No production authentication.** SSO, SCIM and per-user access policy are Phase 2.
  Until then the proxy acts with the agent's own service account (a ceiling set by
  `rbac.accessClusterRole`), the same for every caller.
- **No Vault or cloud-KMS signer.** The root CA is a local file (dev and CI only).
- **No revocation store.** Revocation is a hook plus `Server.Disconnect`.
- **No watch or event stream** (NATS), no inventory, no UI.
- **CI end-to-end job is untested** on a GitHub runner.

## License

Proprietary. All rights reserved; see [LICENSE](LICENSE). Vendored components under
`third_party/` remain under their own licenses.
