Empty, loading and partial-failure states of the Fleet view.

- Empty: `viewsWelcome` with `when: sextant.noKubeconfig`. Text, **Open settings** button (`command:workbench.action.openSettings?["sextant.kubeconfigPaths"]`), docs link. The dashed glyph marks a native gap: welcome views can't show images.
- Loading: `window.withProgress({location:{viewId:'sextant.fleet'}})` draws the 2px bar; `TreeView.message = "Reading kubeconfig files…"` until the first result.
- Unreadable files: a first, non-collapsible row with `$(warning)` in `editorWarning.foreground`; tooltip lists paths only, never parser output (it can quote file content).
