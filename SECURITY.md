# Security policy

Sextant brokers access to Kubernetes clusters, so we treat security reports seriously.

## Reporting a vulnerability

**Please do not open a public issue or pull request for a vulnerability.**

Use GitHub's private reporting: on the repository, open **Security → Report a vulnerability**. Include what you
found, how to reproduce it, the affected version or commit, and the impact you expect. If you can, include a failing
test; this project's convention is that every guard has one.

You can expect an acknowledgement within 3 working days and a first assessment within 10. We will keep you informed
as we fix it, agree a disclosure date with you (we aim to ship a fix before public disclosure), and credit you
unless you prefer otherwise.

## Scope

In scope: the management plane (`cmd/apiserver`), the agent (`cmd/agent`), enrollment and certificate issuance,
tenant isolation, the tunnel, the Helm charts' defaults, and (when released) the VS Code extension and `kx`.
Particularly interesting: cross-tenant data access, authentication or authorization bypass, ways to make the agent
reach anything but its kube-apiserver, credential or token exposure, and anything that lets a kubeconfig secret leave
the machine.

Out of scope: findings that require already having administrative access to the cluster or database, denial of
service by resource exhaustion without amplification, and issues in third-party dependencies without a demonstrable
impact on Sextant (report those upstream).

## Supported versions

Sextant is pre-1.0. Security fixes land on `main`; once releases exist, the latest release is supported.

## Hardening notes for operators

The development bearer token (`--dev-tenant`) and the local-file root CA are for development and CI only and must
not be used in production; see the README's "What is not built yet".
