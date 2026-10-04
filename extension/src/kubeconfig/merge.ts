import type { RawCluster, RawContext, RawKubeconfig, RawNamed, RawUser } from './raw';

export interface MergedKubeconfig {
  currentContext?: string;
  /** Each carries the file that defined it. */
  contexts: (RawNamed<RawContext> & { file: string })[];
  clusters: Map<string, RawCluster>;
  users: Map<string, RawUser>;
}

/**
 * Merges parsed kubeconfigs with kubectl's rules (client-go `ClientConfigLoadingRules`):
 *  - files are taken in order, and the FIRST file to define a given name wins, as a whole entry. If two files both
 *    define user `red`, the second's `red` is discarded entirely, even where the two do not conflict.
 *  - `current-context` is the first non-empty one.
 * Order of the returned contexts is first-seen order, which is stable and matches `kubectl config get-contexts`.
 */
export function mergeKubeconfigs(files: readonly RawKubeconfig[]): MergedKubeconfig {
  const contexts: MergedKubeconfig['contexts'] = [];
  const seenContexts = new Set<string>();
  const clusters = new Map<string, RawCluster>();
  const users = new Map<string, RawUser>();
  let currentContext: string | undefined;

  for (const f of files) {
    if (currentContext === undefined && f.currentContext) currentContext = f.currentContext;
    for (const c of f.contexts) {
      if (seenContexts.has(c.name)) continue;
      seenContexts.add(c.name);
      contexts.push({ ...c, file: f.file });
    }
    for (const c of f.clusters) if (!clusters.has(c.name)) clusters.set(c.name, c.value);
    for (const u of f.users) if (!users.has(u.name)) users.set(u.name, u.value);
  }
  return { ...(currentContext === undefined ? {} : { currentContext }), contexts, clusters, users };
}
