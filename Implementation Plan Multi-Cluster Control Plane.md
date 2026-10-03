# Implementation Plan: Sextant

Oct 2, 2026 · revised Oct 3, 2026 · @Daniel Puig

## Summary

Sextant makes working across many Kubernetes clusters safe and fast, and it is **open source (Apache-2.0)**. It is
delivered first as a **VS Code extension** (VS Code Marketplace plus Open VSX), because that is where the target engineers already work. The
extension is **local-first**: useful on day one against the user's existing kubeconfigs with no server, and
upgraded by connecting to a Sextant **management plane** that brokers short-lived, audited access through an
outbound-only agent in each cluster.

Each milestone ends at an exit gate, not a date: the next one starts only when the gate's criteria are met.

| | Milestone | State |
|---|---|---|
| F0 | **Foundations** (originally "Phase 0"): management plane, agent tunnel, enrollment, API model, charts, kind e2e | **Built.** Hardening items deferred, see below |
| P | **Publication**: make the repository public after the [pre-publication checklist](docs/publishing.md) | Licensed Apache-2.0; checklist in progress. Owner action to flip |
| E1 | **Local-first extension MVP**: fleet tree, environment tagging, context safety, bound terminals, credential audit | **Next.** Spec in review |
| E2 | **Connect**: SSO, brokered short-lived access, no secrets on disk, migration off static kubeconfigs | Planned |
| E3 | **Fleet from the server, just-in-time elevation, audit** | Planned. Commercial gate |
| E4 | **Guardrails** for destructive operations; fan-out; session view | Planned |
| E5 | **Operational assistant** through the platform's MCP server (read-only) | Planned |
| E6 | **Assisted decisions** with earned autonomy | Planned |
| Later | Cluster lifecycle and GitOps; containers and microVMs | **Frozen** until E3 has users |

Specs: [Foundations](docs/specs/phase-0-foundations.md) (built, with gate evidence) and
[the extension](docs/specs/vscode-extension.md) (E1 in depth; E2 onward outlined). Each later milestone gets its
own spec at the previous milestone's gate.

## Context and principles

**Target customer.** A platform team of 3-15 engineers running 10-150 clusters across 2-4 providers (typically
EKS/AKS/GKE plus some on-prem RKE2, K3s or OpenShift). They hand out kubeconfigs by Slack or a shared vault,
cannot answer "who has admin on prod right now", and spend hours on access tickets and offboarding.

**Target user.** The engineer on that team (or any engineer with 5-100 contexts) whose kubeconfig grew by
accident and who is one wrong-cluster command away from an incident.

**Product thesis.** Win the editor first with something useful in minutes and no deployment (a fleet view,
unmistakable production, terminals that cannot drift to the wrong cluster). Then make the management plane the
upgrade: brokered, short-lived, audited access, and finally server-side fleet knowledge, elevation and
assistance, all reachable from the same place.

**Open source and the business model.** All code in this repository is Apache-2.0, including the management
plane, agent, charts, extension and `kx`. Self-hosting is free and a first-class path. Revenue comes from what
open source does not give away: a **hosted management plane** that we run, support and SLAs, and, if customers need
them, separately licensed enterprise add-ons built outside this repository. Nothing may rely on the extension's
code being hidden; paid value has to be the service, not a licence check.

**Why local-first.** A tool that needs a platform team to deploy a server before it helps anyone has a long, gated
adoption path. A tool that is useful against the kubeconfig already on the laptop has a short one, and every
local user is a warm lead for the connected tier (hosted or self-hosted).

**Engineering principles**

- **Outbound-only connectivity.** Agents dial home over mTLS; no inbound ports on customer networks.
- **Declarative everything.** Every platform object is a versioned, CRD-style resource with reconciliation, so
  customers can GitOps the platform itself.
- **Integrate before you build.** Cluster API, Crossplane, Argo CD/Flux, OPA and Kyverno are dependencies, not
  competitors; the tunnel is a vendored, patched remotedialer.
