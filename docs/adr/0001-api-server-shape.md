# ADR 0001: API server shape for Phase 0

Status: accepted (2026-10-02). Supersedes nothing. Revisit at the end of Phase 1.

## Context

The spec calls for a plain apiserver on Postgres (not kcp) exposing CRD-like
resources, with the tenant in every path and row. A full `k8s.io/apiserver`
aggregated server brings discovery, OpenAPI, watch, server-side apply and
`kubectl` compatibility, but also a large dependency surface and slow iteration.

## Decision

Phase 0 ships a small `net/http` server (`pkg/server`) with Kubernetes-style
paths and JSON (`/apis/sextant.andean.io/v1alpha1/organizations/{org}/{plural}`),
over a typed registry (`pkg/registry`) and Postgres store (`pkg/storage`).
API types use `metav1.TypeMeta` / `ObjectMeta`, and the registry owns the
rules (spec/labels from clients, status and resourceVersion server-owned,
optimistic concurrency), so those layers survive a later move to the full
apiserver machinery unchanged. Only `pkg/server` would be replaced.

## Consequences

- Not yet `kubectl`-compatible and no watch, discovery or OpenAPI. Watch
  (LISTEN/NOTIFY or JetStream) and OpenAPI generation are explicit follow-ups.
- Authentication/authorization are interfaces (`server.Authenticator`,
  `server.Authorizer`); the default is deny-all. `StaticAuth` is dev-only and
  is refused on non-loopback addresses.
- Organization is the tenant root: it is stored inside its own tenant and its
  name must equal the tenant ID.
