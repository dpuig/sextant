The credential audit as a Markdown document.

- Copy report: Markdown to clipboard. Open report: untitled `sextant-audit-YYYY-MM-DD.md` + `markdown.showPreviewToSide`.
- Structure: H1 title, generated line, bold summary of counts per risk group, table (Context, Platform, Authentication, Expires, Risk, Suggestion) sorted by risk, closing blockquote saying no secret values are included.
- Context names as code spans. Never include server credentials, tokens, keys or certificate contents.
