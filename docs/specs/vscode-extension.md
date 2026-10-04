# Spec: Sextant for VS Code

Status: **DRAFT for review.** No extension code until approved. Implementation plan and tasks: [vscode-extension-plan.md](vscode-extension-plan.md).
Decided 2026-10-03: local-first then connect; VS Code Marketplace + Open VSX; v1 user is the
engineer who works across many clusters. Defines milestone E1 of the
[implementation plan](../../Implementation%20Plan%20Multi-Cluster%20Control%20Plane.md). The management-plane work
already built (Foundations, originally "Phase 0") is kept.

## Objective

Make working across many Kubernetes clusters safe and fast **inside the editor where engineers
already are**, with no server required to start, and a path to brokered, short-lived, audited
access when a team connects a Sextant management plane.

**User:** an engineer with 5-100 contexts across providers, a kubeconfig that grew by accident,
and a standing fear of running a command against production.

**Headline journey:** install the extension, immediately see every cluster from the existing
kubeconfig grouped by environment, open a terminal bound to the right one, and never mistake
production for staging. Later: sign in once and stop handling kubeconfigs at all.

**Why this order:** the local-first slice delivers value in minutes with no deployment, which is
the adoption funnel; the management plane then becomes the upgrade ("connect to your team's
Sextant") rather than a prerequisite.

## Milestones (extension-led)

| | Milestone | Needs from the backend |
|---|---|---|
| E1 | **Local-first MVP**: discover kubeconfigs, fleet tree, environment tagging, context safety, bound terminals, credential audit | Nothing |
| E2 | **Connect**: sign in to a management plane; brokered access with short-lived credentials, no secret on disk | Identity broker (SSO), `kx token` credential helper, OpenAPI client |
| E3 | **Fleet from the server + JIT**: server-side inventory and labels, request time-boxed elevation, audit links | Inventory, JIT elevation, watch/stream for status |
| E4 | **Guardrails**: confirmation or denial of destructive verbs on critical environments, session recording view | Proxy guardrails |
| E5 | **Operational assistant** via the platform's MCP server, in the editor's own assistant surfaces | Model gateway, MCP server (read-only first) |
| E6 | **Assisted decisions** with earned autonomy (shadow mode first) | Decision engine, policy validation |

Cluster lifecycle and microVMs are **frozen** until E3 has users. Everything below specifies **E1**
in depth; E2-E6 are outlined only and each gets its own spec at the previous milestone's gate.

## Assumptions (correct me now or I proceed)

1. The extension host is Node (TypeScript); the extension is **not** a webview-heavy app: native
   TreeView, StatusBar, Terminal and QuickPick APIs first.
2. kubeconfigs are read from `KUBECONFIG`, `~/.kube/config` and a user-configurable list of paths;
   merge semantics follow kubectl's.
3. Environment and criticality are **user tags** stored in extension settings (workspace or user),
   never written into the kubeconfig. (Later they come from the server as labels.)
4. For E2 the credential helper is the existing Go `kx` binary, shipped **inside** platform-specific
   VSIXes (no download-on-first-run), because kubeconfig exec plugins need an executable and
   marketplace policy discourages fetching binaries.
5. Telemetry is off unless the user opts in, and never includes cluster names, server URLs or any
   kubeconfig content.
6. The extension is open source (Apache-2.0) like the rest of the repo; `package.json` declares `"license": "Apache-2.0"`
   and the VSIX includes `LICENSE`. It is published under a publisher account we own (name TBD, see Open Questions).

## Tech Stack

- TypeScript (strict), VS Code engine `^1.90` (pin when the first task lands), Node as provided by the host.
- Bundler: esbuild. Tests: Vitest (unit), `@vscode/test-electron` (integration).
- Packaging: `@vscode/vsce` (Marketplace), `ovsx` (Open VSX), platform-specific targets for the `kx` binary from E2.
- Runtime dependencies: as few as possible; a YAML parser for kubeconfig is the expected one.
  Adding any other needs approval (see Boundaries).
- Lives in this monorepo at `extension/`; the generated OpenAPI client (E2) lives in `api/client-ts/`
  and is shared with the future `web/` UI.

## Commands

(Targets created by the first task; all from `extension/`.)

```
Install:       npm ci
Build:         npm run build            # esbuild bundle -> dist/
Watch:         npm run watch
Unit tests:    npm test                 # vitest run
Integration:   npm run test:integration # @vscode/test-electron, headless
Lint/format:   npm run lint && npm run typecheck
Package:       npm run package          # vsce package (per target from E2)
Publish:       npm run publish:vsce && npm run publish:ovsx   # CI only, on tag
Run locally:   code --extensionDevelopmentPath=$(pwd)          # or F5
```

## Project Structure

```
extension/
  src/extension.ts          activation, wiring (lazy: onView / onCommand)
  src/kubeconfig/           discovery, parsing, merge, credential classification (pure, no vscode import)
  src/model/                Cluster/Context model, tags, environment rules (pure)
  src/views/                TreeView providers, status bar, QuickPick flows
  src/terminal/             context-bound terminals
  src/audit/                credential audit report
  src/connect/              (E2) sign-in, `kx` integration, management-plane client
  test/unit/  test/integration/  test/fixtures/   real kubeconfig samples, incl. canary secrets
  package.json              contributes: views, commands, configuration, menus
  README.md  CHANGELOG.md   marketplace listing text
api/client-ts/              (E2) generated from the management plane's OpenAPI
```

Rule: everything under `kubeconfig/` and `model/` is pure TypeScript with no `vscode` import, so it
is unit-testable at speed and reusable (CLI, future web UI).

## E1 features

1. **Discovery.** Find kubeconfigs, list every context with cluster, user, namespace, server host.
2. **Fleet tree.** Activity-bar view grouping contexts by environment tag, then provider (inferred
   from server URL and exec plugin, best-effort), with search/filter. Current context marked.
3. **Environment tagging.** Tag a context `dev | staging | prod | custom` and `criticality`,
   via QuickPick or by rule (glob on context name, e.g. `*-prod-*` -> prod). Stored in settings.
4. **Context safety.** Status bar shows the active context; critical environments render in an
   unmistakable colour with a warning icon. The extension never changes the global `current-context` in E1
   (it is read-only on the kubeconfig), so safety works two ways: opening a **bound terminal** on a critical
   context asks for confirmation (configurable), and if the global `current-context` is changed *outside* the
   extension (for example `kubectl config use-context`) a notification warns when the new context is critical.
5. **Bound terminals.** "Open terminal for this cluster" starts a terminal with `KUBECONFIG`
   pointing at a generated, minimal kubeconfig for just that context, a coloured tab and prompt
   hint. This isolates the session from later global context changes (the classic wrong-cluster bug).
6. **Credential audit** (the local analogue of `kx scan`). For each context: auth method (static
   token, client cert, exec plugin, OIDC, cloud IAM), certificate expiry, and whether the secret is
   long-lived. Output is a report view; **no secret value is ever displayed or logged**, only
   classification and expiry.

## Code Style

TypeScript strict, no `any`, ESLint + Prettier, small pure functions at the edges of `vscode`:

```ts
// src/kubeconfig/classify.ts : pure, no vscode import, no secret values in the result
export type AuthKind = 'static-token' | 'client-cert' | 'exec-plugin' | 'oidc' | 'cloud-iam' | 'none';

export interface CredentialSummary {
  kind: AuthKind;
  longLived: boolean;
  expiresAt?: Date;           // from the certificate, never from a token body
}

export function classify(user: KubeconfigUser, now: Date): CredentialSummary {
  if (user.exec) return { kind: 'exec-plugin', longLived: false };
  if (user.token) return { kind: 'static-token', longLived: true };
  /* ... */
}
```

Conventions: `camelCase` functions, `PascalCase` types, one concern per file, errors as values at
module boundaries, no side effects at import time, every public function documented with *why*.

## Testing Strategy

| Level | Tool | Covers |
|---|---|---|
| Unit | Vitest | kubeconfig discovery/merge precedence, exec-plugin and OIDC shapes, classification, env rules, expiry maths. Real-world fixtures (EKS, GKE, AKS, kind, k3s, RKE2, OpenShift) |
| Security | Vitest | **Canary test**: fixtures contain unique fake secrets; assert none appears in any log line, error message, tree label, tooltip, report or telemetry payload |
| Integration | `@vscode/test-electron` | activation is lazy and fast, tree renders, status bar reacts to context change, terminal gets the bound kubeconfig |
| E2E | kind (reuse `make e2e` rig) | bound terminal really cannot touch another cluster; audit against a live kubeconfig |
| Manual | checklist in `docs/testing/extension.md` | Windows path/exec-plugin quirks, Remote/WSL/Dev Containers, Cursor/VSCodium via Open VSX |

Coverage target: >= 90% on `kubeconfig/` and `model/`; the glue in `views/` is covered by integration tests.

## Boundaries

- **Always:** keep kubeconfig parsing pure and tested against real fixtures; treat every kubeconfig
  as containing secrets; show classification, never values; confirm before opening a terminal on a critical
  context; run the canary test in CI; lazy-activate.
- **Ask first:** adding a runtime dependency; enabling any telemetry or network call at all in E1;
  changing `contributes` (views/commands/config) after first publish; publishing a release.
- **Never:** upload, transmit or persist kubeconfig contents or tokens; run `kubectl` with write
  verbs on the user's behalf; modify the user's kubeconfig in E1 (read-only); download executables
  at runtime; store credentials anywhere but VS Code `SecretStorage` or the OS keychain; log values
  from `users[].user`.

## Success Criteria (E1)

- [ ] **Time to value:** from install to a populated fleet tree on a machine with an existing
      kubeconfig in under 60 s, with zero configuration.
- [ ] **Scale:** 200 contexts render the tree in under 500 ms; the audit of 50 contexts completes in under 2 s.
- [ ] **Safety:** a context tagged critical is visually unmistakable in status bar, tree and terminal;
      opening a terminal on it requires confirmation, and an external switch to it raises a warning; a bound terminal's `kubectl config current-context`
      cannot change when the global context changes (e2e on kind).
- [ ] **Secrets:** the canary test passes: no fixture secret appears in any output surface.
- [ ] **Performance:** activation under 200 ms; bundle under 2 MB (E1, no binaries).
- [ ] **Reach:** installs and passes the integration suite on macOS, Linux and Windows; published to
      both the VS Code Marketplace and Open VSX; works in Remote-SSH, WSL and Dev Containers.
- [ ] **Quality bar:** typecheck, lint, unit, integration green in CI; every security-relevant
      behaviour is mutation-checked (the guarding test fails when the guard is removed).

## Relationship to the existing backend

| Already built (Foundations) | Role from here |
|---|---|
| Agent, tunnel, enrollment, proxy, charts, e2e | The E2 "connect" target; no changes needed for E1 |
| Tenant-scoped API (`sextant.andean.io/v1alpha1`) | Becomes the extension's server-side data source in E3 |
| Dev bearer token | Replaced by SSO in E2; never used by the extension |

**Foundations items to defer** (not needed for E1, still required before exposing a multi-tenant service):
generated isolation suite, threat model, cosign/SBOM, OpenTelemetry, Vault signer, revocation store.
**Foundations items to pull forward** because E2/E3 depend on them: OpenAPI + typed client, a watch/stream
endpoint for `Cluster` status, and the identity broker.

## Open Questions

1. **Name and publisher.** "Sextant" is the product; is the extension "Sextant for Kubernetes"? Needs a
   Marketplace publisher ID and an Open VSX namespace, and a trademark check.
2. **Offering.** E1 is free. Because the code is open, the paid offering must be the **hosted management plane**
   (and support), not a feature gate inside the extension: confirm that is the model, and what the free hosted
   allowance, if any, is.
3. ~~Marketplace licence terms~~ Resolved by the Apache-2.0 decision (2026-10-03): open-source extensions are standard
   on both registries. Still to do: a trademark policy for the name so forks cannot publish as "Sextant".
4. **Windows exec plugins**: acceptable to require `kx.exe` bundled per platform from E2?
5. **Staffing:** solo plus agents as before? E1 is roughly 3-4 weeks of focused work on that assumption.
6. **First release channel:** private VSIX to design partners first, or straight to the Marketplace as pre-release?
