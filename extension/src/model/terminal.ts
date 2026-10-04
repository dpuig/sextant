import * as path from 'node:path';
import { S } from '../ui/strings';
import type { ResolvedTag } from './tags';

/**
 * Bound terminals. A terminal is pinned by putting a tiny file that holds ONLY `current-context: <name>` in FRONT of
 * the user's own KUBECONFIG list: kubectl and client-go take current-context from the first file that sets it, so
 * the terminal stays on its cluster when the global context changes, and no credential is copied anywhere. The file is
 * read-only so `kubectl config use-context` inside the terminal is refused instead of silently moving it. Verified on
 * two kind clusters with kubectl and helm (extension/test/spike/bound-terminal.sh).
 */

/** YAML for the pin file. JSON string syntax is valid YAML and escapes anything odd in a name. */
export function pinFileContent(contextName: string): string {
  return `apiVersion: v1\nkind: Config\ncurrent-context: ${JSON.stringify(contextName)}\n`;
}

/** The terminal's KUBECONFIG: the pin first, then the user's own files in their original order. */
export function boundKubeconfig(
  pinPath: string,
  userPaths: readonly string[],
  delimiter: string = path.delimiter,
): string {
  return [pinPath, ...userPaths].join(delimiter);
}

// ---- tab, colour and banner -----------------------------------------------------------------------------------

export interface TabStyle {
  name: string;
  iconId: string;
  /** Terminal ANSI colour id, or undefined for none. */
  colorId: string | undefined;
}

export function tabStyle(contextName: string, tag: ResolvedTag | undefined): TabStyle {
  if (tag === undefined) return { name: contextName, iconId: 'terminal', colorId: undefined };
  const env = tag.environment;
  const colorId =
    tag.critical || env === 'prod'
      ? 'terminal.ansiRed'
      : env === 'staging'
        ? 'terminal.ansiYellow'
        : env === 'dev'
          ? 'terminal.ansiGreen'
          : undefined;
  return {
    name: tag.critical ? `${contextName} · ${env.toUpperCase()}` : `${contextName} · ${env}`,
    iconId: tag.critical ? 'warning' : 'terminal',
    colorId,
  };
}

const CHIP_RED = '\x1b[1;97;41m'; // bold bright white on red
const CHIP_YELLOW = '\x1b[1;30;43m'; // bold black on yellow
const BOLD = '\x1b[1m';
const RESET = '\x1b[0m';

/** First line(s) written into the terminal. Chips (background + contrasting text) stay readable on every theme. */
export function banner(contextName: string, tag: ResolvedTag | undefined): string {
  if (tag?.critical === true) {
    return `${CHIP_RED} ${tag.environment.toUpperCase()} ${RESET} Bound to ${contextName} (${BOLD}${tag.environment}, critical${RESET}). ${S.terminal.bannerTail}\r\n`;
  }
  const where = tag === undefined ? '' : ` (${tag.environment})`;
  return `Bound to ${contextName}${where}. ${S.terminal.bannerTail}\r\n`;
}

export function unverifiableBanner(): string {
  return `${CHIP_YELLOW} WARNING ${RESET} ${S.terminal.couldNotVerify}\r\n`;
}

// ---- shells ------------------------------------------------------------------------------------------------------

export type ShellFamily = 'posix' | 'fish' | 'pwsh' | 'cmd' | 'sh' | 'unknown';

export function shellFamily(shellPath: string): ShellFamily {
  const base = (shellPath.split(/[\\/]/).pop() ?? '').toLowerCase().replace(/\.exe$/, '');
  if (base === 'bash' || base === 'zsh') return 'posix';
  if (base === 'fish') return 'fish';
  if (base === 'pwsh' || base === 'powershell') return 'pwsh';
  if (base === 'cmd') return 'cmd';
  if (base === 'sh' || base === 'dash' || base === 'ksh' || base === 'ash') return 'sh';
  return 'unknown';
}

/** VS Code's shell integration covers bash, zsh, fish and PowerShell; for the rest binding cannot be verified. */
export const canVerify = (family: ShellFamily): boolean =>
  family === 'posix' || family === 'fish' || family === 'pwsh';

/** Prints KUBECONFIG in the given shell. Visible in the terminal by design: it is the proof. */
export function verifyCommand(family: ShellFamily): string {
  return family === 'pwsh' ? '$env:KUBECONFIG' : family === 'cmd' ? 'echo %KUBECONFIG%' : 'echo $KUBECONFIG';
}

const sq = (s: string): string => `'${s.replace(/'/g, `'\\''`)}'`;
const sqPs = (s: string): string => `'${s.replace(/'/g, "''")}'`;

/** Sets KUBECONFIG again after startup, for when a shell startup file overwrote the injected value. */
export function reassertCommand(family: ShellFamily, value: string): string {
  switch (family) {
    case 'fish':
      return `set -gx KUBECONFIG ${sq(value)}`;
    case 'pwsh':
      return `$env:KUBECONFIG = ${sqPs(value)}`;
    case 'cmd':
      return `set "KUBECONFIG=${value}"`;
    default:
      return `export KUBECONFIG=${sq(value)}`;
  }
}

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;

/**
 * Is the terminal bound? True when the output of the verify command contains a line whose first KUBECONFIG entry is
 * the pin file. First, not merely present: a startup file that prepends its own path before ours defeats the pin.
 */
export function isBound(output: string, pinPath: string, delimiter: string = path.delimiter): boolean {
  return output
    .replace(ANSI, '')
    .split(/\r?\n/)
    .map((l) => l.trim())
    .some((l) => l === pinPath || l.startsWith(pinPath + delimiter));
}
