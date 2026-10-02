# Spec: Sextant Phase 0 — Foundations

Source: `Implementation Plan AI-Native Multi-Cluster Control Plane.md` (Phase 0).
Status: **DRAFT — awaiting human review.** No code until approved.

## Objective

Deliver a deployable management plane, one agent that connects to it from behind NAT, and the API
model every later phase extends.

**Users:** platform engineers (3–15 person teams, 10–150 clusters) are the eventual users. In Phase 0
the users are Sextant's own engineers and design partners' evaluators.

**Why:** access and inventory (Phases 1–3) sit on this foundation. Mistakes in tenancy, identity or the
tunnel are expensive to fix later, so Phase 0 exists to get them right.

**Decisions already made (confirmed with owner, 2026-10-02):**

| Decision | Choice |
|---|---|
| API server | Plain aggregated-style apiserver backed by Postgres (kine-style). Not kcp. |
| Tunnel | Fork/vendor Rancher `remotedialer`, adapted to mTLS + HTTP/2 multiplexing. |
| PKI / KMS | `Signer` interface with local-file and Vault implementations. Cloud KMS adapters deferred. |
| Scope | Phase 0 in depth; Phases 1–7 outlined only (see end). |

## Tech Stack

- Backend, agent, CLI: Go (latest stable at kickoff; pinned in `go.mod`, with `toolchain` directive).
- API machinery: `k8s.io/apiserver`, `k8s.io/apimachinery`, `controller-runtime`; codegen via `controller-gen` / `client-gen` / `openapi-gen`.
- Storage: PostgreSQL (system of record), kine-style storage layer. NATS JetStream for events.
- Tunnel: vendored Rancher `remotedialer` + gRPC/HTTP/2 transport adaptation.
- UI: TypeScript + React (`web/`), scaffold only in Phase 0.
- Observability: OpenTelemetry (traces + metrics), OTLP export.
- Delivery: Helm charts, GitHub Actions (assumed; see Open Questions), kind for e2e, cosign + syft (SBOM).
- Versions of every dependency get pinned when the first task lands. This spec does not invent them.

## Commands

(Targets to be created by Task 1; all invoked through `make`.)

```
Build all:        make build                 # go build ./... + web build
Unit tests:       make test                  # go test ./... -race -count=1
Coverage:         make cover                 # go test ./... -coverprofile=cover.out
Lint:             make lint                  # golangci-lint run ./... && (cd web && npm run lint)
Codegen:          make generate              # deepcopy, clients, openapi; CI fails on dirty diff
Dev stack:        make dev-up                # docker compose: postgres + nats; runs apiserver locally
Kind e2e:         make e2e                   # kind cluster + helm install + go test ./test/e2e/... -tags e2e
Tenant isolation: make test-isolation        # go test ./test/isolation/... against real Postgres
Tunnel bench:     make bench-tunnel          # latency + reconnect benchmark used for exit gates
Images:           make images                # build apiserver, controllers, agent, kx
Sign + SBOM:      make release-dry-run       # cosign (keyless disabled locally) + syft
Helm lint:        helm lint deploy/charts/*
```

## Project Structure

```
cmd/apiserver/      → management-plane API server
cmd/controllers/    → reconcilers (Cluster, AccessGrant, ...)
cmd/agent/          → in-cluster agent
cmd/kx/             → CLI (Phase 0: `kx version`, `kx cluster register` only)
pkg/apis/           → versioned API types (v1alpha1): Organization, Workspace, Environment, Cluster, AccessGrant
pkg/client/         → generated typed clients
pkg/storage/        → Postgres-backed storage, tenant-scoped
pkg/tenancy/        → tenant context, path scoping, row-level enforcement helpers
pkg/pki/            → Signer interface; localfile/ and vault/ implementations; per-tenant intermediates
pkg/tunnel/         → server + client over vendored remotedialer (third_party/ for the fork)
pkg/events/         → NATS JetStream publisher/consumer wrappers
pkg/telemetry/      → OTel setup shared by all binaries
web/                → React UI scaffold
deploy/charts/      → sextant (management plane) and sextant-agent Helm charts
test/e2e/           → kind-based end-to-end
test/isolation/     → cross-tenant isolation suite
docs/specs/         → specs (this file)
docs/adr/           → architecture decision records
docs/threat-model/  → per-phase threat models (Phase 0 first)
third_party/        → vendored forks, with UPSTREAM.md recording commit + local patches
```

## Code Style

Go, standard `gofmt`/`goimports`, `golangci-lint` defaults plus `errorlint`, `contextcheck`.
Tenant ID is **never optional** in storage or API signatures:

