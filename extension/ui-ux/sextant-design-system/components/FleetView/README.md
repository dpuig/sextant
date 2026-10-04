The primary view: a tree of every kubeconfig context grouped Environment > Platform > Context.

- API: `TreeDataProvider` in view `sextant.fleet` inside container `sextant`; `showCollapseAll: true`.
- Rows: environment (`$(warning)` prod / `$(beaker)` staging / `$(code)` dev / `$(question)` Untagged, coloured by `sextant.env.*`), platform (`$(cloud)`, `$(server)`, `$(vm)`, `$(symbol-misc)`), context (`$(pass-filled)` in `sextant.currentContextForeground` for current, `$(circle-large-outline)` otherwise).
- Description order: `current` · `terminal` · auth · host. prod groups read `3 · critical`.
- Title buttons: Filter (`search`), Refresh (`refresh`), Collapse all (built-in).
- Provide: contexts from kubeconfig, tags from `sextant.tags`, bound-terminal set. Set `accessibilityInformation` on every row (Accessibility section).
- Don't: colour labels, show any credential value, or reorder environments.
