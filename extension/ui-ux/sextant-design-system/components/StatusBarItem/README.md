The current kubectl context, always visible on the left of the status bar.

- API: `createStatusBarItem('sextant.context', StatusBarAlignment.Left, priority)` placed after source control.
- Normal `$(server-environment) staging-eu-1`. Critical `$(warning) PROD  prod-eu-1` with `backgroundColor = new ThemeColor('statusBarItem.errorBackground')`; VS Code applies the matching foreground. No kubeconfig `$(circle-slash) No cluster`.
- Click runs Open Terminal for Cluster… (no kubeconfig: opens settings). Tooltip repeats the details. Set `accessibilityInformation`.
- High Contrast Dark has no fill for error items; icon and "PROD" carry the state.
