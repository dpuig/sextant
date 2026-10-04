import { describe, expect, it } from 'vitest';
import pkg from '../../package.json';

describe('package manifest', () => {
  it('declares the Apache-2.0 licence and a VS Code engine floor', () => {
    expect(pkg.license).toBe('Apache-2.0');
    expect(pkg.engines.vscode).toMatch(/^\^1\.\d+\.0$/);
  });

  it('pins the VS Code types to the engine floor so newer APIs cannot slip in', () => {
    const floor = pkg.engines.vscode.replace('^', '');
    expect(pkg.devDependencies['@types/vscode']).toBe(floor);
  });

  it('keeps the runtime dependency list to the approved set', () => {
    // Every runtime dependency is shipped to users and must be justified; adding one needs approval (see the spec).
    expect(Object.keys(pkg.dependencies).sort()).toEqual(['yaml']);
  });
});
