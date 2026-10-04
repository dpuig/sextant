A terminal pinned to one context.

- API: `createTerminal({name, iconPath, color, env, message})`. `name` = `prod-eu-1 · PROD`; `iconPath` = `ThemeIcon('warning')` for prod or `ThemeIcon('terminal')`; `color` = `terminal.ansiRed` / `ansiYellow` / `ansiGreen`; `env.KUBECONFIG` = a per-terminal file with only `current-context` prepended to the user's list (no credentials copied).
- `message` writes the first-line banner: ` PROD ` chip (ANSI 1;97;41), then the sentence in default colour with "prod, critical" bold.
- Verify with shell integration (`onDidChangeTerminalShellIntegration`). If unavailable up front, write the WARNING chip variant; if it fails later, toast (Native gaps #6).
- Track bound terminals so the Fleet row says "terminal" and the toast can focus it.
