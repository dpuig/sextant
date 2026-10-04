import * as path from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadFleet, type KubeContext } from '../../src/kubeconfig';
import { classify } from '../../src/kubeconfig/classify';
import { inferProvider } from '../../src/model/provider';
import { expectNoCanary, suspiciousKeys } from '../canary';

const dir = path.join(__dirname, '..', 'fixtures', 'distros');
const load = async (file: string): Promise<KubeContext[]> =>
  (await loadFleet({ env: { KUBECONFIG: path.join(dir, file) }, cwd: dir })).contexts;
const only = async (file: string, name?: string): Promise<KubeContext> => {
  const cs = await load(file);
  const c = name === undefined ? cs[0] : cs.find((x) => x.name === name);
  if (c === undefined) throw new Error(`no context ${String(name)} in ${file}`);
  return c;
};

describe('classification of real-world distro kubeconfigs', () => {
  // [fixture, context (default: first), kind, provider-from-credential, grouping provider]
  const table: [string, string | undefined, string, string | undefined, string][] = [
    ['eks.yaml', undefined, 'cloud-iam', 'aws', 'eks'],
    ['gke.yaml', undefined, 'cloud-iam', 'gcp', 'gke'],
    ['aks.yaml', undefined, 'cloud-iam', 'azure', 'aks'],
    ['aks-legacy.yaml', undefined, 'cloud-iam', 'azure', 'aks'],
    ['kind.yaml', undefined, 'client-cert', undefined, 'kind'],
    ['k3s.yaml', undefined, 'client-cert', undefined, 'unknown'],
    ['rke2.yaml', undefined, 'client-cert', undefined, 'unknown'],
    ['openshift.yaml', undefined, 'static-token', undefined, 'openshift'],
    ['oidc-login.yaml', undefined, 'exec-plugin', 'unknown', 'unknown'],
    ['oidc-plugin.yaml', undefined, 'oidc', 'oidc', 'unknown'],
    ['kx.yaml', undefined, 'exec-plugin', 'sextant', 'unknown'],
    ['unknown-exec.yaml', undefined, 'exec-plugin', 'unknown', 'unknown'],
  ];

  for (const [file, ctx, kind, credProvider, provider] of table) {
    it(`${file} -> ${kind}${credProvider ? ` (${credProvider})` : ''}, grouped as ${provider}`, async () => {
      const c = await only(file, ctx);
      expect(c.credential.kind).toBe(kind);
      expect(c.credential.provider).toBe(credProvider);
      expect(c.provider).toBe(provider);
    });
  }

  it('cloud, exec and OIDC credentials are short-lived; static ones are long-lived', async () => {
    for (const f of [
      'eks.yaml',
      'gke.yaml',
      'aks.yaml',
      'oidc-plugin.yaml',
      'kx.yaml',
      'unknown-exec.yaml',
    ]) {
      expect((await only(f)).credential.longLived, f).toBe(false);
    }
    for (const f of ['openshift.yaml', 'kind.yaml', 'rke2.yaml']) {
      expect((await only(f)).credential.longLived, f).toBe(true);
    }
  });

  it('a path-based client certificate (RKE2) has unknown expiry and is assumed long-lived', async () => {
    const c = await only('rke2.yaml');
    expect(c.credential).toEqual({ kind: 'client-cert', longLived: true });
  });
});

describe('client-certificate expiry and lifetime', () => {
  it('reads expiry from the certificate itself and judges lifetime by its validity span', async () => {
    const short = (await only('certs.yaml', 'short')).credential;
    expect(short.kind).toBe('client-cert');
    expect(short.expiresAt?.toISOString()).toBe('2026-01-02T00:00:00.000Z');
    expect(short.longLived).toBe(false); // valid for one day

    const long = (await only('certs.yaml', 'long')).credential;
    expect(long.expiresAt?.toISOString()).toBe('2036-01-01T00:00:00.000Z');
    expect(long.longLived).toBe(true);

    const expired = (await only('certs.yaml', 'expired')).credential;
    expect(expired.expiresAt?.toISOString()).toBe('2021-01-01T00:00:00.000Z');
    expect(expired.expiresAt && expired.expiresAt.getTime() < Date.now()).toBe(true); // the audit view flags this
    expect(expired.longLived).toBe(true); // it was issued for a year
  });

  it('treats exactly 30 days as not long-lived (the threshold is strictly greater)', async () => {
    expect((await only('certs.yaml', 'boundary')).credential.longLived).toBe(false);
  });

  it('an unparseable certificate does not throw; expiry is unknown and it is assumed long-lived', async () => {
    expect((await only('certs.yaml', 'broken-cert')).credential).toEqual({
      kind: 'client-cert',
      longLived: true,
    });
  });
});