- **No long-lived secrets on endpoints.** Kubeconfigs carry exec plugins, never tokens or client keys.
- **Treat every kubeconfig as a secret.** The extension reads them locally, shows classification and never values,
  and transmits nothing without explicit opt-in.
- **Assistants propose, policy disposes.** Deterministic policy and RBAC always sit between any model output and a
  cluster.
- **Pluggable models.** Every model call goes through one gateway; hosted, self-hosted and specialised decision
  models are interchangeable engines.
- **Open by default.** Apache-2.0, developed in the open once published, with security issues handled privately
  ([SECURITY.md](SECURITY.md)) and contributions under a DCO ([CONTRIBUTING.md](CONTRIBUTING.md)).
- **Self-hostable from day one.** The SaaS and the self-hosted edition share one codebase; SaaS is the default for
  mid-size buyers.
- **Evidence over assertion.** Security-relevant behaviour is guarded by a test that fails when the guard is
  removed, and exit gates are measured on real clusters, with what the measurement does not prove written down.

## F0: Foundations (built)

Goal: a deployable management plane, one agent that connects to it, and the API model everything else extends.
Spec and gate evidence: [docs/specs/phase-0-foundations.md](docs/specs/phase-0-foundations.md),
[docs/testing/e2e.md](docs/testing/e2e.md).

**Delivered**

- [x] Monorepo (`cmd/`, `pkg/`, `deploy/`, `test/`, `third_party/`), Go for backend and agent.
- [x] API model `sextant.andean.io/v1alpha1`: `Organization`, `Workspace`, `Environment`, `Cluster`, `AccessGrant`;
      plain HTTP server on Postgres, not kcp ([ADR 0001](docs/adr/0001-api-server-shape.md)).
- [x] Multi-tenancy: tenant in every API path and row; Postgres row-level security beneath application checks.
- [x] Agent tunnel: outbound mTLS over WebSocket (vendored, patched remotedialer;
      [ADR 0002](docs/adr/0002-tunnel-approach.md)); the agent dials exactly one virtual address and proxies to the
      local kube-apiserver as its own service account.
- [x] Enrollment: single-use registration tokens, 24 h certificates from per-tenant name-constrained intermediates,
      rotation at half-life, revocation hook plus `Disconnect`.
- [x] Delivery: Helm charts for both components, hardened pods, kind-based e2e ([make e2e](docs/testing/e2e.md)).

**Deferred.** Not needed for E1. Still required before exposing a multi-tenant service:

- [ ] Generated per-resource, per-verb tenant-isolation suite (G4).
- [ ] Threat model (G8); signed images and SBOM (G6); OpenTelemetry (G7).
- [ ] Vault `Signer` for the CA (only a local-file root exists); revocation store; NATS JetStream events.
- [ ] G3 re-measured in a real region (overhead is 0.5 ms same-host; the 50 ms gate needs real RTT).

**Pulled forward** because E2 and E3 depend on them:

- [ ] OpenAPI generation and a typed client (also the basis for the Terraform provider and the web UI).
- [ ] A watch/stream endpoint for `Cluster` status.
- [ ] The identity broker (starts E2).

## E1: Local-first extension MVP

Goal: an engineer installs the extension and, within a minute and with no configuration, sees every cluster from
their kubeconfig grouped by environment, opens a terminal bound to the right one, and cannot mistake production
for staging.

**Deliverables** (detail and acceptance criteria in [the extension spec](docs/specs/vscode-extension.md))

- [ ] Kubeconfig discovery and merge (`KUBECONFIG`, `~/.kube/config`, configured paths) with exec-plugin and OIDC
      shapes, tested against real fixtures from EKS, GKE, AKS, kind, k3s, RKE2 and OpenShift.
- [ ] Fleet tree grouped by environment then provider, with search and the current context marked.
- [ ] Environment and criticality tagging by QuickPick or by rule (glob on context name); stored in settings,
      never in the kubeconfig.
- [ ] Context safety: status bar with unmistakable colour for critical environments; confirmation when switching to
      one; warning when opening a terminal or file against one.
- [ ] Bound terminals: a minimal generated kubeconfig for exactly one context, so a later global context change
      cannot redirect the session.
