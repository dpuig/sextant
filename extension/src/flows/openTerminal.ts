import { S } from '../ui/strings';
import type { PickEntry, Prompts } from '../ui/prompts';
import type { ContextView } from '../model/fleetTree';
import { confirmEntries, needsConfirmation, openTerminalEntries, type ConfirmChoice } from '../model/pickers';

export type OpenOutcome = 'opened' | 'cancelled';

export interface OpenDeps {
  prompts: Prompts;
  views: readonly ContextView[];
  skip: readonly string[];
  open(contextName: string): Promise<void>;
  addSkip(contextName: string): Promise<void>;
  editTags(contextName: string): Promise<void>;
  /** Re-read the views after tags change (a context may stop being critical). */
  reload(): readonly ContextView[];
}

/**
 * Open Terminal for Cluster…: choose a context (unless given), confirm if it is critical, open. The confirmation lists
 * Cancel first and focused, so Enter does the safe thing; a native dialog cannot default to Cancel.
 */
export async function openTerminalFlow(deps: OpenDeps, given?: string): Promise<OpenOutcome> {
  let views = deps.views;
  let name = given;

  if (name === undefined) {
    const entries = openTerminalEntries(views);
    const r = await deps.prompts.pick<string>({
      title: 'Open terminal for cluster',
      placeholder: S.openTerminal.placeholder(views.length),
      matchOnDescription: true,
      items: entries.map((e): PickEntry<string> =>
        e.kind === 'separator'
          ? e
          : {
              kind: 'item',
              label: e.label,
              description: e.description,
              iconId: e.iconId,
              value: e.contextName,
            },
      ),
    });
    if (r.kind !== 'picked') return 'cancelled';
    name = r.value;
  }

  // Editing tags can change the answer (a context may stop being critical), so re-evaluate; bounded, so a user who
  // keeps choosing "Edit tags…" cannot loop forever.
  for (let attempt = 0; attempt < 5; attempt++) {
    const view = views.find((v) => v.context.name === name);
    if (view === undefined) return 'cancelled'; // vanished since the list was built
    let skip = deps.skip;
    if (!needsConfirmation(view.critical, skip, view.context.name)) {
      await deps.open(view.context.name);
      return 'opened';
    }
    const choice = await deps.prompts.pick<ConfirmChoice>({
      title: S.confirm.title(view.context.name),
      placeholder: S.confirm.placeholder,
      ignoreFocusOut: true,
      items: confirmEntries(view.context.name).map((c) => ({
        kind: 'item',
        label: c.label,
        iconId: c.iconId,
        value: c.id,
      })),
    });
    if (choice.kind !== 'picked' || choice.value === 'cancel') return 'cancelled';
    if (choice.value === 'openAndSkip') {
      await deps.addSkip(view.context.name);
      skip = [...skip, view.context.name];
    }
    if (choice.value === 'editTags') {
      await deps.editTags(view.context.name);
      views = deps.reload();
      continue;
    }
    await deps.open(view.context.name);
    return 'opened';
  }
  return 'cancelled';
}
