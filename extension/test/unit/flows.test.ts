import { describe, expect, it } from 'vitest';
import { openTerminalFlow, type OpenDeps } from '../../src/flows/openTerminal';
import { runTagFlow } from '../../src/flows/tag';
import type { KubeContext } from '../../src/kubeconfig';
import { UNTAGGED, type ContextView } from '../../src/model/fleetTree';
import { resolveTag, type TagRule } from '../../src/model/tags';
import type { InputOptions, InputResult, PickOptions, PickResult, Prompts } from '../../src/ui/prompts';

type Answer = { pick: number | string } | { input: string };

/** Scripted user: answers each prompt in order, and records what was shown. */
class Script implements Prompts {
  shown: { pick?: PickOptions<unknown>; input?: InputOptions }[] = [];
  constructor(private answers: Answer[]) {}
  pick<T>(o: PickOptions<T>): Promise<PickResult<T>> {
    this.shown.push({ pick: o });
    const a = this.answers.shift();
    if (a === undefined || !('pick' in a)) throw new Error(`unexpected pick: ${o.title}`);
    if (a.pick === 'cancel') return Promise.resolve({ kind: 'cancelled' });
    if (a.pick === 'back') return Promise.resolve({ kind: 'back' });
    const items = o.items.filter((i): i is Extract<typeof i, { kind: 'item' }> => i.kind === 'item');
    const chosen =
      a.pick === 'first'
        ? items[0]
        : typeof a.pick === 'number'
          ? items[a.pick]
          : items.find((i) => i.label === a.pick || i.label.includes(String(a.pick)));
    if (chosen === undefined)
      throw new Error(`no entry "${String(a.pick)}" in ${items.map((i) => i.label).join(' | ')}`);
    return Promise.resolve({ kind: 'picked', value: chosen.value });
  }
  input(o: InputOptions): Promise<InputResult> {
    this.shown.push({ input: o });
    const a = this.answers.shift();
    if (a === undefined || !('input' in a)) throw new Error(`unexpected input: ${o.title}`);
    if (a.input === 'cancel') return Promise.resolve({ kind: 'cancelled' });
    if (a.input === 'back') return Promise.resolve({ kind: 'back' });
    return Promise.resolve({ kind: 'entered', value: a.input });
  }
  get remaining(): number {
    return this.answers.length;
  }
}

const NAMES = ['prod-eu-1', 'prod-us-1', 'staging-eu-1', 'kind-local'];

