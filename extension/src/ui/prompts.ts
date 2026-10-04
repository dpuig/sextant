/**
 * The only way flows ask the user anything. An interface, so the multi-step flows (tagging, confirmation, filter)
 * can be driven by a scripted fake in tests; src/ui/vscodePrompts.ts is the real implementation (QuickPick, InputBox).
 */
export type PickEntry<T> =
  | { kind: 'separator'; label: string }
  | { kind: 'item'; label: string; value: T; description?: string; detail?: string; iconId?: string };

export interface PickOptions<T> {
  title: string;
  placeholder: string;
  items: PickEntry<T>[];
  step?: number;
  totalSteps?: number;
  canGoBack?: boolean;
  /** Keep the picker open when focus moves away. Used for confirmations: a stray click must not choose for the user. */
  ignoreFocusOut?: boolean;
  matchOnDescription?: boolean;
}

export type PickResult<T> = { kind: 'picked'; value: T } | { kind: 'back' } | { kind: 'cancelled' };

export interface InputOptions {
  title: string;
  prompt: string;
  placeholder?: string;
  value?: string;
  step?: number;
  totalSteps?: number;
  canGoBack?: boolean;
  /** Blocking validation on every keystroke: a message means "not acceptable yet". */
  validate?: (value: string) => string | undefined;
  /** Non-blocking feedback while typing (a live count, or "nothing matches"). */
  live?: (value: string) => { message: string; severity: 'info' | 'warning' } | undefined;
}

export type InputResult = { kind: 'entered'; value: string } | { kind: 'back' } | { kind: 'cancelled' };

export interface Prompts {
  pick<T>(options: PickOptions<T>): Promise<PickResult<T>>;
  input(options: InputOptions): Promise<InputResult>;
}
