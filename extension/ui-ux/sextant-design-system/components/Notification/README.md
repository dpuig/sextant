Toast when the global context moves to a critical one outside Sextant.

- API: `showWarningMessage(message, 'Open bound terminal', 'Dismiss')`, non-modal.
- Trigger: file watcher sees `current-context` change, the change didn't come from Sextant, and the new context is critical.
- Non-critical switches never toast; the status item updates in place.
- Don't repeat the toast for the same context within a session after Dismiss.
