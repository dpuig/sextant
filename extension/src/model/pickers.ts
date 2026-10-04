import { S } from '../ui/strings';
import { platformOf, UNTAGGED, type ContextView } from './fleetTree';
import { compareEnvironments } from './tags';

/** Plain data for the QuickPicks, so ordering, wording and icons are unit-tested without a VS Code host. */
export type PickEntry =
  | { kind: 'separator'; label: string }
  | {
      kind: 'item';
      label: string;
      description: string;
      iconId: string;
      contextName: string;
      critical: boolean;
    };

/**
 * Open-terminal list: a "current" section first (the current context, pinned), then prod, staging, dev, custom
 * environments, and Untagged. The current context appears once, in its own section.
 */
export function openTerminalEntries(views: readonly ContextView[]): PickEntry[] {
  const out: PickEntry[] = [];
  const item = (v: ContextView): PickEntry => ({
    kind: 'item',
    label: v.context.name,
    description:
      `${v.critical ? 'critical' : v.environment === UNTAGGED ? 'untagged' : v.environment} · ${platformOf(v.context.provider).label}` +
      (v.terminal ? ` · ${S.openTerminal.terminalOpen}` : ''),
    iconId: v.current ? 'pass-filled' : v.critical ? 'warning' : 'circle-large-outline',
    contextName: v.context.name,
    critical: v.critical,
  });
  const current = views.filter((v) => v.current);
  if (current.length > 0) {
    out.push({ kind: 'separator', label: 'current' }, ...current.map(item));
  }
  const rest = views.filter((v) => !v.current);
  const envs = [...new Set(rest.map((v) => v.environment))].sort((a, b) =>
    a === UNTAGGED ? 1 : b === UNTAGGED ? -1 : compareEnvironments(a, b),
  );
  for (const env of envs) {
    out.push({ kind: 'separator', label: env });
    out.push(
      ...rest
        .filter((v) => v.environment === env)
        .sort((a, b) => a.context.name.localeCompare(b.context.name))
        .map(item),
    );
  }
  return out;
}

export type ConfirmChoice = 'cancel' | 'open' | 'openAndSkip' | 'editTags';

/** Cancel first, so it is focused and Enter does the safe thing (a native dialog cannot default to Cancel). */
export function confirmEntries(name: string): { id: ConfirmChoice; label: string; iconId: string }[] {
  return [
    { id: 'cancel', label: S.confirm.cancel, iconId: 'close' },
    { id: 'open', label: S.confirm.open(name), iconId: 'check' },
    { id: 'openAndSkip', label: S.confirm.openAndSkip, iconId: 'check' },
    { id: 'editTags', label: S.confirm.editTags, iconId: 'edit' },
  ];
}

/** A terminal on a critical context asks first, unless the user chose "don't ask again" for that exact context. */
export const needsConfirmation = (critical: boolean, skip: readonly string[], name: string): boolean =>
  critical && !skip.includes(name);
