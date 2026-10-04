# Accessibility

## Contrast (VS Code default themes)

Measured with the WCAG 2 formula on the values in `tokens.json`; alpha colours are composited over their ground. Text needs 4.5:1, icons, focus rings and other meaningful marks 3:1. ✗ marks a miss.

| Use | Pair | Dark+ | Light+ | HC | Needs |
|---|---|---|---|---|---|
| Tree labels | `foreground` on `sideBar.background` | 9.5 | 5.6 | 21.0 | 4.5:1 |
| Row descriptions, Filtered message | `descriptionForeground` on `sideBar.background` | 5.4 | 4.4 ✗ | 10.0 | 4.5:1 |
| Selected row | `list.activeSelectionForeground` on `list.activeSelectionBackground` | 12.0 | 6.1 | 21.0 | 4.5:1 |
| QuickPick, toast, dialog body | `foreground` on `editorWidget.background` | 9.5 | 5.6 | 18.5 | 4.5:1 |
| QuickPick descriptions | `descriptionForeground` on `editorWidget.background` | 5.4 | 4.4 ✗ | 9.4 | 4.5:1 |
| QuickPick separator labels | `pickerGroup.foreground` on `editorWidget.background` | 5.0 | 5.2 | 18.5 | 4.5:1 |
| Normal status item | `statusBar.foreground` on `statusBar.background` | 4.5 | 4.5 | 21.0 | 4.5:1 |
| Critical status item | `statusBarItem.errorForeground` on `statusBarItem.errorBackground` | 5.5 | 12.8 | 21.0 | 4.5:1 |
| Primary buttons | `button.foreground` on `button.background` | 6.4 | 4.5 | 21.0 | 4.5:1 |
| Open settings link | `textLink.foreground` on `sideBar.background` | 5.0 | 5.1 | 8.0 | 4.5:1 |
| Terminal banner | `terminal.foreground` on `panel.background` | 10.4 | 12.6 | 21.0 | 4.5:1 |
| PROD chip in banner (before minimumContrastRatio) | `terminal.ansiBrightWhite` on `terminal.ansiRed` | 4.1 ✗ | 2.1 ✗ | 5.8 | 4.5:1 |
| WARNING chip in banner | `terminal.ansiBlack` on `terminal.ansiYellow` | 15.5 | 6.8 | 12.3 | 4.5:1 |
| Report body | `editor.foreground` on `editor.background` | 11.2 | 21.0 | 21.0 | 4.5:1 |
| Focus outline | `focusBorder` on `sideBar.background` | 3.6 | 3.0 | 8.2 | 3:1 |
| Prod icon | `sextant.env.prodForeground` on `sideBar.background` | 6.1 | 6.5 | 8.6 | 3:1 |
| Staging icon | `sextant.env.stagingForeground` on `sideBar.background` | 6.6 | 5.2 | 14.8 | 3:1 |
| Dev icon | `sextant.env.devForeground` on `sideBar.background` | 8.4 | 3.9 | 11.5 | 3:1 |
| Expiring icon | `sextant.risk.expiringForeground` on `sideBar.background` | 5.0 | 3.2 | 6.8 | 3:1 |
| Current check | `sextant.currentContextForeground` on `sideBar.background` | 8.4 | 3.9 | 11.5 | 3:1 |
| Toast/dialog warning icon | `editorWarning.foreground` on `editorWidget.background` | 6.6 | 2.8 ✗ | 13.0 | 3:1 |
| Prod terminal tab icon | `terminal.ansiRed` on `panel.background` | 3.2 | 5.1 | 3.6 | 3:1 |
| Staging terminal tab icon | `terminal.ansiYellow` on `panel.background` | 12.3 | 3.1 | 12.3 | 3:1 |
| Dev terminal tab icon | `terminal.ansiGreen` on `panel.background` | 6.7 | 2.6 ✗ | 9.7 | 3:1 |

**What the misses mean, and what Sextant does about them**

- `descriptionForeground` in Light+ (4.4:1) and `editorWarning.foreground` icons in Light+ (2.8:1) are VS Code's own values; Sextant cannot change them without overriding the user's theme. Nothing Sextant shows relies on them alone: every description repeats information found in the label, tooltip or accessibility label, and every warning icon sits beside the word that names the warning.
- Terminal tab icons in Light+ (`terminal.ansiGreen` 2.6:1) and the PROD chip text (`ansiBrightWhite` on `ansiRed`, 4.1 / 2.1:1) are theme ANSI colours. The tab name carries the environment in text (`{name} · dev`), and VS Code's `terminal.integrated.minimumContrastRatio` (default 4.5) adjusts terminal text automatically. The banner states "prod, critical" in default-colour bold text as well.
- The terminal banner avoids coloured text on the default ground; it uses chips (background colour + contrasting text) because `ansiRed` text on Dark+ is only 3.2:1.
- All Sextant-contributed colours pass 3:1 as icons in all three themes.

