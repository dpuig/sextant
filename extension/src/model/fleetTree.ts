import type { KubeContext } from '../kubeconfig';
import { compareEnvironments, type ResolvedTag } from './tags';

/**
 * The fleet tree as plain data: Environment > Platform > Context > detail rows. Pure (no `vscode`), so grouping,
 * ordering, filtering and every string shown are unit-tested and the canary harness can check them. Layout, labels,
 * icons and wording follow the design system (extension/ui-ux/sextant-design-system).
 */
export const UNTAGGED = 'Untagged';
export const COLLAPSE_ABOVE = 40;

/** Platform order is fixed by the design: it is a ranking, not alphabetical. */
const PLATFORMS: { id: string; label: string; icon: string }[] = [
  { id: 'eks', label: 'Amazon EKS', icon: 'cloud' },
  { id: 'gke', label: 'Google GKE', icon: 'cloud' },
  { id: 'aks', label: 'Azure AKS', icon: 'cloud' },
  { id: 'openshift', label: 'OpenShift', icon: 'server' },
  { id: 'kind', label: 'kind', icon: 'vm' },
  { id: 'minikube', label: 'minikube', icon: 'vm' },
  { id: 'docker-desktop', label: 'Docker Desktop', icon: 'vm' },
];
const OTHER = { id: 'other', label: 'Other', icon: 'symbol-misc' };

export const platformOf = (provider: string): { id: string; label: string; icon: string } =>
  PLATFORMS.find((p) => p.id === provider) ?? OTHER;

export interface ContextView {
  context: KubeContext;
  environment: string; // UNTAGGED when there is no tag
  critical: boolean;
  current: boolean;
  /** A bound terminal is open for this context. */
  terminal: boolean;
}

export interface DetailRow {
  kind: 'detail';
  id: string;
  label: string;
  value: string;
  icon: string;
  accessibility: string;
}

export type FleetNode =
  | {
      kind: 'environment';
      id: string;
      label: string;
      environment: string;
      critical: boolean;
      description: string;
      icon: string;
      colorId: string;
      accessibility: string;
      collapsed: boolean;
      children: FleetNode[];
    }
  | {
      kind: 'platform';
      id: string;
      label: string;
      description: string;
      icon: string;
      accessibility: string;
      collapsed: boolean;
      children: FleetNode[];
    }
  | ({
      kind: 'context';
      id: string;
      label: string;
      description: string;
      tooltip: string;
      icon: string;
      colorId: string | undefined;
      accessibility: string;
      children: DetailRow[];
    } & {
      view: ContextView;
    });

export interface BuildOptions {
  currentContext?: string;
  tagOf?: (c: KubeContext) => ResolvedTag | undefined;
  /** Contexts that have a bound terminal open. */
  terminals?: ReadonlySet<string>;
  /** Case-insensitive substring over name, platform, environment and server host. */
  filter?: string;
  now?: Date;
}

export function buildFleetTree(contexts: readonly KubeContext[], opts: BuildOptions = {}): FleetNode[] {
  const now = opts.now ?? new Date();
  const needle = (opts.filter ?? '').trim().toLowerCase();
  const collapseGroups = contexts.length > COLLAPSE_ABOVE;

  const views: ContextView[] = contexts
    .map((context): ContextView => {
      const tag = opts.tagOf?.(context);
      return {
        context,
        environment: tag?.environment ?? UNTAGGED,
        critical: tag?.critical ?? false,
        current: context.name === opts.currentContext,
        terminal: opts.terminals?.has(context.name) ?? false,
      };
    })
    .filter((v) => needle === '' || matchesFilter(v, needle));

  const byEnv = new Map<string, ContextView[]>();
  for (const v of views) byEnv.set(v.environment, [...(byEnv.get(v.environment) ?? []), v]);

  const envs = [...byEnv.keys()].sort((a, b) =>
    a === UNTAGGED ? 1 : b === UNTAGGED ? -1 : compareEnvironments(a, b),
  );
  return envs.map((env): FleetNode => {
    const inEnv = byEnv.get(env) ?? [];
    const critical = env === 'prod';
    const platforms = [...PLATFORMS, OTHER].filter((p) =>
      inEnv.some((v) => platformOf(v.context.provider).id === p.id),
    );
    return {
      kind: 'environment',
      id: `env:${env}`,
      label: env,
      environment: env,
      critical,
      description: critical ? `${String(inEnv.length)} · critical` : String(inEnv.length),
      icon: envIcon(env),
      colorId: envColorId(env),
      accessibility: `${env} environment${critical ? ', critical' : ''}, ${plural(inEnv.length, 'context')}`,
      collapsed: collapseGroups,
      children: platforms.map((p): FleetNode => {
        const inPlatform = inEnv
          .filter((v) => platformOf(v.context.provider).id === p.id)
          .sort((a, b) => a.context.name.localeCompare(b.context.name));
        return {
          kind: 'platform',
          id: `plat:${env}/${p.id}`,
          label: p.label,
          description: String(inPlatform.length),
          icon: p.icon,
          accessibility: `${p.label}, ${plural(inPlatform.length, 'context')}`,
          collapsed: false,
          children: inPlatform.map((v) => contextNode(v, now)),
        };
      }),
    };
  });
}

function contextNode(v: ContextView, now: Date): Extract<FleetNode, { kind: 'context' }> {
  const c = v.context;
  return {
    kind: 'context',
    id: `ctx:${c.name}`,
    label: c.name,
    description: contextDescription(v, now),
    tooltip: contextTooltip(c, v.current, now),
    icon: v.current ? 'pass-filled' : 'circle-large-outline',
    colorId: v.current ? 'sextant.currentContextForeground' : undefined,
    accessibility: contextAccessibility(v, now),
    children: detailRows(c, now),
    view: v,
  };
}

