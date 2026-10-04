import * as vscode from 'vscode';
import { discoverKubeconfigPaths, loadFleet, type Fleet } from '../kubeconfig';
import {
  buildFleetTree,
  contextDescription,
  contextTooltip,
  countContexts,
  type FleetNode,
} from '../model/fleetTree';
import { watchFiles } from './watcher';

/** Marker node shown when some kubeconfig files could not be read. Names files only, never their content. */
interface WarningNode {
  kind: 'warning';
  id: string;
  files: string[];
}
type Node = FleetNode | WarningNode;

export class FleetTreeProvider implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.changed.event;

  private fleet: Fleet = { contexts: [], files: [], errors: [] };
  private tree: FleetNode[] = [];
  private filter = '';
  private generation = 0;
  private watcher: vscode.Disposable | undefined;
  private loaded: Promise<void> = Promise.resolve();

  constructor(private readonly setContext: (hasContexts: boolean) => void) {}

  /** Resolves when the most recent load has finished. For tests and callers that must see fresh data. */
  whenLoaded(): Promise<void> {
    return this.loaded;
  }

  /** (Re)discovers the kubeconfig files, reloads them and re-arms the file watchers. */
  refresh(): Promise<void> {
    const mine = ++this.generation;
    this.loaded = this.load(mine);
    return this.loaded;
  }

  private configuredPaths(): string[] {
    return vscode.workspace.getConfiguration('sextant').get<string[]>('kubeconfigPaths', []);
  }

  private async load(mine: number): Promise<void> {
    const opts = { env: process.env, configuredPaths: this.configuredPaths() };
    const paths = discoverKubeconfigPaths(opts);
    this.watcher?.dispose();
    this.watcher = watchFiles(paths, () => void this.refresh());
    const fleet = await loadFleet(opts);
    if (mine !== this.generation) return; // a newer load started while this one was reading: drop the stale result
    this.fleet = fleet;
    this.rebuild();
  }

  setFilter(text: string): void {
    this.filter = text;
    this.rebuild();
  }

  getFilter(): string {
    return this.filter;
  }

  private rebuild(): void {
    this.tree = buildFleetTree(this.fleet.contexts, {
      ...(this.fleet.currentContext === undefined ? {} : { currentContext: this.fleet.currentContext }),
      filter: this.filter,
    });
    this.setContext(this.fleet.contexts.length > 0);
    void vscode.commands.executeCommand('setContext', 'sextant.filtered', this.filter.trim() !== '');
    this.changed.fire(undefined);
  }

  getChildren(node?: Node): Node[] {
    if (node === undefined) {
      const failed = this.fleet.errors.map((e) => e.file);
      const warning: WarningNode[] =
        failed.length > 0 ? [{ kind: 'warning', id: 'warn', files: failed }] : [];
      return [...warning, ...this.tree];
    }
    return node.kind === 'warning' || node.kind === 'context' ? [] : node.children;
  }

  getTreeItem(node: Node): vscode.TreeItem {
    if (node.kind === 'warning') {
      const item = new vscode.TreeItem(
        `${node.files.length} kubeconfig file${node.files.length === 1 ? '' : 's'} could not be read`,
        vscode.TreeItemCollapsibleState.None,
      );
      item.id = node.id;
      item.iconPath = new vscode.ThemeIcon('warning');
      item.tooltip = `Skipped (unreadable or not valid kubeconfig):\n${node.files.join('\n')}`;
      return item;
    }
    if (node.kind === 'context') {
      const item = new vscode.TreeItem(node.label, vscode.TreeItemCollapsibleState.None);
      item.id = node.id;
      item.description = contextDescription(node.context, node.current);
      item.tooltip = contextTooltip(node.context, node.current);
      item.iconPath = new vscode.ThemeIcon(node.current ? 'pass-filled' : 'circle-outline');
      item.contextValue = 'sextant.context';
      return item;
    }
    const many = countContexts(this.tree) > 30;
    const state =
      node.kind === 'environment' || !many
        ? vscode.TreeItemCollapsibleState.Expanded
        : vscode.TreeItemCollapsibleState.Collapsed;
    const item = new vscode.TreeItem(node.label, state);
    item.id = node.id;
    item.description = String(countContexts([node]));
    item.iconPath = new vscode.ThemeIcon(node.kind === 'environment' ? 'layers' : 'server-environment');
    item.contextValue = `sextant.${node.kind}`;
    return item;
  }

  dispose(): void {
    this.watcher?.dispose();
    this.changed.dispose();
  }
}
