import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import { loadFleet } from '../../src/kubeconfig';

// Differential test: our merge must agree with real `kubectl config view` on the same files. Skipped when kubectl is
// not installed (it is preinstalled on GitHub's Linux runners, so CI always runs it).
const hasKubectl = spawnSync('kubectl', ['version', '--client'], { stdio: 'ignore' }).status === 0;

const tmp = mkdtempSync(path.join(tmpdir(), 'sextant-diff-'));
afterAll(() => {
  rmSync(tmp, { recursive: true, force: true });
});

const cfg = (body: string): string => `apiVersion: v1\nkind: Config\n${body}`;
const ctx = (name: string, cluster: string, user: string, ns?: string): string =>
  `  - name: ${name}\n    context: {cluster: ${cluster}, user: ${user}${ns ? `, namespace: ${ns}` : ''}}\n`;

interface Case {
  name: string;
  /** Files in KUBECONFIG order; `null` content means the path does not exist. */
  files: Record<string, string | null>;
}

const cases: Case[] = [
  {
    name: 'single file',
    files: { a: cfg(`current-context: dev\ncontexts:\n${ctx('dev', 'c1', 'u1', 'web')}`) },
  },
  {
    name: 'two files, distinct contexts',
    files: { a: cfg(`contexts:\n${ctx('a1', 'c', 'u')}`), b: cfg(`contexts:\n${ctx('b1', 'c', 'u')}`) },
  },
  {
    name: 'duplicate context name: first wins',
    files: {
      a: cfg(`contexts:\n${ctx('same', 'from-a', 'u')}`),
      b: cfg(`contexts:\n${ctx('same', 'from-b', 'u')}`),
    },
  },
  {
    name: 'duplicate context, second has namespace: still first wins as a whole',
    files: {
      a: cfg(`contexts:\n${ctx('same', 'ca', 'ua')}`),
      b: cfg(`contexts:\n${ctx('same', 'cb', 'ub', 'extra')}`),
    },
  },
  {
    name: 'current-context only in the second file',
    files: { a: cfg(`contexts:\n${ctx('ctx-x', 'c', 'u')}`), b: cfg(`current-context: ctx-x\n`) },
  },
  {
    name: 'current-context in both: first wins',
    files: {
      // Names avoid bare y/n/yes/no/on/off: kubectl parses YAML 1.1, where those are booleans.
      a: cfg(`current-context: ctx-x\ncontexts:\n${ctx('ctx-x', 'c', 'u')}${ctx('ctx-y', 'c', 'u')}`),
      b: cfg(`current-context: ctx-y\n`),
    },
  },
  {
    name: 'empty first file',
    files: { a: '', b: cfg(`current-context: only\ncontexts:\n${ctx('only', 'c', 'u')}`) },
  },
  {
    name: 'missing file in the list is ignored',
    files: { a: null, b: cfg(`contexts:\n${ctx('k', 'c', 'u')}`) },
  },
  {
    name: 'namespace set versus unset',
    files: { a: cfg(`contexts:\n${ctx('n1', 'c', 'u', 'prod')}${ctx('n2', 'c', 'u')}`) },
  },
  {
    name: 'many contexts keep first-seen order',
    files: {
      a: cfg(`contexts:\n${ctx('z', 'c', 'u')}${ctx('m', 'c', 'u')}`),
      b: cfg(`contexts:\n${ctx('a', 'c', 'u')}${ctx('m', 'c', 'u')}`),
    },
  },
];

interface View {
  current: string;
  contexts: [string, string, string, string][];
}

function kubectlView(paths: string[]): View {
  const out = execFileSync('kubectl', ['config', 'view', '-o', 'json'], {
    env: { ...process.env, KUBECONFIG: paths.join(path.delimiter) },
    encoding: 'utf8',
  });
  const j = JSON.parse(out) as {
    'current-context'?: string;
    contexts?: { name: string; context: { cluster: string; user: string; namespace?: string } }[];
  };
  return {
    current: j['current-context'] ?? '',
    contexts: (j.contexts ?? []).map((c) => [
      c.name,
      c.context.cluster,
      c.context.user,
      c.context.namespace ?? '',
    ]),
  };
}

describe.skipIf(!hasKubectl)('merge agrees with kubectl config view', () => {
  for (const [i, c] of cases.entries()) {
    it(c.name, async () => {
      const dir = path.join(tmp, String(i));
      execFileSync('mkdir', ['-p', dir]);
      const paths: string[] = [];
      for (const [file, content] of Object.entries(c.files)) {
        const p = path.join(dir, file);
        paths.push(p);
        if (content !== null) writeFileSync(p, content);
      }
      const want = kubectlView(paths);
      const fleet = await loadFleet({ env: { KUBECONFIG: paths.join(path.delimiter) }, cwd: dir });
      const got: View = {
        current: fleet.currentContext ?? '',
        contexts: fleet.contexts.map((x) => [x.name, x.clusterName, x.userName, x.namespace ?? '']),
      };
      // kubectl lists contexts sorted by name (it is a Go map); we keep first-seen order. Compare as sets of tuples.
      const sortKey = (t: [string, string, string, string]): string => t.join('\u0000');
      expect({ ...got, contexts: got.contexts.map(sortKey).sort() }).toEqual({
        ...want,
        contexts: want.contexts.map(sortKey).sort(),
      });
    });
  }
});
