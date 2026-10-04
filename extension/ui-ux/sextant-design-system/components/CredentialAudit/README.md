Second view in the Sextant container: contexts sorted by credential risk.

- API: view `sextant.audit` with `"visibility": "collapsed"`. Title buttons Copy report (`copy`) and Open report (`open-preview`).
- `TreeView.message = "Values are never shown."` (top, always visible; trees have no footer).
- Groups in fixed order: Expired (`error`), Long-lived credentials (`warning`), Expiring within 30 days (`watch`), Short-lived or brokered (`verified`, collapsed). Empty group: description `none`, not expandable.
- Each row: name, `auth · expiry`; one child row with the remediation hint (`lightbulb`), same text in the tooltip.
- Classification uses only the kubeconfig structure (auth type, certificate notAfter date). Never read, log or display token, key or certificate bodies.
