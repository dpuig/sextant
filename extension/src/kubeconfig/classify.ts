import { X509Certificate } from 'node:crypto';
import type { AuthKind, CredentialSummary } from './types';
import type { RawUser } from './raw';

/** A certificate valid for longer than this is "long-lived": the sort of credential that outlives an employee. */
export const LONG_LIVED_DAYS = 30;
const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * Turns a raw user entry (secrets and all) into a CredentialSummary that cannot hold one. This is the only function
 * that reads credential fields. It never throws and never echoes any field value: a malformed entry is simply
 * classified as well as it can be.
 *
 * Precedence mirrors how clients treat an entry: an exec plugin or auth-provider replaces static credentials; among
 * static ones the riskiest is reported (a bearer token, then basic auth, then a client certificate).
 */
export function classify(user: RawUser | undefined): CredentialSummary {
  if (user === undefined) return { kind: 'none', longLived: false };

  const exec = asRecord(user.exec);
  if (exec !== undefined) return classifyExec(exec);

  const provider = asRecord(user['auth-provider']);
  if (provider !== undefined) return classifyAuthProvider(provider);

  if (isNonEmptyString(user.token) || isNonEmptyString(user.tokenFile)) {
    // A tokenFile may point at a rotated (projected) token, but nothing in the kubeconfig says so; assuming it is
    // short-lived would give false assurance in an audit, so it is reported as long-lived.
    return { kind: 'static-token', longLived: true };
  }
  if (isNonEmptyString(user.password)) return { kind: 'basic-auth', longLived: true };

  if (isNonEmptyString(user['client-certificate-data']) || isNonEmptyString(user['client-certificate'])) {
    return classifyCertificate(user['client-certificate-data']);
  }
  return { kind: 'none', longLived: false };
}

function classifyCertificate(data: unknown): CredentialSummary {
  const validity = typeof data === 'string' ? readValidity(data) : undefined;
  if (validity === undefined) {
    // Inline data we could not parse, or a certificate stored as a file path: expiry is unknown, so assume long-lived.
    return { kind: 'client-cert', longLived: true };
  }
  return {
    kind: 'client-cert',
    longLived: validity.till.getTime() - validity.from.getTime() > LONG_LIVED_DAYS * DAY_MS,
    expiresAt: validity.till,
  };
}

/** Reads notBefore/notAfter from base64(PEM). Any failure is swallowed: the input is untrusted and never echoed. */
function readValidity(b64: string): { from: Date; till: Date } | undefined {
  try {
    const cert = new X509Certificate(Buffer.from(b64, 'base64'));
    const from = new Date(cert.validFrom);
    const till = new Date(cert.validTo);
    if (Number.isNaN(from.getTime()) || Number.isNaN(till.getTime())) return undefined;
    return { from, till };
  } catch {
    return undefined;
  }
}

// Exec plugins: the command names that identify the cloud or identity system behind a short-lived credential.
const CLOUD_EXEC: [RegExp, string][] = [
  [/^(aws|aws-iam-authenticator|aws-eks)$/, 'aws'],
  [/^(gke-gcloud-auth-plugin|gcloud)$/, 'gcp'],
  [/^kubelogin$/, 'azure'],
];

function classifyExec(exec: Record<string, unknown>): CredentialSummary {
  const base = commandBase(exec.command);
  if (base === 'kx') return { kind: 'exec-plugin', longLived: false, provider: 'sextant' };
  if (base !== undefined && (base.includes('oidc') || base === 'kubectl-oidc_login')) {
    return { kind: 'oidc', longLived: false, provider: 'oidc' };
  }
  for (const [re, provider] of CLOUD_EXEC) {
    if (base !== undefined && re.test(base)) return { kind: 'cloud-iam', longLived: false, provider };
  }
  if (base === 'oc') return { kind: 'exec-plugin', longLived: false, provider: 'openshift' };
  return { kind: 'exec-plugin', longLived: false, provider: 'unknown' };
}

function classifyAuthProvider(provider: Record<string, unknown>): CredentialSummary {
  const name = typeof provider.name === 'string' ? provider.name : '';
  const kinds: Record<string, [AuthKind, string]> = {
    oidc: ['oidc', 'oidc'],
    gcp: ['cloud-iam', 'gcp'],
    azure: ['cloud-iam', 'azure'],
  };
  const hit = kinds[name];
  // The provider's config holds refresh tokens and client secrets; it is never inspected beyond its name.
  return hit === undefined
    ? { kind: 'exec-plugin', longLived: false, provider: 'unknown' }
    : { kind: hit[0], longLived: false, provider: hit[1] };
}

/** Basename without directory or Windows extension, so `/usr/local/bin/aws` and `aws.exe` both give `aws`. */
function commandBase(command: unknown): string | undefined {
  if (typeof command !== 'string' || command === '') return undefined;
  const last = command.split(/[\\/]/).pop() ?? command;
  return last.replace(/\.(exe|cmd|bat)$/i, '').toLowerCase();
}

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : undefined;
}

function isNonEmptyString(v: unknown): boolean {
  return typeof v === 'string' && v !== '';
}
