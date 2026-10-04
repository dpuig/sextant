import * as vscode from 'vscode';
import type { InputOptions, InputResult, PickOptions, PickResult, Prompts } from './prompts';

interface Item<T> extends vscode.QuickPickItem {
  value?: T;
}

/** Real prompts: one QuickPick or InputBox per call, disposed when it closes. */
export const vscodePrompts: Prompts = {
  pick<T>(o: PickOptions<T>): Promise<PickResult<T>> {
    return new Promise((resolve) => {
      const qp = vscode.window.createQuickPick<Item<T>>();
      qp.title = o.title;
      qp.placeholder = o.placeholder;
      qp.ignoreFocusOut = o.ignoreFocusOut ?? false;
      qp.matchOnDescription = o.matchOnDescription ?? false;
      if (o.step !== undefined) qp.step = o.step;
      if (o.totalSteps !== undefined) qp.totalSteps = o.totalSteps;
      if (o.canGoBack === true) qp.buttons = [vscode.QuickInputButtons.Back];
      qp.items = o.items.map((e): Item<T> =>
        e.kind === 'separator'
          ? { label: e.label, kind: vscode.QuickPickItemKind.Separator }
          : {
              label: e.label,
              ...(e.description === undefined ? {} : { description: e.description }),
              ...(e.detail === undefined ? {} : { detail: e.detail }),
              ...(e.iconId === undefined ? {} : { iconPath: new vscode.ThemeIcon(e.iconId) }),
              value: e.value,
            },
      );
      // The first real entry is focused, so Enter picks it. Callers order items so that is the safe choice.
      const firstItem = qp.items.find((i) => i.kind !== vscode.QuickPickItemKind.Separator);
      if (firstItem) qp.activeItems = [firstItem];

      let done = false;
      const finish = (r: PickResult<T>): void => {
        if (done) return;
        done = true;
        resolve(r);
        qp.dispose();
      };
      qp.onDidAccept(() => {
        const chosen = qp.selectedItems[0] ?? qp.activeItems[0];
        if (chosen?.value !== undefined) finish({ kind: 'picked', value: chosen.value });
      });
      qp.onDidTriggerButton((b) => {
        if (b === vscode.QuickInputButtons.Back) finish({ kind: 'back' });
      });
      qp.onDidHide(() => {
        finish({ kind: 'cancelled' });
      });
      qp.show();
    });
  },

  input(o: InputOptions): Promise<InputResult> {
    return new Promise((resolve) => {
      const ib = vscode.window.createInputBox();
      ib.title = o.title;
      ib.prompt = o.prompt;
      if (o.placeholder !== undefined) ib.placeholder = o.placeholder;
      if (o.value !== undefined) ib.value = o.value;
      if (o.step !== undefined) ib.step = o.step;
      if (o.totalSteps !== undefined) ib.totalSteps = o.totalSteps;
      if (o.canGoBack === true) ib.buttons = [vscode.QuickInputButtons.Back];

      const feedback = (value: string): void => {
        const blocking = o.validate?.(value);
        if (blocking !== undefined) {
          ib.validationMessage = { message: blocking, severity: vscode.InputBoxValidationSeverity.Error };
          return;
        }
        const live = o.live?.(value);
        ib.validationMessage =
          live === undefined
            ? undefined
            : {
                message: live.message,
                severity:
                  live.severity === 'warning'
                    ? vscode.InputBoxValidationSeverity.Warning
                    : vscode.InputBoxValidationSeverity.Info,
              };
      };
      feedback(ib.value);

      let done = false;
      const finish = (r: InputResult): void => {
        if (done) return;
        done = true;
        resolve(r);
        ib.dispose();
      };
      ib.onDidChangeValue(feedback);
      ib.onDidAccept(() => {
        if (o.validate?.(ib.value) === undefined) finish({ kind: 'entered', value: ib.value });
      });
      ib.onDidTriggerButton((b) => {
        if (b === vscode.QuickInputButtons.Back) finish({ kind: 'back' });
      });
      ib.onDidHide(() => {
        finish({ kind: 'cancelled' });
      });
      ib.show();
    });
  },
};