describe('Tag Cluster… flow', () => {
  const run = (answers: Answer[], rules: TagRule[] = []) => {
    const ui = new Script(answers);
    return { ui, result: runTagFlow({ prompts: ui, contextName: 'prod-eu-1', allNames: NAMES, rules }) };
  };

  it('prod: step 2 offers critical FIRST (preselected), and "only this context" saves one exact rule', async () => {
    const { ui, result } = run([{ pick: 'prod' }, { pick: 'first' }, { pick: 'Only prod-eu-1' }]);
    const out = await result;
    expect(out?.tag).toEqual({ environment: 'prod', critical: true });
    expect(out?.rules).toEqual([{ match: 'prod-eu-1', environment: 'prod', critical: true }]);
    expect(ui.shown[1]?.pick?.items[0]).toMatchObject({ label: 'critical' });
    expect(ui.shown.map((s) => s.pick?.title)).toEqual([
      'Tag cluster: prod-eu-1 (1/3)',
      'Tag cluster: prod-eu-1 (2/3)',
      'Tag cluster: prod-eu-1 (3/3)',
    ]);
  });

  it('non-prod: step 2 offers normal first', async () => {
    const { ui, result } = run([{ pick: 'dev' }, { pick: 'first' }, { pick: 'Only' }]);
    const out = await result;
    expect(out?.tag).toEqual({ environment: 'dev', critical: false });
    expect(ui.shown[1]?.pick?.items[0]).toMatchObject({ label: 'normal' });
  });

  it('step 3 suggests a glob with its match count and applies it, pinning the context too', async () => {
    const { ui, result } = run([
      { pick: 'prod' },
      { pick: 'first' },
      { pick: 'All contexts matching prod-*' },
    ]);
    const out = await result;
    const glob = ui.shown[2]?.pick?.items.find(
      (i) => i.kind === 'item' && i.label === 'All contexts matching prod-*',
    );
    expect(glob).toMatchObject({ description: '2 contexts: prod-eu-1, prod-us-1' });
    expect(out?.rules.map((r) => r.match)).toEqual(['prod-eu-1', 'prod-*']);
    expect(resolveTag('prod-us-1', out?.rules ?? [])).toEqual({ environment: 'prod', critical: true });
  });

  it('a custom environment is validated, normalised and used', async () => {
    const { ui, result } = run([{ pick: 'Custom…' }, { input: 'QA-2' }, { pick: 'first' }, { pick: 'Only' }]);
    const out = await result;
    expect(out?.tag.environment).toBe('qa-2');
    const validate = ui.shown[1]?.input?.validate;
    expect(validate?.('has space')).toBe('Use letters, numbers and hyphens.');
    expect(validate?.('ok-1')).toBeUndefined();
  });

  it('a custom pattern shows live match counts, including "matches none today"', async () => {
    const { ui, result } = run([
      { pick: 'prod' },
      { pick: 'first' },
      { pick: 'Custom pattern…' },
      { input: 'zzz-*' },
    ]);
    const out = await result;
    expect(out?.rules.map((r) => r.match)).toEqual(['prod-eu-1', 'zzz-*']);
    const live = ui.shown[3]?.input?.live;
    expect(live?.('prod-*')?.message).toBe('2 contexts: prod-eu-1, prod-us-1');
    expect(live?.('zzz-*')?.message).toBe('matches none today · also applies to future matches');
    expect(live?.('   ')).toBeUndefined();
  });

  it('Back returns exactly one step, and the earlier answer can be changed', async () => {
    const { result } = run([
      { pick: 'dev' },
      { pick: 'back' },
      { pick: 'prod' },
      { pick: 'first' },
      { pick: 'Only' },
    ]);
    // 1: dev, 2: back -> 1: prod, 2: critical, 3: only
    const out = await result;
    expect(out?.tag).toEqual({ environment: 'prod', critical: true });
  });

  it('Back from step 3 returns to step 2, not step 1', async () => {
    const { ui, result } = run([
      { pick: 'prod' },
      { pick: 'first' },
      { pick: 'back' },
      { pick: 1 },
      { pick: 'Only' },
    ]);
    const out = await result;
    expect(out?.tag).toEqual({ environment: 'prod', critical: false }); // chose "normal" on the second visit
    expect(ui.shown.map((s) => s.pick?.step)).toEqual([1, 2, 3, 2, 3]);
  });

  it('Escape at any step cancels with nothing returned (so nothing can be saved)', async () => {
    for (const answers of [
      [{ pick: 'cancel' }],
      [{ pick: 'prod' }, { pick: 'cancel' }],
      [{ pick: 'prod' }, { pick: 'first' }, { pick: 'cancel' }],
    ] as Answer[][]) {
      const { ui, result } = run(answers);
      expect(await result).toBeUndefined();
      expect(ui.remaining).toBe(0);
    }
  });

  it('replaces an existing exact rule instead of stacking a second one', async () => {
    const { result } = run(
      [{ pick: 'staging' }, { pick: 'first' }, { pick: 'Only' }],
      [{ match: 'prod-eu-1', environment: 'prod', critical: true }],
    );
    const out = await result;
    expect(out?.rules).toEqual([{ match: 'prod-eu-1', environment: 'staging', critical: false }]);
  });
});

