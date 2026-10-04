import { describe, expect, it } from 'vitest';
import type { KubeContext } from '../../src/kubeconfig';
import {
  authLabel,
  buildFleetTree,
  contextAccessibility,
  contextDescription,
  contextTooltip,
  countContexts,
  detailRows,
  UNTAGGED,
  type ContextView,
  type FleetNode,
} from '../../src/model/fleetTree';
import type { ResolvedTag } from '../../src/model/tags';

const NOW = new Date('2026-10-04T00:00:00Z');

const ctx = (name: string, over: Partial<KubeContext> = {}): KubeContext => ({
  name,
  clusterName: `${name}-cluster`,
  userName: `${name}-user`,
  sourceFile: '/home/u/.kube/config',
  credential: { kind: 'exec-plugin', longLived: false },
  provider: 'unknown',
  ...over,
});

const view = (c: KubeContext, over: Partial<ContextView> = {}): ContextView => ({
  context: c,
  environment: UNTAGGED,
  critical: false,
  current: false,
  terminal: false,
  ...over,
});

const tagFrom = (m: Record<string, ResolvedTag>) => (c: KubeContext) => m[c.name];
const shape = (nodes: FleetNode[]): unknown[] =>
  nodes.map((n) => (n.kind === 'context' ? n.label : { [n.label]: shape(n.children) }));

describe('buildFleetTree: structure and order', () => {
  it('orders environments prod, staging, dev, custom, Untagged and platforms by the design ranking, not alphabetically', () => {
    const cs = [
      ctx('a', { provider: 'kind' }),
      ctx('b', { provider: 'eks' }),
      ctx('c', { provider: 'unknown' }),
      ctx('d', { provider: 'gke' }),
      ctx('e', { provider: 'aks' }),
      ctx('f', { provider: 'openshift' }),
    ];
    const tag = tagFrom({
      a: { environment: 'dev', critical: false },
      b: { environment: 'prod', critical: true },
      c: { environment: 'qa', critical: false },
      d: { environment: 'staging', critical: false },
      e: { environment: 'prod', critical: true },
    });
    const tree = buildFleetTree(cs, { tagOf: tag, now: NOW });
    expect(tree.map((n) => n.label)).toEqual(['prod', 'staging', 'dev', 'qa', UNTAGGED]);
    expect(shape(tree)[0]).toEqual({ prod: [{ 'Amazon EKS': ['b'] }, { 'Azure AKS': ['e'] }] }); // EKS before AKS, per ranking
    expect(shape(tree)[4]).toEqual({ [UNTAGGED]: [{ OpenShift: ['f'] }] });
  });

  it('sorts contexts by name within a platform and puts "Other" last', () => {
    const tree = buildFleetTree([ctx('z', { provider: 'kind' }), ctx('a', { provider: 'kind' }), ctx('m')], {
      now: NOW,
    });
    expect(shape(tree)).toEqual([{ [UNTAGGED]: [{ kind: ['a', 'z'] }, { Other: ['m'] }] }]);
  });

  it('prod groups read "{n} · critical"; others show the count only', () => {
    const tag = tagFrom({
      a: { environment: 'prod', critical: true },
      b: { environment: 'prod', critical: true },
      c: { environment: 'dev', critical: false },
    });
    const tree = buildFleetTree([ctx('a'), ctx('b'), ctx('c')], { tagOf: tag, now: NOW });
    const byLabel = Object.fromEntries(tree.map((n) => [n.label, n])) as Record<
      string,
      Extract<FleetNode, { kind: 'environment' }>
    >;
    expect(byLabel.prod?.description).toBe('2 · critical');
    expect(byLabel.dev?.description).toBe('1');
    expect(byLabel.prod?.icon).toBe('warning');
    expect(byLabel.dev?.icon).toBe('code');
  });

  it('collapses environment groups only above 40 contexts', () => {
    const many = (n: number) => Array.from({ length: n }, (_, i) => ctx(`c${String(i)}`));
    const flags = (n: number) =>
      buildFleetTree(many(n), { now: NOW }).map((x) => (x.kind === 'environment' ? x.collapsed : undefined));
    expect(flags(40)).toEqual([false]);
    expect(flags(41)).toEqual([true]);
  });

  it('with no tags at all everything is Untagged and nothing is hidden', () => {
    const tree = buildFleetTree([ctx('a'), ctx('b')], { now: NOW });
    expect(countContexts(tree)).toBe(2);
    expect(tree.map((n) => n.label)).toEqual([UNTAGGED]);
  });

  it('does not mutate its input', () => {
    const cs = [ctx('b'), ctx('a')];
    buildFleetTree(cs, { now: NOW });
    expect(cs.map((c) => c.name)).toEqual(['b', 'a']);
  });
});

