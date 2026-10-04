import * as vscode from 'vscode';
import { buildAudit, expiredCount, type AuditGroup, type AuditRow } from '../model/audit';
import type { Store } from '../store';
import { S } from '../ui/strings';

type HintNode = { kind: 'hint'; id: string; text: string };
export type AuditNode = { kind: 'group'; group: AuditGroup } | { kind: 'row'; row: AuditRow } | HintNode;

/** The Credential Audit view: contexts by credential risk. Classification only; no credential value is reachable. */
export class AuditTreeProvider implements vscode.TreeDataProvider<AuditNode>, vscode.Disposable {
  private readonly changed = new vscode.EventEmitter<AuditNode | undefined>();
  readonly onDidChangeTreeData = this.changed.event;
  private view: vscode.TreeView<AuditNode> | undefined;
  private readonly sub: vscode.Disposable;

  constructor(private readonly store: Store) {
    this.sub = store.onDidChange(() => {
      this.update();
    });
  }

  attach(view: vscode.TreeView<AuditNode>): void {
    this.view = view;
    view.message = S.audit.message; // trees have no footer, so the reassurance is the always-visible message
    this.update();
  }

  groups(): AuditGroup[] {
    return buildAudit(this.store.fleet.contexts, this.store.now());
  }

  private update(): void {
    const n = expiredCount(this.groups());
    if (this.view) this.view.badge = n > 0 ? { value: n, tooltip: S.audit.badgeTooltip(n) } : undefined;
    this.changed.fire(undefined);
  }

  getChildren(node?: AuditNode): AuditNode[] {
    if (node === undefined) return this.groups().map((group) => ({ kind: 'group', group }));
    if (node.kind === 'group') return node.group.rows.map((row) => ({ kind: 'row', row }));
    if (node.kind === 'row')
      return [{ kind: 'hint', id: `hint:${node.row.context.name}`, text: node.row.hint }];
    return [];
  }

  getTreeItem(node: AuditNode): vscode.TreeItem {
    if (node.kind === 'group') {
      const g = node.group;
      const state =
        g.rows.length === 0
          ? vscode.TreeItemCollapsibleState.None
          : g.collapsed
            ? vscode.TreeItemCollapsibleState.Collapsed
            : vscode.TreeItemCollapsibleState.Expanded;
      const item = new vscode.TreeItem(g.label, state);
      item.id = `audit:${g.id}`;
      item.description = g.description;
      item.iconPath = new vscode.ThemeIcon(g.icon, new vscode.ThemeColor(g.colorId));
      item.contextValue = 'auditGroup';
      item.accessibilityInformation = { label: g.accessibility, role: 'treeitem' };
      return item;
    }
    if (node.kind === 'row') {
      const r = node.row;
      const item = new vscode.TreeItem(r.context.name, vscode.TreeItemCollapsibleState.Collapsed);
      item.id = `audit:row:${r.context.name}`;
      item.description = r.description;
      item.tooltip = r.hint; // same text as the child row
      item.contextValue = 'auditRow';
      item.accessibilityInformation = { label: r.accessibility, role: 'treeitem' };
      return item;
    }
    const item = new vscode.TreeItem(node.text, vscode.TreeItemCollapsibleState.None);
    item.id = node.id;
    item.iconPath = new vscode.ThemeIcon('lightbulb');
    item.tooltip = node.text;
    item.accessibilityInformation = { label: `Suggested fix: ${node.text}`, role: 'treeitem' };
    return item;
  }

  dispose(): void {
    this.sub.dispose();
    this.changed.dispose();
  }
}
