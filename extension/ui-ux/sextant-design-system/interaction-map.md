# Interaction map

Each flow lists the trigger, the surface the user lands on, and the command that does the work. Keyboard paths are in the Accessibility section.

## 1. First run

| # | What happens | Surface | Command / API |
|---|---|---|---|
| 1 | Extension activates on startup (`onStartupFinished`), reads `~/.kube/config` and every file in `KUBECONFIG`. Nothing is sent anywhere. | Fleet view shows the 2px progress bar and "Reading kubeconfig files…" | `window.withProgress({location:{viewId:'sextant.fleet'}})` |
| 2a | Contexts found | Fleet view populated; status item shows the current context; activity-bar badge shows expired credentials (1) | `TreeView.badge` |
| 2b | No kubeconfig | Fleet empty state: "No kubeconfig found." + **Open settings** | `viewsWelcome`, `workbench.action.openSettings` → `sextant.kubeconfigPaths` |
| 2c | Some files unreadable | Warning row at the top of the tree; tooltip lists paths | TreeItem with `$(warning)` |
| 3 | Getting Started walkthrough opens once (first install only) | Walkthrough: found → tag → open terminal | `contributes.walkthroughs`, `workbench.action.openWalkthrough` |

## 2. Tagging a cluster

1. Right-click a context → **Tag Cluster…**, or `Sextant: Tag Cluster…` from the palette (asks for the context first), or the walkthrough button.
2. QuickPick 1/3 **Pick an environment**: prod, staging, dev, Custom…
3. QuickPick 2/3 **How careful should Sextant be?** critical / normal (prod preselects critical).
4. QuickPick 3/3 **Apply the same tags elsewhere?** Only this context / all matching `*-prod-*` / `prod-*` / Custom pattern…
5. Saved to `sextant.tags` in user settings. The tree regroups, the row moves under its environment, and if it is the current context the status item turns critical. No toast.

Back (`Alt+←` or the title-bar arrow) returns one step; Escape cancels with nothing saved.

## 3. Opening a terminal on production

1. Trigger: `Ctrl+Enter` / `Cmd+Enter` on a focused context row, the context menu, a click on the status item, or `Sextant: Open Terminal for Cluster…` (`Ctrl+Alt+Shift+T` / `Cmd+Alt+Shift+T`, proposed).
2. From the status item or palette: QuickPick **Open terminal for cluster**, current context pinned first, grouped by environment.
3. The context is critical → confirmation (recommended form: modal QuickPick with **Cancel** focused; see Confirm dialog). Choosing "Open, and don't ask again for this cluster" stores `sextant.confirm.skip: ["prod-eu-1"]`.
4. Terminal opens named `prod-eu-1 · PROD`, `$(warning)` icon in `terminal.ansiRed`, first line: the PROD banner.
5. Shell integration activates → binding verified silently. If it does not activate within 5s → warning toast "Could not confirm this terminal is bound. Run: echo $KUBECONFIG" with **Copy Command** (see Native gaps #12).
6. The Fleet row gains "terminal" in its description.

Non-critical contexts skip step 3.

## 4. External switch to production

1. Something outside Sextant (another terminal, a script) runs `kubectl config use-context prod-eu-1`.
2. Sextant's file watcher sees `current-context` change in the kubeconfig.
3. Status item turns critical: `$(warning) PROD  prod-eu-1` on `statusBarItem.errorBackground`.
4. Because the new context is critical, a warning toast appears: "Your kubectl context is now prod-eu-1 (production). It was changed outside Sextant." **Open bound terminal** · **Dismiss**.
5. Open bound terminal focuses the existing bound terminal for prod-eu-1, or opens one (with the confirmation).
6. Switch to a non-critical context: status item updates in place, no toast.

## 5. Audit to fix

1. The activity-bar badge (1) or the walkthrough leads to **Credential Audit** (collapsed by default, under Fleet).
2. Groups by risk: Expired (1), Long-lived credentials (4), Expiring within 30 days (none), Short-lived or brokered (5, collapsed).
3. Expand a row → one child row with the remediation hint (e.g. "Static token: replace with an exec plugin"). Hover shows the same hint.
4. Right-click → **Reveal Kubeconfig File** opens the source file at the context's line in the editor; the user makes the change. Sextant never edits kubeconfig in E1.
5. On save, the file watcher refreshes both views; the row moves to its new group.
6. **Copy report** puts the Markdown on the clipboard; **Open report** opens an untitled `sextant-audit-YYYY-MM-DD.md` and its preview side by side.
