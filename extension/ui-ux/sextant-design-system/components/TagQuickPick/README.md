Three-step tagging flow.

- API: one `QuickPick` reused across steps with `title`, `step`, `totalSteps` and a Back button (`QuickInputButtons.Back`).
- Step 1 environment: prod, staging, dev, Custom… (Custom opens an InputBox). Step 2 criticality: critical / normal; prod preselects critical. Step 3 optional scope: only this context, glob matches, custom glob; descriptions show how many contexts each matches.
- Saves to user `sextant.tags`. Escape at any step saves nothing.
