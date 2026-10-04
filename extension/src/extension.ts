import * as path from 'node:path';
import * as vscode from 'vscode';
import { openTerminalFlow } from './flows/openTerminal';
import { runTagFlow } from './flows/tag';
import { renderReport } from './model/audit';
import { shouldWarnOnSwitch } from './model/status';
import { addConfirmSkip, saveTags } from './settings';
import { Store } from './store';
import { TerminalManager } from './terminal/manager';
import type { Prompts } from './ui/prompts';
import { S } from './ui/strings';
import { vscodePrompts } from './ui/vscodePrompts';
import { AuditTreeProvider, type AuditNode } from './views/auditTree';
import { FleetTreeProvider, type FleetViewNode } from './views/fleetTree';
import { StatusBarController } from './views/statusBar';

/** What integration tests can reach; not a public API. */
export interface TestApi {
  store: Store;
  fleet: FleetTreeProvider;
  audit: AuditTreeProvider;
  status: StatusBarController;
  terminals: TerminalManager;
  /** Replace the QuickPick/InputBox layer with a scripted one. */
  setPrompts(p: Prompts): void;
}

type Target = FleetViewNode | AuditNode | undefined;

/** The context name behind a tree node (a Fleet context row or an Audit row), if it is one. */
function nameOf(node: Target): string | undefined {
  if (node === undefined) return undefined;
  if (node.kind === 'context') return node.view.context.name;
  if (node.kind === 'row') return node.row.context.name;
  return undefined;
}

const isoToday = (d: Date): string => d.toISOString().slice(0, 10);

/**
 * Activation is eager (`onStartupFinished`): the status bar must show the current context and the Audit badge must
 * count expired credentials without the user opening anything. It reads kubeconfig files on this machine only.
 */