- [ ] Credential audit (local analogue of `kx scan`): auth method, certificate expiry, long-lived or not. No secret
      value is ever displayed or logged.
- [ ] Release engineering: VSIX packaging, Marketplace and Open VSX publishing from CI on tag, canary secret test
      in CI.

**Key technical risks**

- Secret exposure. A kubeconfig reader that leaks a token into a log, tooltip or telemetry payload is the worst
  outcome. Mitigation: pure parsing modules, classification-only outputs, a canary test over every output surface.
- Host and platform fragmentation: Windows paths and exec plugins, Remote-SSH, WSL, Dev Containers, Cursor and
  VSCodium. Mitigation: an integration matrix and a manual checklist per release.

**Exit gate**

- Install to a populated fleet tree in under 60 s on a machine with an existing kubeconfig; 200 contexts render
  in under 500 ms.
- A context tagged critical is visually unmistakable in status bar, tree and terminal, and a bound terminal's
  current context cannot change when the global context changes (verified on kind).
- The canary test passes: no fixture secret in any output surface.
- Published to both registries; passes the integration suite on macOS, Linux and Windows.
- Design partners use it weekly.

## E2: Connect (identity broker, brokered access)

Goal: engineers sign in from the editor and reach any cluster they are entitled to with short-lived credentials,
and the kubeconfig on their laptop contains no secret. This is the milestone that turns the local tool into the
connected product.

**Deliverables**

- [ ] OIDC integration with Okta, Entra ID, Google Workspace and generic OIDC; SCIM for user and group sync so
      offboarding revokes access automatically.
- [ ] Access policy as resources: `AccessPolicy` maps IdP groups to Kubernetes roles by cluster label selector
      (for example group `payments-eng` gets `edit` on `env=staging, team=payments`).
- [ ] Policy compiler: renders `AccessPolicy` into ClusterRoles and RoleBindings that the agent applies and
      continuously reconciles; drift is reported and reverted.
- [ ] Authenticating proxy with per-user identity: requests carry a broker-issued token; the agent impersonates the
      user and groups (`Impersonate-User`, `Impersonate-Group`). Replaces today's single agent service-account
      ceiling and the dev bearer token.
- [ ] `kx` credential helper and CLI, bundled in platform-specific VSIXes: `kx login` (device code or browser
      SSO), `kx token --cluster <id>` as a `client.authentication.k8s.io/v1` exec plugin, tokens in the OS keychain
      with 15-60 minute TTL.
- [ ] Extension: sign-in, connect to a management plane, "Sync to kubeconfig", and the **migration flow**: import
      unknown clusters, replace static entries with brokered ones, and produce a revocation checklist for the old
      certificates and tokens (including rotating cluster CAs where client certificates cannot be revoked).
- [ ] Compatibility suite: kubectl, Helm, k9s, Lens/Freelens, Terraform kubernetes provider, Argo CD CLI, CI
      runners (GitHub Actions, GitLab) via workload-identity token exchange; service-account flow for CI with OIDC
      federation and no static tokens.
- [ ] Audit log: every proxied request recorded with user, cluster, verb, resource and decision; exportable to a
      SIEM (S3, Splunk, Datadog).
- [ ] Prerequisites from F0: OpenAPI and typed client, watch endpoint, generated isolation suite, threat model,
      Vault signer, revocation store.

**Key technical risks**

- Streaming verbs (exec, attach, port-forward, logs -f) through the tunnel. WebSocket upgrades and streaming
  already pass through the proxy in tests; SPDY exec and port-forward are not yet exercised end to end. Test them
  early on kind.
- The proxy becomes a single point of failure. Run multiple stateless replicas; the shutdown handover measured in
  F0 (about 0.15 s data-plane gap) is the baseline to protect.

**Exit gate**

- A new engineer goes from sign-in to `kubectl get pods` on an authorized cluster in under 2 minutes, with no
  manual kubeconfig handling.
