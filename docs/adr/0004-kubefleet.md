# ADR 0004: KubeFleet is not a core dependency; optional integration later

Status: accepted (2026-10-04). Revisit when the frozen "Later" track (fleet resource distribution, add-ons, rollouts)
is unfrozen, which the plan ties to E3 having users.

## Context

[KubeFleet](https://kubefleet.dev/) is a CNCF sandbox project (accepted January 2025, Microsoft-led, the open-source core
of AKS Fleet Manager) that places and rolls out Kubernetes resources across clusters: a hub cluster, a member agent in each
member, `ClusterResourcePlacement` and `MemberCluster` resources, scheduling on labels, capacity and cost, drift detection,
and progressive rollouts with health checks. We asked whether it can be our multi-cluster resource management layer.

Facts checked on 2026-10-04: Apache-2.0 (compatible with our licence); latest release **v0.3.1 (2026-04-17)**, so pre-1.0;
36 contributors, 162 stars, 81 commits in the last quarter, 188 open issues, Go 1.26 and Kubernetes 0.35 libraries. Member
clusters need only **one-way connections from the member to the hub**, so they can stay private.

## Decision

1. **Do not depend on KubeFleet in E1 to E3.**
2. **Plan an optional, read-only integration after E3**: discover `MemberCluster` objects and placement status from a hub the
   customer already runs, and show them as another import source beside cloud-account discovery. No new agent, no new privilege.
3. **Re-evaluate for the frozen track** (fleet upgrades in waves, add-on bundles, drift detection) with a time-boxed spike
   against the criteria below.

## Why not now

| Concern | Detail |
|---|---|
| The hub must be a Kubernetes API server | Member agents talk to the hub's kube-apiserver and use CRDs. Our management plane is deliberately a Postgres-backed HTTP API ([ADR 0001](0001-api-server-shape.md)), so KubeFleet cannot live inside it. It would be a second hub that members must reach and authenticate to, outside our tunnel |
| Two agents with very different power | Our agent is read-only through the `view` role. A KubeFleet member agent applies arbitrary resources, so it is effectively cluster-admin on every member. A compromised hub then compromises the fleet, which raises the value of the target and changes our threat model |
| The RBAC applier we need is small | E2's policy compiler reconciles a handful of `ClusterRole` and `RoleBinding` objects. Building that is far cheaper than adopting a placement system for it |
| Pre-1.0 and documentation gaps | APIs can still change. We could not find member-agent authentication and token-refresh details, a tenancy model (one fleet per hub) or documented scale limits |
| Overlap with choices already made | The plan already names Argo CD or Flux for GitOps ("no new GitOps engine"); KubeFleet would be a third way to push resources |

## Criteria for the later spike

- Member agent footprint and the **exact permissions** it holds on a member (read the manifests, not the docs).
- Whether hub traffic could be carried over our tunnel, or the hub must be exposed to members directly.
- Behaviour with 100 member clusters on one hub; upgrade path and API stability after 1.0.
- How a hub is isolated between tenants if we host it for customers.
- Governance: progress beyond sandbox; release cadence.

## Not checked

The actual permissions of the member agent (source not read), and alternatives that would be the comparison in that spike:
Karmada, Open Cluster Management, Argo CD ApplicationSets/Flux, Cluster API.
