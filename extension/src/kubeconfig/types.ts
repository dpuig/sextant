/**
 * The public model of a kubeconfig. By construction it has no field that can hold a secret: contexts carry names, a
 * host, and a CredentialSummary (a classification, never a value). Everything that touches raw credential data stays
 * inside this package (see raw.ts), so a leak is a bug in one place, not in every consumer.
 */

export type AuthKind =
  'static-token' | 'client-cert' | 'exec-plugin' | 'oidc' | 'cloud-iam' | 'basic-auth' | 'none';

export interface CredentialSummary {
  kind: AuthKind;
  /** True when the credential never expires on its own (a static token, a long-lived client certificate). */
  longLived: boolean;
  /** Client-certificate expiry, read from the certificate itself. Never derived from a token. */
  expiresAt?: Date;
  /** Best-effort identity of the issuing system, for example `aws`, `gke`, `azure`, `kx`. */
  provider?: string;
}

export interface KubeContext {
  /** Context name, as in the kubeconfig. */
  name: string;
  clusterName: string;
  userName: string;
  namespace?: string;
  /** Host (and port) of the cluster's API server. Never the full URL, which may carry userinfo. */
  serverHost?: string;
  /** The file that defined this context (the first one wins, as in kubectl). */
  sourceFile: string;
  credential: CredentialSummary;
  /** Best-effort hosting platform, for grouping only; `unknown` when it cannot be told. */
  provider: string;
}

export type KubeconfigErrorCode =
  'read-failed' | 'too-large' | 'too-complex' | 'parse-failed' | 'invalid-shape';

/**
 * A problem with one kubeconfig file. Deliberately carries no message derived from file content: YAML libraries quote
 * the offending line in their errors, and a kubeconfig line can be a token.
 */
export interface KubeconfigError {
  file: string;
  code: KubeconfigErrorCode;
  /** The YAML parser's error code (a closed set such as BAD_INDENT), when code is parse-failed. */
  parserCode?: string;
}

export interface Fleet {
  contexts: KubeContext[];
  /** The merged `current-context`, if it names a context that exists. */
  currentContext?: string;
  /** Files that were read (in precedence order) and files that failed. */
  files: string[];
  errors: KubeconfigError[];
}
