Filter and scale states of the Fleet view.

- Filtered: `TreeView.message = "Filtered: gke · 2 of 10"`; context key `sextant.filterActive` swaps the Filter button for Clear filter (`search-stop`). Matching ancestors stay expanded.
- Filtered, no match: the tree returns no children and a `viewsWelcome` entry with `when: sextant.filterActive && sextant.filterEmpty` shows the message and **Clear filter**.
- 60+ contexts: above 40 contexts, environment groups start `Collapsed`; counts stay in descriptions. The activity-bar badge is unaffected.
