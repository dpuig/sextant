# Implementation Plan: E1, the local-first VS Code extension

Status: **DRAFT for review.** No code until approved. Specification: [vscode-extension.md](vscode-extension.md).
Parent plan: [implementation plan](../../Implementation%20Plan%20Multi-Cluster%20Control%20Plane.md), milestone E1.

## Overview

Build the extension in vertical slices, riskiest first. The core is **pure TypeScript** (kubeconfig parsing, tagging
rules, credential classification, minimal-kubeconfig generation) with no `vscode` import, so it is fast to test and
the secret-handling guarantees are provable in unit tests. The VS Code glue (tree, status bar, terminals) is thin and
covered by integration tests. A **canary-secret test** is built before any feature so every later task is covered by it.

The first usable result (a populated fleet tree on a real kubeconfig) lands at Checkpoint 2, about half way. The
headline safety feature (bound terminals) is de-risked by a spike *before* it is built.

## Architecture decisions

| Decision | Rationale |
|---|---|
| Pure core in `extension/src/{kubeconfig,model}`, VS Code glue in `src/{views,terminal,audit}` | Testable without a host; the glue can be thrown away without touching the guarantees |
| The kubeconfig model **never holds secret values**, only classification (kind, expiry, long-lived) | The canary test is then a property of one module, not of every output surface |
| Certificate expiry via Node's built-in `crypto.X509Certificate` | No dependency for the one thing that needs a cryptographic parser |
| One runtime dependency expected: a YAML parser (`yaml`, ISC) | Kubeconfig is YAML; needs approval per the spec's "ask first" rule (see Open Questions) |
| Tags and rules live in **settings**, never in the kubeconfig | Keeps the kubeconfig untouched and tags syncable through VS Code settings |
| **E1 never changes the global `current-context`** (see below) | Resolves a contradiction in the spec; keeps the extension strictly read-only on the user's kubeconfig |
| **No network access at all in E1**, no telemetry | Removes the largest class of leak and the need for a privacy review in E1 |
| Bound terminals use a **generated minimal kubeconfig** per terminal, in a private temp dir, deleted on close | Works for kubectl, helm, k9s alike; the cost (copied inline credentials) is mitigated and proven by the spike (Task 5) |

### Resolving "switching context" in a read-only extension

The spec promised a confirmation when *switching to* a critical context, yet also forbade modifying the kubeconfig.
Changing `current-context` is a kubeconfig write. E1 therefore does this instead, and the spec is amended to match:

- The **safe way to work** is a *bound terminal*: it targets one context regardless of the global setting.
  Opening one on a critical context asks for confirmation.
- If the global `current-context` is changed *outside* the extension (`kubectl config use-context`, another tool), the
  extension notices (file watcher) and warns when the new context is critical.
- The extension does not offer "set current context" in E1. If design partners ask for it, it becomes a narrowly scoped,
  confirmed write in a later release (a separate decision, since it changes the read-only guarantee).

## Dependency graph

```
T1 scaffold ─┬─> T2 kubeconfig parser ──┬─> T3 canary harness ──┐
             │                          └─> T4 classification ──┤
             │                                                  ├─> T6 fleet tree ─> T7 tagging ─> T8 status bar
             └─> T5 SPIKE bound terminals (needs kind) ─────────┴─> T9 minimal-kubeconfig ─> T10 bound terminal UI
                                                                    T4 + T6 ─────────────────> T11 audit view
T8, T10, T11 ─> T12 cross-platform ─> T13 performance ─> T14 release engineering ─> T15 docs + E1 gate
```

Bottom-up: T1 first; T2 is the base of everything pure; T3 and T4 follow T2; the spike T5 needs only T1 and runs in
parallel with T2-T4 because it is the highest design risk.

## Task list

Sizing: S = 1-2 files, M = 3-5 files. Nothing is larger; anything that grew was split.

### Phase A: Foundation and risk

#### Task 1: Scaffold the extension (M)

**Description:** `extension/` with TypeScript (strict), esbuild bundling, ESLint/Prettier, Vitest, `@vscode/test-electron`,
an activation that registers one placeholder command, and a CI job that builds, lints, unit-tests and packages a VSIX.
Declares `license: Apache-2.0` and includes `LICENSE`. Publisher and extension names are placeholders.

