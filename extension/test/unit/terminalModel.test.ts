import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { describe, expect, it } from 'vitest';
import { statusModel, shouldWarnOnSwitch } from '../../src/model/status';
import {
  banner,
  boundKubeconfig,
  canVerify,
  isBound,
  pinFileContent,
  reassertCommand,
  shellFamily,
  tabStyle,
  unverifiableBanner,
  verifyCommand,
} from '../../src/model/terminal';
import { confirmEntries, needsConfirmation, openTerminalEntries } from '../../src/model/pickers';
import type { KubeContext } from '../../src/kubeconfig';
import { UNTAGGED, type ContextView } from '../../src/model/fleetTree';
import { parseKubeconfig } from '../../src/kubeconfig/parse';

const NOW = new Date('2026-10-04T00:00:00Z');
const PROD = { environment: 'prod', critical: true };
const ctx = (name: string, over: Partial<KubeContext> = {}): KubeContext => ({
  name,
  clusterName: `${name}-c`,
  userName: `${name}-u`,
  sourceFile: '/k',
  credential: { kind: 'cloud-iam', longLived: false, provider: 'aws' },
  provider: 'eks',
  serverHost: 'abc.eks.amazonaws.com',
  ...over,
});

describe('pin file', () => {
  it('holds only current-context and parses as a kubeconfig whose only content is that', () => {
    const text = pinFileContent('prod-eu-1');
    expect(text).toBe('apiVersion: v1\nkind: Config\ncurrent-context: "prod-eu-1"\n');
    const parsed = parseKubeconfig('/pin', text);
    expect(parsed.ok && parsed.value.currentContext).toBe('prod-eu-1');
    expect(parsed.ok && [parsed.value.contexts, parsed.value.clusters, parsed.value.users]).toEqual([
      [],
      [],
      [],
    ]);
  });

  it('cannot be corrupted or injected by an unusual context name', () => {
    for (const name of [
      'a: b',
      'x\ny: z',
      '"quoted"',
      "it's",
      'arn:aws:eks:eu-west-1:1:cluster/p',
      '#hash',
      '- dash',
      'tab\tname',
      '\\back',
    ]) {
      const parsed = parseKubeconfig('/pin', pinFileContent(name));
      expect(parsed.ok && parsed.value.currentContext, JSON.stringify(name)).toBe(name);
    }
  });

  it('puts the pin FIRST and keeps the user files in order', () => {
    expect(boundKubeconfig('/pin/a', ['/u/1', '/u/2'], ':')).toBe('/pin/a:/u/1:/u/2');
    expect(boundKubeconfig('C:\\pin\\a', ['C:\\u\\1'], ';')).toBe('C:\\pin\\a;C:\\u\\1');
    expect(boundKubeconfig('/pin/a', [], ':')).toBe('/pin/a');
  });
});

describe('tab and banner', () => {
  it('names and colours the tab by environment; critical gets the warning icon and the loud name', () => {
    expect(tabStyle('prod-eu-1', PROD)).toEqual({
      name: 'prod-eu-1 · PROD',
      iconId: 'warning',
      colorId: 'terminal.ansiRed',
    });
    expect(tabStyle('s', { environment: 'staging', critical: false })).toEqual({
      name: 's · staging',
      iconId: 'terminal',
      colorId: 'terminal.ansiYellow',
    });
    expect(tabStyle('d', { environment: 'dev', critical: false })).toEqual({
      name: 'd · dev',
      iconId: 'terminal',
      colorId: 'terminal.ansiGreen',
    });
    expect(tabStyle('u', undefined)).toEqual({ name: 'u', iconId: 'terminal', colorId: undefined });
    expect(tabStyle('x', { environment: 'dr', critical: true }).name).toBe('x · DR');
  });

  it('the critical banner has the PROD chip, the exact sentence, and bolds "prod, critical"', () => {
    const b = banner('prod-eu-1', PROD);
    expect(b).toContain('\x1b[1;97;41m PROD \x1b[0m');
    expect(b).toContain(
      'Bound to prod-eu-1 (\x1b[1mprod, critical\x1b[0m). This terminal stays on this cluster even if your global context changes.',
    );
    expect(b.endsWith('\r\n')).toBe(true);
    expect(banner('s', { environment: 'staging', critical: false })).toBe(
      'Bound to s (staging). This terminal stays on this cluster even if your global context changes.\r\n',
    );
    expect(banner('u', undefined)).toBe(
      'Bound to u. This terminal stays on this cluster even if your global context changes.\r\n',
    );
  });

  it('the unverifiable banner is the WARNING chip variant', () => {
    expect(unverifiableBanner()).toBe(
      '\x1b[1;30;43m WARNING \x1b[0m Could not confirm this terminal is bound. Run: echo $KUBECONFIG\r\n',
    );
  });
});

