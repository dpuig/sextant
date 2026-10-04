import * as path from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadFleet, type KubeContext } from '../../src/kubeconfig';
import { buildAudit, codeSpan, expiredCount, remediation, renderReport, riskOf } from '../../src/model/audit';
import { expectNoCanary } from '../canary';

const NOW = new Date('2026-10-04T00:00:00Z');
const dir = path.join(__dirname, '..', 'fixtures');
const ctx = (name: string, cr: KubeContext['credential'], over: Partial<KubeContext> = {}): KubeContext => ({
  name,
  clusterName: name,
  userName: name,
  sourceFile: '/k',
  credential: cr,
  provider: 'unknown',
  ...over,
});
const cert = (days: number, longLived = true): KubeContext['credential'] => ({
  kind: 'client-cert',
  longLived,
  expiresAt: new Date(NOW.getTime() + days * 86_400_000),
});

describe('riskOf: most urgent first', () => {
  it('expired beats expiring beats long-lived beats short-lived', () => {
    expect(riskOf(ctx('a', cert(-1)), NOW)).toBe('expired');
    expect(riskOf(ctx('a', cert(10)), NOW)).toBe('expiring'); // long-lived but about to lapse: the urgent story wins
    expect(riskOf(ctx('a', cert(30)), NOW)).toBe('expiring'); // the boundary day is inside the window
    expect(riskOf(ctx('a', cert(31)), NOW)).toBe('longLived');
    expect(riskOf(ctx('a', { kind: 'static-token', longLived: true }), NOW)).toBe('longLived');
    expect(riskOf(ctx('a', { kind: 'cloud-iam', longLived: false, provider: 'aws' }), NOW)).toBe(
      'shortLived',
    );
    expect(riskOf(ctx('a', { kind: 'none', longLived: false }), NOW)).toBe('shortLived');
  });
});

describe('buildAudit', () => {
  const contexts = [
    ctx(
      'minikube',
      { kind: 'client-cert', longLived: true, expiresAt: new Date('2026-03-02T00:00:00Z') },
      { provider: 'minikube' },
    ),
    ctx('old-ci', { kind: 'basic-auth', longLived: true }),
    ctx('prod-onprem', { kind: 'static-token', longLived: true }, { provider: 'openshift' }),
    ctx(
      'kind-local',
      { kind: 'client-cert', longLived: true, expiresAt: new Date('2026-12-31T00:00:00Z') },
      { provider: 'kind' },
    ),
    ctx('prod-eu-1', { kind: 'cloud-iam', longLived: false, provider: 'aws' }),
  ];
  const groups = buildAudit(contexts, NOW);

  it('has the four groups in fixed order with the design labels, icons and counts', () => {
    expect(groups.map((g) => [g.label, g.icon, g.description])).toEqual([
      ['Expired', 'error', '1'],
      ['Long-lived credentials', 'warning', '3'],
      ['Expiring within 30 days', 'watch', 'none'],
      ['Short-lived or brokered', 'verified', '1'],
    ]);
  });

  it('collapses the short-lived group and every empty group', () => {
    expect(groups.map((g) => g.collapsed)).toEqual([false, false, true, true]);
  });

  it('describes a row as "{auth} · {expiry}" and sorts rows by name', () => {
    expect(groups[0]?.rows[0]?.description).toBe('client certificate · expired 2026-03-02');
    const long = groups[1]?.rows.map((r) => [r.context.name, r.description]);
    expect(long).toEqual([
      ['kind-local', 'client certificate · expires 2026-12-31'],
      ['old-ci', 'basic auth · no expiry'],
      ['prod-onprem', 'static token · no expiry'],
    ]);
  });

  it('gives each row a remediation hint, specific where the platform allows', () => {
    const hint = (name: string): string | undefined =>
      groups.flatMap((g) => g.rows).find((r) => r.context.name === name)?.hint;
    expect(hint('minikube')).toBe(
      'Certificate expired: run minikube update-context or re-create the cluster',
    );
    expect(hint('prod-onprem')).toBe('Static token: replace with an exec plugin (oc login --web)');
    expect(hint('old-ci')).toBe('Basic auth: switch to a token issued by an identity provider');
    expect(hint('kind-local')).toBe('Local cluster certificate: acceptable for kind; rotate yearly');
    expect(hint('prod-eu-1')).toBe('Exec plugin: no action needed');
    expect(remediation(ctx('x', { kind: 'static-token', longLived: true }), 'longLived')).toBe(
      'Static token: replace with an exec plugin',
    );
    expect(remediation(ctx('x', cert(4000)), 'longLived')).toBe(
      'Long-lived certificate: issue a shorter-lived certificate',
    );
  });

  it('counts expired credentials for the activity-bar badge', () => {
    expect(expiredCount(groups)).toBe(1);
    expect(expiredCount(buildAudit([ctx('a', cert(100))], NOW))).toBe(0);
  });

  it('screen-reader labels follow the design', () => {
    expect(groups[0]?.accessibility).toBe('Expired credentials, 1 context');
    expect(groups[2]?.accessibility).toBe('Expiring within 30 days, none');
    expect(groups[0]?.rows[0]?.accessibility).toBe(
      'minikube, expired credential, client certificate, expired 4 March 2026. Expand for the suggested fix.'.replace(
        '4 March',
        '2 March',
      ),
    );
  });
});

