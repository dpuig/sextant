Confirmation before opening a terminal on a critical context.

- Shown only for contexts tagged critical and not in `sextant.confirm.skip`.
- A (as briefed): `showWarningMessage(title, {modal:true, detail}, 'Open terminal')`. Checkbox, link and Cancel-as-default can't be built (dashed).
- B (recommended): modal `QuickPick` with `ignoreFocusOut: true`, Cancel first and focused, then Open terminal on {name}, Open and don't ask again, Edit tags…. Enter does the safe thing.
- Copy: title "Open terminal on production?", body "prod-eu-1 is tagged critical. Commands here affect real users."
