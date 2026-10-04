# Icons

`sextant-activity.svg` is the only custom icon: the activity-bar glyph for the `sextant` view container. 24×24, stroke 1.6, round caps and joins, `stroke="currentColor"`, no fill. VS Code renders it as a mask, so its ink is `activityBar.foreground` (active) or `activityBar.inactiveForeground`. Shown here through `<img>`, it draws in black because `currentColor` cannot be inherited.

Every other icon is a Codicon (see the Token sheet); `fonts/codicon.ttf` is the official `@vscode/codicons` 0.0.46 font (CC BY 4.0, Microsoft), included so the specs render the real glyphs.