describe('buildFleetTree: filter', () => {
  const cs = [
    ctx('alpha', { serverHost: 'api.one.example.com:6443', provider: 'eks' }),
    ctx('beta', { clusterName: 'special-cluster' }),
    ctx('gamma', { provider: 'gke' }),
  ];
  const n = (f: string): number => countContexts(buildFleetTree(cs, { filter: f, now: NOW }));

  it('matches name, platform, environment and server host, case-insensitively', () => {
    expect(n('ALPHA')).toBe(1);
    expect(n('amazon')).toBe(1); // platform label
    expect(n('gke')).toBe(1); // platform id
    expect(n('one.example')).toBe(1); // server host
    expect(n('untagged')).toBe(3); // environment
    expect(n('nothing-here')).toBe(0);
  });

  it('does NOT match cluster or user names (the design lists name, platform, environment and server)', () => {
    expect(n('special-cluster')).toBe(0);
    expect(n('gamma-user')).toBe(0);
  });

  it('whitespace is no filter, and an empty result is an empty tree (the view shows its own message)', () => {
    expect(n('   ')).toBe(3);
    expect(buildFleetTree(cs, { filter: 'nope', now: NOW })).toEqual([]);
  });
});

describe('context rows', () => {
  it('marks only the current context, with the filled check and the contributed colour', () => {
    const tree = buildFleetTree([ctx('a'), ctx('b')], { currentContext: 'b', now: NOW });
    const rows = (tree[0] as Extract<FleetNode, { kind: 'environment' }>).children.flatMap(
      (p) => (p as Extract<FleetNode, { kind: 'platform' }>).children,
    ) as Extract<FleetNode, { kind: 'context' }>[];
    expect(rows.map((r) => [r.label, r.icon, r.colorId])).toEqual([
      ['a', 'circle-large-outline', undefined],
      ['b', 'pass-filled', 'sextant.currentContextForeground'],
    ]);
  });

  it('describes a context as current · terminal · auth · host, each part optional, in that order', () => {
    const c = ctx('x', {
      serverHost: 'h:6443',
      credential: { kind: 'cloud-iam', longLived: false, provider: 'aws' },
    });
    expect(contextDescription(view(c, { current: true, terminal: true }), NOW)).toBe(
      'current · terminal · aws IAM · h:6443',
    );
    expect(contextDescription(view(c), NOW)).toBe('aws IAM · h:6443');
    expect(contextDescription(view(ctx('y', { credential: { kind: 'none', longLived: false } })), NOW)).toBe(
      'no credentials',
    );
  });

  it('uses the Fleet wording for authentication, including expiry judged against "now"', () => {
    const label = (cr: KubeContext['credential']): string => authLabel(ctx('x', { credential: cr }), NOW);
    expect(label({ kind: 'static-token', longLived: true })).toBe('static token · long-lived');
    expect(label({ kind: 'basic-auth', longLived: true })).toBe('basic auth · long-lived');
    expect(label({ kind: 'client-cert', longLived: true, expiresAt: new Date('2036-01-01') })).toBe(
      'client cert · long-lived',
    );
    expect(label({ kind: 'client-cert', longLived: true, expiresAt: new Date('2026-03-02') })).toBe(
      'client cert · expired',
    );
    expect(label({ kind: 'client-cert', longLived: false, expiresAt: new Date('2026-10-05') })).toBe(
      'client cert',
    );
    expect(label({ kind: 'cloud-iam', longLived: false, provider: 'gcp' })).toBe('gcp IAM');
    expect(label({ kind: 'cloud-iam', longLived: false, provider: 'azure' })).toBe('azure IAM');
    expect(label({ kind: 'oidc', longLived: false })).toBe('OIDC');
    expect(label({ kind: 'exec-plugin', longLived: false, provider: 'unknown' })).toBe('exec plugin');
  });

  it('tooltip is plain text, one "Label: value" per line, in the design order', () => {
    const c = ctx('kind-local', {
      namespace: 'web',
      serverHost: '127.0.0.1:41873',
      credential: { kind: 'client-cert', longLived: true, expiresAt: new Date('2026-12-31T00:00:00Z') },
    });
    expect(contextTooltip(c, true, NOW).split('\n')).toEqual([
      'Context: kind-local (current)',
      'Cluster: kind-local-cluster',
      'User: kind-local-user',
      'Namespace: web',
      'Server: 127.0.0.1:41873',
      'Authentication: client cert (long-lived)',
      'Certificate expiry: 2026-12-31',
      'Defined in: /home/u/.kube/config',
    ]);
    const t = contextTooltip(ctx('x', { credential: { kind: 'static-token', longLived: true } }), false, NOW);
    expect(t).toContain('Namespace: default');
    expect(t).toContain('Certificate expiry: n/a (token)');
    expect(t).toContain('Authentication: static token (long-lived)');
  });

  it('expands to the five detail rows with the design icons', () => {
    const rows = detailRows(
      ctx('x', {
        serverHost: 'h',
        credential: { kind: 'client-cert', longLived: true, expiresAt: new Date('2026-03-02T00:00:00Z') },
      }),
      NOW,
    );
    expect(rows.map((r) => [r.label, r.value, r.icon])).toEqual([
      ['Server', 'h', 'globe'],
      ['Namespace', 'default', 'symbol-namespace'],
      ['Credential', 'client cert · expired', 'key'],
      ['Expires', 'expired 2026-03-02', 'calendar'],
      ['Source file', '/home/u/.kube/config', 'file'],
    ]);
    expect(rows[3]?.accessibility).toBe('Expires: expired 2 March 2026');
    expect(
      detailRows(ctx('t', { credential: { kind: 'static-token', longLived: true } }), NOW)[3]?.value,
    ).toBe('no expiry');
    expect(
      detailRows(ctx('e', { credential: { kind: 'cloud-iam', longLived: false, provider: 'aws' } }), NOW)[3]
        ?.value,
    ).toBe('issued on demand');
  });

  it('screen-reader label names environment and criticality BEFORE the platform', () => {
    const v = view(
      ctx('prod-eu-1', {
        provider: 'eks',
        credential: { kind: 'cloud-iam', longLived: false, provider: 'aws' },
      }),
      {
        environment: 'prod',
        critical: true,
        terminal: true,
      },
    );
    expect(contextAccessibility(v, NOW)).toBe(
      'prod-eu-1, prod, critical, Amazon EKS, aws IAM, terminal open',
    );
    const cur = view(
      ctx('staging-eu-1', {
        provider: 'gke',
        credential: { kind: 'cloud-iam', longLived: false, provider: 'gcp' },
      }),
      {
        environment: 'staging',
        current: true,
      },
    );
    expect(contextAccessibility(cur, NOW)).toBe(
      'staging-eu-1, current context, staging, Google GKE, gcp IAM',
    );
  });

  it('group accessibility labels follow the design', () => {
    const tag = tagFrom({ a: { environment: 'prod', critical: true } });
    const tree = buildFleetTree([ctx('a', { provider: 'eks' })], { tagOf: tag, now: NOW });
    expect(tree[0]?.accessibility).toBe('prod environment, critical, 1 context');
    const env = tree[0] as Extract<FleetNode, { kind: 'environment' }>;
    expect(env.children[0]?.accessibility).toBe('Amazon EKS, 1 context');
  });
});
