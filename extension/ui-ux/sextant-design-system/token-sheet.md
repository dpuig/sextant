# Token sheet

Every colour on every surface, mapped to its VS Code theme token, and every icon to its Codicon. Values per theme are in `tokens.json` (Colors view).

## Colours by surface

| Surface | Element | Token |
|---|---|---|
| Activity bar | Ground / active glyph / inactive glyph | `activityBar.background` / `activityBar.foreground` / `activityBar.inactiveForeground` |
| Activity bar | Active indicator · expired-count badge | `activityBar.activeBorder` · `activityBarBadge.background` |
| Fleet, Audit | View ground · titles | `sideBar.background` · `sideBarTitle.foreground` |
| Fleet, Audit | Row label · description · neutral icons | `foreground` · `descriptionForeground` · `icon.foreground` |
| Fleet, Audit | Selected row · its text · focus outline | `list.activeSelectionBackground` · `list.activeSelectionForeground` · `list.focusOutline` |
| Fleet, Audit | Unfocused selection · hover · indent guides | `list.inactiveSelectionBackground` · `list.hoverBackground` · `tree.indentGuidesStroke` |
| Fleet | prod / staging / dev / Untagged group icon | `sextant.env.prodForeground` / `sextant.env.stagingForeground` / `sextant.env.devForeground` / `sextant.env.untaggedForeground` |
| Fleet | Current context check | `sextant.currentContextForeground` |
| Fleet | Unreadable-files row icon | `editorWarning.foreground` |
| Fleet | Loading bar | `progressBar.background` |
| Fleet | Empty-state button · link | `button.background` + `button.foreground` · `textLink.foreground` |
| Audit | Expired / Long-lived / Expiring / Short-lived icons | `sextant.risk.expiredForeground` / `sextant.risk.longLivedForeground` / `sextant.risk.expiringForeground` / `sextant.risk.shortLivedForeground` |
| Tooltips | Ground · border | `editorHoverWidget.background` · `editorHoverWidget.border` |
| Context menu | Ground · text · highlight · separator | `menu.background` · `menu.foreground` · `menu.selectionBackground` + `menu.selectionForeground` · `menu.separatorBackground` |
| Status bar | Ground · normal item text | `statusBar.background` · `statusBar.foreground` |
| Status bar | Critical item fill · text | `statusBarItem.errorBackground` · `statusBarItem.errorForeground` |
| Status bar | Expired-credential item (reserved) | `statusBarItem.warningBackground` · `statusBarItem.warningForeground` |
| Status bar | Hover · HC border | `statusBarItem.hoverBackground` · `statusBar.border` |
| Terminal | Panel ground · text · panel titles | `panel.background` · `terminal.foreground` · `panelTitle.activeForeground`, `panelTitle.inactiveForeground`, `panelTitle.activeBorder` |
| Terminal | prod / staging / dev tab icon | `terminal.ansiRed` / `terminal.ansiYellow` / `terminal.ansiGreen` |
| Terminal | PROD chip (ANSI 1;97;41) · WARNING chip (ANSI 1;30;43) | `terminal.ansiBrightWhite` on `terminal.ansiRed` · `terminal.ansiBlack` on `terminal.ansiYellow` |
| QuickPick, InputBox | Ground · shadow · title bar | `editorWidget.background` · `widget.shadow` · `quickInputTitle.background` |
| QuickPick, InputBox | Field · field border · focus | `input.background` · `input.border` · `focusBorder` |
| QuickPick | Separator label · line | `pickerGroup.foreground` · `pickerGroup.border` |
| InputBox | Live count (Info) · no match (Warning) | `inputValidation.infoBorder` · `inputValidation.warningBorder` |
| Dialog | Ground · icon · buttons | `editorWidget.background` · `editorWarning.foreground` · `button.*`, `button.secondary*` |
| Toast | Ground · warning icon · actions | `editorWidget.background` (notifications.background) · `editorWarning.foreground` · `button.*`, `button.secondary*` |
| Walkthrough | Page · step card · links | `editor.background` · `welcomePage.tileBackground` · `textLink.foreground` |
| Report | Preview ground · text · code spans | `editor.background` · `editor.foreground` · `textCodeBlock.background` |
| High Contrast | Every surface border · active outline | `contrastBorder` · `contrastActiveBorder` |