describe('shells', () => {
  it('classifies shells and knows which can be verified', () => {
    expect(shellFamily('/bin/zsh')).toBe('posix');
    expect(shellFamily('/usr/bin/bash')).toBe('posix');
    expect(shellFamily('/usr/bin/fish')).toBe('fish');
    expect(shellFamily('C:\\Program Files\\PowerShell\\7\\pwsh.exe')).toBe('pwsh');
    expect(shellFamily('C:\\Windows\\System32\\cmd.exe')).toBe('cmd');
    expect(shellFamily('/bin/sh')).toBe('sh');
    expect(shellFamily('/usr/bin/nu')).toBe('unknown');
    expect(['posix', 'fish', 'pwsh'].every((f) => canVerify(f as never))).toBe(true);
    expect(['cmd', 'sh', 'unknown'].some((f) => canVerify(f as never))).toBe(false);
  });

  it('builds a verify and a re-assert command per shell, quoting safely', () => {
    expect(verifyCommand('posix')).toBe('echo $KUBECONFIG');
    expect(verifyCommand('pwsh')).toBe('$env:KUBECONFIG');
    expect(verifyCommand('cmd')).toBe('echo %KUBECONFIG%');
    expect(reassertCommand('posix', '/p/a:/u/b')).toBe("export KUBECONFIG='/p/a:/u/b'");
    expect(reassertCommand('posix', "/it's/a")).toBe("export KUBECONFIG='/it'\\''s/a'");
    expect(reassertCommand('fish', '/p')).toBe("set -gx KUBECONFIG '/p'");
    expect(reassertCommand('pwsh', "C:\\it's")).toBe("$env:KUBECONFIG = 'C:\\it''s'");
    expect(reassertCommand('cmd', 'C:\\p')).toBe('set "KUBECONFIG=C:\\p"');
  });

  // The strongest possible check of the quoting: run the generated command in a REAL shell and look for side effects.
  describe.skipIf(process.platform === 'win32')('quoting, proven in a real shell', () => {
    const hostile = [
      "/tmp/x'; touch SENTINEL; echo '",
      '/tmp/x"; touch SENTINEL; echo "',
      '/tmp/$(touch SENTINEL)',
      '/tmp/`touch SENTINEL`',
      "/tmp/it's a path with spaces",
      '/tmp/a\\b;c|d&e',
    ];
    for (const shell of ['sh', 'bash', 'zsh']) {
      it(`${shell}: the value round-trips exactly and nothing is executed`, () => {
        const which = spawnSync(shell, ['-c', 'exit 0']);
        if (which.error) return; // shell not installed here
        const dir = mkdtempSync(path.join(tmpdir(), 'sextant-quote-'));
        try {
          for (const value of hostile) {
            const cmd = reassertCommand('posix', value);
            const out = spawnSync(shell, ['-c', `${cmd}; printf %s "$KUBECONFIG"`], {
              cwd: dir,
              encoding: 'utf8',
            });
            expect(out.stdout, `${shell}: ${value}`).toBe(value);
            expect(
              existsSync(path.join(dir, 'SENTINEL')),
              `${shell} executed injected code for ${value}`,
            ).toBe(false);
          }
        } finally {
          rmSync(dir, { recursive: true, force: true });
        }
      });
    }
  });
});

describe('isBound: first entry or nothing', () => {
  const pin = '/g/pin/b.kubeconfig';
  it('accepts the pin alone or first, tolerating terminal escape codes and the echoed command', () => {
    expect(isBound(`echo $KUBECONFIG\r\n${pin}:/home/u/.kube/config\r\n`, pin, ':')).toBe(true);
    expect(isBound(`\x1b[32m${pin}\x1b[0m\r\n`, pin, ':')).toBe(true);
    expect(isBound(pin, pin, ':')).toBe(true);
  });
  it('rejects a startup file that replaced it, or that put its own path in front', () => {
    expect(isBound('/home/u/.kube/config\r\n', pin, ':')).toBe(false);
    expect(isBound(`/home/u/extra:${pin}\r\n`, pin, ':')).toBe(false); // present but not first: kubectl would not pin
    expect(isBound('', pin, ':')).toBe(false);
  });
  it('does not mistake a longer path that merely starts with the pin for the pin', () => {
    expect(isBound(`${pin}.bak:/x`, pin, ':')).toBe(false);
  });
  it('works with the Windows delimiter', () => {
    expect(isBound('C:\\g\\pin.yaml;C:\\u\\config', 'C:\\g\\pin.yaml', ';')).toBe(true);
  });
  it('real delimiter of this platform is what the extension uses', () => {
    expect(isBound(`${pin}${path.delimiter}/x`, pin)).toBe(true);
  });
});

