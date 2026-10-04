// The only public surface of the kubeconfig package. raw.ts, parse.ts and merge.ts are internal on purpose.
export { loadFleet, hostOf, type ReadFile } from './load';
export { discoverKubeconfigPaths, type DiscoverOptions } from './discover';
export type {
  AuthKind,
  CredentialSummary,
  Fleet,
  KubeContext,
  KubeconfigError,
  KubeconfigErrorCode,
} from './types';