describe('renderReport', () => {
  const contexts = [
    ctx(
      'minikube',
      { kind: 'client-cert', longLived: true, expiresAt: new Date('2026-03-02T00:00:00Z') },
      { provider: 'minikube' },
    ),
    ctx('prod-eu-1', { kind: 'cloud-iam', longLived: false, provider: 'aws' }, { provider: 'eks' }),
  ];
  const md = renderReport(contexts, 2, NOW);

  it('has the structure from the design: title, generated line, bold summary, table, closing note', () => {
    const lines = md.split('\n');
    expect(lines[0]).toBe('# Sextant credential audit');
    expect(lines[2]).toBe('Generated 2026-10-04 from 2 contexts in 2 kubeconfig files.');
    expect(lines[4]).toBe(
      '**Expired 1 · Long-lived 0 · Expiring within 30 days 0 · Short-lived or brokered 1**',
    );
    expect(lines[6]).toBe('| Context | Platform | Authentication | Expires | Risk | Suggestion |');
    expect(md).toContain(
      '| `minikube` | minikube | client certificate | 2026-03-02 (expired) | Expired | Certificate expired: run minikube update-context or re-create the cluster |',
    );
    expect(md).toContain('No tokens, keys, passwords or certificate contents are included.');
  });

  it('sorts by risk, expired first', () => {
    expect(md.indexOf('`minikube`')).toBeLessThan(md.indexOf('`prod-eu-1`'));
  });

  it('singular file count', () => {
    expect(renderReport(contexts, 1, NOW)).toContain('in 1 kubeconfig file.');
  });

  it('cannot be broken or injected through a hostile context name', () => {
    const evil = ctx('a|b`c\nd', { kind: 'static-token', longLived: true });
    const out = renderReport([evil], 1, NOW);
    const row = out.split('\n').find((l) => l.includes('a\\|b'));
    expect(row).toBeDefined();
    expect(row?.split(/(?<!\\)\|/).length).toBe(8); // 6 columns => the row still has exactly 6 cells + 2 edges
    expect(out).not.toContain('\nd'); // newline in a name cannot start a new line
    expect(codeSpan('plain')).toBe('`plain`');
    expect(codeSpan('has`tick')).toBe('`` has`tick ``');
  });

  it('never contains a secret: the canary fixture produces a clean report', async () => {
    const fleet = await loadFleet({ env: { KUBECONFIG: path.join(dir, 'canary', 'full.yaml') }, cwd: dir });
    const report = renderReport(fleet.contexts, fleet.files.length, NOW);
    expectNoCanary('audit report', report);
    expectNoCanary('audit groups', buildAudit(fleet.contexts, NOW));
    expect(report).toContain('`all-secrets`'); // and it really did include the contexts
  });
});
