import * as vscode from 'vscode';
import type { TagRule } from './model/tags';

/**
 * Writes go to USER settings (ConfigurationTarget.Global). `sextant.tags` and `sextant.confirm.skip` are declared with
 * application scope, so a workspace can neither set nor override them: a repository must not be able to downgrade prod
 * to "normal" or silence the production confirmation.
 */
const section = (): vscode.WorkspaceConfiguration => vscode.workspace.getConfiguration('sextant');

export const readKubeconfigPaths = (): string[] =>
  section()
    .get<string[]>('kubeconfigPaths', [])
    .filter((p) => typeof p === 'string');
export const readTags = (): unknown => section().get<unknown>('tags');
export const readConfirmSkip = (): string[] =>
  section()
    .get<unknown[]>('confirm.skip', [])
    .filter((s): s is string => typeof s === 'string');

export async function saveTags(rules: readonly TagRule[]): Promise<void> {
  await section().update(
    'tags',
    rules.map((r) => ({ match: r.match, environment: r.environment, critical: r.critical })),
    vscode.ConfigurationTarget.Global,
  );
}

export async function addConfirmSkip(name: string): Promise<void> {
  const current = readConfirmSkip();
  if (!current.includes(name))
    await section().update('confirm.skip', [...current, name], vscode.ConfigurationTarget.Global);
}
