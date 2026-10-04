import { S } from '../ui/strings';
import type { PickEntry, Prompts } from '../ui/prompts';
import {
  matchingNames,
  normalizeEnvironment,
  suggestGlobs,
  tagMatching,
  tagOnly,
  type ResolvedTag,
  type TagRule,
} from '../model/tags';

export interface TagOutcome {
  rules: TagRule[];
  tag: ResolvedTag;
}

type Scope = { kind: 'only' } | { kind: 'glob'; glob: string } | { kind: 'custom' };

/**
 * Tag Cluster…: three steps (environment, how careful, scope) with Back at every step and Escape cancelling with
 * nothing saved. It only computes the new rule list; saving is the caller's job, so a cancelled flow cannot write.
 */
export async function runTagFlow(deps: {
  prompts: Prompts;
  contextName: string;
  allNames: readonly string[];
  rules: readonly TagRule[];
}): Promise<TagOutcome | undefined> {
  const { prompts, contextName, allNames, rules } = deps;
  let step = 1;
  let environment = '';
  let critical = false;

  for (;;) {
    if (step === 1) {
      const r = await prompts.pick<string>({
        title: S.tag.title(contextName, 1),
        placeholder: S.tag.envPlaceholder,
        step: 1,
        totalSteps: 3,
        items: [
          {
            kind: 'item',
            label: 'prod',
            description: S.tag.envDetails.prod,
            iconId: 'warning',
            value: 'prod',
          },
          {
            kind: 'item',
            label: 'staging',
            description: S.tag.envDetails.staging,
            iconId: 'beaker',
            value: 'staging',
          },
          { kind: 'item', label: 'dev', description: S.tag.envDetails.dev, iconId: 'code', value: 'dev' },
          { kind: 'item', label: S.tag.custom, description: S.tag.customDetail, iconId: 'edit', value: '' },
        ],
      });
      if (r.kind !== 'picked') return undefined; // step 1 has no Back; Escape cancels
      if (r.value === '') {
        const typed = await prompts.input({
          title: S.tag.title(contextName, 1),
          prompt: S.tag.customPrompt,
          placeholder: S.tag.customPlaceholder,
          step: 1,
          totalSteps: 3,
          canGoBack: true,
          validate: (v) =>
            v === '' || normalizeEnvironment(v) !== undefined ? undefined : S.tag.customInvalid,
        });
        if (typed.kind === 'cancelled') return undefined;
        if (typed.kind === 'back') continue;
        const normalized = normalizeEnvironment(typed.value);
        if (normalized === undefined) continue; // empty input: ask again
        environment = normalized;
      } else {
        environment = r.value;
      }
      step = 2;
    } else if (step === 2) {
      const entries: PickEntry<boolean>[] = [
        { kind: 'item', label: 'critical', description: S.tag.critical, iconId: 'warning', value: true },
        { kind: 'item', label: 'normal', description: S.tag.normal, iconId: 'check', value: false },
      ];
      // "prod preselects critical": the first entry is the focused one, so put the likely answer first.
      const items = environment === 'prod' ? entries : [...entries].reverse();
      const r = await prompts.pick<boolean>({
        title: S.tag.title(contextName, 2),
        placeholder: S.tag.carePlaceholder,
        step: 2,
        totalSteps: 3,
        canGoBack: true,
        items,
      });
      if (r.kind === 'cancelled') return undefined;
      if (r.kind === 'back') {
        step = 1;
        continue;
      }
      critical = r.value;
      step = 3;
    } else {
      const globs = suggestGlobs(contextName, environment);
      const items: PickEntry<Scope>[] = [
        { kind: 'item', label: S.tag.only(contextName), iconId: 'tag', value: { kind: 'only' } },
        ...globs.map((glob): PickEntry<Scope> => ({
          kind: 'item',
          label: S.tag.allMatching(glob),
          description: describeMatches(glob, allNames),
          iconId: 'tag',
          value: { kind: 'glob', glob },
        })),
        {
          kind: 'item',
          label: S.tag.customPattern,
          description: S.tag.customPatternDetail,
          iconId: 'edit',
          value: { kind: 'custom' },
        },
      ];
      const r = await prompts.pick<Scope>({
        title: S.tag.title(contextName, 3),
        placeholder: S.tag.scopePlaceholder,
        step: 3,
        totalSteps: 3,
        canGoBack: true,
        items,
      });
      if (r.kind === 'cancelled') return undefined;
      if (r.kind === 'back') {
        step = 2;
        continue;
      }
      const tag: ResolvedTag = { environment, critical };
      if (r.value.kind === 'only') return { rules: tagOnly(rules, contextName, tag), tag };
      if (r.value.kind === 'glob') return { rules: tagMatching(rules, contextName, r.value.glob, tag), tag };

      const typed = await prompts.input({
        title: S.tag.title(contextName, 3),
        prompt: S.tag.customPatternDetail,
        placeholder: 'e.g. *-prod-*',
        step: 3,
        totalSteps: 3,
        canGoBack: true,
        validate: (v) => (v.trim() === '' && v !== '' ? 'Enter a pattern, for example *-prod-*.' : undefined),
        live: (v) =>
          v.trim() === '' ? undefined : { message: describeMatches(v.trim(), allNames), severity: 'info' },
      });
      if (typed.kind === 'cancelled') return undefined;
      if (typed.kind === 'back' || typed.value.trim() === '') continue; // back (or empty) returns to this step's list
      return { rules: tagMatching(rules, contextName, typed.value.trim(), tag), tag };
    }
  }
}

function describeMatches(glob: string, names: readonly string[]): string {
  const hits = matchingNames(glob, names);
  return hits.length === 0 ? S.tag.matchesNone : S.tag.matchesDetail(hits);
}
