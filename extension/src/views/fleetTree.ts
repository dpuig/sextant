import * as vscode from 'vscode';
import { buildFleetTree, countContexts, type DetailRow, type FleetNode } from '../model/fleetTree';
import type { Store } from '../store';
import { S } from '../ui/strings';

interface WarningNode {
  kind: 'warning';
  id: string;
  files: string[];
}
export type FleetViewNode = FleetNode | DetailRow | WarningNode;

const setContext = (key: string, value: boolean): void => {
  void vscode.commands.executeCommand('setContext', key, value);
};

/**
 * The Fleet view: Environment > Platform > Context > detail rows, drawn from the pure model in src/model/fleetTree.ts.
 * Everything visible is a native TreeItem; the wording, icons and colours come from the design system.
 */
export class FleetTreeProvider implements vscode.TreeDataProvider<FleetViewNode>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<FleetViewNode | undefined>();
  readonly onDidChangeTreeData = this.changed.event;
  private filter = '';
  private view: vscode.TreeView<FleetViewNode> | undefined;
  private readonly sub: vscode.Disposable;

  constructor(private readonly store: Store) {
    this.sub = store.onDidChange(() => {
      this.update();
    });
  }

  attach(view: vscode.TreeView<FleetViewNode>): void {
    this.view = view;
    this.update();
  }

  getFilter(): string {
    return this.filter;
  }

  setFilter(text: string): void {
    this.filter = text.trim();
    this.update();
  }

  /** How many contexts a filter would leave, for the live message in the filter box. */
  previewCount(text: string): { shown: number; total: number } {
    return { shown: countContexts(this.build(text.trim())), total: this.store.fleet.contexts.length };
  }

  private build(filter: string): FleetNode[] {
    return buildFleetTree(this.store.fleet.contexts, {
      ...(this.store.fleet.currentContext === undefined
        ? {}
        : { currentContext: this.store.fleet.currentContext }),
      tagOf: this.store.tagOf,
      terminals: this.store.terminalSet(),
      filter,
      now: this.store.now(),
    });
  }

  tree(): FleetNode[] {
    return this.build(this.filter);
  }

  private update(): void {
    const total = this.store.fleet.contexts.length;
    const shown = countContexts(this.tree());
    const filtered = this.filter !== '';
    setContext('sextant.filterActive', filtered);
    setContext('sextant.filterEmpty', filtered && shown === 0);
    setContext('sextant.noKubeconfig', this.store.loaded && total === 0);
    if (this.view) {
      this.view.message =
        this.store.loading && !this.store.loaded
          ? S.fleet.loading
          : filtered
            ? S.fleet.filtered(this.filter, shown, total)
            : undefined;
    }
    this.changed.fire(undefined);
  }

  getChildren(node?: FleetViewNode): FleetViewNode[] {
    if (node === undefined) {
      const failed = this.store.fleet.errors.map((e) => e.file);
      const warning: WarningNode[] =
        failed.length > 0 ? [{ kind: 'warning', id: 'warning', files: failed }] : [];
      return [...warning, ...this.tree()];
    }
    if (node.kind === 'warning' || node.kind === 'detail') return [];
    return node.children;
  }

  getTreeItem(node: FleetViewNode): vscode.TreeItem {
    if (node.kind === 'warning') {
      const item = new vscode.TreeItem(
        S.fleet.warningRow(node.files.length),
        vscode.TreeItemCollapsibleState.None,
      );
      item.id = node.id;
      item.iconPath = new vscode.ThemeIcon('warning', new vscode.ThemeColor('editorWarning.foreground'));
      item.tooltip = S.fleet.warningTooltip(node.files); // paths only; parser output can quote file content
      item.contextValue = 'warning';
      item.accessibilityInformation = {
        label: `Warning: ${S.fleet.warningRow(node.files.length)}. Hover or press Ctrl+K Ctrl+I for the paths.`,
        role: 'treeitem',
      };
      return item;
    }
    if (node.kind === 'detail') {
      const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.None);
      item.id = node.id;
      item.description = node.value;
      item.iconPath = new vscode.ThemeIcon(node.icon);
      item.contextValue = 'detail';
      item.accessibilityInformation = { label: node.accessibility, role: 'treeitem' };
      return item;
    }
    if (node.kind === 'context') {
      const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.Collapsed);
      item.id = node.id;
      item.description = node.description;
      item.tooltip = node.tooltip; // plain text, as the design specifies
      item.iconPath = new vscode.ThemeIcon(
        node.icon,
        node.colorId === undefined ? undefined : new vscode.ThemeColor(node.colorId),
      );
      item.contextValue = 'context';
      item.accessibilityInformation = { label: node.accessibility, role: 'treeitem' };
      return item;
    }
    const state = node.collapsed
      ? vscode.TreeItemCollapsibleState.Collapsed
      : vscode.TreeItemCollapsibleState.Expanded;
    const item = new vscode.TreeItem(node.label, state);
    item.id = node.id;
    item.description = node.description;
    item.iconPath =
      node.kind === 'environment'
        ? new vscode.ThemeIcon(node.icon, new vscode.ThemeColor(node.colorId))
        : new vscode.ThemeIcon(node.icon);
    item.contextValue = node.kind;
    item.accessibilityInformation = { label: node.accessibility, role: 'treeitem' };
    return item;
  }

  dispose(): void {
    this.sub.dispose();
    this.changed.dispose();
  }
}
