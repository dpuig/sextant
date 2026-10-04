/**
 * Environment tags. Stored in the user's VS Code settings (`sextant.tags`, application scope), never in the kubeconfig
 * and never in workspace settings: a repository must not be able to silently downgrade prod to "normal".
 *
 * A tag is a rule: a context name or a glob (`*` and `?`), an environment, and whether it is critical. Resolution:
 * an exact name beats any glob; among globs the first in the list wins. Pure (no `vscode`), so it is unit-tested.
 */
export interface TagRule {
  /** A context name, or a glob with `*` and `?`. */
  match: string;
  /** Lower-case environment name: `prod`, `staging`, `dev` or a custom one. */
  environment: string;
  critical: boolean;
}

export interface ResolvedTag {
  environment: string;
  critical: boolean;
}

/** "Use letters, numbers and hyphens." Stored lower-case so `Prod` and `prod` are one environment. */
export const ENVIRONMENT_PATTERN = /^[a-z0-9][a-z0-9-]*$/;

export function normalizeEnvironment(input: string): string | undefined {
  const lower = input.trim().toLowerCase();
  return ENVIRONMENT_PATTERN.test(lower) ? lower : undefined;
}

export const isGlob = (match: string): boolean => /[*?]/.test(match);

/**
 * Glob match on a context name: `*` is any run of characters, `?` exactly one, everything else is literal.
 *
 * Deliberately NOT compiled to a regular expression: a pattern such as `*a*a*a*a*a*b` becomes a regex that backtracks
 * exponentially, and a pattern in someone's settings must never be able to hang the extension host. This is the classic
 * two-pointer algorithm: it remembers only the last `*`, so it is O(pattern x text) in the worst case.
 */
export function globMatch(pattern: string, text: string): boolean {
  let p = 0;
  let t = 0;
  let starP = -1; // position in the pattern just after the last `*`
  let starT = 0; // text position that `*` is currently allowed to have consumed up to
  while (t < text.length) {
    const pc = pattern[p];
    if (pc === '*') {
      starP = ++p;
      starT = t;
    } else if (p < pattern.length && (pc === '?' || pc === text[t])) {
      p++;
      t++;
    } else if (starP !== -1) {
      p = starP;
      t = ++starT; // let the last `*` swallow one more character and retry
    } else {
      return false;
    }
  }
  while (pattern[p] === '*') p++;
  return p === pattern.length;
}

export function matches(match: string, contextName: string): boolean {
  return isGlob(match) ? globMatch(match, contextName) : match === contextName;
}

/**
 * Reads the raw setting value defensively: a hand-edited settings file can contain anything, and a bad entry must be
 * skipped (and counted) rather than throw or hide the rest.
 */
export function parseTagRules(raw: unknown): { rules: TagRule[]; invalid: number } {
  if (!Array.isArray(raw)) return { rules: [], invalid: raw === undefined || raw === null ? 0 : 1 };
  const rules: TagRule[] = [];
  let invalid = 0;
  for (const item of raw as unknown[]) {
    const r = item as Partial<Record<keyof TagRule, unknown>> | null;
    const env = typeof r?.environment === 'string' ? normalizeEnvironment(r.environment) : undefined;
    if (
      r === null ||
      typeof r !== 'object' ||
      typeof r.match !== 'string' ||
      r.match === '' ||
      env === undefined
    ) {
      invalid++;
      continue;
    }
    rules.push({
      match: r.match,
      environment: env,
      critical: typeof r.critical === 'boolean' ? r.critical : env === 'prod',
    });
  }
  return { rules, invalid };
}

/** Exact names first (the most specific statement the user made), then globs in list order; first match wins. */
export function resolveTag(contextName: string, rules: readonly TagRule[]): ResolvedTag | undefined {
  const hit =
    rules.find((r) => !isGlob(r.match) && r.match === contextName) ??
    rules.find((r) => isGlob(r.match) && globMatch(r.match, contextName));
  return hit === undefined ? undefined : { environment: hit.environment, critical: hit.critical };
}

/** "Only this context": replaces any exact rule for the name and puts it first. */
export function tagOnly(rules: readonly TagRule[], name: string, tag: ResolvedTag): TagRule[] {
  return [{ match: name, ...tag }, ...rules.filter((r) => r.match !== name)];
}

/**
 * "All contexts matching a glob": adds (or replaces) the glob rule and also pins this context exactly, so the context
 * the user is tagging is guaranteed to get these tags even if an earlier glob would otherwise claim it.
 */
export function tagMatching(
  rules: readonly TagRule[],
  name: string,
  glob: string,
  tag: ResolvedTag,
): TagRule[] {
  const rest = rules.filter((r) => r.match !== name && r.match !== glob);
  return [{ match: name, ...tag }, ...rest, { match: glob, ...tag }];
}

export const matchingNames = (glob: string, names: readonly string[]): string[] =>
  names.filter((n) => matches(glob, n));

/**
 * Glob suggestions for the third tagging step, derived from the context name and the chosen environment:
 * `prod-eu-1` + prod -> `prod-*`; `eu-prod-1` -> `*-prod-*`; `eu-prod` -> `*-prod`.
 */
export function suggestGlobs(name: string, environment: string): string[] {
  const out: string[] = [];
  if (name.startsWith(`${environment}-`)) out.push(`${environment}-*`);
  if (name.includes(`-${environment}-`)) out.push(`*-${environment}-*`);
  if (name.endsWith(`-${environment}`)) out.push(`*-${environment}`);
  return [...new Set(out)];
}

/** Environment order in the tree: prod, staging, dev, then custom ones alphabetically; "Untagged" is placed by the caller. */
const BUILT_IN = ['prod', 'staging', 'dev'];
export function compareEnvironments(a: string, b: string): number {
  const ia = BUILT_IN.indexOf(a);
  const ib = BUILT_IN.indexOf(b);
  if (ia !== -1 || ib !== -1) return (ia === -1 ? 99 : ia) - (ib === -1 ? 99 : ib);
  return a.localeCompare(b);
}
