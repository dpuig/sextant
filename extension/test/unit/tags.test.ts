import { describe, expect, it } from 'vitest';
import {
  compareEnvironments,
  globMatch,
  matchingNames,
  normalizeEnvironment,
  parseTagRules,
  resolveTag,
  suggestGlobs,
  tagMatching,
  tagOnly,
  type TagRule,
} from '../../src/model/tags';

const r = (match: string, environment: string, critical = environment === 'prod'): TagRule => ({
  match,
  environment,
  critical,
});

describe('globMatch', () => {
  it('treats * as any run, ? as one character, and everything else literally', () => {
    expect(globMatch('prod-*', 'prod-eu-1')).toBe(true);
    expect(globMatch('prod-*', 'xprod-eu-1')).toBe(false);
    expect(globMatch('*-prod-*', 'eu-prod-1')).toBe(true);
    expect(globMatch('app-?', 'app-1')).toBe(true);
    expect(globMatch('app-?', 'app-12')).toBe(false);
    expect(globMatch('a.b', 'axb')).toBe(false); // dot is literal
    expect(globMatch('a(b)+', 'a(b)+')).toBe(true); // regex metacharacters are literal
    expect(globMatch('arn:aws:eks:*:cluster/prod-*', 'arn:aws:eks:eu-west-1:cluster/prod-eu')).toBe(true);
  });

  it('is anchored: a partial match is not a match', () => {
    expect(globMatch('prod', 'my-prod-1')).toBe(false);
    expect(globMatch('prod', 'prod')).toBe(true);
  });

  it('handles the edge cases of empty input and stars', () => {
    expect(globMatch('*', '')).toBe(true);
    expect(globMatch('', '')).toBe(true);
    expect(globMatch('', 'x')).toBe(false);
    expect(globMatch('**', 'anything')).toBe(true);
    expect(globMatch('a*', 'a')).toBe(true);
    expect(globMatch('*a', 'ba')).toBe(true);
    expect(globMatch('*a', 'ab')).toBe(false);
    expect(globMatch('a*b*c', 'axxbyyc')).toBe(true);
    expect(globMatch('a*b*c', 'axxbyy')).toBe(false);
    expect(globMatch('?', '')).toBe(false);
  });

  it('is linear-ish: a pattern that melts a backtracking regex finishes instantly', () => {
    const start = Date.now();
    expect(globMatch('*a*a*a*a*a*a*a*a*a*b', 'a'.repeat(5000))).toBe(false);
    expect(globMatch('*a*a*a*a*a*a*a*a*a*b', `${'a'.repeat(5000)}b`)).toBe(true);
    expect(Date.now() - start).toBeLessThan(500);
  });
});

describe('resolveTag', () => {
  it('an exact name beats any glob, wherever it sits in the list', () => {
    const rules = [r('prod-*', 'prod'), r('prod-lab', 'dev', false)];
    expect(resolveTag('prod-lab', rules)).toEqual({ environment: 'dev', critical: false });
    expect(resolveTag('prod-eu-1', rules)).toEqual({ environment: 'prod', critical: true });
  });

  it('among globs the first in the list wins', () => {
    const rules = [r('*-eu-*', 'staging'), r('prod-*', 'prod')];
    expect(resolveTag('prod-eu-1', rules)?.environment).toBe('staging');
  });

  it('returns undefined when nothing matches (untagged)', () => {
    expect(resolveTag('mystery', [r('prod-*', 'prod')])).toBeUndefined();
    expect(resolveTag('x', [])).toBeUndefined();
  });

  it('keeps criticality independent of the environment name', () => {
    expect(resolveTag('a', [r('a', 'prod', false)])).toEqual({ environment: 'prod', critical: false });
    expect(resolveTag('a', [r('a', 'dr', true)])).toEqual({ environment: 'dr', critical: true });
  });
});

describe('parseTagRules', () => {
  it('accepts well-formed rules, lower-cases environments, and defaults criticality from the environment', () => {
    const { rules, invalid } = parseTagRules([
      { match: 'a', environment: 'Prod' },
      { match: 'b', environment: 'dev' },
      { match: 'c', environment: 'qa', critical: true },
    ]);
    expect(invalid).toBe(0);
    expect(rules).toEqual([r('a', 'prod', true), r('b', 'dev', false), r('c', 'qa', true)]);
  });

  it('skips and counts malformed entries without throwing', () => {
    const { rules, invalid } = parseTagRules([
      null,
      7,
      'prod',
      { match: '', environment: 'prod' },
      { match: 'x' },
      { match: 'x', environment: 'has space' },
      { match: 'x', environment: 'ok' },
    ]);
    expect(rules).toHaveLength(1);
    expect(invalid).toBe(6);
  });

  it('treats a missing setting as empty and a wrongly-typed one as one invalid value', () => {
    expect(parseTagRules(undefined)).toEqual({ rules: [], invalid: 0 });
    expect(parseTagRules({ not: 'a list' })).toEqual({ rules: [], invalid: 1 });
  });
});

describe('editing rules', () => {
  it('tagOnly replaces the existing exact rule and puts it first', () => {
    const out = tagOnly([r('x', 'dev'), r('prod-*', 'prod')], 'x', {
      environment: 'staging',
      critical: false,
    });
    expect(out.map((t) => [t.match, t.environment])).toEqual([
      ['x', 'staging'],
      ['prod-*', 'prod'],
    ]);
  });

  it('tagMatching pins the context itself and adds the glob, replacing an identical glob', () => {
    const out = tagMatching([r('prod-*', 'dev', false)], 'prod-eu-1', 'prod-*', {
      environment: 'prod',
      critical: true,
    });
    expect(out.map((t) => t.match)).toEqual(['prod-eu-1', 'prod-*']);
    expect(out.every((t) => t.environment === 'prod')).toBe(true);
    // the tagged context is guaranteed its tags even if an earlier glob would claim it
    const earlier = tagMatching([r('*-eu-*', 'staging', false)], 'prod-eu-1', 'prod-*', {
      environment: 'prod',
      critical: true,
    });
    expect(resolveTag('prod-eu-1', earlier)?.environment).toBe('prod');
  });
});

describe('helpers', () => {
  it('validates environment names ("letters, numbers and hyphens")', () => {
    expect(normalizeEnvironment(' QA ')).toBe('qa');
    expect(normalizeEnvironment('perf-2')).toBe('perf-2');
    for (const bad of ['', ' ', 'has space', '-lead', 'under_score', 'emoji🙂', 'a/b'])
      expect(normalizeEnvironment(bad)).toBeUndefined();
  });

  it('suggests globs from the name and counts what they match', () => {
    expect(suggestGlobs('prod-eu-1', 'prod')).toEqual(['prod-*', '*-prod-*'].slice(0, 1));
    expect(suggestGlobs('eu-prod-1', 'prod')).toEqual(['*-prod-*']);
    expect(suggestGlobs('eu-prod', 'prod')).toEqual(['*-prod']);
    expect(suggestGlobs('kind-local', 'prod')).toEqual([]);
    expect(matchingNames('prod-*', ['prod-eu-1', 'prod-us-1', 'staging-1'])).toEqual([
      'prod-eu-1',
      'prod-us-1',
    ]);
  });

  it('orders environments prod, staging, dev, then custom alphabetically', () => {
    expect(['qa', 'dev', 'zeta', 'prod', 'staging', 'alpha'].sort(compareEnvironments)).toEqual([
      'prod',
      'staging',
      'dev',
      'alpha',
      'qa',
      'zeta',
    ]);
  });
});