## Icons

| Meaning | Codicon | Where |
|---|---|---|
| Sextant container | custom glyph (24×24, stroke 1.6) | Activity bar only |
| Filter / Clear filter | `search` / `search-stop` | Fleet title |
| Refresh · Collapse all | `refresh` · `collapse-all` | Fleet title |
| Copy report · Open report | `copy` · `open-preview` | Audit title |
| prod (critical) | `warning` | Group row, QuickPick, terminal tab, status item, dialog, toast |
| staging · dev · Untagged | `beaker` · `code` · `question` | Group rows, Tag QuickPick |
| Amazon EKS, Google GKE, Azure AKS | `cloud` | Platform rows |
| OpenShift | `server` | Platform row |
| kind, minikube, Docker Desktop | `vm` | Platform rows |
| Other | `symbol-misc` | Platform row |
| Current context · other context | `pass-filled` · `circle-large-outline` | Context rows, Open terminal QuickPick |
| Detail: Server · Namespace · Credential · Expires · Source file | `globe` · `symbol-namespace` · `key` · `calendar` · `file` | Detail child rows |
| Unreadable files | `warning` | Warning row |
| Expired · Long-lived · Expiring · Short-lived | `error` · `warning` · `watch` · `verified` | Audit groups and rows |
| Remediation hint | `lightbulb` | Audit child row |
| Values never shown | `eye-closed` | Audit message |
| Status: normal · critical · none | `server-environment` · `warning` · `circle-slash` | Status bar |
| Bound terminal (non-critical) | `terminal` | Terminal tab |
| Tag · Custom… · Back | `tag` · `edit` · `arrow-left` | QuickPicks |
| Cancel · Confirm option | `close` · `check` | Confirm QuickPick |
| Walkthrough done · to do | `pass-filled` · `circle-large-outline` | Rendered by VS Code |

## Contributed colours (package.json)

```json
"contributes": { "colors": [
  { "id": "sextant.env.prodForeground", "description": "Prod environment icon.",
    "defaults": { "dark": "list.errorForeground", "light": "list.errorForeground", "highContrast": "errorForeground", "highContrastLight": "errorForeground" } },
  { "id": "sextant.env.stagingForeground", "description": "Staging environment icon.",
    "defaults": { "dark": "list.warningForeground", "light": "list.warningForeground", "highContrast": "editorWarning.foreground", "highContrastLight": "editorWarning.foreground" } },
  { "id": "sextant.env.devForeground", "description": "Dev environment icon.",
    "defaults": { "dark": "charts.green", "light": "charts.green", "highContrast": "charts.green", "highContrastLight": "charts.green" } },
  { "id": "sextant.env.untaggedForeground", "description": "Untagged environment icon.",
    "defaults": { "dark": "icon.foreground", "light": "icon.foreground", "highContrast": "icon.foreground", "highContrastLight": "icon.foreground" } },
  { "id": "sextant.risk.expiredForeground", "description": "Expired credential icon.",
    "defaults": { "dark": "list.errorForeground", "light": "list.errorForeground", "highContrast": "errorForeground", "highContrastLight": "errorForeground" } },
  { "id": "sextant.risk.longLivedForeground", "description": "Long-lived credential icon.",
    "defaults": { "dark": "list.warningForeground", "light": "list.warningForeground", "highContrast": "editorWarning.foreground", "highContrastLight": "editorWarning.foreground" } },
  { "id": "sextant.risk.expiringForeground", "description": "Credential expiring within 30 days.",
    "defaults": { "dark": "editorInfo.foreground", "light": "editorInfo.foreground", "highContrast": "editorInfo.foreground", "highContrastLight": "editorInfo.foreground" } },
  { "id": "sextant.risk.shortLivedForeground", "description": "Short-lived or brokered credential.",
    "defaults": { "dark": "charts.green", "light": "charts.green", "highContrast": "charts.green", "highContrastLight": "charts.green" } },
  { "id": "sextant.currentContextForeground", "description": "Check mark on the current context.",
    "defaults": { "dark": "charts.green", "light": "charts.green", "highContrast": "charts.green", "highContrastLight": "charts.green" } }
]}
```