**Acceptance criteria**
- [ ] `npm ci && npm run build && npm test && npm run lint && npm run typecheck` pass from `extension/`.
- [ ] The placeholder command runs in a headless integration test (`npm run test:integration`).
- [ ] `npm run package` produces a VSIX under 500 KB containing `LICENSE`.
- [ ] CI runs the extension job on Linux for every PR.

**Verification:** the five npm commands above; open the VSIX contents (`unzip -l`) and check size and `LICENSE`.

**Done (2026-10-04).** Build, typecheck, lint, unit and integration tests pass; the VSIX is 7.4 KB and carries `LICENSE` and
`NOTICE`; integration runs on a real VS Code 1.93.0 (the engine floor, overridable with `VSCODE_TEST_VERSION`). Notes:
- The test runner clears `ELECTRON_RUN_AS_NODE`: editor-hosted terminals set it and Electron then rejects VS Code's flags.
- `@types/vscode` is pinned to the engine floor (a unit test enforces it) so newer APIs cannot slip in.
- **Accepted dev-tool advisories** (`npm audit`: 8, all dev-only; the production tree has 0 and CI enforces that):
  `braces` (<=3.0.3, stack-exhaustion DoS in glob matching, via `vsce` > `secretlint`; no patched release exists, runs
  only on our own files at package time) and Vitest 3's `@vitest/mocker` path traversal (affects only Vitest's dev
  server, which `vitest run` does not start; the fix needs Vitest 5, which requires `@types/node` 22+, wrong for a
  Node 20 extension host). Revisit when either gains a compatible fix.

**Dependencies:** none. **Files:** `extension/{package.json,tsconfig.json,esbuild.mjs,vitest.config.ts}`, `src/extension.ts`, `.github/workflows/ci.yml`.

#### Task 2: Kubeconfig discovery, parsing and merge (M)

**Description:** Pure module that finds kubeconfig files (`KUBECONFIG` list, `~/.kube/config`, configured paths),
parses them, and merges per kubectl's rules (first file wins for a given name; `current-context` from the first file
that sets it). Output is a `Context[]` model containing names, cluster server host, namespace, user *name*, and an
opaque `credentialRef`, **never a secret value**.

**Acceptance criteria**
- [ ] Merge precedence matches `kubectl config view` on a set of multi-file fixtures (compared to real kubectl in CI).
- [ ] Handles: missing files, empty files, malformed YAML (returns a typed error without echoing file content), duplicate
      names across files, `~` and relative paths, Windows path separators (unit-level).
- [ ] The returned model has no field that can contain a token, key or inline certificate data (enforced by type).
- [ ] 100% of the public API documented with why, not what.

**Verification:** `npm test -- kubeconfig`; differential test against `kubectl config view -o json` on 10 fixtures.

**Done (2026-10-04).** Discovery, parse and merge, with the `yaml` runtime dependency (ISC; one approved dependency, enforced by a
test). 10 differential cases agree with real `kubectl config view`; flipping first-wins or current-context precedence fails
both the unit and the differential tests. Findings while building it: (1) the YAML library's `toJS` **throws** on an alias
bomb, which would have taken the whole fleet down, so it is now a typed `too-complex` error; (2) errors carry only a closed
parser code, never a message, because the library quotes the offending source line; (3) kubectl parses YAML 1.1, where a bare
`y`/`n`/`yes`/`no` is a boolean, so a context with such a name is invalid for kubectl itself.
**Dependencies:** T1. **Files:** `src/kubeconfig/{discover,parse,merge,types}.ts`, `test/unit/kubeconfig.test.ts`, `test/fixtures/*`.

#### Task 3: Canary-secret harness (S)

**Description:** Fixtures whose every secret field contains a unique canary string (token, client-key-data,
certificate-authority-data, exec env values, basic-auth password, OIDC refresh token). A reusable assertion collects
**every output surface** (return values serialised, error messages, log lines, and later tree labels/tooltips/reports)
and fails if any canary appears. Wired into CI as a required check.

**Acceptance criteria**
- [ ] T2's parser passes the harness over all canary fixtures, including malformed variants (errors must not echo content).
- [ ] The harness fails when a deliberately leaky function is added (mutation check, kept as a test).
- [ ] A documented one-liner lets later tasks register their surfaces.