describe('status bar model', () => {
  it('normal, critical and none match the copy deck', () => {
    const n = statusModel(
      ctx('staging-eu-1', {
        provider: 'gke',
        credential: { kind: 'cloud-iam', longLived: false, provider: 'gcp' },
      }),
      { environment: 'staging', critical: false },
      NOW,
    );
    expect(n.text).toBe('$(server-environment) staging-eu-1');
    expect(n.critical).toBe(false);
    expect(n.tooltip.split('\n')[0]).toBe('staging-eu-1 · staging');
    expect(n.tooltip).toContain('Cluster: staging-eu-1-c · Google GKE');
    expect(n.tooltip).toContain('Click to open a terminal for a cluster');
    expect(n.accessibility).toBe(
      'Kubernetes context staging-eu-1, staging. Activate to open a terminal for a cluster.',
    );

    const c = statusModel(ctx('prod-eu-1'), PROD, NOW);
    expect(c.text).toBe('$(warning) PROD  prod-eu-1');
    expect(c.critical).toBe(true);
    expect(c.tooltip.split('\n')[0]).toBe('prod-eu-1 · prod, critical');
    expect(c.accessibility).toBe(
      'Kubernetes context prod-eu-1, prod, critical. Activate to open a terminal for a cluster.',
    );
    expect(c.command).toBe('sextant.openTerminal');

    const none = statusModel(undefined, undefined, NOW);
    expect(none.text).toBe('$(circle-slash) No cluster');
    expect(none.command).toBe('workbench.action.openSettings');
    expect(none.commandArgs).toEqual(['sextant.kubeconfigPaths']);
    expect(none.accessibility).toBe('No Kubernetes cluster. Activate to open settings.');
  });

  it('an untagged current context says so; a prod context tagged normal is NOT shown as critical', () => {
    expect(statusModel(ctx('x'), undefined, NOW).tooltip.split('\n')[0]).toBe('x · untagged');
    const normal = statusModel(ctx('p'), { environment: 'prod', critical: false }, NOW);
    expect(normal.critical).toBe(false);
    expect(normal.text).toBe('$(server-environment) p');
  });
});

describe('external switch toast', () => {
  const base = { previous: 'dev', next: 'prod-eu-1', nextIsCritical: true, dismissed: new Set<string>() };
  it('warns only for a switch to a critical context', () => {
    expect(shouldWarnOnSwitch(base)).toBe(true);
    expect(shouldWarnOnSwitch({ ...base, nextIsCritical: false })).toBe(false);
  });
  it('never on the first load, an unchanged context, or a vanished one', () => {
    expect(shouldWarnOnSwitch({ ...base, previous: undefined })).toBe(false);
    expect(shouldWarnOnSwitch({ ...base, previous: 'prod-eu-1' })).toBe(false);
    expect(shouldWarnOnSwitch({ ...base, next: undefined })).toBe(false);
  });
  it('not again for a context the user dismissed this session, but yes for another critical one', () => {
    expect(shouldWarnOnSwitch({ ...base, dismissed: new Set(['prod-eu-1']) })).toBe(false);
    expect(shouldWarnOnSwitch({ ...base, next: 'prod-us-1', dismissed: new Set(['prod-eu-1']) })).toBe(true);
  });
});

describe('pickers', () => {
  const v = (name: string, over: Partial<ContextView> = {}): ContextView => ({
    context: ctx(name),
    environment: UNTAGGED,
    critical: false,
    current: false,
    terminal: false,
    ...over,
  });
  it('open-terminal list: current pinned first, then prod, staging, dev, custom, Untagged', () => {
    const entries = openTerminalEntries([
      v('u1'),
      v('d1', { environment: 'dev' }),
      v('p1', { environment: 'prod', critical: true }),
      v('cur', { environment: 'staging', current: true }),
      v('q1', { environment: 'qa' }),
      v('s1', { environment: 'staging' }),
    ]);
    expect(entries.map((e) => (e.kind === 'separator' ? `[${e.label}]` : e.label))).toEqual([
      '[current]',
      'cur',
      '[prod]',
      'p1',
      '[staging]',
      's1',
      '[dev]',
      'd1',
      '[qa]',
      'q1',
      '[Untagged]',
      'u1',
    ]);
  });
  it('items carry the design description and icons, including "terminal open"', () => {
    const [, cur, , prod] = openTerminalEntries([
      v('cur', { environment: 'staging', current: true }),
      v('p1', { environment: 'prod', critical: true, terminal: true }),
    ]);
    expect(cur).toMatchObject({ description: 'staging · Amazon EKS', iconId: 'pass-filled' });
    expect(prod).toMatchObject({
      description: 'critical · Amazon EKS · terminal open',
      iconId: 'warning',
      critical: true,
    });
  });
  it('confirmation puts Cancel first so Enter does the safe thing', () => {
    const e = confirmEntries('prod-eu-1');
    expect(e.map((x) => x.id)).toEqual(['cancel', 'open', 'openAndSkip', 'editTags']);
    expect(e[1]?.label).toBe('Open terminal on prod-eu-1');
    expect(e[2]?.label).toBe("Open, and don't ask again for this cluster");
  });
  it('asks only for critical contexts that are not in the skip list', () => {
    expect(needsConfirmation(true, [], 'p')).toBe(true);
    expect(needsConfirmation(true, ['p'], 'p')).toBe(false);
    expect(needsConfirmation(true, ['other'], 'p')).toBe(true);
    expect(needsConfirmation(false, [], 'p')).toBe(false);
  });
});
