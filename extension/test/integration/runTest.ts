import { copyFileSync, mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import * as path from 'node:path';
import { runTests } from '@vscode/test-electron';
import pkg from '../../package.json';

// Test against the oldest VS Code we claim to support (the engine floor) so a newer API cannot slip in unnoticed.
// Set VSCODE_TEST_VERSION=stable to check the latest release too.
const floor = pkg.engines.vscode.replace('^', '');
const version = process.env.VSCODE_TEST_VERSION ?? floor;

const MAIN = `apiVersion: v1
kind: Config
current-context: kind-dev
contexts:
  - {name: kind-dev, context: {cluster: kind-dev, user: kind-dev}}
  - {name: gke_proj_eu_stg, context: {cluster: gke_proj_eu_stg, user: gke-user}}
  - {name: plain, context: {cluster: plain-c, user: plain-u, namespace: web}}
clusters:
  - {name: kind-dev, cluster: {server: "https://127.0.0.1:41873"}}
  - {name: gke_proj_eu_stg, cluster: {server: "https://34.77.1.2"}}
  - {name: plain-c, cluster: {server: "https://plain.example.com:6443"}}
users:
  - {name: kind-dev, user: {client-certificate-data: Zm9v, client-key-data: CANARY-client-key-data-8d4e6a0f1c93}}
  - {name: gke-user, user: {exec: {command: gke-gcloud-auth-plugin}}}
  - {name: plain-u, user: {token: CANARY-static-token-3f9a1c7e5b2d}}
`;

// Launches a real VS Code with the built extension and runs the suite in ./suite.
async function main(): Promise<void> {
  // Editor-hosted terminals (and some CI wrappers) export this; with it set Electron treats VS Code's own flags as
  // Node options and exits with "bad option".
  delete process.env.ELECTRON_RUN_AS_NODE;

  // The extension host reads only these files: a mutable one the tests edit, and the canary fixture (every secret
  // field holds a canary), so the suite can prove both live updates and that no UI text leaks a secret.
  const dir = mkdtempSync(path.join(tmpdir(), 'sextant-it-'));
  const main = path.join(dir, 'main.yaml');
  const canary = path.join(dir, 'canary-full.yaml');
  writeFileSync(main, MAIN);
  copyFileSync(path.resolve(__dirname, '..', 'test', 'fixtures', 'canary', 'full.yaml'), canary);

  const extensionDevelopmentPath = path.resolve(__dirname, '..');
  const extensionTestsPath = path.resolve(__dirname, 'suite', 'index.js');
  await runTests({
    version,
    extensionDevelopmentPath,
    extensionTestsPath,
    extensionTestsEnv: {
      KUBECONFIG: [main, canary].join(path.delimiter),
      SEXTANT_IT_MAIN: main,
      SEXTANT_NO_WELCOME: '1',
    },
    // A throwaway profile: tests never touch the developer's settings or extensions.
    launchArgs: ['--disable-extensions', '--disable-workspace-trust'],
  });
}

main().catch((err: unknown) => {
  console.error('integration tests failed:', err);
  process.exit(1);
});
