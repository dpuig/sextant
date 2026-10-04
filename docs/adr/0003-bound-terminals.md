# ADR 0003: Bound terminals use a generated one-context kubeconfig

Status: **superseded by [ADR 0005](0005-pin-file-bound-terminals.md)** (2026-10-04). Kept for the reasoning; the mechanism changed from a generated one-context kubeconfig to a pin file that holds only `current-context`.
Spike: [extension/test/spike/bound-terminal.sh](../../extension/test/spike/bound-terminal.sh) (throwaway; replaced by the
real tests in Tasks 9 and 10 of the [E1 plan](../specs/vscode-extension-plan.md)).

## Context

The extension's headline safety feature is a terminal that stays on one cluster no matter what the global
`current-context` later becomes (the classic "ran it on the wrong cluster" mistake). E1 must stay read-only on the
user's kubeconfig, so it cannot change `current-context` itself. We needed a mechanism that works for `kubectl`, `helm`
and other tools alike, and we needed to know what it costs.

## Decision

For each bound terminal the extension writes a **minimal kubeconfig** containing exactly one context with its cluster
and user, and starts the terminal with `KUBECONFIG` pointing at that file. The file lives in a per-user private
directory (VS Code's `globalStorageUri`, not the shared system temp dir), directory mode 0700 and file mode 0600, is
deleted when the terminal closes and on deactivate, and any leftovers are swept at the next start.

## What the spike proved (kind, two clusters, `kubectl` and `helm`)

| Property | Result |
|---|---|
| Bound shell stays on its cluster while the global context is changed and changed back | **Holds**: `kubectl get nodes` and `helm list` reached the bound cluster every time |
| `helm` honours it | **Holds** (a release present only in cluster B is visible to the bound shell and invisible globally) |
| Other contexts are reachable from the bound shell | **No**: `kubectl --context <other>` fails ("not found"), which is the safe default |
| Exec-plugin users (EKS, GKE, AKS, `kx`, OIDC login) need a secret copied | **No**: the minimal file keeps only the `exec` stanza |
| Certificate and token users need their credential copied | **Yes** (inline `client-key-data` / `token`): the cost of this approach |
| File and directory modes | 0600 / 0700 hold |

## The failure mode: shell startup files override KUBECONFIG

The spike also tested what happens when the user's `~/.zshrc` or `~/.bashrc` contains the very common
`export KUBECONFIG=~/.kube/config`. **Both zsh and bash replaced the injected value after startup.** Setting
`KUBECONFIG` through the terminal's environment alone would therefore silently fail, leaving the terminal on whatever
the user's rc file selects, with no sign that the safety feature is not working.

**Mitigation (required, and the reason this ADR is conditional).** After the shell has started, the extension sends a
command that re-asserts the path, using the syntax of the user's shell (`export` for bash/zsh/sh, `set -gx` for fish,
`$env:KUBECONFIG=` for PowerShell, `set` for cmd), and then verifies it. Re-exporting after startup restored the bound
path in the spike (zsh). Task 10 must include an integration test in which the rc file overrides `KUBECONFIG`, and the
terminal must **announce when it cannot verify** (unknown shell) instead of pretending to be bound. If the re-export
proves unreliable in a real VS Code terminal (it is queued input, so it can race a slow rc file), the fallback is a
generated launcher script used as the terminal's shell that sources the user's rc and then sets the variable.

## Consequences

- Copying inline credentials to disk is unavoidable for certificate and token users. Mitigations: private 0700
  directory, 0600 file, deletion on close and deactivate, a startup sweep for crashes, and the canary tests (the path and
  contents never appear in logs or UI). A crash can leave a file until the next sweep; that window is accepted and
  documented. Users on exec-plugin authentication (the recommended end state) copy nothing.
- Binding is by environment, so tools that ignore `KUBECONFIG` are not covered.

## What the spike did NOT prove

- **VS Code terminals themselves.** The shell-level behaviour is proven; the terminal's environment injection, the
  `sendText` race and the coloured tab are Task 10's tests.
- **Windows.** POSIX modes do not apply; the per-user `globalStorageUri` inherits the profile's private ACLs. Needs
  verifying on a Windows runner (Task 12), as do PowerShell and cmd syntax.
- **`k9s` and other TUIs** (not installed on the spike machine); they read `KUBECONFIG` like `kubectl`, but this is
  untested. **fish** and **PowerShell** were not available either.

## Alternatives rejected

| Alternative | Why not |
|---|---|
| A shell function wrapping `kubectl --context X` | Covers one binary; `helm`, `k9s` and scripts bypass it |
| Changing the global `current-context` | A write to the user's kubeconfig (E1 is read-only), and it is the very thing that causes the mistake |
| Pointing `KUBECONFIG` at the user's full file and relying on `--context` | Does not pin anything; any later command can name another context |
| Per-terminal `HOME` override | Breaks every other tool's configuration and the user's shell setup |
