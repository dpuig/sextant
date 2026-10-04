import type { KubeContext } from '../kubeconfig';

/**
 * The fleet tree as plain data: environment -> provider -> context. Pure (no `vscode`), so grouping, ordering, filtering
 * and the text shown to the user are unit-tested, and the canary harness can check every label and tooltip.
 */
export type FleetNode =
  | { kind: 'environment'; id: string; label: string; children: FleetNode[] }
  | { kind: 'provider'; id: string; label: string; children: FleetNode[] }
  | { kind: 'context'; id: string; label: string; context: KubeContext; current: boolean };

export interface BuildOptions {
  currentContext?: string;
  /** Environment tag of a context (Task 7); `undefined` means untagged. */
  environmentOf?: (c: KubeContext) => string | undefined;
  /** Case-insensitive substring over name, cluster, user, host, provider and environment. */
  filter?: string;
}

export const UNTAGGED = 'Untagged';
export const OTHER_PROVIDER = 'Other';

const PROVIDER_LABELS: Record<string, string> = {
  eks: 'Amazon EKS',
  gke: 'Google GKE',
  aks: 'Azure AKS',
  openshift: 'OpenShift',
  kind: 'kind',
  minikube: 'minikube',
  'docker-desktop': 'Docker Desktop',
};

export function providerLabel(provider: string): string {
  return PROVIDER_LABELS[provider] ?? OTHER_PROVIDER;
}

export function buildFleetTree(contexts: readonly KubeContext[], opts: BuildOptions = {}): FleetNode[] {
  const envOf = opts.environmentOf ?? (() => undefined);
  const needle = (opts.filter ?? '').trim().toLowerCase();

  // environment -> provider label -> contexts
  const groups = new Map<string, Map<string, KubeContext[]>>();
  for (const c of contexts) {
    const env = envOf(c) ?? UNTAGGED;
    if (needle !== '' && !matches(c, env, needle)) continue;
    const provider = providerLabel(c.provider);
    const byProvider = groups.get(env) ?? new Map<string, KubeContext[]>();
    const list = byProvider.get(provider) ?? [];
    list.push(c);
    byProvider.set(provider, list);
    groups.set(env, byProvider);
  }

  const envs = [...groups.keys()].sort(untaggedLast);
  return envs.map((env): FleetNode => {
    const byProvider = groups.get(env) ?? new Map<string, KubeContext[]>();
    const providers = [...byProvider.keys()].sort(otherLast);
    return {
      kind: 'environment',
      id: `env:${env}`,
      label: env,
      children: providers.map((p): FleetNode => ({
        kind: 'provider',
        id: `prov:${env}/${p}`,
        label: p,
        children: (byProvider.get(p) ?? [])
          .slice()
          .sort((a, b) => a.name.localeCompare(b.name))
          .map((c): FleetNode => ({
            kind: 'context',
            id: `ctx:${c.name}`,
            label: c.name,
            context: c,
            current: c.name === opts.currentContext,
          })),
      })),
    };
  });
}

export function countContexts(nodes: readonly FleetNode[]): number {
  let n = 0;
  for (const node of nodes) {
    if (node.kind === 'context') n++;
    else n += countContexts(node.children);
  }
  return n;
}

function matches(c: KubeContext, env: string, needle: string): boolean {
  return [
    c.name,
    c.clusterName,
    c.userName,
    c.serverHost ?? '',
    c.provider,
    providerLabel(c.provider),
    env,
  ].some((s) => s.toLowerCase().includes(needle));
}

const untaggedLast = (a: string, b: string): number =>
  a === b ? 0 : a === UNTAGGED ? 1 : b === UNTAGGED ? -1 : a.localeCompare(b);
const otherLast = (a: string, b: string): number =>
  a === b ? 0 : a === OTHER_PROVIDER ? 1 : b === OTHER_PROVIDER ? -1 : a.localeCompare(b);

/** One line under the context name: current marker, how it authenticates, and where it points. */
export function contextDescription(c: KubeContext, current: boolean): string {
  const parts: string[] = [];
  if (current) parts.push('current');
  parts.push(authLabel(c));
  if (c.serverHost !== undefined) parts.push(c.serverHost);
  return parts.join(' · ');
}

export function authLabel(c: KubeContext): string {
  const { kind, provider } = c.credential;
  switch (kind) {
    case 'static-token':
      return 'static token';
    case 'client-cert':
      return 'client certificate';
    case 'basic-auth':
      return 'basic auth';
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

/** Plain-text tooltip. Names, hosts and credential classification only; never a credential value. */
export function contextTooltip(c: KubeContext, current: boolean): string {
  const lines = [
    `Context: ${c.name}${current ? '  (current)' : ''}`,
    `Cluster: ${c.clusterName}`,
    `User: ${c.userName}`,
  ];
  if (c.namespace !== undefined) lines.push(`Namespace: ${c.namespace}`);
  if (c.serverHost !== undefined) lines.push(`Server: ${c.serverHost}`);
  lines.push(`Authentication: ${authLabel(c)}${c.credential.longLived ? ' (long-lived)' : ''}`);
  if (c.credential.expiresAt !== undefined)
    lines.push(`Certificate expires: ${c.credential.expiresAt.toISOString().slice(0, 10)}`);
  lines.push(`Defined in: ${c.sourceFile}`);
  return lines.join('\n');
}
