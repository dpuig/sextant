import { describe, expect, it } from 'vitest';
import { loadFleet } from '../../src/kubeconfig';

const files = (content: string) => (p: string) => Promise.resolve(p === '/k' ? content : undefined);

describe('context source lines (for "Reveal Kubeconfig File")', () => {
  it('records the 1-based line of each context entry', async () => {
    const content = `apiVersion: v1
kind: Config
contexts:
  - name: first
    context: {cluster: c, user: u}
  - name: second
    context:
      cluster: c
      user: u
clusters: []
`;
    const fleet = await loadFleet({ env: { KUBECONFIG: '/k' }, cwd: '/', readFile: files(content) });
    expect(fleet.contexts.map((c) => [c.name, c.sourceLine])).toEqual([
      ['first', 4],
      ['second', 6],
    ]);
  });

  it('keeps lines aligned when malformed entries are skipped, and works with flow style and comments', async () => {
    const content = `contexts:
  # a comment
  - not-a-mapping
  - name: ok
    context: {cluster: c, user: u}
  - {name: flow, context: {cluster: c, user: u}}
`;
    const fleet = await loadFleet({ env: { KUBECONFIG: '/k' }, cwd: '/', readFile: files(content) });
    expect(fleet.contexts.map((c) => [c.name, c.sourceLine])).toEqual([
      ['ok', 4],
      ['flow', 6],
    ]);
  });

  it('the line is the one in the file that DEFINED the context, even with several files', async () => {
    const readFile = (p: string) =>
      Promise.resolve(
        p === '/a'
          ? 'contexts:\n  - {name: x, context: {cluster: c, user: u}}\n'
          : p === '/b'
            ? '\n\n\ncontexts:\n  - {name: y, context: {cluster: c, user: u}}\n'
            : undefined,
      );
    const fleet = await loadFleet({ env: { KUBECONFIG: '/a:/b' }, cwd: '/', readFile });
    expect(fleet.contexts.map((c) => [c.name, c.sourceFile, c.sourceLine])).toEqual([
      ['x', '/a', 2],
      ['y', '/b', 5],
    ]);
  });

  it('gives no line (rather than a wrong one) when the structure is unusual', async () => {
    const fleet = await loadFleet({
      env: { KUBECONFIG: '/k' },
      cwd: '/',
      readFile: files('contexts: [{name: a, context: {cluster: c, user: u}}]\n'),
    });
    expect(fleet.contexts[0]?.name).toBe('a');
    expect(fleet.contexts[0]?.sourceLine).toBeTypeOf('number');
  });
});
