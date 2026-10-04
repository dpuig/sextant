Sextant is a VS Code extension that reads local kubeconfig files and answers three questions for an engineer who works across many clusters: what do I have, which one is production, and which one is this terminal pointed at. This system covers the E1 UI. Every surface is a native VS Code component; the previews are specs of those components, not a webview to build.

## Principles

1. **Borrow everything.** Use VS Code's own tree views, status bar, QuickPick, InputBox, modal dialog, notifications, terminal tabs, walkthroughs and Markdown preview. No webviews, no custom chrome, no gradients, no illustrations except the activity-bar glyph.
2. **Colour is never alone.** Every environment or risk signal is icon + word + colour. If you remove the colour, the meaning must survive (prod is `$(warning)` + "critical", expired is `$(error)` + "Expired").
3. **Quiet until it matters.** Calm and neutral for staging, dev and untagged. Production is the one loud thing: the red status item, the warning icon, the confirmation.
4. **Theme tokens, never hex.** Reference every colour as a `ThemeColor` id. The hex values in `tokens.json` are VS Code's own defaults, recorded only to draw specs and check contrast.
5. **Local and secret-free.** No network calls. Never display a token, key, password or certificate body, anywhere, including tooltips, logs and the report. Say so where people look for it ("Values are never shown.").

## Voice

- Calm, precise, plain. Short declarative sentences. State the fact, then the consequence: "prod-eu-1 is tagged critical. Commands here affect real users."
- Sentence case for messages and tree labels; Title Case for commands and buttons in the Command Palette (`Sextant: Open Terminal for Cluster…`), as VS Code does.
- Name the context exactly as kubeconfig spells it. Never shorten `prod-eu-1` to "EU prod".
- Use "context" for a kubeconfig entry and "cluster" in user-facing actions where the user thinks of a cluster ("Open terminal for cluster").
- An ellipsis `…` ends any command that asks for more input (Tag Cluster…, Filter Clusters…).
- No exclamation marks, no "Oops", no emoji, no assistant or AI wording. Alarm words ("production", "critical", "real users") appear only for critical contexts.
- Errors say what happened and what to do next, in that order: "2 kubeconfig files could not be read" → tooltip lists the paths.

## Colour

Use the VS Code built-ins named in `tokens.json`, and Sextant's nine contributed colours (`contributes.colors`), whose defaults point at built-ins so user themes win:

- Environments: `sextant.env.prodForeground` (default `list.errorForeground`) with `$(warning)`; `sextant.env.stagingForeground` (`list.warningForeground`) with `$(beaker)`; `sextant.env.devForeground` (`charts.green`) with `$(code)`; `sextant.env.untaggedForeground` (`icon.foreground`) with `$(question)`.
- Risk: `sextant.risk.expiredForeground` + `$(error)`, `sextant.risk.longLivedForeground` + `$(warning)`, `sextant.risk.expiringForeground` + `$(watch)`, `sextant.risk.shortLivedForeground` + `$(verified)`.
- Current context: `sextant.currentContextForeground` on `$(pass-filled)`, and the word "current" in the row description.
- Critical status item: `statusBarItem.errorBackground` (VS Code applies `statusBarItem.errorForeground`). Reserve `statusBarItem.warningBackground` for "the current context's credential has expired".
- Terminal tabs: `terminal.ansiRed` (prod, with `$(warning)`), `terminal.ansiYellow` (staging), `terminal.ansiGreen` (dev), no colour for untagged.
- Never colour a label or description directly; VS Code does not allow it in trees, and doing it through a decoration would hide the signal from high-contrast users.

## Type

- Chrome uses the VS Code UI font at VS Code's sizes: `tree-label` 13px, `tree-description` 0.9em, `status-bar` 12px, `section-header` 11px bold uppercase.
- Machine strings (context names, commands) use the editor font wherever the surface allows it: the terminal, the Markdown report (code spans), walkthrough media. Tree rows, status bar, QuickPick, toasts and dialogs cannot change font, so names appear in the UI font there (see Native gaps).

## Layout and measurements

Activity bar 48px; container title 35px; section header 22px; rows 22px (44px for QuickPick items with a detail line); indent 8px per level plus a 16px twistie cell; row icons 16px with a 6px gap; status bar 22px with 5px item padding and 14px icons; QuickPick 600px; toast 450px; buttons 26px. Specs are drawn at sidebar widths 220, 300 and 420px. At narrow widths the description truncates with an ellipsis first; the label never truncates before the description.

## Iconography

- Codicons only (the real `codicon.ttf` ships in this system under `fonts/`). The one exception is the Sextant activity-bar glyph: a 24×24 sextant built from two radii, an arc and an index arm, stroke 1.6, round caps, `currentColor`. VS Code renders it as a mask, so it takes `activityBar.foreground` / `activityBar.inactiveForeground` automatically.
- One icon per meaning, used the same way everywhere: `$(warning)` = critical or long-lived, `$(error)` = expired, `$(pass-filled)` = current, `$(circle-large-outline)` = not current, `$(terminal)` = bound terminal, `$(server-environment)` = status item, `$(circle-slash)` = no cluster.
- Platform icons are generic on purpose (no vendor marks): `$(cloud)` for EKS, GKE and AKS; `$(server)` for OpenShift; `$(vm)` for kind, minikube and Docker Desktop; `$(symbol-misc)` for Other. The platform name is always the label.
- Full map in the Token sheet section.

## Behaviour rules

- Order is fixed: environments prod, staging, dev, Untagged; platforms Amazon EKS, Google GKE, Azure AKS, OpenShift, kind, minikube, Docker Desktop, Other. Within a group, contexts sort by name.
- Tags (environment, criticality) live in VS Code settings (`sextant.tags`), never in kubeconfig. Sextant never writes to kubeconfig in E1.
- A bound terminal prepends a small per-terminal file to `KUBECONFIG` that holds only `current-context: <name>` (no credentials are copied). kubectl takes `current-context` from the first file, so `kubectl config use-context` in another window cannot move it.
- The confirmation before a terminal on a critical context is on by default and can be skipped per cluster.
- Toasts are only for a switch to a critical context made outside Sextant. Everything else updates the status item silently.
- Above 40 contexts, environment groups start collapsed.

## What you will find here

- **Components**: one live spec per surface, each in Dark+, Light+ and High Contrast. Dashed outlines mark anything VS Code's extension API cannot do.
- **Sections**: Interaction map, Token sheet, Copy deck, Accessibility, Native gaps.
- **Tokens**: `tokens.json`, VS Code's default values per theme, with a usage note on every token.