export function activate(context: vscode.ExtensionContext): TestApi {
  let prompts: Prompts = vscodePrompts;
  const store = new Store();
  const fleet = new FleetTreeProvider(store);
  const audit = new AuditTreeProvider(store);
  const status = new StatusBarController(store);
  const terminals = new TerminalManager(store, path.join(context.globalStorageUri.fsPath, 'bound'));

  const fleetView = vscode.window.createTreeView('sextant.fleet', {
    treeDataProvider: fleet,
    showCollapseAll: true,
  });
  const auditView = vscode.window.createTreeView('sextant.audit', { treeDataProvider: audit });
  fleet.attach(fleetView);
  audit.attach(auditView);

  const say = (text: string): void => {
    vscode.window.setStatusBarMessage(text, 3000);
  };
  const pickContext = async (): Promise<string | undefined> => {
    const names = store.fleet.contexts.map((c) => c.name).sort((a, b) => a.localeCompare(b));
    const r = await prompts.pick<string>({
      title: 'Choose a cluster',
      placeholder: S.openTerminal.placeholder(names.length),
      items: names.map((n) => ({ kind: 'item', label: n, value: n })),
    });
    return r.kind === 'picked' ? r.value : undefined;
  };

  const tagCluster = async (name: string | undefined): Promise<void> => {
    const target = name ?? (await pickContext());
    if (target === undefined) return;
    const outcome = await runTagFlow({
      prompts,
      contextName: target,
      allNames: store.fleet.contexts.map((c) => c.name),
      rules: store.rules,
    });
    if (outcome === undefined) return;
    try {
      await saveTags(outcome.rules);
    } catch {
      void vscode.window.showErrorMessage(S.tag.saveFailed);
      return;
    }
    store.readSettings();
    say(S.tag.done(target, outcome.tag.environment, outcome.tag.critical));
  };

  const openTerminal = async (name: string | undefined): Promise<void> => {
    if (name !== undefined && store.contextByName(name) === undefined) {
      void vscode.window.showWarningMessage(S.fleet.removed(name));
      return;
    }
    await openTerminalFlow(
      {
        prompts,
        views: store.views(),
        skip: store.skip,
        open: (n) => {
          terminals.open(n);
          return Promise.resolve();
        },
        addSkip: async (n) => {
          await addConfirmSkip(n);
          store.readSettings();
        },
        editTags: (n) => tagCluster(n),
        reload: () => store.views(),
      },
      name,
    );
  };

  const filter = async (): Promise<void> => {
    const total = store.fleet.contexts.length;
    const r = await prompts.input({
      title: 'Filter clusters',
      prompt: S.filter.prompt,
      value: fleet.getFilter(),
      live: (text) => {
        const { shown } = fleet.previewCount(text);
        return shown === 0 && text.trim() !== ''
          ? { message: S.filter.none, severity: 'warning' }
          : { message: S.filter.live(shown, total), severity: 'info' };
      },
    });
    if (r.kind === 'entered') fleet.setFilter(r.value);
  };

  const reveal = async (name: string | undefined): Promise<void> => {
    const c = name === undefined ? undefined : store.contextByName(name);
    if (c === undefined) {
      if (name !== undefined) void vscode.window.showWarningMessage(S.fleet.removed(name));
      return;
    }
    const line = Math.max(0, (c.sourceLine ?? 1) - 1);
    const at = new vscode.Position(line, 0);
    await vscode.window.showTextDocument(vscode.Uri.file(c.sourceFile), {
      selection: new vscode.Range(at, at),
    });
  };

  const report = (): string => renderReport(store.fleet.contexts, store.fleet.files.length, store.now());

  const dismissed = new Set<string>();
  const onSwitch = async (previous: string | undefined, next: string | undefined): Promise<void> => {
    if (next === undefined) return;
    const c = store.contextByName(next);
    const critical = c !== undefined && (store.tagOf(c)?.critical ?? false);
    if (!shouldWarnOnSwitch({ previous, next, nextIsCritical: critical, dismissed })) return;
    const choice = await vscode.window.showWarningMessage(
      S.toast.externalSwitch(next),
      S.toast.openBound,
      S.toast.dismiss,
    );
    if (choice === S.toast.openBound) {
      if (!terminals.focusExisting(next)) await openTerminal(next);
    } else {
      dismissed.add(next);
    }
  };

  const cmd = (id: string, fn: (...args: never[]) => unknown): vscode.Disposable =>
    vscode.commands.registerCommand(id, fn as (...args: unknown[]) => unknown);

  context.subscriptions.push(
    store,
    fleet,
    audit,
    status,
    terminals,
    fleetView,
    auditView,
    store.onDidSwitch((e) => void onSwitch(e.previous, e.next)),
    cmd('sextant.openTerminal', () => openTerminal(undefined)),
    cmd('sextant.openTerminalForItem', (node: Target) => openTerminal(nameOf(node))),
    cmd('sextant.tagCluster', (node: Target) => tagCluster(nameOf(node))),
    cmd('sextant.filter', filter),
    cmd('sextant.clearFilter', () => {
      fleet.setFilter('');
    }),
    cmd('sextant.refresh', () => store.refresh()),
    cmd('sextant.copyContextName', async (node: Target) => {
      const name = nameOf(node ?? fleetView.selection[0]);
      if (name === undefined) return;
      await vscode.env.clipboard.writeText(name);
      say(S.fleet.copied(name));
    }),
    cmd('sextant.revealKubeconfig', (node: Target) => reveal(nameOf(node))),
    cmd('sextant.copyAuditReport', async () => {
      await vscode.env.clipboard.writeText(report());
      say(S.audit.copied);
    }),
    cmd('sextant.openAuditReport', async () => {
      const uri = vscode.Uri.parse(`untitled:sextant-audit-${isoToday(store.now())}.md`);
      const doc = await vscode.workspace.openTextDocument(uri);
      const edit = new vscode.WorkspaceEdit();
      edit.insert(uri, new vscode.Position(0, 0), report());
      await vscode.workspace.applyEdit(edit);
      await vscode.window.showTextDocument(doc, { preview: false });
      await vscode.commands.executeCommand('markdown.showPreviewToSide', uri);
    }),
    cmd('sextant.gettingStarted', () =>
      vscode.commands.executeCommand(
        'workbench.action.openWalkthrough',
        `${context.extension.id}#sextant.gettingStarted`,
        false,
      ),
    ),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration('sextant.kubeconfigPaths')) void store.refresh();
      if (e.affectsConfiguration('sextant.tags') || e.affectsConfiguration('sextant.confirm'))
        store.readSettings();
    }),
  );

  void store.refresh();

  // First install only: open the walkthrough once. Tests set SEXTANT_NO_WELCOME so a tab never steals focus.
  const seen = 'sextant.walkthroughShown';
  if (process.env.SEXTANT_NO_WELCOME === undefined && context.globalState.get(seen) !== true) {
    void context.globalState.update(seen, true);
    void vscode.commands.executeCommand('sextant.gettingStarted');
  }

  return {
    store,
    fleet,
    audit,
    status,
    terminals,
    setPrompts: (p) => {
      prompts = p;
    },
  };
}

export function deactivate(): void {
  // Bound-terminal pin files are removed by TerminalManager.dispose and swept again at the next start.
}
