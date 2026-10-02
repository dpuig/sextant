# ADR 0002: Tunnel approach

Status: accepted for fork; transport divergence **proposed, needs owner sign-off**. 2026-10-02.

## Context

The spec says to fork/vendor Rancher `remotedialer` rather than write a tunnel,
and the Phase 0 gates require: agent reconnect within 30 s (G2) and added p95
latency under 50 ms (G3). We spiked `remotedialer` v0.6.1 on loopback
(`make bench-tunnel`, `spikes/tunnel/`).

## Measurements

| Check | Result | Gate |
|---|---|---|
| Added p95 latency, keep-alive connection | 20-80 µs | < 50 ms |
| Added p95 latency, new connection per request | 150-300 µs | < 50 ms |
| 64 concurrent clients, one tunnel | ~45k req/s, p95 ~2 ms | n/a |
| Reconnect after management-plane restart (TCP closed), 10 runs | ~105 ms | < 30 s |
| Reconnect after **silent** partition (no FIN/RST), blackholed conn | **60 s** | < 30 s |
| `go test -race` | **data race inside the library** | clean |

Loopback measures protocol overhead only. Real added latency also includes the
extra network hop client -> management plane, so G3 must be re-measured in a
real region (kind e2e in CI will not show it).

## Findings that require patches

1. **Dead-peer detection is 60 s and a `const`** (`PingWaitDuration`, `PingWriteInterval`
   in `types.go`). A load-balancer failover or NAT expiry that drops silently
   takes 60 s to notice, failing G2 for that class of failure. Patch: make both
   configurable; target ping 5 s / wait 15 s.
2. **Data race** between `Session.startPings` (writes `pingCancel`) and
   `stopPings` (reads it, via `Close`). Patch: guard with the session mutex or
   `sync.Once`. Also upstream the fix.
3. **`ClientConnect` sleeps a fixed 5 s on error.** Not used: the agent calls
   `ConnectToProxy` in its own jittered-backoff loop (100 ms doubling to a 5 s
   cap, reset when a session is established). Worst-case delay once the
   management plane is back is therefore about 5 s.

## Decision

Vendor `remotedialer` into `third_party/remotedialer` with an `UPSTREAM.md`
recording the base commit and each local patch (1 and 2 above), keep its
Apache-2.0 LICENSE, and wrap it in `pkg/tunnel` so the rest of the codebase
never imports it directly. Add a CI test for each patch.

## Proposed divergence from the spec (needs sign-off)

The spec says "outbound gRPC over mTLS with HTTP/2 multiplexing".
`remotedialer` is **WebSocket (HTTP/1.1 upgrade) with its own stream
multiplexing**. We recommend keeping it: mTLS applies the same way (TLS config
on the dialer and server), multiplexing is provided by the library, and
WebSocket traverses corporate HTTP proxies, which matters for the "outbound
only, behind NAT" customers. Switching to gRPC would mean writing the tunnel,
which the spec says not to do. If gRPC/HTTP2 is a hard requirement, say so and
we re-plan.
