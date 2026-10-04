import type { KubeContext } from '../kubeconfig';
import { S } from '../ui/strings';
import { isExpired, isoDate, platformOf } from './fleetTree';

/**
 * Credential audit: contexts sorted by how risky their credential is. Uses only the classification the kubeconfig
 * package produced (auth kind, certificate notAfter); there is no way to reach a token, key or certificate body from
 * here. Wording follows the design system's copy deck.
 */
export type RiskId = 'expired' | 'longLived' | 'expiring' | 'shortLived';
export const RISK_ORDER: RiskId[] = ['expired', 'longLived', 'expiring', 'shortLived'];
const EXPIRING_DAYS = 30;
const DAY_MS = 24 * 60 * 60 * 1000;

const META: Record<RiskId, { label: string; short: string; icon: string; colorId: string }> = {
  expired: {
    label: S.audit.groups.expired,
    short: 'Expired',
    icon: 'error',
    colorId: 'sextant.risk.expiredForeground',
  },
  longLived: {
    label: S.audit.groups.longLived,
    short: 'Long-lived',
    icon: 'warning',
    colorId: 'sextant.risk.longLivedForeground',
  },
  expiring: {
    label: S.audit.groups.expiring,
    short: 'Expiring',
    icon: 'watch',
    colorId: 'sextant.risk.expiringForeground',
  },
  shortLived: {
    label: S.audit.groups.shortLived,
    short: 'Short-lived',
    icon: 'verified',
    colorId: 'sextant.risk.shortLivedForeground',
  },
};

export interface AuditRow {
  context: KubeContext;
  risk: RiskId;
  /** `{auth} · {expiry}` */
  description: string;
  hint: string;
  accessibility: string;
}

export interface AuditGroup {
  id: RiskId;
  label: string;
  icon: string;
  colorId: string;
  description: string;
  accessibility: string;
  /** The design collapses "Short-lived or brokered" by default; empty groups are not expandable. */
  collapsed: boolean;
  rows: AuditRow[];
}

/** Most urgent first: an expired credential is expired even if it was also long-lived. */
export function riskOf(c: KubeContext, now: Date): RiskId {
  const exp = c.credential.expiresAt;
  if (isExpired(c, now)) return 'expired';
  if (exp !== undefined && exp.getTime() - now.getTime() <= EXPIRING_DAYS * DAY_MS) return 'expiring';
  return c.credential.longLived ? 'longLived' : 'shortLived';
}

/** Audit wording differs slightly from Fleet's ("client certificate", not "client cert"). */
export function auditAuth(c: KubeContext): string {
  const { kind, provider } = c.credential;
  switch (kind) {
    case 'static-token':
      return 'static token';
    case 'client-cert':
      return 'client certificate';
    case 'basic-auth':
      return 'basic auth';
    case 'oidc':
      return 'OIDC';
    case 'cloud-iam':
      return provider === undefined ? 'cloud IAM' : `${provider} IAM`;
    case 'exec-plugin':
      return 'exec plugin';
    case 'none':
      return 'no credentials';
  }
}

export function auditExpiry(c: KubeContext, now: Date): string {
  const exp = c.credential.expiresAt;
  if (exp !== undefined) return isExpired(c, now) ? `expired ${isoDate(exp)}` : `expires ${isoDate(exp)}`;
  return c.credential.kind === 'static-token' ||
    c.credential.kind === 'basic-auth' ||
    c.credential.kind === 'client-cert'
    ? 'no expiry'
    : 'on demand';
}

const LOCAL = new Set(['kind', 'minikube', 'docker-desktop']);

/** The suggested fix. Generic unless the platform makes a more specific one possible. */
export function remediation(c: KubeContext, risk: RiskId): string {
  const { kind } = c.credential;
  if (kind === 'client-cert') {
    if (risk === 'expired') {
      return c.provider === 'minikube'
        ? 'Certificate expired: run minikube update-context or re-create the cluster'
        : 'Certificate expired: renew it or re-create the cluster';
    }
    if (LOCAL.has(c.provider)) {
      return `Local cluster certificate: acceptable for ${platformOf(c.provider).label}; rotate yearly`;
    }
    return risk === 'shortLived'
      ? 'Short-lived certificate: no action needed'
      : 'Long-lived certificate: issue a shorter-lived certificate';
  }
  if (kind === 'static-token') {
    return c.provider === 'openshift'
      ? 'Static token: replace with an exec plugin (oc login --web)'
      : 'Static token: replace with an exec plugin';
  }
  if (kind === 'basic-auth') return 'Basic auth: switch to a token issued by an identity provider';
  if (kind === 'none') return 'No credentials in this entry: nothing to rotate';
  return 'Exec plugin: no action needed';
}

