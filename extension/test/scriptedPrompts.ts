import type { InputOptions, InputResult, PickOptions, PickResult, Prompts } from '../src/ui/prompts';

export type Answer = { pick: number | string } | { input: string };

/** A scripted stand-in for the QuickPick/InputBox layer: answers in order, and fails loudly on a surprise prompt. */
export class ScriptedPrompts implements Prompts {
  shown: PickOptions<unknown>[] = [];
  constructor(private answers: Answer[]) {}
  pick<T>(o: PickOptions<T>): Promise<PickResult<T>> {
    this.shown.push(o);
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
    const a = this.answers.shift();
    if (a === undefined || !('input' in a)) throw new Error(`unexpected input: ${o.title}`);
    return Promise.resolve({ kind: 'entered', value: a.input });
  }
  get remaining(): number {
    return this.answers.length;
  }
}