```go
// pkg/storage/cluster.go
func (s *ClusterStore) Get(ctx context.Context, name string) (*v1alpha1.Cluster, error) {
	tid, err := tenancy.FromContext(ctx) // errors if ctx has no tenant
	if err != nil {
		return nil, fmt.Errorf("get cluster %q: %w", name, err)
	}
	row := s.db.QueryRow(ctx,
		`SELECT spec, status FROM clusters WHERE tenant_id = $1 AND name = $2`, tid, name)
	...
}
```

Conventions: errors wrapped with `%w` and context; no global state; `context.Context` first arg;
API types follow Kubernetes conventions (`spec`/`status`, conditions, `metadata.generation`);
tests named `TestThing_Behavior`; table-driven where inputs vary; comments explain why, not what.

## Testing Strategy

| Level | Tool | Where | Covers |
|---|---|---|---|
| Unit | `go test` | next to code | storage, tenancy, pki, tunnel framing, reconcilers |
| Integration | `go test` + testcontainers (Postgres, NATS) | `pkg/**/_integration_test.go` | storage semantics, watch, event flow |
| Isolation | `go test` against real Postgres | `test/isolation/` | every API verb × resource, cross-tenant read/write/list/watch must fail |
| E2E | kind + Helm | `test/e2e/` | install, connect, kubectl via tunnel, restart/reconnect |
| Chaos (basic) | scripted pod kills / network drops | `test/e2e/chaos/` | management-plane restart, NAT/conn drop |
| Bench | Go benchmarks + e2e harness | `make bench-tunnel` | added p95 latency, reconnect time |

Coverage expectation: ≥80% on `pkg/tenancy`, `pkg/pki`, `pkg/storage`, `pkg/tunnel`. These are high-risk
packages and are reviewed line by line. The isolation suite is generated from the API resource list, so a new
resource without isolation coverage fails CI.

## Boundaries

- **Always:** run `make lint test generate` before commit; put tenant ID in every query and API path;
  write an ADR for every decision in the "Key decisions" list; keep upstream forks documented in `third_party/*/UPSTREAM.md`;
  validate and size-limit all inputs at the API and tunnel boundary.
- **Ask first:** adding a dependency; Postgres schema changes after the first migration ships; changing CI config or
  release signing; changing the API group or version; any change to the PKI trust chain or token lifetimes.
- **Never:** commit secrets, keys or kubeconfigs; add a code path without tenant scoping; log tokens, cert keys or
  registration tokens; open inbound ports on the agent side; delete or skip a failing isolation test without approval;
  hand-edit generated code.

## Risk tiers (per user working agreement)

| Area | Tier |
|---|---|
| `pkg/pki`, registration-token exchange, cert rotation | **High** — implement and review inline, security-review skill |
| `pkg/tenancy`, `pkg/storage`, isolation suite | **High** |
| `pkg/tunnel`, agent proxy | **High** (network auth boundary) |
| API types, controllers, Helm charts | Medium |
| Makefile, CI scaffolding, docs, UI scaffold | Low — fine to delegate, still check the diff |

## Deliverables → Requirements

1. **Monorepo & tooling.** Layout above builds and lints green from a clean checkout.
2. **API model.** `Organization`, `Workspace`, `Environment`, `Cluster`, `AccessGrant` as `sextant.andean.io/v1alpha1`
   (group name is a placeholder; see Open Questions). CRUD + watch via the apiserver, OpenAPI published, typed Go client generated.
   Note: the plan lists `Organization`, `Cluster`, `Environment`, `AccessGrant`; `Workspace` is added because the
   tenancy model requires it.
3. **Tenancy.** Hierarchy Organization → Workspace → Environment. `tenant_id` column on every table; tenant in
   every API path (`/apis/<group>/<version>/organizations/{org}/...`); Postgres row-level security as defense in depth
   behind application checks.
4. **Storage/events.** Postgres migrations (versioned, forward-only, tested up from empty). JetStream stream per concern,
   subjects namespaced by tenant: `sextant.<tenant>.<kind>.<event>`.
5. **Agent tunnel.** Agent dials out over mTLS; server multiplexes requests over HTTP/2; the agent proxies authenticated
   requests to the local kube-apiserver using its in-cluster ServiceAccount. Registration: one-time token → short-lived
   client cert (default TTL 24h, rotation at 50% of lifetime, no restart needed).
