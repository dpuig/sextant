import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import { loadFleet } from '../../src/kubeconfig';
import { buildAudit, renderReport } from '../../src/model/audit';
import { buildFleetTree, countContexts } from '../../src/model/fleetTree';
import { parseTagRules, resolveTag } from '../../src/model/tags';

// Budgets from the spec (docs/specs/vscode-extension.md, Success Criteria). Generous multiples are NOT applied: the
// numbers are the spec's, and the work is pure CPU, so a failure means a real regression, not a slow runner.
const tmp = mkdtempSync(path.join(tmpdir(), 'sextant-perf-'));
afterAll(() => {
  rmSync(tmp, { recursive: true, force: true });
});

/** A kubeconfig with `n` contexts spread over a few platforms, each with its own cluster and user. */
function kubeconfig(n: number): string {
  const hosts = [
    'https://api.eks.example.com',
    'https://34.1.2.3',
    'https://x.hcp.westeurope.azmk8s.io',
    'https://127.0.0.1:6443',
  ];
  const lines = ['apiVersion: v1', 'kind: Config', 'current-context: ctx-0', 'contexts:'];
  for (let i = 0; i < n; i++)
    lines.push(
      `  - {name: ctx-${String(i)}-${i % 3 === 0 ? 'prod' : 'dev'}, context: {cluster: c${String(i)}, user: u${String(i)}}}`,
    );
  lines.push('clusters:');
  for (let i = 0; i < n; i++)
    lines.push(
      `  - {name: c${String(i)}, cluster: {server: "${hosts[i % hosts.length] ?? ''}:${String(6000 + i)}"}}`,
    );
  lines.push('users:');
  for (let i = 0; i < n; i++)
    lines.push(
      i % 2 === 0
        ? `  - {name: u${String(i)}, user: {token: tok-${String(i)}}}`
        : `  - {name: u${String(i)}, user: {exec: {command: aws, args: [eks, get-token]}}}`,
    );
  return lines.join('\n') + '\n';
}

const time = async <T>(fn: () => Promise<T> | T): Promise<{ ms: number; value: T }> => {
  const start = performance.now();
  const value = await fn();
  return { ms: performance.now() - start, value };
};

describe('performance budgets', () => {
  const file = path.join(tmp, 'big.yaml');
  writeFileSync(file, kubeconfig(200));
  const rules = parseTagRules([{ match: '*-prod', environment: 'prod', critical: true }]).rules;
  const now = new Date('2026-10-04T00:00:00Z');

  it('loads and renders 200 contexts in under 500 ms', async () => {
    const { ms, value } = await time(async () => {
      const fleet = await loadFleet({ env: { KUBECONFIG: file }, configuredPaths: [] });
      const tree = buildFleetTree(fleet.contexts, {
        tagOf: (c) => resolveTag(c.name, rules),
        terminals: new Set(),
        filter: '',
        now,
      });
      return { fleet, tree };
    });
    expect(value.fleet.contexts).toHaveLength(200);
    expect(countContexts(value.tree)).toBe(200);
    expect(ms).toBeLessThan(500);
  });

  it('filters 200 contexts on every keystroke well inside a frame budget (50 ms)', async () => {
    const fleet = await loadFleet({ env: { KUBECONFIG: file }, configuredPaths: [] });
    const { ms } = await time(() => {
      for (const q of ['c', 'ct', 'ctx', 'ctx-1', 'ctx-12', 'prod', 'zzz-none'])
        buildFleetTree(fleet.contexts, {
          tagOf: (c) => resolveTag(c.name, rules),
          terminals: new Set(),
          filter: q,
          now,
        });
    });
    expect(ms / 7).toBeLessThan(50);
  });

  it('audits 50 contexts and renders the report in under 2 s', async () => {
    const small = path.join(tmp, 'fifty.yaml');
    writeFileSync(small, kubeconfig(50));
    const fleet = await loadFleet({ env: { KUBECONFIG: small }, configuredPaths: [] });
    const { ms, value } = await time(() => {
      const groups = buildAudit(fleet.contexts, now);
      return { groups, report: renderReport(fleet.contexts, 1, now) };
    });
    expect(value.groups.flatMap((g) => g.rows)).toHaveLength(50);
    expect(ms).toBeLessThan(2000);
  });
});
