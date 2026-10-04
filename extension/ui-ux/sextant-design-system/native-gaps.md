# Native gaps

What the brief asks for that VS Code's extension API cannot draw, the compromise used in the specs, and the alternative if you want to push further. Dashed outlines in the component previews point here. Ordered by how much each one matters for the "wrong cluster" problem.

| # | Brief asks for | Why it can't be native | Compromise in the specs | Alternative |
|---|---|---|---|---|
| 1 | Confirmation dialog with **Cancel as the default** button | `showWarningMessage({modal:true})` makes the first item the default; Cancel is added by VS Code and is never the default. | **Recommended: modal QuickPick** (`ignoreFocusOut`) with Cancel focused first. Fully native, keyboard-first, and Enter does the safe thing. | Keep the dialog and accept "Open terminal" as default (unsafe for a prod guard), or require typing the context name in an InputBox for critical clusters. |
| 2 | "Don't ask again for this cluster" **checkbox** in the dialog | Extension dialogs have no checkbox (VS Code's internal dialogs do, the API doesn't expose it). | A separate option: "Open, and don't ask again for this cluster". | A third dialog button with the same text. |
| 3 | "Edit tags" **link** in the dialog | No links in modal messages. | An "Edit tags…" item in the QuickPick (or a dialog button). | None. |
| 4 | **Monospace** context names in tree, status bar, QuickPick, toasts, dialogs | Trees, status bar and quick input render only the UI font; no per-item font. | UI font everywhere; mono only where VS Code renders Markdown code or a terminal (report, walkthrough media, terminal). | Switch tree tooltips to `MarkdownString` with code spans (brief asked for plain text). |
| 5 | Terminal tab named with a **helm-wheel icon** (`⎈`) | No helm codicon; terminal names don't render `$(icon)`. The tab shows one `iconPath`. | `iconPath` = `$(warning)` (prod) or `$(terminal)`, tinted by `color`; environment in the name: `prod-eu-1 · PROD`. | Ship a custom SVG `iconPath` (Uri) — loses theme tinting, so not recommended. |
| 6 | **"Could not confirm" banner inside the terminal** | `TerminalOptions.message` is written only at creation. Verification needs shell integration, which activates after the shell starts; afterwards an extension can only `sendText` (that types into the shell). | Banner inside the terminal only when Sextant knows up front that verification is impossible (shell without integration, e.g. `sh`, `cmd.exe`). Otherwise a warning toast after 5s. | Make bound terminals Pseudoterminals (full control of output) — costs native shell behaviour; not recommended. |
| 7 | **Two icons** on a context row (status + terminal) | A TreeItem has one icon. | The word "terminal" in the description. | `FileDecoration` badge via a synthetic `resourceUri` (2 characters, e.g. `T`), or an inline action button (visible on hover/selection only). |
| 8 | **Count badge** on group rows | TreeItems have no badge; `TreeView.badge` is per view. | Count as the description (`3 · critical`). | `FileDecoration` badge (max 2 characters, so 99+ breaks). |
| 9 | **Illustration or icon** in the empty state | `viewsWelcome` renders text, links and buttons only. | Text + button + link; the glyph lives in the activity bar. | None. |
| 10 | Description **right-aligned** | Descriptions follow the label inline and truncate. | Inline description; truncates before the label at 220px. | None. |
| 11 | **Footer** note "Values are never shown" in Credential Audit | Tree views have no footer. | `TreeView.message` at the top of the view (always visible). | `viewsWelcome` text when the audit is empty. |
| 12 | **Walkthrough step shows the count** ("We found 10 clusters") | Walkthrough content is static, declared in package.json. | Static sentence pointing to the Fleet view; the count is in the activity-bar badge and Fleet. | Markdown media is also static; none. |
| 13 | **Critical badge colour** in the Open terminal QuickPick | QuickPick ignores `ThemeIcon` colours. | `$(warning)` icon + "critical" in the description, uncoloured. | None. |
| 14 | **Muted** "No cluster" status item | `StatusBarItem.color` exists, but muted text on `statusBar.background` falls below 4.5:1. | Default foreground; `$(circle-slash)` + "No cluster" carry the state. | None advised. |
| 15 | **Status item colour in High Contrast** | VS Code leaves `statusBarItem.errorBackground` unset in HC Dark; only error/warning background tokens are allowed on items. | HC shows no fill; `$(warning)` + "PROD" carry it, plus VS Code's HC outline. | None. |
| 16 | Coloured **label** for prod rows | Trees don't colour labels. | Coloured icon + "critical". | `FileDecoration.color` via `resourceUri` tints the label — deliberately not used (ignored by some themes, loses meaning in HC). |
| 17 | Every surface in **Dark+ / Light+** | VS Code's defaults are now Dark Modern / Light Modern. | Specs drawn in Dark+ / Light+ as asked. All colours are token references, so Modern themes work unchanged. | Re-check contrast in Modern themes before release. |

## Data note

The brief's populated state says 14 contexts and the filter bar "gke · 2 of 14", but the sample table has 10 contexts. The specs use the 10 sample contexts throughout ("2 of 10"). The 60+ state uses illustrative group counts (18 / 14 / 22 / 9). Cluster and user names in the tooltip (`ocp-prod`, `admin/ocp-prod`) are illustrative; the sample table doesn't list them. With today's date (2026-10-04) no sample context expires within 30 days, so that audit group shows "none"; `kind-local` (expires 2026-12-31) sits under Long-lived as the sample notes say.

## Decisions to confirm

1. Confirmation form: QuickPick (recommended) or dialog.
2. Critical status item: `statusBarItem.errorBackground` (used here) or `warningBackground`. Error is chosen because Dark+ warning (#7A6400) reads as olive, not as a warning.
3. Proposed default keybinding for Open Terminal for Cluster: `Ctrl/Cmd+Alt+Shift+T`.
4. Tags stored in user settings (`sextant.tags`), not workspace settings, so a repository can't silently downgrade prod to normal.
