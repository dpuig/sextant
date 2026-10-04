import type { KubeContext } from '../kubeconfig';
import { S } from '../ui/strings';
import { authLabel, platformOf, UNTAGGED } from './fleetTree';
import type { ResolvedTag } from './tags';

/** What the status bar item shows. `critical` selects `statusBarItem.errorBackground`; the text carries it too. */
export interface StatusModel {
  text: string;
  tooltip: string;
  accessibility: string;
  critical: boolean;
  /** Command run on click. */
  command: 'sextant.openTerminal' | 'workbench.action.openSettings';
  commandArgs?: unknown[];
}

export function statusModel(
  c: KubeContext | undefined,
  tag: ResolvedTag | undefined,
  now: Date,
): StatusModel {
  if (c === undefined) {
    return {
      text: S.status.none,
      tooltip: S.status.noneTooltip,
      accessibility: S.status.noneAccessibility,
      critical: false,
      command: 'workbench.action.openSettings',
      commandArgs: ['sextant.kubeconfigPaths'],
    };
  }
  const env = tag?.environment ?? UNTAGGED.toLowerCase();
  const critical = tag?.critical ?? false;
  const first = `${c.name} · ${env}${critical ? ', critical' : ''}`;
  return {
    text: critical ? `$(warning) ${env.toUpperCase()}  ${c.name}` : `$(server-environment) ${c.name}`,
    tooltip: [
      first,
      `Cluster: ${c.clusterName} · ${platformOf(c.provider).label}`,
      `Server: ${c.serverHost ?? 'n/a'}`,
      `Authentication: ${authLabel(c, now)}`,
      S.status.click,
    ].join('\n'),
    accessibility: `Kubernetes context ${c.name}, ${env}${critical ? ', critical' : ''}. Activate to open a terminal for a cluster.`,
    critical,
    command: 'sextant.openTerminal',
  };
}

/**
 * Should a toast appear because the global current context changed? Only for a switch made outside Sextant (Sextant
 * never changes it in E1) to a CRITICAL context, never on the first load, and not again for a context the user
 * already dismissed this session.
 */
export function shouldWarnOnSwitch(args: {
  previous: string | undefined;
  next: string | undefined;
  nextIsCritical: boolean;
  dismissed: ReadonlySet<string>;
}): boolean {
  const { previous, next, nextIsCritical, dismissed } = args;
  if (previous === undefined || next === undefined || previous === next) return false;
  return nextIsCritical && !dismissed.has(next);
}