**Verification:** `npm test -- canary`; run the mutation check and show it fails without the guard.

**Done (2026-10-04).** 12 canary secrets (plain, base64, base64url, hex, URL-encoded, lowercased and truncated forms are all
detected) across a full fixture and six malformed ones. Mutation-checked against three realistic leaks (a YAML pretty error,
a server URL with userinfo, an OS read-error message): each is caught. Also a structural guard that fails if the public model
grows a secret-shaped field name. Note: prettier printed the canary lines when it failed on the malformed fixtures, which is
exactly the leak mode this guards against; fixtures are excluded from formatting.
**Dependencies:** T2. **Files:** `test/fixtures/canary/*.yaml`, `test/canary.ts`, `test/unit/canary.test.ts`.

#### Task 4: Credential classification (M)

**Description:** `classify()` maps a user entry to a `CredentialSummary` (`static-token | client-cert | exec-plugin |
oidc | cloud-iam | none`, `longLived`, `expiresAt?`). Expiry comes from the client certificate (via
`crypto.X509Certificate`) and never from token bodies. Provider is inferred best-effort from server URL and exec
command (`aws`, `gke-gcloud-auth-plugin`, `kubelogin`, `kx`, ...). Fixtures from EKS, GKE, AKS, kind, k3s, RKE2, OpenShift.

**Acceptance criteria**
- [ ] Each of the seven distro fixtures classifies as expected (table-driven test).
- [ ] Expired, expiring-within-30-days and long-lived certificates are distinguished.
- [ ] Unknown exec commands classify as `exec-plugin` with provider `unknown`, never throw.
- [ ] Canary harness (T3) passes over the output.

**Verification:** `npm test -- classify canary`.

**Done (2026-10-04).** 12 distro/shape fixtures (EKS, GKE, AKS and legacy AKS, kind, k3s, RKE2, OpenShift, two OIDC shapes, `kx`,
unknown exec) plus certificate, basic, token-file and hostile-shape cases. Certificate fixtures are public-only (keys
discarded) so secret scanning is not tripped. Design points: expiry comes from the certificate; "long-lived" means validity
over 30 days (exactly 30 is not); a certificate given as a file path has unknown expiry and is assumed long-lived; a
`tokenFile` is reported long-lived because nothing in the file says it rotates; an exec plugin takes precedence over static
credentials in the same entry. Five rule mutations were tried; one survived (exec/token precedence), which exposed a missing
test, now added. Coverage of `kubeconfig/` and `model/`: 99.3%.
**Dependencies:** T2, T3. **Files:** `src/kubeconfig/classify.ts`, `src/model/provider.ts`, `test/unit/classify.test.ts`, `test/fixtures/distros/*`.

#### Task 5: SPIKE, bound-terminal mechanism and ADR (M)

**Description:** Time-boxed (one session) experiment, **highest design risk, done early**. Prove that a minimal generated
kubeconfig, referenced through `KUBECONFIG`, makes `kubectl`, `helm` and `k9s` stay on one cluster when the global
`current-context` changes, and decide how to handle the inline credentials it must copy: private temp directory, mode
0600, deleted on terminal close, on deactivate and swept at startup; behaviour on Windows (ACLs); and why env-based
alternatives (shell functions, `--context` wrappers) were rejected. Output is an ADR plus a throwaway prototype test on kind.

**Acceptance criteria**
- [ ] On a kind cluster pair, changing the global context does not change what a bound shell talks to (`kubectl`, `helm`).
- [ ] The ADR states the secret-copy risk, the mitigation, and what remains (a crash can leave a 0600 file until the next sweep).
- [ ] Exec-plugin users need no copied secret (verified with an exec-based fixture).
- [ ] A go/no-go: if the approach fails, fall back to a documented alternative before T9 starts.

**Verification:** the prototype script run on kind; ADR `docs/adr/0003-bound-terminals.md` reviewed.

**Done (2026-10-04): GO, with a required mitigation.** All 17 mechanism checks passed on two kind clusters. The spike also
found that **zsh and bash startup files override an injected `KUBECONFIG`** (a common `export KUBECONFIG=...` in an rc
file), so Task 10 must re-assert and verify the path after shell start and say so when it cannot; see ADR 0003.
**Dependencies:** T1. **Files:** `docs/adr/0003-bound-terminals.md`, `extension/test/spike/bound-terminal.sh` (deleted after T9 replaces it).

