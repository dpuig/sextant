import { describe, expect, it } from 'vitest';
import { discoverKubeconfigPaths, loadFleet, hostOf } from '../../src/kubeconfig';
import { parseKubeconfig, MAX_KUBECONFIG_BYTES } from '../../src/kubeconfig/parse';
import { mergeKubeconfigs } from '../../src/kubeconfig/merge';

const kc = (body: string): string => `apiVersion: v1\nkind: Config\n${body}`;
const mem = (files: Record<string, string>) => (p: string) => Promise.resolve(files[p]);

describe('discoverKubeconfigPaths', () => {
  const base = { homedir: '/home/u', cwd: '/work', delimiter: ':' };

  it('uses ~/.kube/config when KUBECONFIG is unset or empty', () => {
    expect(discoverKubeconfigPaths({ ...base, env: {} })).toEqual(['/home/u/.kube/config']);
    expect(discoverKubeconfigPaths({ ...base, env: { KUBECONFIG: '' } })).toEqual(['/home/u/.kube/config']);
    expect(discoverKubeconfigPaths({ ...base, env: { KUBECONFIG: ':  :' } })).toEqual([
      '/home/u/.kube/config',
    ]);
  });

  it('uses KUBECONFIG entries in order, resolves relative ones against cwd, and drops duplicates', () => {
    const got = discoverKubeconfigPaths({ ...base, env: { KUBECONFIG: '/a/one:rel/two::/a/one' } });
    expect(got).toEqual(['/a/one', '/work/rel/two']);
  });

  it('does not expand ~ in KUBECONFIG (kubectl leaves that to the shell) but does in settings', () => {
    const got = discoverKubeconfigPaths({
      ...base,
      env: { KUBECONFIG: '~/k' },
      configuredPaths: ['~/extra'],
    });
    expect(got).toEqual(['/work/~/k', '/home/u/extra']);
  });

  it('appends configured paths LAST so a setting cannot shadow the real config', () => {
    const got = discoverKubeconfigPaths({
      ...base,
      env: { KUBECONFIG: '/real' },
      configuredPaths: ['/extra'],
    });
    expect(got).toEqual(['/real', '/extra']);
  });

  it('honours a Windows-style delimiter', () => {
    const got = discoverKubeconfigPaths({ ...base, delimiter: ';', env: { KUBECONFIG: '/a;/b' } });
    expect(got).toEqual(['/a', '/b']);
  });
});

describe('parseKubeconfig', () => {
  it('treats an empty file as an empty config, like kubectl', () => {
    for (const content of ['', '   \n', '# only a comment\n', 'null\n']) {
      const r = parseKubeconfig('/f', content);
      expect(r).toMatchObject({ ok: true, value: { contexts: [], clusters: [], users: [] } });
    }
  });

  it('returns a typed error without any file content for malformed YAML', () => {
    const r = parseKubeconfig('/f', 'users:\n  - name: a\n    user: {token: SECRET-VALUE-123\n');
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.error).toMatchObject({ file: '/f', code: 'parse-failed' });
    expect(JSON.stringify(r.error)).not.toContain('SECRET-VALUE-123');
  });

  it('rejects a document that is not a mapping', () => {
    for (const content of ['- a\n- b\n', 'just a string\n', '42\n']) {
      expect(parseKubeconfig('/f', content)).toMatchObject({ ok: false, error: { code: 'invalid-shape' } });
    }
  });

  it('rejects an oversized file before parsing it', () => {
    const big = 'x'.repeat(MAX_KUBECONFIG_BYTES + 1);
    expect(parseKubeconfig('/f', big)).toMatchObject({ ok: false, error: { code: 'too-large' } });
  });

  it('turns an alias bomb into a typed error instead of throwing', () => {
    const bomb = [
      'a: &a [x,x,x,x,x,x,x,x,x]',
      'b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]',
      'c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]',
      'd: [*c,*c,*c,*c,*c,*c,*c,*c,*c]',
    ].join('\n');
    expect(() => parseKubeconfig('/f', bomb)).not.toThrow();
    expect(parseKubeconfig('/f', bomb)).toMatchObject({ ok: false, error: { code: 'too-complex' } });
  });

  it('skips malformed list items instead of failing the whole file', () => {
    const r = parseKubeconfig(
      '/f',
      kc(`contexts:
  - name: ok
    context: {cluster: c, user: u}
  - name: no-cluster
    context: {user: u}
  - context: {cluster: c, user: u}
  - 7
clusters: not-a-list
`),
    );
    expect(r).toMatchObject({ ok: true });
    if (r.ok) expect(r.value.contexts.map((c) => c.name)).toEqual(['ok']);
  });

  it('accepts users with no body (anonymous)', () => {
    const r = parseKubeconfig('/f', kc('users:\n  - name: anon\n'));
    expect(r.ok && r.value.users.map((u) => u.name)).toEqual(['anon']);
  });
});

