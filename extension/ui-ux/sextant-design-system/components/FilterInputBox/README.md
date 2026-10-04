Filter the Fleet view.

- API: `InputBox`; on every `onDidChangeValue`, set `validationMessage = {message, severity: Info}` with the live count, or Warning severity when nothing matches.
- Matches context name, platform, environment and server host, case-insensitive substring.
- Enter applies (`TreeView.message` shows the filter); Escape cancels.
