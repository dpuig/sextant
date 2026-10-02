# remotedialer (vendored fork)

- Upstream: https://github.com/rancher/remotedialer
- Base: v0.6.1 (Apache-2.0, see LICENSE)
- Scope: root package and `metrics/` only (no `client/`, `server/`, `dummy/` binaries).
- Import path rewritten to `github.com/dpuig/sextant/third_party/remotedialer`.
- Only `pkg/tunnel` may import this package (see ADR 0002).

## Local patches

Each patch has a test in this directory (`sextant_patch_test.go`).

1. **Configurable liveness timing.** Upstream hard-codes `PingWriteInterval` (5 s)
   and `PingWaitDuration` (60 s) as consts, so a silent partition takes 60 s to
   detect. They are now package variables, defaulting to 5 s / 15 s.
2. **Data race in `Session.Close`.** `startPings` wrote `pingCancel` while
   `stopPings` read it (found with `go test -race`). Guarded by a mutex.

3. **Data race on `connection.err`.** `doTunnelClose` and `Write` read/wrote it from
   different goroutines (tunnel disconnect vs. pipe teardown). Guarded by a mutex.
4. **`Server.Disconnect(clientKey)`.** Closes a client's live sessions so credential
   revocation takes effect immediately, not at the next natural disconnect.

5. **`Server.OnSessionChange`.** A callback fired after a client session is added or
   removed, so the management plane can record cluster connectivity.

Upstream these when practical; drop the patch when upstream ships the fix.
