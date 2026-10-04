import { describe, expect, it } from 'vitest';
import type { KubeContext } from '../../src/kubeconfig';
import {
  buildFleetTree,
  contextDescription,
  contextTooltip,
  countContexts,
  UNTAGGED,
  type FleetNode,
} from '../../src/model/fleetTree';

const ctx = (name: string, over: Partial<KubeContext> = {}): KubeContext => ({
  name,
  clusterName: `${name}-cluster`,
  userName: `${name}-user`,
  sourceFile: '/home/u/.kube/config',
  credential: { kind: 'exec-plugin', longLived: false },
  provider: 'unknown',
  ...over,
});

const labels = (nodes: FleetNode[]): unknown[] =>
  nodes.map((n) => (n.kind === 'context' ? n.label : { [n.label]: labels(n.children) }));

describe('buildFleetTree', () => {
  it('groups by environment then provider, with context names sorted', () => {
    const tree = buildFleetTree(
      [ctx('b', { provider: 'eks' }), ctx('a', { provider: 'eks' }), ctx('z', { provider: 'gke' })],
      {
        environmentOf: () => 'prod',
      },
    );
    expect(labels(tree)).toEqual([{ prod: [{ 'Amazon EKS': ['a', 'b'] }, { 'Google GKE': ['z'] }] }]);
  });

  it('puts untagged contexts under "Untagged", which sorts after every real environment', () => {
    const tag = (c: KubeContext): string | undefined =>
      c.name === 'p' ? 'prod' : c.name === 'd' ? 'dev' : undefined;
    const tree = buildFleetTree([ctx('x'), ctx('p'), ctx('d')], { environmentOf: tag });
    expect(tree.map((n) => n.label)).toEqual(['dev', 'prod', UNTAGGED]);
  });

  it('with no tagging at all, everything is under Untagged and nothing is hidden', () => {
    const tree = buildFleetTree([ctx('a'), ctx('b')]);
    expect(labels(tree)).toEqual([{ [UNTAGGED]: [{ Other: ['a', 'b'] }] }]);
    expect(countContexts(tree)).toBe(2);
  });

  it('sorts the "Other" provider group last', () => {
    const tree = buildFleetTree([ctx('a'), ctx('b', { provider: 'kind' }), ctx('c', { provider: 'aks' })]);
    const providers = tree[0]?.kind === 'environment' ? tree[0].children.map((n) => n.label) : [];
    expect(providers).toEqual(['Azure AKS', 'kind', 'Other']);
  });

  it('marks exactly the current context', () => {
    const tree = buildFleetTree([ctx('a'), ctx('b')], { currentContext: 'b' });
    const flat: FleetNode[] = [];
    const walk = (ns: FleetNode[]): void => {
      for (const n of ns) {
        if (n.kind === 'context') flat.push(n);
        else walk(n.children);
      }
    };
    walk(tree);
    expect(flat.map((n) => n.kind === 'context' && [n.label, n.current])).toEqual([
      ['a', false],
      ['b', true],
    ]);
  });

  it('filters case-insensitively across name, cluster, user, host, provider and environment, and drops empty groups', () => {
    const cs = [
      ctx('alpha', { serverHost: 'api.one.example.com:6443', provider: 'eks' }),
      ctx('beta', { clusterName: 'special-cluster' }),
      ctx('gamma', { userName: 'dana@example.com', provider: 'gke' }),
    ];
    const names = (f: string): number => countContexts(buildFleetTree(cs, { filter: f }));
    expect(names('ALPHA')).toBe(1);
    expect(names('special')).toBe(1);
    expect(names('dana')).toBe(1);
    expect(names('one.example')).toBe(1);
    expect(names('amazon')).toBe(1); // provider label
    expect(names('untagged')).toBe(3); // environment
    expect(names('nothing-matches')).toBe(0);
    expect(buildFleetTree(cs, { filter: 'nothing-matches' })).toEqual([]);
    expect(names('   ')).toBe(3); // whitespace is no filter
  });

  it('does not mutate its input', () => {
    const cs = [ctx('b'), ctx('a')];
    buildFleetTree(cs);
    expect(cs.map((c) => c.name)).toEqual(['b', 'a']);
  });
});

describe('labels and tooltips', () => {
  it('describes a context by current marker, authentication and host', () => {
    const c = ctx('x', {
      serverHost: 'h:6443',
      credential: { kind: 'cloud-iam', longLived: false, provider: 'aws' },
    });
    expect(contextDescription(c, true)).toBe('current · aws IAM · h:6443');
    expect(
      contextDescription(ctx('y', { credential: { kind: 'static-token', longLived: true } }), false),
    ).toBe('static token');
  });

  it('flags long-lived credentials and shows certificate expiry in the tooltip', () => {
    const c = ctx('x', {
      namespace: 'web',
      serverHost: 'h',
      credential: { kind: 'client-cert', longLived: true, expiresAt: new Date('2036-01-01T00:00:00Z') },
    });
    const t = contextTooltip(c, false);
    expect(t).toContain('Authentication: client certificate (long-lived)');
    expect(t).toContain('Certificate expires: 2036-01-01');
    expect(t).toContain('Namespace: web');
    expect(t).toContain('Defined in: /home/u/.kube/config');
  });
});
