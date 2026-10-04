import { promises as fsp } from 'node:fs';
import { classify } from './classify';
import { inferProvider } from '../model/provider';
import { discoverKubeconfigPaths, type DiscoverOptions } from './discover';
import { mergeKubeconfigs } from './merge';
import { parseKubeconfig } from './parse';
import type { Fleet, KubeconfigError, KubeContext } from './types';
import type { RawKubeconfig } from './raw';

/** Reads a file; `undefined` means it does not exist (kubectl silently skips missing files in KUBECONFIG). */
export type ReadFile = (path: string) => Promise<string | undefined>;

export const nodeReadFile: ReadFile = async (p) => {
  try {
    return await fsp.readFile(p, 'utf8');
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return undefined;
    throw err;
  }
};

/**
 * Discovers, reads, parses and merges kubeconfigs into a Fleet. One broken file does not hide the rest: it becomes an
 * entry in `errors` and the other files are still used (kubectl would abort; for a fleet view that would be worse).
 */
export async function loadFleet(opts: DiscoverOptions & { readFile?: ReadFile }): Promise<Fleet> {
  const read = opts.readFile ?? nodeReadFile;
  const paths = discoverKubeconfigPaths(opts);
  const parsed: RawKubeconfig[] = [];
  const errors: KubeconfigError[] = [];
  const files: string[] = [];

  for (const p of paths) {
    let content: string | undefined;
    try {
      content = await read(p);
    } catch {
      errors.push({ file: p, code: 'read-failed' }); // never the OS message: it can include the path only, but keep it uniform
      continue;
    }
    if (content === undefined) continue;
    const r = parseKubeconfig(p, content);
    if (r.ok) {
      parsed.push(r.value);
      files.push(p);
    } else {
      errors.push(r.error);
    }
  }

  const merged = mergeKubeconfigs(parsed);
  const contexts: KubeContext[] = merged.contexts.map((c) => {
    const server = merged.clusters.get(c.value.cluster)?.server;
    const host = server === undefined ? undefined : hostOf(server);
    const credential = classify(merged.users.get(c.value.user));
    return {
      name: c.name,
      clusterName: c.value.cluster,
      userName: c.value.user,
      ...(c.value.namespace === undefined ? {} : { namespace: c.value.namespace }),
      ...(host === undefined ? {} : { serverHost: host }),
      sourceFile: c.file,
      ...(c.line === undefined ? {} : { sourceLine: c.line }),
      credential,
      provider: inferProvider({
        contextName: c.name,
        clusterName: c.value.cluster,
        ...(host === undefined ? {} : { serverHost: host }),
        ...(credential.provider === undefined ? {} : { credentialProvider: credential.provider }),
      }),
    };
  });

  const current = merged.currentContext;
  return {
    contexts,
    ...(current !== undefined && contexts.some((c) => c.name === current) ? { currentContext: current } : {}),
    files,
    errors,
  };
}

/** host[:port] only. A server URL may carry userinfo (`https://user:pass@host`), which must never reach the model. */
export function hostOf(server: string): string | undefined {
  try {
    const u = new URL(server);
    return u.host === '' ? undefined : u.host;
  } catch {
    return undefined;
  }
}
