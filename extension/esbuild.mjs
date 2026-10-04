// Bundles the extension (and, with --test, the integration tests) with esbuild.
import * as esbuild from 'esbuild';

const production = process.argv.includes('--production');
const watch = process.argv.includes('--watch');
const tests = process.argv.includes('--test');

/** @type {import('esbuild').BuildOptions} */
const common = {
  bundle: true,
  platform: 'node',
  format: 'cjs',
  target: 'node20',
  external: ['vscode'], // provided by the host
  sourcemap: !production,
  minify: production,
  logLevel: 'info',
};

const build = tests
  ? {
      ...common,
      entryPoints: ['test/integration/runTest.ts', 'test/integration/suite/index.ts'],
      outdir: 'dist-test',
      external: ['vscode', '@vscode/test-electron'],
    }
  : { ...common, entryPoints: ['src/extension.ts'], outfile: 'dist/extension.js' };

if (watch) {
  const ctx = await esbuild.context(build);
  await ctx.watch();
} else {
  await esbuild.build(build);
}