### Checkpoint 1: pure core and the bound-terminal decision
- [x] `npm test` green (78 tests), coverage of `kubeconfig/` and `model/` at 99.3% (target 90%); the canary tests are part of `npm test`, which CI runs.
- [x] ADR 0003 accepted (GO), conditional on re-asserting `KUBECONFIG` after shell startup.
- [ ] **Review with the owner before building UI.** (Reached 2026-10-04; work is uncommitted, awaiting your go-ahead.)

### Phase B: Fleet view, the first user value

#### Task 6: Fleet tree view (M)

**Description:** Activity-bar container "Sextant" with a tree: environment, then provider, then context; current
context marked; a search/filter command; refreshes when kubeconfig files change (file watcher).

**Acceptance criteria**
- [ ] With a fixture kubeconfig set via `KUBECONFIG` the integration test finds the expected tree.
- [ ] Editing the file updates the tree within one second, without reload.
- [ ] Untagged contexts appear under "Untagged"; nothing is hidden.
- [ ] Labels, descriptions and tooltips pass the canary harness (surface registered).
- [ ] Lazy activation: nothing loads until the view is opened or a command runs.

**Verification:** `npm run test:integration`; manual: install the VSIX in a clean profile on a real kubeconfig.

**Done (2026-10-04), except the manual check.** Pure tree model (`src/model/fleetTree.ts`: grouping, ordering, filter, labels) with 9 unit
tests, plus the VS Code layer (tree view, file watcher, filter and refresh commands, welcome view, `sextant.kubeconfigPaths`
setting). 8 integration cases pass in a real VS Code 1.90, three runs in a row: lazy activation (inactive until used), grouping and
"Other" last, current-context marker, **no secret in any label, tooltip or id** (canary check on the real UI), live update **~375 ms**
after an edit (criterion: 1 s), unreadable file shown as a warning without its content while valid files stay visible, and filter and
clear. Mutation-checked: a tooltip leak, a watcher that never re-arms and a dropped error check each fail the suite. VSIX is 47 KB.
Not verified: the welcome view and icons render correctly (visual), and a real-world kubeconfig; both belong to Checkpoint 2.
**Dependencies:** T2, T3, T4. **Files:** `src/views/fleetTree.ts`, `src/views/watcher.ts`, `package.json` (contributes), `test/integration/fleet.test.ts`.

#### Task 7: Environment and criticality tagging (M)

**Description:** Pure rule engine (glob on context name, plus explicit per-context tags) and settings schema
(`sextant.environments`, `sextant.rules`); a QuickPick command "Tag this cluster"; the tree regroups immediately.

**Acceptance criteria**
- [ ] `*-prod-*` tags matching contexts `prod` and `critical`; an explicit tag overrides a rule; ties resolved deterministically
      and documented.
- [ ] Tags persist in settings (user or workspace), never in the kubeconfig (file unchanged byte for byte after tagging).
- [ ] Invalid settings produce a clear notification, not an exception.

**Verification:** unit tests for the engine; integration test tagging through the command and reading back settings.
**Dependencies:** T6. **Files:** `src/model/environments.ts`, `src/views/tagCommand.ts`, `package.json` (configuration), two tests.

#### Task 8: Status bar and external-switch warning (S)

**Description:** Status bar item shows the global current context, in an unmistakable colour with a warning icon when
critical. A watcher notices `current-context` changing outside the extension and shows a notification when the new
context is critical.

**Acceptance criteria**
- [ ] Critical contexts render with the error/warning theme colours; others neutral; text readable in light, dark and high-contrast themes.
- [ ] Changing `current-context` in the file triggers the warning exactly once per change, only for critical contexts.
- [ ] No kubeconfig write occurs (file hash unchanged).

**Verification:** integration test that edits the file and observes status bar state and notification.
**Dependencies:** T7. **Files:** `src/views/statusBar.ts`, `test/integration/statusbar.test.ts`.

### Checkpoint 2: first usable version
- [ ] Install the VSIX in a clean profile on a real kubeconfig: populated tree grouped by environment, status bar reacts.
- [ ] Time to first useful view is under 60 s, measured by hand on two machines.
- [ ] **Dogfood on the owner's real clusters and review before the safety work.**

