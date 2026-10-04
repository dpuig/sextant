import * as vscode from 'vscode';
import { statusModel } from '../model/status';
import type { Store } from '../store';

/** The current kubectl context, always visible. Critical contexts get the error background plus icon and text. */
export class StatusBarController implements vscode.Disposable {
  readonly item: vscode.StatusBarItem;
  private readonly sub: vscode.Disposable;

  constructor(private readonly store: Store) {
    this.item = vscode.window.createStatusBarItem('sextant.context', vscode.StatusBarAlignment.Left, 100);
    this.item.name = 'Sextant: Kubernetes context';
    this.sub = store.onDidChange(() => {
      this.render();
    });
    this.render();
  }

  render(): void {
    const { fleet } = this.store;
    const current =
      fleet.currentContext === undefined ? undefined : this.store.contextByName(fleet.currentContext);
    const m = statusModel(
      current,
      current === undefined ? undefined : this.store.tagOf(current),
      this.store.now(),
    );
    this.item.text = m.text;
    this.item.tooltip = m.tooltip;
    this.item.accessibilityInformation = { label: m.accessibility, role: 'button' };
    this.item.backgroundColor = m.critical
      ? new vscode.ThemeColor('statusBarItem.errorBackground')
      : undefined;
    this.item.command = {
      command: m.command,
      title: 'Sextant',
      ...(m.commandArgs === undefined ? {} : { arguments: m.commandArgs }),
    };
    // Before the first load there is nothing to say; afterwards the item is always shown ("No cluster" is a state).
    if (this.store.loaded) this.item.show();
    else this.item.hide();
  }

  dispose(): void {
    this.sub.dispose();
    this.item.dispose();
  }
}