const plural = (n: number): string => `${String(n)} context${n === 1 ? '' : 's'}`;
const longDate = (d: Date): string =>
  `${String(d.getUTCDate())} ${d.toLocaleString('en-GB', { month: 'long', timeZone: 'UTC' })} ${String(d.getUTCFullYear())}`;

export function buildAudit(contexts: readonly KubeContext[], now: Date): AuditGroup[] {
  const rowsBy = new Map<RiskId, AuditRow[]>(RISK_ORDER.map((r) => [r, []]));
  for (const c of contexts) {
    const risk = riskOf(c, now);
    const exp = c.credential.expiresAt;
    const description = `${auditAuth(c)} · ${auditExpiry(c, now)}`;
    const hint = remediation(c, risk);
    const spoken =
      risk === 'expired'
        ? 'expired credential'
        : risk === 'longLived'
          ? 'long-lived credential'
          : risk === 'expiring'
            ? 'credential expiring soon'
            : 'short-lived credential';
    rowsBy.get(risk)?.push({
      context: c,
      risk,
      description,
      hint,
      accessibility: `${c.name}, ${spoken}, ${auditAuth(c)}, ${
        exp === undefined
          ? auditExpiry(c, now)
          : `${isExpired(c, now) ? 'expired' : 'expires'} ${longDate(exp)}`
      }. Expand for the suggested fix.`,
    });
  }
  return RISK_ORDER.map((id): AuditGroup => {
    const rows = (rowsBy.get(id) ?? []).sort((a, b) => a.context.name.localeCompare(b.context.name));
    const m = META[id];
    return {
      id,
      label: m.label,
      icon: m.icon,
      colorId: m.colorId,
      description: rows.length === 0 ? S.audit.none : String(rows.length),
      accessibility:
        id === 'expired'
          ? `Expired credentials, ${rows.length === 0 ? 'none' : plural(rows.length)}`
          : `${m.label}, ${rows.length === 0 ? 'none' : plural(rows.length)}`,
      collapsed: id === 'shortLived' || rows.length === 0,
      rows,
    };
  });
}

export const expiredCount = (groups: readonly AuditGroup[]): number =>
  groups.find((g) => g.id === 'expired')?.rows.length ?? 0;

// ---- the Markdown report ----------------------------------------------------------------------------------------

/** A context name as a Markdown code span, safe against backticks and table pipes. */
export function codeSpan(text: string): string {
  const clean = text.replace(/\|/g, '\\|').replace(/[\r\n]+/g, ' ');
  const longest = Math.max(0, ...(clean.match(/`+/g) ?? []).map((m) => m.length));
  const fence = '`'.repeat(longest + 1);
  return `${fence}${longest > 0 ? ` ${clean} ` : clean}${fence}`;
}

const cell = (text: string): string => text.replace(/\|/g, '\\|').replace(/[\r\n]+/g, ' ');

/** The report. Auth methods and dates only; the closing line says so, and tests prove it is true. */
export function renderReport(contexts: readonly KubeContext[], fileCount: number, now: Date): string {
  const groups = buildAudit(contexts, now);
  const count = (id: RiskId): number => groups.find((g) => g.id === id)?.rows.length ?? 0;
  const lines = [
    '# Sextant credential audit',
    '',
    `Generated ${isoDate(now)} from ${String(contexts.length)} contexts in ${String(fileCount)} kubeconfig file${fileCount === 1 ? '' : 's'}.`,
    '',
    `**Expired ${String(count('expired'))} · Long-lived ${String(count('longLived'))} · Expiring within 30 days ${String(count('expiring'))} · Short-lived or brokered ${String(count('shortLived'))}**`,
    '',
    '| Context | Platform | Authentication | Expires | Risk | Suggestion |',
    '|---|---|---|---|---|---|',
  ];
  for (const g of groups) {
    for (const r of g.rows) {
      const exp = r.context.credential.expiresAt;
      const expires =
        exp === undefined
          ? auditExpiry(r.context, now)
          : isExpired(r.context, now)
            ? `${isoDate(exp)} (expired)`
            : isoDate(exp);
      lines.push(
        `| ${codeSpan(r.context.name)} | ${cell(platformOf(r.context.provider).label)} | ${auditAuth(r.context)} | ${expires} | ${META[r.risk].short} | ${cell(r.hint)} |`,
      );
    }
  }
  lines.push(
    '',
    '> This report lists authentication methods and expiry dates only. No tokens, keys, passwords or certificate contents are included.',
    '',
  );
  return lines.join('\n');
}
