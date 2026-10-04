The Sextant activity-bar glyph: the only custom icon in the extension.

- 24×24 SVG, stroke 1.6, round caps and joins, `stroke="currentColor"`, no fill. Two radii, an arc and an index arm, with a pivot circle at the apex.
- Contribute it as the `icon` of the `sextant` view container. VS Code masks it, so it takes `activityBar.foreground` when active and `activityBar.inactiveForeground` otherwise, in every theme.
- Badge: `TreeView.badge` on the Credential Audit view = number of expired credentials (tooltip "1 expired credential"). No badge when zero.
- Don't use the glyph anywhere else in VS Code chrome (welcome views can't show images). The file is in the Icons asset group.
