import * as path from 'node:path';
import * as vscode from 'vscode';

/**
 * Calls `onChange` (debounced) whenever any of the given kubeconfig files is created, changed or deleted. Watches
 * the file by its absolute path, so it works for files outside the workspace, and also fires for a file that does
 * not exist yet.
 */
export function watchFiles(
  files: readonly string[],
  onChange: () => void,
  debounceMs = 150,
): vscode.Disposable {
  let timer: NodeJS.Timeout | undefined;
  const fire = (): void => {
    if (timer !== undefined) clearTimeout(timer);
    timer = setTimeout(onChange, debounceMs);
  };
  const watchers = files.map((file) => {
    const w = vscode.workspace.createFileSystemWatcher(
      new vscode.RelativePattern(vscode.Uri.file(path.dirname(file)), path.basename(file)),
    );
    w.onDidCreate(fire);
    w.onDidChange(fire);
    w.onDidDelete(fire);
    return w;
  });
  return new vscode.Disposable(() => {
    if (timer !== undefined) clearTimeout(timer);
    watchers.forEach((w) => {
      w.dispose();
    });
  });
}
