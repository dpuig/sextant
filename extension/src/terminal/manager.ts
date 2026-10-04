import { chmodSync, mkdirSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import * as path from 'node:path';
import * as vscode from 'vscode';
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
} from '../model/terminal';
import type { Store } from '../store';
import { S } from '../ui/strings';

export type Binding = 'pending' | 'verified' | 'unverified' | 'not-checkable';

interface Bound {
  terminal: vscode.Terminal;
  contextName: string;
  pin: string;
  binding: Binding;
}

const VERIFY_TIMEOUT_MS = 5000;

/**
 * Opens and tracks bound terminals. Each one gets a read-only pin file (only `current-context`) FIRST in its own
 * KUBECONFIG, see src/model/terminal.ts. Shell startup files can overwrite KUBECONFIG, so where VS Code's shell
 * integration is available the terminal prints $KUBECONFIG (visibly: that is the proof), re-asserts it once if the
 * pin is not first, and warns if it still cannot be confirmed.
 */
export class TerminalManager implements vscode.Disposable {
  private readonly bound: Bound[] = [];
  private readonly subs: vscode.Disposable[] = [];

  constructor(
    private readonly store: Store,
    private readonly pinDir: string,
  ) {
    mkdirSync(pinDir, { recursive: true, mode: 0o700 });
    // Terminals are created transient (not revived after a reload), so any pin left here belongs to a dead terminal.
    for (const f of readdirSync(pinDir)) rmSync(path.join(pinDir, f), { force: true });
    this.subs.push(
      vscode.window.onDidCloseTerminal((t) => {
        this.closed(t);
      }),
    );
  }

  /** The bound terminals, for tests and for "focus the existing one". */
  list(): readonly Readonly<Bound>[] {
    return this.bound;
  }

  bindingOf(t: vscode.Terminal): Binding | undefined {
    return this.bound.find((b) => b.terminal === t)?.binding;
  }

  focusExisting(contextName: string): boolean {
    const b = this.bound.find((x) => x.contextName === contextName);
    if (b === undefined) return false;
    b.terminal.show();
    return true;
  }

  open(contextName: string): vscode.Terminal | undefined {
    const context = this.store.contextByName(contextName);
    if (context === undefined) {
      void vscode.window.showWarningMessage(S.fleet.removed(contextName));
      return undefined;
    }
    const tag = this.store.tagOf(context);
    const style = tabStyle(contextName, tag);
    const userPaths = this.store.userPaths();
    const pin = path.join(this.pinDir, `${randomBytes(8).toString('hex')}.yaml`);
    const family = shellFamily(vscode.env.shell);
    const checkable = canVerify(family);
    try {
      writeFileSync(pin, pinFileContent(contextName), { mode: 0o400 });
      chmodSync(pin, 0o400);
      const kubeconfig = boundKubeconfig(pin, userPaths);
      const terminal = vscode.window.createTerminal({
        name: style.name,
        iconPath: new vscode.ThemeIcon(style.iconId),
        ...(style.colorId === undefined ? {} : { color: new vscode.ThemeColor(style.colorId) }),
        env: { KUBECONFIG: kubeconfig },
        message: banner(contextName, tag) + (checkable ? '' : unverifiableBanner()),
        isTransient: true,
      });
      const entry: Bound = { terminal, contextName, pin, binding: checkable ? 'pending' : 'not-checkable' };
      this.bound.push(entry);
      this.store.terminalOpened(contextName);
      terminal.show();
      if (checkable) void this.verify(entry, family, kubeconfig);
      return terminal;
    } catch (err) {
      rmSync(pin, { force: true });
      const reason = err instanceof Error ? err.message : String(err);
      void vscode.window.showErrorMessage(S.terminal.startFailed(contextName, reason));
      return undefined;
    }
  }

  private async verify(
    entry: Bound,
    family: ReturnType<typeof shellFamily>,
    kubeconfig: string,
  ): Promise<void> {
    const ok = await this.check(entry, family, kubeconfig);
    if (!this.bound.includes(entry)) return; // closed meanwhile
    entry.binding = ok ? 'verified' : 'unverified';
    if (ok) return;
    const choice = await vscode.window.showWarningMessage(
      S.terminal.toastNotBound(entry.contextName),
      S.terminal.copyCommand,
      S.terminal.dismiss,
    );
    if (choice === S.terminal.copyCommand) await vscode.env.clipboard.writeText(verifyCommand(family));
  }

  /** Runs `echo $KUBECONFIG` through shell integration; one re-assert if the pin is not first. */
  private async check(
    entry: Bound,
    family: ReturnType<typeof shellFamily>,
    kubeconfig: string,
  ): Promise<boolean> {
    const deadline = Date.now() + VERIFY_TIMEOUT_MS;
    const integration = await this.integration(entry.terminal, deadline);
    if (integration === undefined) return false;
    const probe = async (): Promise<boolean> => {
      const exec = integration.executeCommand(verifyCommand(family));
      let out = '';
      const read = (async (): Promise<void> => {
        for await (const chunk of exec.read()) out += chunk;
      })();
      const finished = new Promise<void>((resolve) => {
        const sub = vscode.window.onDidEndTerminalShellExecution((e) => {
          if (e.execution === exec) {
            sub.dispose();
            resolve();
          }
        });
        setTimeout(
          () => {
            sub.dispose();
            resolve();
          },
          Math.max(0, deadline - Date.now()),
        );
      });
      await finished;
      await Promise.race([read, new Promise((r) => setTimeout(r, 200))]);
      return isBound(out, entry.pin);
    };
    if (await probe()) return true;
    integration.executeCommand(reassertCommand(family, kubeconfig));
    return Date.now() < deadline && (await probe());
  }

  private integration(
    t: vscode.Terminal,
    deadline: number,
  ): Promise<vscode.TerminalShellIntegration | undefined> {
    if (t.shellIntegration) return Promise.resolve(t.shellIntegration);
    return new Promise((resolve) => {
      const sub = vscode.window.onDidChangeTerminalShellIntegration((e) => {
        if (e.terminal === t) {
          clear();
          resolve(e.shellIntegration);
        }
      });
      const timer = setTimeout(
        () => {
          clear();
          resolve(undefined);
        },
        Math.max(0, deadline - Date.now()),
      );
      const clear = (): void => {
        sub.dispose();
        clearTimeout(timer);
      };
    });
  }

  private closed(t: vscode.Terminal): void {
    const i = this.bound.findIndex((b) => b.terminal === t);
    if (i < 0) return;
    const [entry] = this.bound.splice(i, 1);
    if (entry === undefined) return;
    rmSync(entry.pin, { force: true }); // only now: a pin deleted under a live terminal would let it drift silently
    this.store.terminalClosed(entry.contextName);
  }

  dispose(): void {
    for (const s of this.subs) s.dispose();
    for (const b of this.bound) rmSync(b.pin, { force: true });
  }
}
