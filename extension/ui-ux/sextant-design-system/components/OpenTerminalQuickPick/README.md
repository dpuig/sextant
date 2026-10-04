Searchable list of every context for opening a bound terminal.

- API: `QuickPick` with `QuickPickItemKind.Separator` rows: current (pinned first), prod, staging, dev, Untagged.
- Item: icon (`pass-filled` current, `warning` critical, `circle-large-outline` other), label = context name, description `critical · {platform}` or `{env} · {platform}`, plus `terminal open`.
- `matchOnDescription: true` so typing "gke" or "critical" works. Critical choices continue into the confirmation.
