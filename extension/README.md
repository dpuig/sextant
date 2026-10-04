# Sextant for VS Code

See every Kubernetes cluster you run, tell production from staging at a glance, and work in terminals that cannot
drift to the wrong cluster.

> **Status: pre-release scaffold.** Features land milestone by milestone; see the repository's
> [implementation plan](https://github.com/dpuig/sextant/blob/main/docs/specs/vscode-extension-plan.md).

Sextant treats every kubeconfig as a secret: it reads them locally, shows classification and never values, and makes
no network connections.

Licensed under [Apache-2.0](LICENSE).