6. **PKI.** `pki.Signer` interface; local-file (dev/CI) and Vault (self-hosted) implementations; per-tenant intermediate CAs;
   root key never leaves the Signer. Cloud KMS adapters are out of scope.
7. **Delivery.** Helm charts for management plane and agent; GitHub Actions CI running lint, unit, integration, isolation, kind e2e;
   cosign-signed images and SBOM attached to release artifacts.
8. **Observability.** OTel traces and metrics from apiserver, controllers and agent; trace context propagates across the tunnel.

## Success Criteria

Exit gate from the plan, made testable:

- [ ] **G1 Install:** on a fresh kind cluster, `helm install sextant-agent` with a registration token results in `Cluster.status.connected=true`
      within 60 s, with the kind node behind a network that blocks all inbound traffic.
- [ ] **G2 Resilience:** killing the apiserver pod (and separately, a full management-plane rollout restart) does not require operator action;
      the agent shows reconnected within **30 s** in 20/20 consecutive runs.
- [ ] **G3 Latency:** `kubectl get pods` through the tunnel against kind, in-region, adds **p95 < 50 ms** over direct access across ≥1,000 requests
      (`make bench-tunnel`).
- [ ] **G4 Isolation:** isolation suite passes — for every resource and every verb (get, list, watch, create, update, delete), tenant A cannot read or
      affect tenant B's objects, including via direct SQL-adjacent paths (RLS test with the app role) and NATS subject subscription.
- [ ] **G5 Identity:** registration token is single-use (second use rejected), expired tokens rejected, agent cert rotates automatically without dropping
      in-flight requests, a revoked agent cannot reconnect.
- [ ] **G6 Supply chain:** release dry-run produces cosign signatures and an SBOM for each image; CI verifies signatures.
- [ ] **G7 Observability:** a single `kubectl get pods` produces one trace spanning apiserver → tunnel → agent.
- [ ] **G8 Hygiene:** `make lint test generate` green; CI green on main; threat model for Phase 0 committed in `docs/threat-model/`.

## Key decisions to record as ADRs

1. apiserver on Postgres (decided). 2. remotedialer fork and the adapted transport (decided). 3. Signer interface (decided).
4. Tenancy enforcement layering: app-level + RLS (proposed). 5. Cert TTL and rotation policy (proposed 24h / 50%).
6. API group name and versioning policy.

## Outline of later phases (not specified here — each gets its own spec at the previous gate)

| Phase | One-line goal | Gate (abridged) |
|---|---|---|
| 1 Import & inventory | Import every cluster <1h; searchable graph | 6 distros in e2e; 100 clusters / 50k pods <10 s lag; 3 design partners |
| 2 Identity broker & `kx` | SSO to kubectl with no secrets on disk | <2 min onboarding; revoke <60 s; full compat suite; clean pen test |
| 3 Catalog, migration, guardrails | Replace kubeconfig sprawl; safer multi-cluster ops | 100% migration at a partner; JIT <1 min; break-glass with MP down; first paying customers (**revenue gate**) |
| 4 Read-only AI copilot | Answer estate questions, never mutate | ≥80% eval; zero secrets in model inputs; used in a real incident |
| 5 Decision fabric | Bounded AI decisions with earned autonomy | 95% shadow agreement; 30 days auto-apply, no harm; replay compares engines |
| 6 Lifecycle & GitOps | Create/upgrade/retire clusters | Provision+upgrade+delete per provider; 20+ cluster fleet upgrade |
| 7 Containers & microVMs | One `Workload`, many substrates | Same spec as pod/Kata/microVM; <1 s cold start; real partner workload |

Per the plan: hold Phases 4–7 until the Phase 3 revenue gate passes.

## Open Questions

1. **CI provider:** GitHub Actions assumed. Correct?
2. **Names:** API group (`sextant.andean.io`?), Go module path, container registry (`ghcr.io/andean-products/...`?).
3. **Repo hosting:** create a GitHub repo now? (Directory is not yet a git repo.)
4. **Licensing** of the codebase, which matters for the vendored `remotedialer` (Apache-2.0): proprietary, open-core, or OSS?
5. **Agent cert TTL:** is 24h with rotation at 50% acceptable, or do you want shorter (e.g. 1h)?
6. **Postgres deployment** for the self-hosted Helm chart: bundled (CloudNativePG/Bitnami) or BYO only?
7. **Scale target** for Phase 0 load tests: how many simultaneous agents should one apiserver replica handle (proposal: 500)?
8. **Team:** are the 7 roles in the plan's team shape staffed, or is this solo plus agents? This changes how I'd sequence parallel tasks.
