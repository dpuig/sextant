# Implementation Plan: Multi-Cluster Control Plane

Oct 2, 2026 · @Daniel Puig

## Context and principles

The plan lands access and inventory first, because kubeconfig sprawl is the pain mid-size platform teams feel daily; AI, lifecycle and microVMs build on that trust. Each phase ends at an exit gate, not a date: the next phase starts only when the gate's criteria are met.

**Target customer.** A platform team of 3–15 engineers running 10–150 clusters across 2–4 providers (typically EKS/AKS/GKE plus some on-prem RKE2, K3s or OpenShift). They hand out kubeconfigs by Slack or a shared vault, cannot answer "who has admin on prod right now", and spend hours on access tickets and offboarding.

**Product thesis.** Win the access layer first (brokered, short-lived, audited), use the resulting inventory graph as AI context, then expand into lifecycle and workloads.

**Engineering principles**

- **Outbound-only connectivity.** Agents dial home over mTLS; no inbound ports on customer networks.
- **Declarative everything.** Every platform object is a versioned, CRD-style resource with reconciliation, so customers can GitOps the platform itself.
- **Integrate before you build.** Cluster API, Crossplane, Argo CD/Flux, OPA and Kyverno are dependencies, not competitors.
- **No long-lived secrets on endpoints.** Kubeconfigs carry exec plugins, never tokens or client keys.
- **AI proposes, policy disposes.** Deterministic policy and RBAC always sit between any model output and a cluster.
- **Pluggable models.** Every AI call goes through one gateway; Jev, hosted LLMs and self-hosted models are interchangeable engines.
- **Self-hostable from day one.** The SaaS and the self-hosted edition share one codebase; SaaS is the default for mid-size buyers.

## Phase 0: Foundations

Goal: a deployable management plane, one agent that connects to it, and the API model every later phase extends.

**Deliverables**

- [ ] Monorepo: `cmd/` (apiserver, controllers, agent, kx CLI), `pkg/` (shared API types), `web/` (React UI). Go for backend and CLI, TypeScript for UI.
- [ ] API model: a dedicated aggregated API server or kcp-style workspace server exposing CRD-like resources (`Organization`, `Cluster`, `Environment`, `AccessGrant`), with OpenAPI generation and typed clients.
- [ ] Multi-tenancy model: Organization → Workspace → Environment, with tenant ID on every row and every API path from the start.
- [ ] Storage: Postgres as the system of record; NATS JetStream for events between controllers and from agents.
- [ ] Agent tunnel: outbound gRPC over mTLS with HTTP/2 multiplexing; the agent proxies authenticated requests to the local kube-apiserver. Agent identity comes from a one-time registration token exchanged for a short-lived client cert, auto-rotated.
- [ ] Internal PKI: per-tenant intermediate CAs, backed by a KMS (cloud KMS for SaaS, Vault or HSM for self-hosted).
- [ ] Delivery: Helm chart for the management plane and agent; CI with kind-based e2e tests; signed images (cosign) and SBOMs per release.
- [ ] Baseline observability: OpenTelemetry traces and metrics across apiserver, controllers and agent.

**Key decisions to make here**