## Meaning carried besides colour

| Signal | Colour | Also carried by |
|---|---|---|
| Production / critical | `sextant.env.prodForeground`, `statusBarItem.errorBackground`, `terminal.ansiRed` | `$(warning)` icon; the words "critical" (tree), "PROD" (status bar, terminal tab name, banner chip), "production" (dialog, toast) |
| Staging / dev / Untagged | env colours | `$(beaker)` / `$(code)` / `$(question)` and the group label |
| Current context | `sextant.currentContextForeground` | `$(pass-filled)` vs `$(circle-large-outline)` (filled vs empty shape) and the word "current" |
| Bound terminal | none | the word "terminal" in the description; "terminal open" in the QuickPick |
| Credential risk | risk colours | group label; distinct icons `$(error)` `$(warning)` `$(watch)` `$(verified)`; "expired" / "long-lived" in descriptions |
| Unreadable files | `editorWarning.foreground` | `$(warning)` + full sentence |
| HC selection and focus | none | `contrastActiveBorder` outline (VS Code) |

## Screen-reader labels

Set `TreeItem.accessibilityInformation` on every row (role `treeitem`) and `StatusBarItem.accessibilityInformation`.

| Element | Label |
|---|---|
| Environment group | `prod environment, critical, 3 contexts` · `staging environment, 2 contexts` |
| Platform group | `Amazon EKS, 2 contexts` |
| Context row | `prod-eu-1, prod, critical, Amazon EKS, aws IAM, terminal open` · `staging-eu-1, current context, staging, Google GKE, gcp IAM` |
| Detail row | `Server: api.ocp.example.com:6443` · `Expires: expired 2 March 2026` |
| Warning row | `Warning: 2 kubeconfig files could not be read. Hover or press Ctrl+K Ctrl+I for the paths.` |
| Audit group | `Expired credentials, 1 context` · `Expiring within 30 days, none` |
| Audit row | `minikube, expired credential, client certificate, expired 2 March 2026. Expand for the suggested fix.` |
| Audit hint row | `Suggested fix: Static token: replace with an exec plugin` |
| Status item | `Kubernetes context prod-eu-1, prod, critical. Activate to open a terminal for a cluster.` · `No Kubernetes cluster. Activate to open settings.` |
| Terminal | name `prod-eu-1 · PROD`; banner is real terminal text, readable in the terminal's accessible buffer (Terminal: Focus Accessible Buffer, `Alt+F2`) |
| Toast | VS Code announces the message; actions are buttons |
| Fleet view message | `Filtered: gke, 2 of 10` is announced when it changes (`TreeView.message`) |

Dates in labels are spelled out ("2 March 2026"); the visual description uses ISO dates.

## Keyboard paths

| Action | Path |
|---|---|
| Open the Sextant container | `Sextant: Focus on Fleet View` from the palette, or bind `workbench.view.extension.sextant` |
| Move in the tree | `↑ ↓`, `←` collapse / go to parent, `→` expand, `Home` / `End`, type-to-find (`Ctrl+Alt+F` / `Cmd+Alt+F` built-in) |
| Read a row's tooltip | `Ctrl+K Ctrl+I` / `Cmd+K Cmd+I` (Show Hover) on the focused row |
| Context menu | `Shift+F10` or the Menu key |
| Open terminal for focused context | `Ctrl+Enter` / `Cmd+Enter` |
| Open terminal for any cluster | `Ctrl+Alt+Shift+T` / `Cmd+Alt+Shift+T` (proposed) or palette → type, `↑ ↓`, `Enter` |
| Confirm on critical | QuickPick form: Cancel is focused; `↓` then `Enter` to open; `Escape` cancels |
| Tag a cluster | context menu → Tag Cluster…, or palette; each step `↑ ↓ Enter`; `Alt+←` back; `Escape` cancel |
| Filter | Fleet title button via `Tab` to the view header, or palette Filter Clusters…; `Escape` in the tree clears |
| Copy context name | `Ctrl+C` / `Cmd+C` in the tree |
| Toast actions | palette → Notifications: Focus Notification Toast, then `Tab` to the buttons |
| Status item | `F6` (Focus Next Part) until the status bar, then `←/→` and `Enter` |
| Audit report | palette Open Audit Report / Copy Audit Report |
| Walkthrough | palette Sextant: Get Started; `Tab` through steps, `Enter` on buttons |

## Checks before release

- Run every surface in Dark+, Light+, Dark Modern, Light Modern, High Contrast and High Contrast Light.
- NVDA + VS Code on Windows and VoiceOver on macOS: the context row label must name environment and criticality before platform.
- Turn on `editor.accessibilitySupport: on`; confirm the PROD banner is read once, not repeated.
- With colour removed (greyscale filter), each signal in the table above is still identifiable.
