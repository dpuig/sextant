/**
 * Best-effort guess of where a cluster runs, from facts already in the kubeconfig. It is only used to group the fleet
 * tree, so a wrong or unknown answer is cosmetic; it must never be used for a security decision.
 */
export type Provider =
  'eks' | 'gke' | 'aks' | 'openshift' | 'kind' | 'minikube' | 'docker-desktop' | 'unknown';

export interface ProviderHints {
  contextName: string;
  clusterName: string;
  serverHost?: string;
  /** The credential's issuing system, from classification (`aws`, `gcp`, `azure`, ...). */
  credentialProvider?: string;
}

export function inferProvider(h: ProviderHints): Provider {
  const host = (h.serverHost ?? '').toLowerCase();
  if (host.endsWith('.eks.amazonaws.com') || h.credentialProvider === 'aws') return 'eks';
  if (h.clusterName.startsWith('gke_') || h.credentialProvider === 'gcp') return 'gke';
  if (host.endsWith('.azmk8s.io') || h.credentialProvider === 'azure') return 'aks';
  // OpenShift contexts are named `<namespace>/api-<cluster>-<domain>:6443/<user>`; its API host starts with `api.`.
  if (
    /^[^/]+\/api-.+:\d+\/.+/.test(h.contextName) ||
    h.clusterName.startsWith('api-') ||
    h.credentialProvider === 'openshift'
  ) {
    return 'openshift';
  }
  if (h.clusterName.startsWith('kind-') || h.contextName.startsWith('kind-')) return 'kind';
  if (h.clusterName === 'minikube' || h.contextName === 'minikube') return 'minikube';
  if (h.contextName === 'docker-desktop' || h.clusterName === 'docker-desktop') return 'docker-desktop';
  return 'unknown';
}