- Removing a user from an IdP group revokes their access across all clusters within 60 seconds.
- The compatibility suite passes, including exec and port-forward.
- An external penetration test of the broker, proxy and agent finds no critical issues.

## E3: Fleet from the server, just-in-time elevation, audit (revenue gate)

Goal: the hosted service becomes worth paying for: one accurate, searchable estate view, and time-boxed elevation
instead of standing admin.

**Deliverables**

- [ ] Import flows: a generated `helm install` per cluster; cloud-account discovery for EKS, AKS and GKE (read-only
      role that lists clusters and offers one-click import).
- [ ] Agent collectors: informer-based watchers for nodes, namespaces, workloads, services, ingresses, RBAC bindings
      and CRDs, streaming deltas with backpressure and metadata-only informers where full objects are not needed.
- [ ] Inventory graph in Postgres (provider, account, region, cluster, namespace, workload, image; RBAC subjects,
      bindings, roles) with recursive queries through the API; enrichment (Kubernetes version and EOL, node OS,
      image registry and digest, owner labels, cost tags); a health model.
- [ ] Labels and Environments as the basis for access policy and for the extension's fleet grouping.
- [ ] Just-in-time elevation: `kx request admin --cluster prod-us-1 --for 30m --reason "INC-123"`, requestable from
      the extension; approvers notified in Slack or Teams; a time-boxed RoleBinding created and removed by the agent.
- [ ] Session recording for elevated sessions (exec input and output, API calls), searchable from the audit view.
- [ ] Break-glass: offline, hardware-key-signed credentials with 1-hour TTL, usable when the management plane is
      unreachable, with mandatory post-incident review.
- [ ] Access reviews: quarterly report of who can do what where, with one-click revocation (needed for SOC 2).
- [ ] Extension: server-backed fleet view and "who can reach this cluster", elevation requests, links into the
      audit log.

**Key technical risks**

- Watch volume on large clusters; graph freshness against cost (target sub-10-second propagation for spec
  changes, aggregate status fields).

**Exit gate**

- 100 clusters with 50,000 pods stay in sync with under 10 seconds lag on a single management-plane replica set.
- JIT elevation, from request to working access, takes under 1 minute when an approver is online.
- Break-glass works with the management plane fully down.
- Import works on EKS, AKS, GKE, RKE2, K3s and OpenShift in the e2e matrix.
- **First paying customers** (the hosted service or a support contract). This is the commercial gate for funding
  later milestones.

## E4: Guardrails and session view

Goal: working across many clusters becomes safer than it was before, not merely more convenient.

**Deliverables**

- [ ] Guardrails in the proxy: configurable confirmation or denial for destructive verbs (`delete`,
      `scale --replicas=0`, `drain`) on critical environments; dry-run enforcement for bulk operations; confirmation
      surfaced in the extension.
- [ ] Context sets and fan-out (`kx exec --set prod-eu -- kubectl get nodes`) with parallelism and aggregated
      output, available from the extension.
- [ ] Session recording view in the extension for elevated sessions.

**Exit gate**

- A design partner has migrated 100% of human access and decommissioned all static kubeconfigs.
- A destructive verb on a critical environment is blocked or confirmed in 100% of attempts across the compatibility
  suite.

## E5: Operational assistant

