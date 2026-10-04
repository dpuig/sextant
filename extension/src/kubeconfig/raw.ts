/**
 * INTERNAL to src/kubeconfig. These types describe a kubeconfig as it is on disk, secrets included. Nothing outside
 * this package may import this module; index.ts is the only public surface and it exposes types.ts only.
 */

export interface RawNamed<T> {
  name: string;
  value: T;
}

export interface RawContext {
  cluster: string;
  user: string;
  namespace?: string;
}

/** Cluster entry: only fields we use. The CA bundle and anything else is never read. */
export interface RawCluster {
  server?: string;
}

/** User entry: deliberately loose, since this is where the secrets live. Classified in classify.ts and dropped. */
export type RawUser = Record<string, unknown>;

export interface RawKubeconfig {
  file: string;
  currentContext?: string;
  contexts: RawNamed<RawContext>[];
  clusters: RawNamed<RawCluster>[];
  users: RawNamed<RawUser>[];
}