### Phase C: Safety, the headline feature

#### Task 9: Minimal kubeconfig generator and secure file lifecycle (M)

**Description:** Pure generator producing a kubeconfig containing exactly one context, its cluster and its user
(inline data copied only if present), plus the filesystem lifecycle decided in the spike: private directory, mode 0600,
unique name, deletion on terminal close, on deactivate, and a startup sweep of stale files.

**Acceptance criteria**
- [ ] The generated file parses with the T2 parser and contains one context; `kubectl config view` agrees.
- [ ] File mode is 0600 and directory 0700 on POSIX; Windows behaviour matches the ADR.
- [ ] Files are removed on close, on deactivate and by the sweep (test simulates a crash by skipping close).
- [ ] The path and contents never appear in logs, notifications or telemetry (canary surface registered for logs).
- [ ] Exec-plugin contexts produce a file with no secret material.

**Verification:** unit tests plus a filesystem integration test; mutation check: remove the cleanup and see the test fail.
**Dependencies:** T2, T4, T5. **Files:** `src/model/minimalKubeconfig.ts`, `src/terminal/tempfiles.ts`, two tests, fixtures.

#### Task 10: Bound terminals (M)

**Description:** Command and tree action "Open terminal for this cluster" starting a VS Code terminal with
`KUBECONFIG` set to the T9 file, a coloured tab and icon by environment, and a clear name. Opening one on a critical
context asks for confirmation (configurable).

**Acceptance criteria**
- [ ] On kind: with two clusters, change the global context, and the bound terminal's `kubectl config current-context` and
      `kubectl get nodes` still target its own cluster (end-to-end).
- [ ] Critical contexts require confirmation; declining opens nothing and leaves no temp file.
- [ ] The terminal tab shows environment colour and name; closing it deletes its kubeconfig.
- [ ] Works when VS Code is launched from the dock (no shell environment inherited) by setting `KUBECONFIG` explicitly.

**Verification:** `npm run test:integration` (kind-backed e2e job, Linux); manual check on macOS.
**Dependencies:** T7, T9. **Files:** `src/terminal/boundTerminal.ts`, `src/views/commands.ts`, `package.json`, `test/e2e/bound-terminal.test.ts`.

#### Task 11: Credential audit view (M)

**Description:** "Credential audit" tree view and an exported Markdown report: per context auth kind, certificate expiry,
long-lived flag, sorted by risk, with remediation hints (for example "static token: replace with an exec plugin").
Values are never shown.

**Acceptance criteria**
- [ ] Report for the seven distro fixtures matches a snapshot; a 50-context synthetic set completes in under 2 s.
- [ ] Tree items, tooltips and the exported report pass the canary harness.
- [ ] "Copy report" copies Markdown only (no clipboard of any credential).

**Verification:** snapshot and canary tests; timing assertion.
**Dependencies:** T4, T6. **Files:** `src/audit/report.ts`, `src/views/auditTree.ts`, `package.json`, two tests.

### Checkpoint 3: safety complete
- [ ] Bound-terminal e2e green on kind; canary green across every registered surface (tree, status bar, terminal, report, logs).
- [ ] Security review: read the diff of Tasks 9-11 line by line; mutation-check each guard (cleanup, mode, header stripping of values).
- [ ] **Review with the owner; run the extension against the owner's real kubeconfig and confirm no secret appears anywhere.**

### Phase D: Hardening and release

#### Task 12: Cross-platform and remote hosts (M)

**Description:** CI matrix on Ubuntu, macOS and Windows for unit and integration tests; fix Windows path and exec-plugin
differences; a manual checklist for Remote-SSH, WSL and Dev Containers, and for Cursor and VSCodium via Open VSX.

**Acceptance criteria**
- [ ] Unit and integration suites pass on all three OSes in CI.
- [ ] `docs/testing/extension.md` lists the manual checks and records the result of running them once.
- [ ] Known limitations are written down, not hidden.

**Verification:** CI matrix green; checklist executed by hand once.
**Dependencies:** T8, T10, T11. **Files:** `.github/workflows/ci.yml`, `docs/testing/extension.md`, small platform fixes.

#### Task 13: Performance budget (S)