describe('static and miscellaneous credentials', () => {
  it('classifies basic auth, token files, anonymous users, mixed users and dangling user references', async () => {
    expect((await only('basic-and-misc.yaml', 'basic')).credential).toEqual({
      kind: 'basic-auth',
      longLived: true,
    });
    // A token file may be rotated, but nothing in the kubeconfig says so: report it as long-lived, not as assurance.
    expect((await only('basic-and-misc.yaml', 'tokfile')).credential).toEqual({
      kind: 'static-token',
      longLived: true,
    });
    expect((await only('basic-and-misc.yaml', 'anon')).credential).toEqual({
      kind: 'none',
      longLived: false,
    });
    // token + certificate: the riskiest is reported (a bearer token)
    expect((await only('basic-and-misc.yaml', 'both')).credential.kind).toBe('static-token');
    expect((await only('basic-and-misc.yaml', 'missing-user')).credential).toEqual({
      kind: 'none',
      longLived: false,
    });
  });

  it('an exec plugin or auth-provider takes precedence over static credentials in the same entry', () => {
    // client-go rejects exec combined with a token or certificate, but a file can still contain both; the plugin is
    // what actually authenticates, so it is what the audit must describe (a stale token beside it is not the story).
    expect(classify({ exec: { command: 'aws' }, token: 't' })).toMatchObject({
      kind: 'cloud-iam',
      provider: 'aws',
    });
    expect(classify({ 'auth-provider': { name: 'oidc' }, token: 't' })).toMatchObject({ kind: 'oidc' });
    expect(classify({ exec: { command: 'x' }, password: 'p' }).kind).toBe('exec-plugin');
  });

  it('never throws on hostile shapes', () => {
    const hostile = [
      {},
      { exec: 'a string' },
      { exec: [] },
      { exec: { command: 42 } },
      { 'auth-provider': null },
      { 'auth-provider': { name: { nested: true } } },
      { token: 12345 },
      { 'client-certificate-data': { not: 'a string' } },
      { 'client-certificate-data': '' },
      { password: ['x'] },
    ];
    for (const u of hostile) expect(() => classify(u)).not.toThrow();
  });

  it('exec command matching ignores directories, Windows extensions and case', () => {
    expect(classify({ exec: { command: '/usr/local/bin/AWS' } }).provider).toBe('aws');
    expect(classify({ exec: { command: 'C:\\tools\\aws.exe' } }).provider).toBe('aws');
    expect(classify({ exec: { command: 'kubelogin.exe' } }).provider).toBe('azure');
  });
});

describe('inferProvider', () => {
  it('prefers hard evidence (server host, credential issuer) over name patterns', () => {
    expect(
      inferProvider({
        contextName: 'x',
        clusterName: 'x',
        serverHost: 'ABC.gr7.eu-west-1.eks.amazonaws.com',
      }),
    ).toBe('eks');
    expect(inferProvider({ contextName: 'x', clusterName: 'kind-foo', credentialProvider: 'aws' })).toBe(
      'eks',
    );
    expect(inferProvider({ contextName: 'minikube', clusterName: 'minikube' })).toBe('minikube');
    expect(inferProvider({ contextName: 'docker-desktop', clusterName: 'docker-desktop' })).toBe(
      'docker-desktop',
    );
    expect(inferProvider({ contextName: 'anything', clusterName: 'anything' })).toBe('unknown');
  });
});

describe('classification output is secret-free', () => {
  it('the full canary fixture classifies every secret-bearing user without leaking any value', async () => {
    const fleet = await loadFleet({
      env: { KUBECONFIG: path.join(__dirname, '..', 'fixtures', 'canary', 'full.yaml') },
      cwd: dir,
    });
    const kinds = Object.fromEntries(fleet.contexts.map((c) => [c.name, c.credential.kind]));
    expect(kinds).toEqual({
      'all-secrets': 'static-token',
      oidc: 'oidc',
      basic: 'basic-auth',
      exec: 'exec-plugin',
      impersonate: 'static-token',
    });
    expectNoCanary('classified Fleet', fleet);
    expect(suspiciousKeys(fleet)).toEqual([]);
  });

  it('every distro fixture classifies without a secret-shaped field in the output', async () => {
    for (const f of [
      'eks',
      'gke',
      'aks',
      'aks-legacy',
      'kind',
      'k3s',
      'rke2',
      'openshift',
      'oidc-login',
      'oidc-plugin',
      'kx',
      'certs',
    ]) {
      const cs = await load(`${f}.yaml`);
      expect(suspiciousKeys(cs), f).toEqual([]);
    }
  });
});
