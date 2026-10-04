import * as vscode from 'vscode';
import { FleetTreeProvider } from './views/fleetTree';

/** What integration tests can reach; not a public API. */
export interface TestApi {
  fleet: FleetTreeProvider;
}

/**
 * Activation is lazy: package.json declares no activation events beyond the views and commands it contributes, so
 * nothing is read or watched until the user opens the Sextant view or runs one of its commands.
 */
export function activate(context: vscode.ExtensionContext): TestApi {
  const setHasContexts = (has: boolean): void => {
    void vscode.commands.executeCommand('setContext', 'sextant.hasContexts', has);
  };
  const fleet = new FleetTreeProvider(setHasContexts);
  const view = vscode.window.createTreeView('sextant.fleet', {
    treeDataProvider: fleet,
    showCollapseAll: true,
  });

  context.subscriptions.push(
    fleet,
    view,
    vscode.commands.registerCommand('sextant.showVersion', () => {
      const version = (context.extension.packageJSON as { version: string }).version;
      void vscode.window.showInformationMessage(`Sextant ${version}`);
    }),
    vscode.commands.registerCommand('sextant.fleet.refresh', () => fleet.refresh()),
    vscode.commands.registerCommand('sextant.fleet.filter', async () => {
      const text = await vscode.window.showInputBox({
        title: 'Filter clusters',
        prompt: 'Matches context, cluster, user, host, platform and environment',
        value: fleet.getFilter(),
      });
      if (text !== undefined) fleet.setFilter(text);
    }),
    vscode.commands.registerCommand('sextant.fleet.clearFilter', () => {
      fleet.setFilter('');
    }),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration('sextant.kubeconfigPaths')) void fleet.refresh();
    }),
  );

  void fleet.refresh();
  return { fleet };
}

export function deactivate(): void {
  // Nothing to release yet; later tasks delete their temporary files here.
}