describe('Open terminal flow', () => {
  const ctx = (name: string): KubeContext => ({
    name,
    clusterName: name,
    userName: name,
    sourceFile: '/k',
    credential: { kind: 'exec-plugin', longLived: false },
    provider: 'eks',
  });
  const v = (name: string, critical: boolean): ContextView => ({
    context: ctx(name),
    environment: critical ? 'prod' : UNTAGGED,
    critical,
    current: false,
    terminal: false,
  });

  const setup = (answers: Answer[], views: ContextView[], skip: string[] = []) => {
    const ui = new Script(answers);
    const calls: string[] = [];
    let current = views;
    const deps: OpenDeps = {
      prompts: ui,
      views,
      skip,
      open: (n) => {
        calls.push(`open:${n}`);
        return Promise.resolve();
      },
      addSkip: (n) => {
        calls.push(`skip:${n}`);
        return Promise.resolve();
      },
      editTags: (n) => {
        calls.push(`edit:${n}`);
        current = current.map((x) =>
          x.context.name === n ? { ...x, critical: false, environment: 'dev' } : x,
        );
        return Promise.resolve();
      },
      reload: () => current,
    };
    return { ui, calls, deps };
  };

  it('a non-critical context opens with no confirmation', async () => {
    const { calls, deps, ui } = setup([], [v('dev-1', false)]);
    expect(await openTerminalFlow(deps, 'dev-1')).toBe('opened');
    expect(calls).toEqual(['open:dev-1']);
    expect(ui.shown).toHaveLength(0);
  });

  it('a critical context asks first, Cancel is the FIRST (focused) entry, and Enter on it opens nothing', async () => {
    const { calls, deps, ui } = setup([{ pick: 'first' }], [v('prod-eu-1', true)]);
    expect(await openTerminalFlow(deps, 'prod-eu-1')).toBe('cancelled');
    expect(calls).toEqual([]);
    const shown = ui.shown[0]?.pick;
    expect(shown?.title).toBe('Open terminal on production? prod-eu-1 is critical');
    expect(shown?.ignoreFocusOut).toBe(true);
    expect(shown?.items.map((i) => (i.kind === 'item' ? i.label : ''))).toEqual([
      'Cancel',
      'Open terminal on prod-eu-1',
      "Open, and don't ask again for this cluster",
      'Edit tags…',
    ]);
  });

  it('Escape on the confirmation opens nothing', async () => {
    const { calls, deps } = setup([{ pick: 'cancel' }], [v('p', true)]);
    expect(await openTerminalFlow(deps, 'p')).toBe('cancelled');
    expect(calls).toEqual([]);
  });

  it('choosing "Open terminal" opens; "don\'t ask again" records the skip first', async () => {
    let s = setup([{ pick: 'Open terminal on p' }], [v('p', true)]);
    expect(await openTerminalFlow(s.deps, 'p')).toBe('opened');
    expect(s.calls).toEqual(['open:p']);
    s = setup([{ pick: "don't ask again" }], [v('p', true)]);
    expect(await openTerminalFlow(s.deps, 'p')).toBe('opened');
    expect(s.calls).toEqual(['skip:p', 'open:p']);
  });

  it('a context in the skip list opens without asking', async () => {
    const { calls, deps, ui } = setup([], [v('p', true)], ['p']);
    expect(await openTerminalFlow(deps, 'p')).toBe('opened');
    expect(calls).toEqual(['open:p']);
    expect(ui.shown).toHaveLength(0);
  });

  it('"Edit tags…" re-evaluates: once the context is no longer critical it opens directly', async () => {
    const { calls, deps, ui } = setup([{ pick: 'Edit tags' }], [v('p', true)]);
    expect(await openTerminalFlow(deps, 'p')).toBe('opened');
    expect(calls).toEqual(['edit:p', 'open:p']);
    expect(ui.shown).toHaveLength(1);
  });

  it('with no context given it lists them (current pinned, grouped) and then continues into the confirmation', async () => {
    const { calls, deps, ui } = setup(
      [{ pick: 'prod-eu-1' }, { pick: 'Open terminal on prod-eu-1' }],
      [v('prod-eu-1', true), v('dev-1', false)],
    );
    expect(await openTerminalFlow(deps)).toBe('opened');
    expect(calls).toEqual(['open:prod-eu-1']);
    expect(ui.shown[0]?.pick?.placeholder).toBe('Open terminal for cluster (type to search 2 contexts)');
    expect(ui.shown[0]?.pick?.matchOnDescription).toBe(true);
  });

  it('Escape in the list cancels; a context that vanished cancels instead of throwing', async () => {
    expect(await openTerminalFlow(setup([{ pick: 'cancel' }], [v('a', false)]).deps)).toBe('cancelled');
    expect(await openTerminalFlow(setup([], [v('a', false)]).deps, 'gone')).toBe('cancelled');
  });
});