- Build on kcp or a plain aggregated apiserver backed by Postgres. Recommendation: plain apiserver on Postgres (kine-style storage) for simpler operations at mid-size scale.
- Tunnel library: start from a proven reverse-tunnel design (Rancher's remotedialer or similar) rather than writing one.

**Exit gate**

- An agent installed by one `helm install` connects through NAT, survives management-plane restarts, and reconnects within 30 seconds.
- `kubectl get pods` through the tunnel works against a kind cluster, with p95 added latency under 50 ms in-region.
- Tenant isolation tests pass: no API call can read another tenant's objects.

## Phase 1: Cluster import and inventory graph

Goal: a platform team imports every cluster they run in under an hour and sees one accurate, searchable picture of their estate.

**Deliverables**

- [ ] Import flows: generated `helm install` / `kubectl apply` command per cluster; cloud-account discovery for EKS, AKS and GKE (read-only IAM role, lists clusters, offers one-click import).
- [ ] Agent collectors: informer-based watchers for nodes, namespaces, workloads, services, ingresses, RBAC bindings and CRDs; stream deltas, not snapshots.
- [ ] Inventory graph in Postgres: provider → account → region → cluster → namespace → workload → image, plus RBAC subjects → bindings → roles. Expose recursive queries through the API.
- [ ] Enrichment: Kubernetes version and EOL status, node OS, image registry and digest, owner labels, cost tags from cloud APIs.
- [ ] Health model: per-cluster connectivity, API latency, control-plane status, node pressure, failing workloads.
- [ ] UI: fleet view with filters by provider, region, version and label; cluster detail; global search across all resources.
- [ ] Labels and Environments: user-defined tags (env, team, criticality) on clusters, the basis for access policy in Phase 2.

**Key technical risks**

- Watch volume on large clusters. Mitigate with field filtering, metadata-only informers where full objects aren't needed, and backpressure on the stream.
- Graph freshness versus cost. Target sub-10-second propagation for spec changes; aggregate status fields.

**Exit gate**

- Import works on EKS, AKS, GKE, RKE2, K3s and OpenShift in the e2e matrix.
- 100 clusters with 50,000 pods stay in sync with under 10 seconds lag on a single management-plane replica set.
- 3 design-partner teams have imported their full estate and use the fleet view weekly.

## Phase 2: Identity broker, brokered access and the kx CLI

Goal: engineers reach any cluster with SSO and short-lived credentials, and the kubeconfig on their laptop contains no secret. This is the phase that sells the product.

**Deliverables**

- [ ] OIDC integration with Okta, Entra ID, Google Workspace and generic OIDC; SCIM for user and group sync so offboarding revokes access automatically.
- [ ] Access policy as resources: `AccessPolicy` maps IdP groups to Kubernetes roles by cluster label selector (e.g. group `payments-eng` gets `edit` on `env=staging, team=payments`).
- [ ] Policy compiler: renders `AccessPolicy` into ClusterRoles and RoleBindings that the agent applies and continuously reconciles; drift is reported and reverted.
- [ ] Authenticating proxy: requests carry a broker-issued token; the agent impersonates the user and groups (`Impersonate-User`, `Impersonate-Group`) against the local apiserver. No cloud IAM mapping required for imported clusters.
- [ ] `kx` CLI: `kx login` (device-code or browser SSO), `kx sync` writes kubeconfig entries that use the `client.authentication.k8s.io/v1` exec plugin (`kx token --cluster <id>`), tokens cached in the OS keychain with 15–60 minute TTL.
- [ ] Compatibility test suite: kubectl, Helm, k9s, Lens/Freelens, Terraform kubernetes provider, Argo CD CLI, CI runners (GitHub Actions, GitLab) via workload-identity token exchange.
- [ ] Service-account flow for CI: OIDC federation from CI providers, no static tokens.
- [ ] Audit log: every proxied request recorded with user, cluster, verb, resource and decision; exportable to SIEM (S3, Splunk, Datadog).

**Key technical risks**

- Streaming verbs (exec, attach, port-forward, logs -f) through the tunnel. Test SPDY and WebSocket upgrades early; they are the most common breakage.
- Proxy becoming a single point of failure. Run multiple stateless proxy replicas and pin sessions by connection, not by node.

**Exit gate**

- A new engineer goes from SSO login to `kubectl get pods` on an authorized cluster in under 2 minutes, with no manual kubeconfig handling.
- Removing a user from an IdP group revokes their access across all clusters within 60 seconds.
- The full compatibility suite passes, including exec and port-forward.
- An external penetration test of the broker, proxy and agent finds no critical issues.

## Phase 3: Context catalog, migration and guardrails

Goal: replace the team's existing kubeconfig sprawl and make working across many clusters safer than it was before.

**Deliverables**

- [ ] Kubeconfig scanner (`kx scan`): reads local kubeconfigs, `KUBECONFIG` paths, and optionally Vault, AWS Secrets Manager or 1Password vaults. Reports each credential's cluster, identity, privilege level, expiry and whether the cluster is already imported.
- [ ] Migration flow: one command imports unknown clusters, replaces static entries with brokered ones, and produces a revocation checklist for the old certs and tokens (including rotating cluster CAs where client certs can't be revoked).
- [ ] Context catalog: contexts as queryable objects with labels; `kx use` with fuzzy search; saved context sets (`kx set prod-eu`); fan-out (`kx exec --set prod-eu -- kubectl get nodes`) with parallelism and aggregated output.
- [ ] Shell integration: prompt segment showing cluster and environment, red for `criticality=high`.
- [ ] Guardrails in the proxy: configurable confirmation or denial for destructive verbs (`delete`, `scale --replicas=0`, `drain`) on critical environments; dry-run enforcement for bulk operations.
- [ ] Just-in-time elevation: `kx request admin --cluster prod-us-1 --for 30m --reason "INC-123"`; approvers notified in Slack or Teams; time-boxed RoleBinding created and removed by the agent.
- [ ] Session recording for elevated sessions (`exec` input and output, API calls), searchable from the audit view.
- [ ] Break-glass: offline, hardware-key-signed credentials with 1-hour TTL, usable when the management plane is unreachable, with mandatory post-incident review.
- [ ] Access reviews: quarterly report of who can do what where, with one-click revocation. Mid-size buyers need this for SOC 2.

**Exit gate**

- A design partner has migrated 100% of human access and decommissioned all static kubeconfigs.
- JIT elevation, from request to working access, takes under 1 minute when an approver is online.
- Break-glass works in a test where the management plane is fully down.
- First paying customers on the access tier. This is the commercial gate for funding later phases.

## Phase 4: Read-only AI copilot, model gateway and MCP server

Goal: answer real questions about the estate ("who can delete deployments in prod?", "why is checkout crashlooping in eu-west?") using the inventory graph, the audit log and live cluster reads, without the AI ever changing anything.

**Deliverables**

- [ ] Model gateway service: one internal API for all model calls; adapters for Anthropic, OpenAI-compatible endpoints, self-hosted vLLM/KServe, and TypeSafe (Jev). Handles per-tenant keys (BYO key supported), quotas, cost metering, retries, fallbacks and PII/secret redaction.
- [ ] Context assembly: tools that query the inventory graph, RBAC graph, audit log, events and logs. Retrieval over customer runbooks and docs with a per-tenant vector index (pgvector).
- [ ] Platform MCP server: exposes the platform API as tools, scoped by the calling user's own permissions. It has read-only tools in this phase. Customers can also connect their own MCP clients to it.
- [ ] Copilot UI: chat panel in context (cluster, workload, incident), with every answer linking the resources and queries it used.
- [ ] Prompt-injection defenses: treat pod logs, annotations, events and ConfigMaps as untrusted data; separate them from instructions; strip secrets before they reach any model.
- [ ] Evaluation harness: a library of recorded estates and questions with expected answers; run on every prompt, tool or model change.

**Exit gate**

- 80% or more of the eval questions answered correctly on recorded estates, with citations.
- Zero secret values (Secret data, tokens) in model inputs across the eval suite and a red-team run.
- Design partners use the copilot in at least one real incident and rate it useful.

## Phase 5: AI decision fabric

Goal: move from AI that answers to AI that decides, using fast decision models for bounded, high-frequency choices and LLMs for reasoning, with autonomy earned through measured accuracy.

**Deliverables**

- [ ] `DecisionPolicy` resource: trigger (event type and scope), question, enumerated answers, context sources, engine (`jev`, `llm`, `rules`), autonomy rules (minimum confidence, maximum blast radius) and fallback.
- [ ] Decision engine interface: `Decide(ctx, question, answers, state) → (answer, confidence)`. Implementations: Jev via the TypeSafe API, an LLM with constrained structured output, and a rules engine. The engine is swappable per policy.
- [ ] Starter decision library for the target customer, in order of value:
  1. Access request triage: auto-approve, require approver, or deny, for JIT elevation in Phase 3.
  2. Alert triage: page, ticket, suppress, or attach to an existing incident.
  3. Escalation routing: resolve with a playbook, escalate to the LLM, or escalate to a human.
  4. Remediation gating: is this proposed action safe to apply automatically?
- [ ] Execution path: decisions produce a `Plan` (a diff of API calls), validated by OPA or Kyverno and RBAC before any apply. Mutating MCP tools are enabled here, always behind plans.
- [ ] Shadow mode: every policy starts in recommend-only mode; humans act, and the system records agreement between the AI decision and the human action.
- [ ] Replay and evaluation: every decision stores its full input snapshot; replay history against a new engine, model version or threshold and compare accuracy before switching.
- [ ] Autonomy controls: per-policy promotion from shadow to auto-apply, global kill switch, rate limits on automated actions per cluster.

**Key technical risks**

- Jev is a recent, hosted model with limited published detail. Keep it behind the engine interface, check TypeSafe's data residency terms, and keep the LLM-structured-output path as a working fallback.
- Wrong automated actions erode trust fast. Start with access triage, where a wrong "require approver" costs minutes, not outages.

**Exit gate**

- In shadow mode, access triage agrees with human decisions on 95% or more of cases across design partners.
- At least one policy running in auto-apply at a customer for 30 days with no harmful action.
- Replay can compare two engines on the same history and report accuracy, latency and cost per decision.

## Phase 6: Lifecycle and GitOps

Goal: create, upgrade and retire clusters across providers from the same control plane, with the AI fabric assessing risk at each step.

**Deliverables**

- [ ] Cluster API integration: management-plane-hosted CAPI with providers for AWS (CAPA), Azure (CAPZ), GCP (CAPG), vSphere (CAPV), Proxmox and bare metal (Metal3). Managed clusters (EKS, AKS, GKE) via their CAPI managed-cluster providers.
- [ ] Crossplane for surrounding cloud resources (VPCs, subnets, load balancers, IAM roles), composed into `ClusterTemplate` resources the platform team publishes.
- [ ] Cluster templates and self-service: app teams request a cluster from an approved template; access policies from Phase 2 attach automatically.
- [ ] Fleet upgrades: waves by label (dev → staging → prod), pre-flight checks (deprecated APIs in use, PodDisruptionBudgets that block drains, add-on compatibility), automatic pause on health regression.
- [ ] Upgrade risk decision: a `DecisionPolicy` that scores each cluster's upgrade as proceed, hold or needs review, with the LLM writing the explanation.
- [ ] GitOps integration: install and manage Argo CD or Flux per cluster or per fleet; show sync status in the inventory graph. Do not build a new GitOps engine.
- [ ] Add-on management: versioned bundles for ingress, cert-manager, external-dns, monitoring agents and policy engines.

**Exit gate**

- Provision, upgrade two minor versions and delete a cluster on each supported provider in the e2e matrix.
- A design partner runs a fleet upgrade across 20 or more clusters with automatic pause and resume working.
- Self-service cluster requests go from request to usable, access-enabled cluster without platform-team hands-on work.

## Phase 7: Containers and microVMs

Goal: one `Workload` abstraction that can run as a pod, a sandboxed pod, a plain container on a host, or a microVM, placed by the scheduler according to isolation, cost and locality constraints.

**Deliverables, in order**

- [ ] Sandboxed pods first: install and manage Kata Containers (Firecracker or Cloud Hypervisor backend) as a `RuntimeClass` on capable node pools. This delivers microVM isolation with the least new infrastructure.
- [ ] Host agent (Rust): manages containerd and microVMs on bare hosts outside Kubernetes. It uses the same tunnel and identity model as the cluster agent. Study flintlock (Liquid Metal) for the microVM API design.
- [ ] MicroVM lifecycle: image building from OCI images, root filesystems and kernels, networking (TAP plus CNI or a host bridge), snapshot and restore, resource limits.
- [ ] `Workload` resource: a spec with `isolation` (container, sandbox, vm), resources, placement constraints and exposure. A per-substrate renderer outputs a Deployment, a pod with `runtimeClassName`, or a host-agent microVM spec.
- [ ] Placement scheduler: filters substrates by hard constraints (region, compliance labels, isolation), then scores by cost, capacity and latency. Optionally delegates the final pick to a placement `DecisionPolicy` running on Jev.
- [ ] Access parity: brokered `exec`, logs and port-forward work the same for microVMs and host containers as for pods, through the Phase 2 proxy and audit.

**Exit gate**

- The same `Workload` spec runs as a pod, a Kata pod and a bare-host microVM, with identical access, logs and audit.
- MicroVM cold start under 1 second from a cached image on the host agent.
- At least one design partner runs a real workload (CI runners, untrusted code, or multi-tenant jobs) on microVMs through the platform.

## Cross-cutting tracks

These run through every phase rather than belonging to one.

| Track | Starts in | What it covers |
| --- | --- | --- |
| Security | Phase 0 | Threat model per phase, PKI and key rotation, signed releases, external pen test before each commercial gate, SOC 2 Type II for the SaaS from Phase 3 |
| Testing | Phase 0 | kind-based e2e on every PR; nightly matrix across EKS, AKS, GKE, RKE2, K3s, OpenShift; scale tests at 100+ clusters; chaos tests on tunnel and proxy |
| AI evaluation | Phase 4 | Recorded-estate eval suite, red-team prompt injection suite, decision replay; gates every model, prompt or engine change |
| Observability | Phase 0 | OpenTelemetry everywhere, SLOs for proxy latency and inventory lag, per-tenant usage metering (also the billing source) |
| Packaging | Phase 0 | SaaS as default; self-hosted Helm edition from the same codebase; air-gapped bundle only when a customer requires it |
| Developer experience | Phase 2 | `kx` CLI, Terraform provider and API docs; the CLI is the product for most engineers |

**Team shape for the first three phases:** 2 engineers on management plane and API, 2 on agent, tunnel and proxy, 1 on CLI and identity, 1 on UI, 1 on security and infrastructure. Add 2 AI engineers before Phase 4 and 2 virtualization engineers before Phase 7.

## Roadmap and key risks

&#91;embedded content: roadmap · 8 phases, 2 parallel tracks after the revenue gate\]

Phases 0–3 run in sequence because each depends on the last; after the Phase 3 revenue gate, the AI and platform tracks can staff and run in parallel.

**Key risks**

- **Scope creep before revenue.** Hold AI mutations, lifecycle and microVMs until the Phase 3 gate passes, however tempting they are to demo.
- **Proxy reliability.** The broker and proxy sit in every engineer's path; one bad outage loses the customer. Invest in multi-replica proxies and break-glass early.
- **Teleport and Rancher overlap.** Teleport already sells brokered Kubernetes access. Differentiate on the inventory graph, multi-cluster workflows and the AI layer, not on access alone.
- **Dependency on a new decision model.** Jev is pluggable by design; keep the LLM and rules engines working so a vendor change never blocks a customer.
- **AI with cluster reach.** Treat the copilot and MCP server as the highest-value attack target; every phase that adds AI capability adds red-team scope.