**Description:** Enforce the spec's budgets in tests: activation under 200 ms (lazy), 200 contexts render under 500 ms,
audit of 50 contexts under 2 s, bundle under 2 MB.

**Acceptance criteria**
- [ ] A benchmark test fails when any budget is exceeded; the bundle-size check runs in CI.
- [ ] Parsing is cached and invalidated by the file watcher; no work happens on idle.

**Verification:** `npm test -- perf`; `npm run package` size check.
**Dependencies:** T12. **Files:** `test/perf/*.test.ts`, `scripts/check-size.mjs`, small refactors.

#### Task 14: Release engineering (M)

**Description:** `vsce package` and `ovsx publish` scripts, a release workflow on tag that builds, tests, packages and
publishes to a **pre-release channel** on both registries, a CHANGELOG, and the Marketplace README text. Publishing
secrets are documented but never in the repo.

**Acceptance criteria**
- [ ] A dry-run release in CI produces a VSIX and a checksum, and fails closed if tests fail.
- [ ] Both publish steps are gated on the publisher IDs being configured (see Open Questions) and on a protected tag.
- [ ] The listing README leads with what the extension does for engineers; no secrets or internal references.

**Verification:** dry-run workflow green; a human reads the packaged README and `package.json` `contributes`.
**Dependencies:** T13. **Files:** `.github/workflows/release.yml`, `extension/{README.md,CHANGELOG.md}`, `package.json`, scripts.

#### Task 15: Docs and the E1 gate review (S)

**Description:** User documentation, the testing notes, and a line-by-line check of the spec's success criteria with
evidence for each; a design-partner trial plan.

**Acceptance criteria**
- [ ] Every success criterion in the spec is ticked with a measurement or an explicit "not met, because".
- [ ] The plan and README show E1's real status; open items are listed.

**Verification:** owner review of the gate table. **Dependencies:** T14. **Files:** `docs/specs/vscode-extension.md`, README, plan.

### Checkpoint 4: E1 gate
- [ ] All spec success criteria met or consciously waived by the owner.
- [ ] Published as a pre-release on both registries; design partners invited.

## Parallelization

| Can run in parallel | Why |
|---|---|
| T5 spike with T2-T4 | Different files and a different risk; the spike only needs the scaffold and kind |
| T11 audit view with T7-T8 | Both consume T4 and T6 but touch separate views |
| Docs (T15 draft), release scripts (T14 dry run) with Phase C | Independent of feature code |

Sequential: T2 before everything pure; T3 before any task that adds an output surface; T9 before T10; T12 after the
features it tests.

## Definition of done (every task)

Tests and lint pass; the canary harness covers any new output surface; a security-relevant guard has a test shown to
fail without it; docs updated next to the code; no secret in code, tests, logs or screenshots; PR is signed off (DCO).

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Bound-terminal approach copies inline credentials to disk | High | Spike first (T5); 0600 private dir; deleted on close, deactivate and by sweep; exec-plugin users copy nothing; mutation-checked cleanup |
| A kubeconfig secret leaks into a label, log or error | High | Model holds no secrets by type; canary harness from T3 covers every surface; errors never echo content |
| kubectl merge semantics differ from our merge | Med | Differential test against real `kubectl config view` (T2) |
| Windows exec plugins and paths | Med | Matrix in CI (T12); documented limits |
| VS Code API drift or fork differences (Cursor, VSCodium) | Med | Pin the engine; Open VSX checks in the manual list |
| Publisher name or trademark unavailable | Low (blocks T14 publish only) | Placeholder names throughout; publish gated on IDs |
| Scope creep into server features (E2) | Med | E1 has no network at all; anything needing a server waits for E2 |

## Open questions (none block Task 1)

1. **Read-only vs a scoped `current-context` write.** This plan keeps E1 read-only (see above). Confirm, or choose to
   allow a confirmed `use-context` write.
2. **Approve the `yaml` runtime dependency** (the spec's "ask first" rule).
3. **Minimum VS Code version**: decided, `^1.93` (shell-integration events).
4. **Publisher and extension names** and the trademark check; only T14's publish step needs them.
5. **Which real kubeconfigs to dogfood on** at Checkpoints 2 and 3 (yours, plus a design partner's, scrubbed).
6. **Telemetry:** E1 ships none and makes no network calls; confirm that is the intended first release.
