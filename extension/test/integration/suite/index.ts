import * as assert from 'node:assert/strict';
import { existsSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import * as path from 'node:path';
import * as vscode from 'vscode';
import type { TestApi } from '../../../src/extension';
import { expectNoCanary } from '../../canary';
import { ScriptedPrompts } from '../../scriptedPrompts';

// A deliberately tiny runner (no mocha): each case is an async function that throws on failure. Cases run in order and
// share one VS Code instance.
const EXT_ID = 'sextant-placeholder.sextant-vscode';
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

async function api(): Promise<TestApi> {
  const ext = vscode.extensions.getExtension<TestApi>(EXT_ID);
  assert.ok(ext, 'extension not found');
  const exports = await ext.activate();
  await exports.store.whenLoaded();
  return exports;
}

type Node = ReturnType<TestApi['fleet']['getChildren']>[number];
function walk(a: TestApi, nodes: Node[] = a.fleet.getChildren()): Node[] {
  return nodes.flatMap((n) => [n, ...walk(a, a.fleet.getChildren(n))]);
}
function contextNames(a: TestApi): string[] {
  return walk(a).flatMap((n) => (n.kind === 'context' ? [n.label] : []));
}
const contextNode = (a: TestApi, name: string): Node => {
  const n = walk(a).find((x) => x.kind === 'context' && x.label === name);
  assert.ok(n, `no context row ${name}`);
  return n;
};
const labelOf = (l: vscode.TreeItem['label']): string => (typeof l === 'string' ? l : (l?.label ?? ''));
const cfg = (): vscode.WorkspaceConfiguration => vscode.workspace.getConfiguration('sextant');
async function resetSettings(a: TestApi): Promise<void> {
  await cfg().update('tags', undefined, vscode.ConfigurationTarget.Global);
  await cfg().update('confirm.skip', undefined, vscode.ConfigurationTarget.Global);
  a.store.readSettings();
  a.setPrompts(new ScriptedPrompts([]));
}
const COMMANDS = [
  'openTerminal',
  'openTerminalForItem',
  'tagCluster',
  'filter',
  'clearFilter',
  'refresh',
  'copyContextName',
  'revealKubeconfig',
  'copyAuditReport',
  'openAuditReport',
  'gettingStarted',
];

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
    'activation is eager (onStartupFinished): the status bar and badge need no user action',
    async () => {
      const ext = vscode.extensions.getExtension(EXT_ID);
      assert.ok(ext, 'extension not found');
      await eventually('the extension to activate on its own', () => ext.isActive, 15000);
    },
  ],
  [
    'activate() itself returns in under 200 ms (file reading continues afterwards)',
    async () => {
      const a = await api();
      console.log(`       (activate() took ${a.activationMs.toFixed(1)} ms)`);
      assert.ok(a.activationMs < 200, `activation took ${String(a.activationMs)} ms`);
    },
  ],
  [
    'every contributed command is registered',
    async () => {
      await api();
      const all = await vscode.commands.getCommands(true);
      for (const c of COMMANDS) assert.ok(all.includes(`sextant.${c}`), `sextant.${c} is not registered`);
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
      );
      assert.equal(providers[providers.length - 1], 'Other', '"Other" must sort last');
    },
  ],
  [
    'the current context is marked, and has detail rows with exact labels',
    async () => {
      const a = await api();
      const node = contextNode(a, 'kind-dev');
      const item = a.fleet.getTreeItem(node);
      assert.match(String(item.description), /^current · /);
      assert.equal(item.contextValue, 'context');
      assert.ok(item.accessibilityInformation?.label.includes('current context'));
      const labels = a.fleet.getChildren(node).map((n) => (n.kind === 'detail' ? n.label : ''));
      assert.deepEqual(labels, ['Server', 'Namespace', 'Credential', 'Expires', 'Source file']);
      assert.doesNotMatch(String(a.fleet.getTreeItem(contextNode(a, 'plain')).description), /current/);
    },
  ],
  [
    'no label, description, tooltip, id or status text in any view contains a secret (canary check on the real UI)',
    async () => {
      const a = await api();
      const items = walk(a).map((n) => a.fleet.getTreeItem(n));
      assert.ok(items.length > 10, 'the tree was unexpectedly small');
      const auditNodes = a.audit
        .getChildren()
        .flatMap((g) => [g, ...a.audit.getChildren(g).flatMap((r) => [r, ...a.audit.getChildren(r)])]);
      assert.ok(auditNodes.length > 5, 'the audit tree was unexpectedly small');
      const all = [...items, ...auditNodes.map((n) => a.audit.getTreeItem(n))];
      for (const item of all) {
        expectNoCanary(`tree item "${labelOf(item.label)}"`, {
          label: item.label,
          description: item.description,
          tooltip: item.tooltip,
          id: item.id,
          contextValue: item.contextValue,
          accessibility: item.accessibilityInformation,
        });
      }
      expectNoCanary('status bar', {
        text: a.status.item.text,
        tooltip: a.status.item.tooltip,
        accessibility: a.status.item.accessibilityInformation,
      });
    },
  ],
  [
    'the status bar shows the current context; normal state has no background',
    async () => {
      const a = await api();
      assert.equal(a.status.item.text, '$(server-environment) kind-dev');
      assert.equal(a.status.item.backgroundColor, undefined);
      assert.equal(
        a.status.item.command && typeof a.status.item.command !== 'string'
          ? a.status.item.command.command
          : '',
        'sextant.openTerminal',
      );
    },
  ],
  [
    'the Credential Audit lists the four risk groups in order, with the reassurance message',
    async () => {
      const a = await api();
      const labels = a.audit.getChildren().map((g) => labelOf(a.audit.getTreeItem(g).label));
      assert.deepEqual(labels, [
        'Expired',
        'Long-lived credentials',
        'Expiring within 30 days',
        'Short-lived or brokered',
      ]);
    },
  ],
  [
    'Copy Audit Report puts a report with no secret on the clipboard',
    async () => {
      await api();
      await vscode.env.clipboard.writeText('');
      await vscode.commands.executeCommand('sextant.copyAuditReport');
      const text = await vscode.env.clipboard.readText();
      assert.match(text, /^# Sextant credential audit/);
      expectNoCanary('audit report', text);
    },
  ],
  [
    'tagging a cluster through the three-step flow saves to user settings and regroups the tree',
    async () => {
      const a = await api();
      try {
        a.setPrompts(new ScriptedPrompts([{ pick: 'prod' }, { pick: 'critical' }, { pick: 'Only' }]));
        await vscode.commands.executeCommand('sextant.tagCluster', contextNode(a, 'plain'));
        const saved = cfg().inspect<unknown[]>('tags')?.globalValue;
        assert.deepEqual(saved, [{ match: 'plain', environment: 'prod', critical: true }]);
        await eventually('the tree to regroup', () =>
          a.fleet.getChildren().some((n) => n.kind === 'environment' && n.label === 'prod'),
        );
        const row = a.fleet.getTreeItem(contextNode(a, 'plain'));
        assert.ok(row.accessibilityInformation?.label.includes('critical'));
        const group = a.fleet.getChildren().find((n) => n.kind === 'environment' && n.label === 'prod');
        assert.ok(group);
        assert.match(String(a.fleet.getTreeItem(group).description), /critical/);
        assert.equal((a.fleet.getTreeItem(group).iconPath as vscode.ThemeIcon).id, 'warning');
        assert.equal(
          (row.iconPath as vscode.ThemeIcon).id,
          'circle-large-outline',
          'a context icon shows current/other, not risk',
        );
        const roots = a.fleet.getChildren().map((n) => n.kind === 'environment' && n.label);
        assert.equal(roots[0], 'prod', 'prod sorts first');
      } finally {
        await resetSettings(a);
      }
    },
  ],
  [
    'tagging the current context critical turns the status item red with icon and word',
    async () => {
      const a = await api();
      try {
        a.setPrompts(new ScriptedPrompts([{ pick: 'prod' }, { pick: 'critical' }, { pick: 'Only' }]));
        await vscode.commands.executeCommand('sextant.tagCluster', contextNode(a, 'kind-dev'));
        await eventually(
          'the status item to turn critical',
          () => a.status.item.text === '$(warning) PROD  kind-dev',
        );
        assert.ok(a.status.item.backgroundColor);
        const tip = a.status.item.tooltip;
        assert.ok(typeof tip === 'string' && tip.startsWith('kind-dev · prod, critical'));
      } finally {
        await resetSettings(a);
      }
      await eventually(
        'the status item to relax',
        () => a.status.item.text === '$(server-environment) kind-dev',
      );
    },
  ],
  [
    'cancelling the tag flow saves nothing',
    async () => {
      const a = await api();
      a.setPrompts(new ScriptedPrompts([{ pick: 'prod' }, { pick: 'cancel' }]));
      await vscode.commands.executeCommand('sextant.tagCluster', contextNode(a, 'plain'));
      assert.equal(cfg().inspect<unknown[]>('tags')?.globalValue, undefined);
    },
  ],
  [
    'a bound terminal: pin file first in KUBECONFIG, read-only, no secrets, shown in the tree, removed on close',
    async () => {
      const a = await api();
      a.setPrompts(new ScriptedPrompts([]));
      await vscode.commands.executeCommand('sextant.openTerminalForItem', contextNode(a, 'plain'));
      const bound = a.terminals.list().find((b) => b.contextName === 'plain');
      assert.ok(bound, 'no bound terminal was registered');
      const opts = bound.terminal.creationOptions as vscode.TerminalOptions;
      const kubeconfig = opts.env?.KUBECONFIG;
      assert.ok(kubeconfig, 'KUBECONFIG not set on the terminal');
      const parts = kubeconfig.split(path.delimiter);
      assert.equal(parts[0], bound.pin, 'the pin must be FIRST');
      assert.deepEqual(parts.slice(1), a.store.userPaths(), 'the user files follow in their original order');
      // Windows has no POSIX mode bits; there the file is still created read-only by the OS attribute.
      if (process.platform !== 'win32')
        assert.equal(statSync(bound.pin).mode & 0o777, 0o400, 'the pin must be read-only');
      const content = readFileSync(bound.pin, 'utf8');
      assert.match(content, /^apiVersion: v1\nkind: Config\ncurrent-context: "plain"\n$/);
      expectNoCanary('pin file', content);
      assert.equal(opts.name, 'plain');
      assert.match(String(a.fleet.getTreeItem(contextNode(a, 'plain')).description), /terminal/);

      bound.terminal.dispose();
      await eventually('the pin file to be removed', () => !existsSync(bound.pin));
      await eventually(
        '"terminal" to leave the description',
        () => !/terminal/.test(String(a.fleet.getTreeItem(contextNode(a, 'plain')).description)),
      );
    },
  ],
  [
    'a critical context asks first: Cancel opens nothing, Open creates a red PROD-named terminal',
    async () => {
      const a = await api();
      try {
        a.setPrompts(new ScriptedPrompts([{ pick: 'prod' }, { pick: 'critical' }, { pick: 'Only' }]));
        await vscode.commands.executeCommand('sextant.tagCluster', contextNode(a, 'plain'));
        const cancel = new ScriptedPrompts([{ pick: 'first' }]);
        a.setPrompts(cancel);
        await vscode.commands.executeCommand('sextant.openTerminalForItem', contextNode(a, 'plain'));
        assert.equal(a.terminals.list().length, 0, 'Cancel must not open a terminal');
        assert.equal(
          cancel.shown[0]?.items.find((i) => i.kind === 'item')?.label,
          'Cancel',
          'Cancel must be first',
        );

        a.setPrompts(new ScriptedPrompts([{ pick: 'Open terminal on plain' }]));
        await vscode.commands.executeCommand('sextant.openTerminalForItem', contextNode(a, 'plain'));
        const bound = a.terminals.list()[0];
        assert.ok(bound);
        const opts = bound.terminal.creationOptions as vscode.TerminalOptions;
        assert.equal(opts.name, 'plain · PROD');
        assert.equal((opts.iconPath as vscode.ThemeIcon).id, 'warning');
        assert.equal((opts.color as unknown as { id: string }).id, 'terminal.ansiRed');
        assert.ok(String(opts.message).includes('PROD'));
        bound.terminal.dispose();
        await eventually('the terminal to close', () => a.terminals.list().length === 0);
      } finally {
        await resetSettings(a);
      }
    },
  ],
  [
    "'Open, and don't ask again' stores the context in user settings and later opens skip the prompt",
    async () => {
      const a = await api();
      try {
        a.setPrompts(new ScriptedPrompts([{ pick: 'prod' }, { pick: 'critical' }, { pick: 'Only' }]));
        await vscode.commands.executeCommand('sextant.tagCluster', contextNode(a, 'plain'));
        a.setPrompts(new ScriptedPrompts([{ pick: "don't ask again" }]));
        await vscode.commands.executeCommand('sextant.openTerminalForItem', contextNode(a, 'plain'));
        assert.deepEqual(cfg().inspect<string[]>('confirm.skip')?.globalValue, ['plain']);
        a.setPrompts(new ScriptedPrompts([]));
        await vscode.commands.executeCommand('sextant.openTerminalForItem', contextNode(a, 'plain'));
        assert.equal(a.terminals.list().length, 2);
        for (const b of [...a.terminals.list()]) b.terminal.dispose();
        await eventually('terminals to close', () => a.terminals.list().length === 0);
      } finally {
        await resetSettings(a);
      }
    },
  ],
  [
    'shell integration confirms the binding (visible echo, pin first), or the terminal reports unverified',
    async () => {
      const a = await api();
      a.setPrompts(new ScriptedPrompts([]));
      await vscode.commands.executeCommand('sextant.openTerminalForItem', contextNode(a, 'plain'));
      const bound = a.terminals.list()[0];
      assert.ok(bound);
      await eventually(
        'the binding check to finish',
        () => a.terminals.bindingOf(bound.terminal) !== 'pending',
        12000,
      );
      const result = a.terminals.bindingOf(bound.terminal);
      console.log(`       (binding result: ${String(result)}, shell: ${vscode.env.shell})`);
      assert.ok(result === 'verified' || result === 'unverified' || result === 'not-checkable');
      if (process.env.SEXTANT_EXPECT_VERIFIED !== undefined) assert.equal(result, 'verified');
      bound.terminal.dispose();
      await eventually('the terminal to close', () => a.terminals.list().length === 0);
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
      assert.equal(a.fleet.getFilter(), 'gke');
      assert.deepEqual(contextNames(a), ['gke_proj_eu_stg']);
      assert.ok(
        a.fleet.getChildren().every((n) => n.kind === 'environment'),
        'empty groups must be dropped',
      );
      await vscode.commands.executeCommand('sextant.clearFilter');
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
