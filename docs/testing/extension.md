# Testing the VS Code extension

## Automated (CI, every push)

| Layer | What it proves | Where |
|---|---|---|
| Unit (vitest) | kubeconfig merge and classification, tags, fleet/audit/status/terminal models, prompt flows | `extension/test/unit` |
| Differential | our merge agrees with real `kubectl config view` (runs where kubectl exists) | `test/unit/differential.test.ts` |
| Canary | no UI text, report or pin file ever contains a secret | `test/canary.ts`, unit and integration |
| Performance | 200 contexts load and render < 500 ms, a keystroke filter < 50 ms, audit of 50 < 2 s | `test/perf` |
| Integration | real VS Code 1.93 (the engine floor): eager activation, `activate()` < 200 ms, tree, status bar, tagging, confirmation, bound terminal (pin file first, read-only, removed on close), shell-integration verification | `test/integration` |
| Package | bundle under 2 MB | `scripts/check-size.mjs` |

CI runs build, typecheck, lint and unit tests on Ubuntu, macOS and Windows; integration runs on all three
(Linux under xvfb). Packaging and the production audit run on Linux only.

## Manual checks (before a release)

Not automated; record the date and result next to each when run.

- [ ] Remote-SSH: kubeconfig is read on the remote host, terminal is bound there.
- [ ] WSL and Dev Containers: same.
- [ ] Cursor and VSCodium (Open VSX build): activation, views and a bound terminal.
- [ ] Every surface in Dark+, Light+, Dark Modern, Light Modern, High Contrast, High Contrast Light.
- [ ] NVDA + VS Code (Windows) and VoiceOver (macOS): a context row reads environment and criticality before platform.
- [ ] A zsh/bash startup file that overwrites `KUBECONFIG`: the terminal re-asserts or shows the warning toast.
- [ ] External `kubectl config use-context <critical>` from another terminal: the toast appears once per session.

## Known limitations

- Bound-terminal verification needs VS Code shell integration (bash, zsh, fish, PowerShell). Other shells get a
  banner warning and no check.
- The budgets in `test/perf` and the 200 ms activation check are wall-clock; measured locally they use a tenth of
  the budget, so a failure on a slow runner is worth investigating, not retrying.
- Packaging (`npm run package`) uses `mkdir -p` and runs on Linux only.
- Windows is covered by CI but has not been exercised by hand; results above are blank until someone does.
- The external-switch toast is covered by unit tests of its decision logic only; the API cannot stub it in the host.
