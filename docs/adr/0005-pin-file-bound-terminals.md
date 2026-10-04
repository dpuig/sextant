# ADR 0005: Bound terminals use a read-only pin file, verified through shell integration

Status: accepted (2026-10-04). Supersedes [ADR 0003](0003-bound-terminals.md).
Spike: [extension/test/spike/bound-terminal.sh](../../extension/test/spike/bound-terminal.sh).

## Context

A bound terminal must stay on one cluster even if the global `current-context` changes, without Sextant writing to the
user's kubeconfig (E1 is read-only) and without copying any credential.

## Decision

- Each terminal gets a small file containing only `apiVersion`, `kind` and `current-context: "<name>"`. It is placed
  **first** in that terminal's `KUBECONFIG`, followed by the user's own files in their original order. `kubectl` and
  `helm` take `current-context` from the first file that sets it and everything else from the merge, so the terminal
  stays on its cluster and no credential is duplicated.
- The file is mode 0400, in a 0700 directory under the extension's global storage. `kubectl config use-context` inside
  the terminal fails instead of silently moving it. The file is deleted when its terminal closes (and the directory is
  swept at start-up; terminals are created transient so none is revived pointing at a deleted pin).
- Shell start-up files (`.zshrc`, `.bashrc`) can overwrite `KUBECONFIG`. Where VS Code's shell integration is
  available (bash, zsh, fish, PowerShell) Sextant runs `echo $KUBECONFIG` in the terminal. It is visible on purpose, as
  the proof. If the pin is not the first entry it sets `KUBECONFIG` again once, and if it still cannot confirm within 5
  seconds it shows a warning toast with a Copy Command action. Shells without integration (sh, cmd.exe, others) get a
  WARNING line in the banner at creation.

## Consequences

- No silent verification: the check is visible text in the terminal. A native API cannot do it quietly.
- The banner cannot be edited after creation, hence the toast for late failures.
- Verified on real kubectl and helm in the spike, and by the integration suite in a real VS Code 1.93 with zsh.
- Not covered: tools that ignore `KUBECONFIG`, and a user who edits `KUBECONFIG` by hand inside the terminal.
