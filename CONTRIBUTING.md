# Contributing to Sextant

Thanks for helping. Sextant is licensed under [Apache-2.0](LICENSE); contributions are accepted under the same
licence (inbound = outbound) and need a **Developer Certificate of Origin** sign-off.

## Sign your commits (DCO)

By adding `Signed-off-by: Your Name <you@example.com>` to a commit you certify the
[DCO 1.1](https://developercertificate.org/): that you wrote the change or have the right to submit it under
Apache-2.0. Git does it for you:

```bash
git commit -s -m "Fix tunnel reconnect backoff"
```

CI rejects pull requests with unsigned commits. Forgot? `git rebase --signoff origin/main` then force-push your branch.

## Setting up

Requires Go 1.26+. Docker or Podman for the integration tests and images; `kind`, `helm` and `kubectl` for end-to-end.

```bash
make build
make lint
make test-integration     # Postgres in a container + the storage/API tests
make test                 # unit tests, race detector on
make e2e                  # two kind clusters, both charts, kubectl through the tunnel (slow)
```

The repository layout is described in the [README](README.md#repository-layout); direction and priorities are in the
[implementation plan](Implementation%20Plan%20Multi-Cluster%20Control%20Plane.md).

## What we expect in a pull request

- **Tests with the change.** A bug fix starts with a test that fails without the fix.
- **Security-relevant changes prove their test has teeth.** If your change adds or alters a guard (authentication,
  authorization, tenant isolation, certificate or token handling, allow-lists, header stripping, mount permissions),
  show the guarding test failing when the guard is removed, and say so in the PR description.
- **Real dependencies over mocks** where it matters: Postgres for storage and the API, kind for end-to-end.
- **`make lint test` is green**, and for chart or deployment changes `make e2e` too.
- **Docs next to the code.** Update the relevant spec or README; record a non-trivial decision as an ADR in
  [docs/adr](docs/adr). Match surrounding style and comment density; comments explain *why*.
- **Small, focused commits** with messages that say what changed and why.

### Areas that get extra review

`pkg/pki`, `pkg/tenancy`, `pkg/storage` (and its migrations), `pkg/enroll`, `pkg/tunnel`, `pkg/agent` (the
kube-apiserver proxy), `third_party/`, and the Helm charts' security contexts. Changes there are reviewed line by
line, and new migrations are forward-only.

### Dependencies

Prefer the standard library. A new dependency needs a reason in the PR and must be permissively licensed
(`make licenses` fails on copyleft or unrecognised licences and regenerates
[docs/third-party-licenses.md](docs/third-party-licenses.md)). Changes to the vendored fork in `third_party/` are
recorded in its `UPSTREAM.md` with a test, and upstreamed where practical.

## Reporting bugs and security issues

Bugs: open an issue with what you did, what you expected, and what happened (versions, logs without secrets).
**Security vulnerabilities: do not open a public issue**; see [SECURITY.md](SECURITY.md).

## Conduct

Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md).
