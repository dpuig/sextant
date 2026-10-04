import * as vscode from 'vscode';
import { discoverKubeconfigPaths, loadFleet, type Fleet, type KubeContext } from './kubeconfig';
import type { ContextView } from './model/fleetTree';
import { parseTagRules, resolveTag, type ResolvedTag, type TagRule } from './model/tags';
import { readConfirmSkip, readKubeconfigPaths, readTags } from './settings';
import { watchFiles } from './views/watcher';

const EMPTY: Fleet = { contexts: [], files: [], errors: [] };

/**
 * The extension's single source of truth: the loaded kubeconfig fleet, the user's tag rules, and which contexts have a
 * bound terminal. Views, the status bar and the terminal manager read from here and re-render on `onDidChange`.
 */
export class Store implements vscode.Disposable {
  fleet: Fleet = EMPTY;
  rules: TagRule[] = [];
  skip: string[] = [];
  loading = false;
  loaded = false;

  private generation = 0;
  private watcher: vscode.Disposable | undefined;
  private current: Promise<void> = Promise.resolve();
  private readonly terminals = new Map<string, number>();
  private readonly changed = new vscode.EventEmitter<void>();
  private readonly switched = new vscode.EventEmitter<{
    previous: string | undefined;
    next: string | undefined;
  }>();
  readonly onDidChange = this.changed.event;
  /** Fires when the global current-context changes between two loads (never on the first load). */
  readonly onDidSwitch = this.switched.event;

  constructor(
    private readonly env: NodeJS.ProcessEnv = process.env,
    private readonly clock: () => Date = () => new Date(),
  ) {
    this.readSettings();
  }

  now(): Date {
    return this.clock();
  }

  whenLoaded(): Promise<void> {
    return this.current;
  }

  /** Re-reads the tag and confirmation settings (no file I/O) and tells the views. */
  readSettings(): void {
    this.rules = parseTagRules(readTags()).rules;
    this.skip = readConfirmSkip();
    this.changed.fire();
  }

  userPaths(): string[] {
    return discoverKubeconfigPaths({ env: this.env, configuredPaths: readKubeconfigPaths() });
  }

  refresh(): Promise<void> {
    const mine = ++this.generation;
    this.loading = true;
    this.changed.fire();
    this.current = this.load(mine);
    return this.current;
  }

  private async load(mine: number): Promise<void> {
    const opts = { env: this.env, configuredPaths: readKubeconfigPaths() };
    const paths = discoverKubeconfigPaths(opts);
    this.watcher?.dispose();
    this.watcher = watchFiles(paths, () => void this.refresh());
    const fleet = await loadFleet(opts);
    if (mine !== this.generation) return; // a newer load started while this one was reading: drop the stale result
    const hadLoad = this.loaded;
    const previous = this.fleet.currentContext;
    this.fleet = fleet;
    this.loaded = true;
    this.loading = false;
    this.changed.fire();
    if (hadLoad && previous !== fleet.currentContext)
      this.switched.fire({ previous, next: fleet.currentContext });
  }

  tagOf = (c: KubeContext): ResolvedTag | undefined => resolveTag(c.name, this.rules);

  terminalSet(): ReadonlySet<string> {
    return new Set(this.terminals.keys());
  }

  terminalOpened(name: string): void {
    this.terminals.set(name, (this.terminals.get(name) ?? 0) + 1);
    this.changed.fire();
  }

  terminalClosed(name: string): void {
    const n = (this.terminals.get(name) ?? 0) - 1;
    if (n <= 0) this.terminals.delete(name);
    else this.terminals.set(name, n);
    this.changed.fire();
  }

  contextByName(name: string): KubeContext | undefined {
    return this.fleet.contexts.find((c) => c.name === name);
  }

  /** Every context with its tag, whether it is current, and whether a bound terminal is open. */
  views(): ContextView[] {
    return this.fleet.contexts.map((context) => {
      const tag = this.tagOf(context);
      return {
        context,
        environment: tag?.environment ?? 'Untagged',
        critical: tag?.critical ?? false,
        current: context.name === this.fleet.currentContext,
        terminal: this.terminals.has(context.name),
      };
    });
  }

  dispose(): void {
    this.watcher?.dispose();
    this.changed.dispose();
    this.switched.dispose();
  }
}
