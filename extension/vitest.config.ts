import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['test/unit/**/*.test.ts', 'test/perf/**/*.test.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/kubeconfig/**', 'src/model/**'],
      thresholds: { lines: 90, functions: 90, branches: 85, statements: 90 },
    },
  },
});
