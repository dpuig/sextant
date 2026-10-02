# End-to-end tests

`make e2e` runs the Phase 0 exit gates against two real kind clusters:
`sextant-e2e-mp` (management plane + Postgres) and `sextant-e2e-wl` (a workload
cluster running the agent). Needs `kind`, `helm`, `kubectl` and a container
runtime (`CONTAINER=podman` by default, `docker` in CI). `SEXTANT_E2E_KEEP=1`
leaves the clusters up for inspection; on any failure the test dumps pods,
events and logs from both clusters before deleting them.

## What it proves

| Gate | Check | Last local result |
|---|---|---|
| G1 | `helm install` of the agent chart enrolls with a one-time token and `Cluster.status.connected` becomes true; the agent exposes no ports/Services; credentials land in its Secret and the chart's `keep` annotation survives | connected ~1 s after install; all assertions pass |
| G1 | agent RBAC is get+update on its own credentials Secret and nothing else | pass |
| G3 | `kubectl get pods -A` through `.../clusters/wl/proxy` lists the workload cluster's pods; a write is refused (agent is bound to `view`) | pass |
| G3 | added p95 latency over a direct path, 1000 requests each | ~0.4-0.6 ms on this rig |
| G2 | longest data-plane gap across 3 management-plane rollouts (probe every 50 ms until the old pod is gone) | ~155 ms (was ~10 s before the shutdown-delay fix) |
| - | a replacement agent pod comes up from its stored credentials with the token deleted | pass (asserted from the pod's own log) |

## What it does not prove

- **G3 is a same-host measurement.** Network RTT is ~0, so this bounds protocol
  overhead only. The 50 ms gate must be re-measured with the management plane and
  a cluster in a real region.
- **No real NAT.** Both clusters share a container network. The agent is
  outbound-only by construction (no ports, no Service, asserted above), but a NAT
  traversal has not been exercised.
- **Not run in CI yet.** The `e2e` job in `.github/workflows/ci.yml` is untested
  (it uses docker + kind on a GitHub runner); expect a first-run fix or two.
- The database role `sextant_app` is created out of band (as it would be in a real
  deployment), and the dev bearer token stands in for the identity broker (Phase 2).

## Findings this suite produced

1. The apiserver pod could not read its Secret-mounted keys as a non-root user
   (`permission denied`): needed `fsGroup` + mode 0440, and `localfile.Load` to accept
   group-read.
2. A management-plane rolling restart left the data plane down ~10 s: Kubernetes
   removes a terminating pod from the Service asynchronously, so the agent's first
   reconnect hit a dead endpoint and sat in a 10 s handshake timeout. Fixed with
   `--shutdown-delay` (keep serving, then drain tunnels, then stop) and a 5 s TCP
   connect timeout in the agent. An earlier version of the G2 check reported "0 s"
   because it declared the rollout finished too early; the check now measures the
   longest gap until the old pod is actually gone.
