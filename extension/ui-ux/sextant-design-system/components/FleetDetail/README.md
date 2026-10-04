Expanded context, hover tooltip and context menu.

- Expanding a context shows five leaf rows: Server (`globe`), Namespace (`symbol-namespace`), Credential (`key`), Expires (`calendar`), Source file (`file`). No separate panel.
- Tooltip: plain-text `TreeItem.tooltip`, one `Label: value` per line: Context, Cluster, User, Namespace, Server, Authentication (with "long-lived"), Certificate expiry, Defined in.
- Context menu (`view/item/context`, `viewItem == context`): Open Terminal for This Cluster · Tag Cluster… | Copy Context Name · Reveal Kubeconfig File. Keybinding hints appear automatically.
- Reveal opens the source file at the context's line; Sextant never edits kubeconfig in E1.