function matchesFilter(v: ContextView, needle: string): boolean {
  const c = v.context;
  return [c.name, platformOf(c.provider).label, c.provider, v.environment, c.serverHost ?? ''].some((s) =>
    s.toLowerCase().includes(needle),
  );
}

export function countContexts(nodes: readonly FleetNode[]): number {
  let n = 0;
  for (const node of nodes) n += node.kind === 'context' ? 1 : countContexts(node.children);
  return n;
}

const plural = (n: number, word: string): string => `${String(n)} ${word}${n === 1 ? '' : 's'}`;

function envIcon(env: string): string {
  return env === 'prod'
    ? 'warning'
    : env === 'staging'
      ? 'beaker'
      : env === 'dev'
        ? 'code'
        : env === UNTAGGED
          ? 'question'
          : 'tag';
}

function envColorId(env: string): string {
  return env === 'prod'
    ? 'sextant.env.prodForeground'
    : env === 'staging'
      ? 'sextant.env.stagingForeground'
      : env === 'dev'
        ? 'sextant.env.devForeground'
        : 'sextant.env.untaggedForeground';
}

// ---- text -----------------------------------------------------------------------------------------------------

export const isoDate = (d: Date): string => d.toISOString().slice(0, 10);
const longDate = (d: Date): string =>
  `${String(d.getUTCDate())} ${d.toLocaleString('en-GB', { month: 'long', timeZone: 'UTC' })} ${String(d.getUTCFullYear())}`;

export function isExpired(c: KubeContext, now: Date): boolean {
  return c.credential.expiresAt !== undefined && c.credential.expiresAt.getTime() < now.getTime();
}

/** How the context authenticates, in the Fleet view's wording ("aws IAM", "client cert · long-lived", ...). */
export function authLabel(c: KubeContext, now: Date): string {
  const { kind, provider } = c.credential;
  switch (kind) {
    case 'static-token':
      return 'static token · long-lived';
    case 'basic-auth':
      return 'basic auth · long-lived';
    case 'client-cert':
      return isExpired(c, now)
        ? 'client cert · expired'
        : c.credential.longLived
          ? 'client cert · long-lived'
          : 'client cert';
    case 'oidc':
      return 'OIDC';
    case 'cloud-iam':
      return provider === undefined ? 'cloud IAM' : `${provider} IAM`;
    case 'exec-plugin':
      return provider === undefined || provider === 'unknown' ? 'exec plugin' : `${provider} plugin`;
    case 'none':
      return 'no credentials';
  }
}

/** `current · terminal · {auth} · {host}`, each part optional, in that order. */
export function contextDescription(v: ContextView, now: Date): string {
  const parts: string[] = [];
  if (v.current) parts.push('current');
  if (v.terminal) parts.push('terminal');
  parts.push(authLabel(v.context, now));
  if (v.context.serverHost !== undefined) parts.push(v.context.serverHost);
  return parts.join(' · ');
}

function expiryText(c: KubeContext, now: Date): string {
  const exp = c.credential.expiresAt;
  if (exp !== undefined) return isExpired(c, now) ? `expired ${isoDate(exp)}` : isoDate(exp);
  return c.credential.kind === 'static-token' ||
    c.credential.kind === 'basic-auth' ||
    c.credential.kind === 'client-cert'
    ? 'no expiry'
    : 'issued on demand';
}

/** Plain-text tooltip, one `Label: value` per line. Names, hosts and classification only; never a credential value. */
export function contextTooltip(c: KubeContext, current: boolean, now: Date): string {
  const exp = c.credential.expiresAt;
  const auth = authLabel(c, now).replace(' · long-lived', '').replace(' · expired', '');
  return [
    `Context: ${c.name}${current ? ' (current)' : ''}`,
    `Cluster: ${c.clusterName}`,
    `User: ${c.userName}`,
    `Namespace: ${c.namespace ?? 'default'}`,
    `Server: ${c.serverHost ?? 'n/a'}`,
    `Authentication: ${auth}${c.credential.longLived ? ' (long-lived)' : ''}`,
    `Certificate expiry: ${exp !== undefined ? isoDate(exp) : 'n/a (token)'}`,
    `Defined in: ${c.sourceFile}`,
  ].join('\n');
}

export function detailRows(c: KubeContext, now: Date): DetailRow[] {
  const rows: [string, string, string][] = [
    ['Server', c.serverHost ?? 'n/a', 'globe'],
    ['Namespace', c.namespace ?? 'default', 'symbol-namespace'],
    ['Credential', authLabel(c, now), 'key'],
    ['Expires', expiryText(c, now), 'calendar'],
    ['Source file', c.sourceFile, 'file'],
  ];
  return rows.map(([label, value, icon]) => ({
    kind: 'detail',
    id: `detail:${c.name}/${label}`,
    label,
    value,
    icon,
    accessibility:
      label === 'Expires' && c.credential.expiresAt !== undefined
        ? `Expires: ${isExpired(c, now) ? 'expired ' : ''}${longDate(c.credential.expiresAt)}`
        : `${label}: ${value}`,
  }));
}

/** Screen-reader label: environment and criticality come BEFORE the platform, as the design requires. */
export function contextAccessibility(v: ContextView, now: Date): string {
  const parts = [v.context.name];
  if (v.current) parts.push('current context');
  parts.push(v.environment === UNTAGGED ? 'untagged' : v.environment);
  if (v.critical) parts.push('critical');
  parts.push(platformOf(v.context.provider).label, authLabel(v.context, now).replace(' · ', ', '));
  if (v.terminal) parts.push('terminal open');
  return parts.join(', ');
}
