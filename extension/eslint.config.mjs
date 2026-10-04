import js from '@eslint/js';
import tseslint from 'typescript-eslint';

// Type-aware rules apply to TypeScript only; the plain ESM build scripts get the base recommended set.
const typed = tseslint.configs.strictTypeChecked.map((c) => ({ ...c, files: ['**/*.ts'] }));

export default tseslint.config(
  {
    ignores: ['dist/**', 'dist-test/**', 'dist-vsix/**', 'node_modules/**', 'coverage/**', '.vscode-test/**'],
  },
  js.configs.recommended,
  ...typed,
  {
    files: ['**/*.ts'],
    languageOptions: { parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname } },
    rules: {
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/restrict-template-expressions': ['error', { allowNumber: true }],
    },
  },
  {
    files: ['**/*.mjs'],
    languageOptions: { globals: { process: 'readonly', console: 'readonly', URL: 'readonly' } },
  },
);