describe('mergeKubeconfigs (kubectl semantics)', () => {
  const file = (name: string, body: string) => {
    const r = parseKubeconfig(name, kc(body));
    if (!r.ok) throw new Error('fixture failed to parse');
    return r.value;
  };

  it('first file to define a name wins, as a whole entry', () => {
    const a = file('/a', 'users:\n  - name: red\n    user: {username: from-a}\n');
    const b = file('/b', 'users:\n  - name: red\n    user: {username: from-b, token: extra}\n');
    const m = mergeKubeconfigs([a, b]);
    expect(m.users.get('red')).toEqual({ username: 'from-a' }); // b's non-conflicting token is discarded too
  });

  it('current-context is the first non-empty one', () => {
    const a = file('/a', 'contexts: []\n');
    const b = file('/b', 'current-context: from-b\n');
    const c = file('/c', 'current-context: from-c\n');
    expect(mergeKubeconfigs([a, b, c]).currentContext).toBe('from-b');
  });

  it('keeps first-seen order of contexts and records which file defined each', () => {
    const a = file('/a', 'contexts:\n  - {name: z, context: {cluster: c, user: u}}\n');
    const b = file(
      '/b',
      'contexts:\n  - {name: a, context: {cluster: c, user: u}}\n  - {name: z, context: {cluster: x, user: x}}\n',
    );
    const m = mergeKubeconfigs([a, b]);
    expect(m.contexts.map((c) => [c.name, c.file, c.value.cluster])).toEqual([
      ['z', '/a', 'c'],
      ['a', '/b', 'c'],
    ]);
  });
});

describe('loadFleet', () => {
  const two = {
    '/a': kc(`current-context: dev
contexts:
  - {name: dev, context: {cluster: c1, user: u1, namespace: web}}
clusters:
  - {name: c1, cluster: {server: "https://admin:hunter2@api.example.com:6443/path"}}
users:
  - {name: u1, user: {token: t}}
`),
    '/b': kc(`contexts:
  - {name: prod, context: {cluster: c2, user: u2}}
clusters:
  - {name: c2, cluster: {server: "https://10.0.0.5"}}
users:
  - {name: u2, user: {exec: {command: aws}}}
`),
  };

  it('builds contexts from the merged files without any secret-bearing field', async () => {
    const fleet = await loadFleet({ env: { KUBECONFIG: '/a:/b' }, cwd: '/', readFile: mem(two) });
    expect(fleet.currentContext).toBe('dev');
    expect(fleet.contexts.map((c) => c.name)).toEqual(['dev', 'prod']);
    expect(fleet.contexts[0]).toMatchObject({
      clusterName: 'c1',
      userName: 'u1',
      namespace: 'web',
      serverHost: 'api.example.com:6443',
      sourceFile: '/a',
    });
    // userinfo and path in the server URL never reach the model
    expect(JSON.stringify(fleet)).not.toContain('hunter2');
    expect(JSON.stringify(fleet)).not.toContain('admin');
    expect(fleet.contexts[1]?.serverHost).toBe('10.0.0.5');
  });

  it('skips missing files silently, reports unreadable and malformed ones, and keeps the rest', async () => {
    const files: Record<string, string> = { '/a': two['/a'], '/bad': 'a: [unclosed\n' };
    const readFile = (p: string) =>
      p === '/boom' ? Promise.reject(new Error('EACCES: secret path detail')) : Promise.resolve(files[p]);
    const fleet = await loadFleet({ env: { KUBECONFIG: '/missing:/a:/bad:/boom' }, cwd: '/', readFile });
    expect(fleet.contexts.map((c) => c.name)).toEqual(['dev']);
    expect(fleet.files).toEqual(['/a']);
    expect(fleet.errors.map((e) => [e.file, e.code])).toEqual([
      ['/bad', 'parse-failed'],
      ['/boom', 'read-failed'],
    ]);
    expect(JSON.stringify(fleet.errors)).not.toContain('EACCES');
  });

  it('omits currentContext when it names a context that does not exist', async () => {
    const fleet = await loadFleet({
      env: { KUBECONFIG: '/x' },
      cwd: '/',
      readFile: mem({ '/x': kc('current-context: ghost\n') }),
    });
    expect(fleet.currentContext).toBeUndefined();
  });

  it('a context whose cluster is defined only in a later file still resolves its host', async () => {
    const files = {
      '/a': kc('contexts:\n  - {name: x, context: {cluster: c, user: u}}\n'),
      '/b': kc('clusters:\n  - {name: c, cluster: {server: "https://late.example.com"}}\n'),
    };
    const fleet = await loadFleet({ env: { KUBECONFIG: '/a:/b' }, cwd: '/', readFile: mem(files) });
    expect(fleet.contexts[0]?.serverHost).toBe('late.example.com');
  });
});

describe('hostOf', () => {
  it('returns host[:port] only, and undefined for junk', () => {
    expect(hostOf('https://u:p@h.example:6443/x?y=z')).toBe('h.example:6443');
    expect(hostOf('not a url')).toBeUndefined();
    expect(hostOf('')).toBeUndefined();
  });
});
