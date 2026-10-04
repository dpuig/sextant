import * as os from 'node:os';
import * as path from 'node:path';

export interface DiscoverOptions {
  /** `process.env`-like. Only KUBECONFIG is read. */
  env: Record<string, string | undefined>;
  homedir?: string;
  cwd?: string;
  /** Extra paths from the extension's settings. They are appended last, so they can never override a real config. */
  configuredPaths?: readonly string[];
  /** Path-list delimiter; `:` on POSIX and `;` on Windows. Injectable for tests. */
  delimiter?: string;
}

/**
 * The kubeconfig files to read, in precedence order.
 *
 * kubectl's rule: if KUBECONFIG is set, its (non-empty) entries are the files, in order; otherwise `~/.kube/config`.
 * Duplicates are collapsed. Paths from settings are appended after, because the first definition of a name wins when
 * merging and a setting must not be able to shadow the user's real configuration.
 */
export function discoverKubeconfigPaths(opts: DiscoverOptions): string[] {
  const home = opts.homedir ?? os.homedir();
  const cwd = opts.cwd ?? process.cwd();
  const delimiter = opts.delimiter ?? path.delimiter;

  const fromEnv = (opts.env.KUBECONFIG ?? '')
    .split(delimiter)
    .map((p) => p.trim())
    .filter((p) => p !== '');
  const base =
    fromEnv.length > 0 ? fromEnv.map((p) => resolve(p, cwd)) : [path.join(home, '.kube', 'config')];
  // Settings accept ~ because they are typed by humans; KUBECONFIG, like in kubectl, does not (the shell expands it).
  const extra = (opts.configuredPaths ?? [])
    .map((p) => resolve(expandHome(p, home), cwd))
    .filter((p) => p !== '');

  return [...new Set([...base, ...extra])];
}

function expandHome(p: string, home: string): string {
  if (p === '~') return home;
  if (p.startsWith('~/') || p.startsWith('~\\')) return path.join(home, p.slice(2));
  return p;
}

function resolve(p: string, cwd: string): string {
  return path.isAbsolute(p) ? path.normalize(p) : path.resolve(cwd, p);
}
