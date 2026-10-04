/**
 * Every user-facing string, from the design system's copy deck (extension/ui-ux/sextant-design-system/copy-deck.md).
 * Kept in one place so wording is reviewed once and tests can pin it. Names are interpolated exactly as the
 * kubeconfig spells them.
 */
export const S = {
  fleet: {
    loading: 'Reading kubeconfig files…',
    emptyTitle: 'No kubeconfig found.',
    filtered: (query: string, n: number, total: number): string =>
      `Filtered: ${query} · ${String(n)} of ${String(total)}`,
    noMatch: (query: string): string => `No contexts match “${query}”.`,
    warningRow: (n: number): string =>
      n === 1 ? '1 kubeconfig file could not be read' : `${String(n)} kubeconfig files could not be read`,
    warningTooltip: (paths: readonly string[]): string => `Could not read:\n${paths.join('\n')}`,
    copied: (name: string): string => `Copied ${name}`,
    removed: (name: string): string => `${name} is no longer in your kubeconfig. Refresh to update the list.`,
  },
  audit: {
    message: 'Values are never shown.',
    groups: {
      expired: 'Expired',
      longLived: 'Long-lived credentials',
      expiring: 'Expiring within 30 days',
      shortLived: 'Short-lived or brokered',
    },
    none: 'none',
    copied: 'Audit report copied (no secret values included)',
    badgeTooltip: (n: number): string =>
      n === 1 ? '1 expired credential' : `${String(n)} expired credentials`,
  },
  status: {
    none: '$(circle-slash) No cluster',
    noneTooltip: 'No kubeconfig found. Click to open settings.',
    noneAccessibility: 'No Kubernetes cluster. Activate to open settings.',
    click: 'Click to open a terminal for a cluster',
  },
  terminal: {
    bannerTail: 'This terminal stays on this cluster even if your global context changes.',
    couldNotVerify: 'Could not confirm this terminal is bound. Run: echo $KUBECONFIG',
    toastNotBound: (name: string): string =>
      `Could not confirm the terminal for ${name} is bound. Run echo $KUBECONFIG in it to check.`,
    copyCommand: 'Copy Command',
    dismiss: 'Dismiss',
    startFailed: (name: string, reason: string): string => `Could not open a terminal for ${name}: ${reason}`,
  },
  confirm: {
    title: (name: string): string => `Open terminal on production? ${name} is critical`,
    placeholder: 'Commands here affect real users. Choose an option',
    cancel: 'Cancel',
    open: (name: string): string => `Open terminal on ${name}`,
    openAndSkip: "Open, and don't ask again for this cluster",
    editTags: 'Edit tags…',
  },
  toast: {
    externalSwitch: (name: string): string =>
      `Your kubectl context is now ${name} (production). It was changed outside Sextant.`,
    openBound: 'Open bound terminal',
    dismiss: 'Dismiss',
  },
  tag: {
    title: (name: string, step: number): string => `Tag cluster: ${name} (${String(step)}/3)`,
    envPlaceholder: 'Pick an environment',
    envDetails: {
      prod: 'Production. Usually critical',
      staging: 'Pre-production',
      dev: 'Development and local clusters',
    },
    custom: 'Custom…',
    customDetail: 'Type your own environment name',
    customPrompt: 'Environment name',
    customPlaceholder: 'e.g. qa, perf, sandbox',
    customInvalid: 'Use letters, numbers and hyphens.',
    carePlaceholder: 'How careful should Sextant be?',
    critical: 'Confirm before opening terminals; red status bar',
    normal: 'No confirmation',
    scopePlaceholder: 'Apply the same tags elsewhere? (optional)',
    only: (name: string): string => `Only ${name}`,
    allMatching: (glob: string): string => `All contexts matching ${glob}`,
    matchesDetail: (names: readonly string[]): string =>
      `${String(names.length)} context${names.length === 1 ? '' : 's'}: ${names.slice(0, 4).join(', ')}${names.length > 4 ? ', …' : ''}`,
    matchesNone: 'matches none today · also applies to future matches',
    customPattern: 'Custom pattern…',
    customPatternDetail: 'Glob on context name',
    done: (name: string, env: string, critical: boolean): string =>
      `Tagged ${name}: ${env}, ${critical ? 'critical' : 'normal'}`,
    saveFailed: 'Could not save tags. Check that your user settings file is writable.',
  },
  openTerminal: {
    placeholder: (n: number): string => `Open terminal for cluster (type to search ${String(n)} contexts)`,
    terminalOpen: 'terminal open',
  },
  filter: {
    prompt: 'Filter by name, platform, environment or server',
    live: (n: number, total: number): string =>
      `${String(n)} of ${String(total)} contexts match. Enter to apply, Escape to cancel.`,
    none: 'No contexts match. Enter keeps the filter; the view will be empty.',
  },
} as const;
