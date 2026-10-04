import * as assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import * as vscode from 'vscode';
import type { TestApi } from '../../../src/extension';
import { expectNoCanary } from '../../canary';

// A deliberately tiny runner (no mocha): each case is an async function that throws on failure. Cases run in order and
// share one VS Code instance, so the first one must be the "nothing runs until used" check.
const EXT_ID = 'sextant-placeholder.sextant-vscode';
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

async function api(): Promise<TestApi> {
  const ext = vscode.extensions.getExtension<TestApi>(EXT_ID);
  assert.ok(ext, 'extension not found');
  const exports = await ext.activate();
  await exports.fleet.whenLoaded();
  return exports;
}

type Node = ReturnType<TestApi['fleet']['getChildren']>[number];
function walk(a: TestApi, nodes: Node[] = a.fleet.getChildren()): Node[] {
  return nodes.flatMap((n) => [n, ...walk(a, a.fleet.getChildren(n))]);
}
function contextNames(a: TestApi): string[] {
  return walk(a).flatMap((n) => (n.kind === 'context' ? [n.label] : []));
}
async function eventually(what: string, cond: () => boolean, ms = 5000): Promise<void> {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (cond()) return;
    await sleep(50);
  }
  throw new Error(`timed out waiting for: ${what}`);
}

const cases: [string, () => Promise<void>][] = [
  [
    'activation is lazy: the extension is inactive until something uses it',
    () => {
      const ext = vscode.extensions.getExtension(EXT_ID);
      assert.ok(ext, 'extension not found');
      assert.equal(ext.isActive, false, 'the extension activated without being used');
      return Promise.resolve();
    },
  ],
  [
    'the commands are registered once activated',
    async () => {
      await api();
      const all = await vscode.commands.getCommands(true);
      for (const c of [
        'sextant.showVersion',
        'sextant.fleet.refresh',
        'sextant.fleet.filter',
        'sextant.fleet.clearFilter',
      ]) {
        assert.ok(all.includes(c), `${c} is not registered`);
      }
      await vscode.commands.executeCommand('sextant.showVersion'); // must not throw
    },
  ],
  [
    'the tree is grouped Untagged > platform > context and shows every context from KUBECONFIG',
    async () => {
      const a = await api();
      const roots = a.fleet.getChildren();
      assert.deepEqual(
        roots.map((n) => n.kind === 'environment' && n.label),
        ['Untagged'],
      );
      const names = contextNames(a);
      for (const want of ['kind-dev', 'gke_proj_eu_stg', 'plain', 'all-secrets', 'oidc']) {
        assert.ok(names.includes(want), `missing context ${want}; got ${names.join(', ')}`);
      }
      const env = roots[0];
      assert.ok(env?.kind === 'environment');
      const providers = env.children.map((n) => n.label);
      assert.ok(
        providers.includes('kind') && providers.includes('Google GKE') && providers.includes('Other'),
        providers.join(', '),
      );
      assert.equal(providers[providers.length - 1], 'Other', '"Other" must sort last');
    },
  ],
  [
    'the current context is marked',
    async () => {
      const a = await api();
      const node = walk(a).find((n) => n.kind === 'context' && n.label === 'kind-dev');
      assert.ok(node);
      const item = a.fleet.getTreeItem(node);
      assert.match(String(item.description), /^current · /);
      const other = walk(a).find((n) => n.kind === 'context' && n.label === 'plain');
      assert.ok(other);
      assert.doesNotMatch(String(a.fleet.getTreeItem(other).description), /current/);
    },
  ],
  [
    'no label, description, tooltip or id in the tree contains a secret (canary check on the real UI)',
    async () => {
      const a = await api();
      const items = walk(a).map((n) => a.fleet.getTreeItem(n));
      assert.ok(items.length > 10, 'the tree was unexpectedly small');
      for (const item of items) {
        const label = typeof item.label === 'string' ? item.label : (item.label?.label ?? '');
        expectNoCanary(`tree item "${label}"`, {
          label: item.label,
          description: item.description,
          tooltip: item.tooltip,
          id: item.id,
          contextValue: item.contextValue,
        });
      }
    },
  ],
  [
    'editing the kubeconfig updates the tree without a reload, and removing the context removes it again',
    async () => {
      const a = await api();
      const main = process.env.SEXTANT_IT_MAIN;
      assert.ok(main, 'SEXTANT_IT_MAIN not set');
      const original = readFileSync(main, 'utf8');
      assert.ok(!contextNames(a).includes('added-live'));

      const edited = original.replace(
        'contexts:\n',
        'contexts:\n  - {name: added-live, context: {cluster: plain-c, user: plain-u}}\n',
      );
      assert.notEqual(edited, original, 'fixture edit did not apply');
      const started = Date.now();
      writeFileSync(main, edited);
      // Acceptance criterion: the tree reflects an edit within one second.
      await eventually(
        'the new context to appear within 1 s',
        () => contextNames(a).includes('added-live'),
        1000,
      );
      console.log(`       (updated ${String(Date.now() - started)} ms after the edit)`);

      writeFileSync(main, original);
      await eventually('the context to disappear again', () => !contextNames(a).includes('added-live'));
    },
  ],
  [
    'a malformed kubeconfig does not hide the others and is reported as a warning without its content',
    async () => {
      const a = await api();
      const main = process.env.SEXTANT_IT_MAIN;
      assert.ok(main, 'SEXTANT_IT_MAIN not set');
      const original = readFileSync(main, 'utf8');
      writeFileSync(main, 'users: [CANARY-static-token-3f9a1c7e5b2d\n'); // unterminated, with a canary on the line
      await eventually('the warning node', () => a.fleet.getChildren().some((n) => n.kind === 'warning'));
      const warning = a.fleet.getChildren().find((n) => n.kind === 'warning');
      assert.ok(warning);
      const item = a.fleet.getTreeItem(warning);
      expectNoCanary('warning node', {
        label: item.label,
        tooltip: item.tooltip,
        description: item.description,
      });
      assert.ok(contextNames(a).includes('all-secrets'), 'the other (valid) file must still be shown');
      writeFileSync(main, original);
      await eventually(
        'the warning to clear',
        () => !a.fleet.getChildren().some((n) => n.kind === 'warning'),
      );
    },
  ],
  [
    'filtering narrows the tree and clearing restores it',
    async () => {
      const a = await api();
      const all = contextNames(a).length;
      a.fleet.setFilter('gke');
      assert.deepEqual(contextNames(a), ['gke_proj_eu_stg']);
      assert.ok(
        a.fleet.getChildren().every((n) => n.kind === 'environment'),
        'empty groups must be dropped',
      );
      await vscode.commands.executeCommand('sextant.fleet.clearFilter');
      assert.equal(contextNames(a).length, all);
      a.fleet.setFilter('no-such-cluster');
      assert.deepEqual(
        a.fleet.getChildren().filter((n) => n.kind !== 'warning'),
        [],
      );
      a.fleet.setFilter('');
    },
  ],
];

export async function run(): Promise<void> {
  let failed = 0;
  for (const [name, fn] of cases) {
    try {
      await fn();
      console.log(`  ok   ${name}`);
    } catch (err) {
      failed++;
      console.error(`  FAIL ${name}\n       ${err instanceof Error ? err.message : String(err)}`);
    }
  }
  if (failed > 0) throw new Error(`${failed} integration test(s) failed`);
}