Goal: answer real questions about the estate ("who can delete deployments in prod?", "why is checkout crashlooping
in eu-west?") from the inventory graph, the audit log and live cluster reads, without ever changing anything.

**Deliverables**

- [ ] Model gateway: one internal API for all model calls, with adapters for hosted providers, OpenAI-compatible
      endpoints, self-hosted vLLM/KServe and a specialised decision model; per-tenant keys (bring your own), quotas,
      cost metering, retries, fallbacks and PII and secret redaction.
- [ ] Context assembly over the inventory graph, RBAC graph, audit log, events and logs; retrieval over customer
      runbooks with a per-tenant vector index.
- [ ] Platform MCP server: the platform API as tools, scoped by the calling user's own permissions, read-only in this
      milestone. Editor-native assistants and customers' own MCP clients connect to it; no custom chat panel is
      required in the extension.
- [ ] Prompt-injection defences: pod logs, annotations, events and ConfigMaps are untrusted data, separated from
      instructions; secrets are stripped before they reach any model.
- [ ] Evaluation harness: recorded estates and questions with expected answers, run on every prompt, tool or model
      change.

**Exit gate**

- At least 80% of eval questions answered correctly on recorded estates, with citations.
- Zero secret values in model inputs across the eval suite and a red-team run.
- Design partners use it in at least one real incident and rate it useful.

## E6: Assisted decisions

Goal: move from answering to deciding for bounded, high-frequency choices, with autonomy earned through measured
accuracy.

**Deliverables**

- [ ] `DecisionPolicy` resource: trigger, question, enumerated answers, context sources, engine (specialised model,
      LLM, rules), autonomy rules (minimum confidence, maximum blast radius) and fallback.
- [ ] Decision engine interface `Decide(ctx, question, answers, state) -> (answer, confidence)`, swappable per policy.
- [ ] Starter decision library, in order of value: access-request triage (approve, require approver, deny, for E3
      JIT); alert triage; escalation routing; remediation gating.
- [ ] Execution path: decisions produce a `Plan` (a diff of API calls) validated by OPA or Kyverno and RBAC before any
      apply; mutating MCP tools enabled only behind plans.
- [ ] Shadow mode first for every policy; replay of stored input snapshots to compare engines before switching;
      per-policy promotion, a global kill switch and rate limits.

**Key risks**

- A recent specialised decision model with limited published detail: keep it behind the engine interface and keep the
  LLM and rules paths working so a vendor change never blocks a customer.
- Wrong automated actions erode trust fast: start with access triage, where a wrong answer costs minutes, not
  outages.

**Exit gate**

- In shadow mode, access triage agrees with human decisions on 95% or more of cases across design partners.
- At least one policy in auto-apply at a customer for 30 days with no harmful action.
- Replay compares two engines on the same history and reports accuracy, latency and cost per decision.

## Later (frozen until E3 has users)

**Cluster lifecycle and GitOps.** Cluster API with providers for AWS, Azure, GCP, vSphere, Proxmox and Metal3;
Crossplane for surrounding cloud resources composed into `ClusterTemplate`; self-service from approved templates
with access policy attached automatically; fleet upgrades in waves with pre-flight checks and automatic pause on
health regression; an upgrade-risk decision policy; Argo CD or Flux management (no new GitOps engine); versioned
add-on bundles. Gate: provision, upgrade two minor versions and delete a cluster on each supported provider; a fleet
upgrade across 20 or more clusters with pause and resume.

**Containers and microVMs.** One `Workload` abstraction as a pod, a sandboxed pod (Kata on a `RuntimeClass`), a
plain container on a host, or a microVM, placed by a scheduler on isolation, cost and locality; a Rust host agent
using the same tunnel and identity model; access parity (brokered exec, logs, port-forward) through the same proxy
and audit. Gate: the same `Workload` spec runs as a pod, a Kata pod and a bare-host microVM with identical access
and audit; sub-second microVM cold start; a design partner running a real workload.

## Cross-cutting tracks

| Track | Starts in | What it covers |
|---|---|---|
| Security | F0 | Threat model per milestone, PKI and key rotation, signed releases, external pen test before each commercial gate, SOC 2 Type II for the hosted service from E3. Because the repository is public, assume attackers read every line: threat model before any multi-tenant hosting |
| Extension security and quality | E1 | Canary secret test over every output surface, no network call without opt-in, SecretStorage and keychain only, review of every `contributes` change |
| Testing | F0 | kind-based e2e on every PR; extension unit, integration (`@vscode/test-electron`) and OS matrix; nightly matrix across EKS, AKS, GKE, RKE2, K3s, OpenShift; scale tests at 100+ clusters; chaos tests on tunnel and proxy |
| Release engineering | E1 | VSIX per target platform, Marketplace plus Open VSX publishing from CI on tag, bundled `kx` binaries signed and checksummed, pre-release channel |
| Assistant evaluation | E5 | Recorded-estate eval suite, red-team prompt-injection suite, decision replay; gates every model, prompt or engine change |
| Observability | F0 | OpenTelemetry everywhere, SLOs for proxy latency and inventory lag, per-tenant usage metering (also the billing source) |
| Packaging | F0 | SaaS as default; self-hosted Helm edition from the same codebase; air-gapped bundle only when a customer requires it |
| Community and governance | P | DCO and CI checks on pull requests, issue triage, CODEOWNERS on security-sensitive paths, private vulnerability reporting, release notes and changelog, a trademark policy for the name |
| Developer experience | E1 | The extension and `kx` are the product for most engineers; OpenAPI, typed client, Terraform provider and API docs follow |

**Team shape.** Today: solo plus agents, with high-risk areas (PKI, tenancy, tunnel, identity) done and reviewed
inline and low-risk work delegated. E1 is roughly 3-4 weeks on that assumption. At scale: 2 on management plane and
API, 2 on agent, tunnel and proxy, 1 on identity, 1 on the extension and web UI, 1 on security and infrastructure;
add 2 ML and assistant engineers before E5 and 2 virtualization engineers only if the frozen items are unfrozen.

## Roadmap and key risks

E1 can start immediately; it does not depend on the backend. E2 depends on the pulled-forward F0 items. E3 follows
E2 and is the commercial gate. After it, the guardrail (E4), assistant (E5-E6) and platform (lifecycle) tracks can staff
and run in parallel.

**Key risks**

- **Secret exposure in the extension.** It reads credential files. One leaked token in a log or telemetry payload
  ends trust. Pure parsing, classification-only output, the canary test and opt-in-only networking are the control.
- **Scope creep before revenue.** Hold assisted decisions, lifecycle and microVMs until the E3 gate passes, however
  tempting they are to demo.
- **Proxy reliability.** The broker and proxy sit in every engineer's path; one bad outage loses the customer. Keep
  multi-replica proxies, the shutdown handover and break-glass on the critical path.
- **Marketplace and host dependency.** Publisher policy and forks that use Open VSX (which lag the Marketplace)
  constrain distribution. The extension is open and inspectable, so no secret or paid gate can live in it.
- **Open-source business model.** Anyone may host Sextant, including cloud vendors. Mitigations: the quality and
  operations of our hosted service, support, a trademark policy for the name, and enterprise add-ons kept outside
  this repository if customers need them. Do not plan around a licence moat.
- **Public-repository exposure.** Source, tests and history are readable by attackers from day one of publication.
  Mitigations: the pre-publication scan and checklist, a private disclosure path, the isolation suite, threat model
  and pen test before any multi-tenant hosting, and never committing a secret (history cannot be un-published).
- **Teleport, Rancher and Lens overlap.** Brokered Kubernetes access and cluster UIs already exist. Differentiate on
  the local-first editor experience, safety on the machine the engineer actually uses, the inventory graph and the
  assistant layer, not on access alone.
- **Dependency on a new decision model.** Pluggable by design; keep the LLM and rules engines working so a vendor
  change never blocks a customer.
- **Assistants with cluster reach.** Treat the assistant and MCP server as the highest-value attack target; every
  milestone that adds capability adds red-team scope.

## Document map

| Document | Purpose |
|---|---|
| [README](README.md) | What Sextant is, quick start, status |
| [CONTRIBUTING](CONTRIBUTING.md), [SECURITY](SECURITY.md), [CODE_OF_CONDUCT](CODE_OF_CONDUCT.md) | How to contribute, report vulnerabilities, and behave |
| [docs/publishing.md](docs/publishing.md) | Pre-publication checklist for making the repository public |
| [docs/specs/vscode-extension.md](docs/specs/vscode-extension.md) | E1 specification; E2-E6 outline |
| [docs/specs/phase-0-foundations.md](docs/specs/phase-0-foundations.md) | F0 specification and gate status |
| [docs/adr/](docs/adr/) | Architecture decisions (API server shape, tunnel) |
| [docs/testing/e2e.md](docs/testing/e2e.md) | What `make e2e` proves and does not |
