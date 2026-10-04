import { readdirSync, readFileSync } from 'node:fs';
import * as path from 'node:path';
import { describe, expect, it } from 'vitest';
import { loadFleet } from '../../src/kubeconfig';
import { parseKubeconfig } from '../../src/kubeconfig/parse';
import { CANARIES, expectNoCanary, findCanaries, suspiciousKeys } from '../canary';

const dir = path.join(__dirname, '..', 'fixtures', 'canary');
const files = readdirSync(dir).filter((f) => f.endsWith('.yaml'));

describe('the harness itself (a canary check that cannot fail would be worthless)', () => {
  it('has a unique canary per secret field in the full fixture', () => {
    const full = readFileSync(path.join(dir, 'full.yaml'), 'utf8');
    for (const c of CANARIES) expect(full, `fixture is missing ${c}`).toContain(c);
    expect(new Set(CANARIES).size).toBe(CANARIES.length);
  });

  it('detects a leak in plain text, base64, hex, URL-encoded and truncated forms', () => {
    const c = CANARIES[0];
    expect(findCanaries(`prefix ${c} suffix`)).toHaveLength(1);
    expect(findCanaries({ nested: { v: Buffer.from(c).toString('base64') } })).toHaveLength(1);
    expect(findCanaries([Buffer.from(c).toString('hex')])).toHaveLength(1);
    expect(findCanaries(encodeURIComponent(c))).toHaveLength(1);
    expect(findCanaries(c.slice(0, 14) + '...')).toHaveLength(1);
  });

  it('detects a leak inside an Error, a hidden property, and a cyclic structure', () => {
    const c = CANARIES[2];
    expect(() => {
      expectNoCanary('error', new Error(`bad: ${c}`));
    }).toThrow(/secret leaked into error/);
    const hidden = Object.defineProperty({}, 'h', { value: c, enumerable: false });
    expect(() => {
      expectNoCanary('hidden', hidden);
    }).toThrow(/secret leaked into hidden/);
    const cyc: Record<string, unknown> = { v: c };
    cyc.self = cyc;
    expect(() => {
      expectNoCanary('cyclic', cyc);
    }).toThrow(/secret leaked into cyclic/);
  });

  it('fails a deliberately leaky parser (mutation check kept as a permanent test)', () => {
    const leakyError = (content: string) => ({
      ok: false,
      message: `cannot parse near: ${content.slice(0, 200)}`,
    });
    const content = readFileSync(path.join(dir, 'malformed-unterminated.yaml'), 'utf8');
    expect(() => {
      expectNoCanary('leaky parser', leakyError(content));
    }).toThrow(/secret leaked/);
  });

  it('flags a model field whose name suggests it holds a secret, but allows the credential summary', () => {
    expect(suspiciousKeys({ contexts: [{ name: 'x', credential: { kind: 'static-token' } }] })).toEqual([]);
    expect(suspiciousKeys({ contexts: [{ name: 'x', token: 't' }] })).toEqual(['$.contexts[0].token']);
    expect(suspiciousKeys({ a: { clientKeyData: 'k' } })).toEqual(['$.a.clientKeyData']);
  });
});

describe('kubeconfig package never emits a canary', () => {
  for (const file of files) {
    it(`${file}: parse result`, () => {
      const content = readFileSync(path.join(dir, file), 'utf8');
      const r = parseKubeconfig(file, content);
      // The raw parse of a VALID file legitimately holds secrets internally (that is why raw.ts is private); what
      // must be clean is every failure, and everything that is public.
      if (!r.ok) expectNoCanary(`parse error for ${file}`, r.error);
    });

    it(`${file}: public Fleet (contexts, files, errors), including via the filesystem path`, async () => {
      const fleet = await loadFleet({ env: { KUBECONFIG: path.join(dir, file) }, cwd: dir });
      expectNoCanary(`Fleet from ${file}`, fleet);
      expect(suspiciousKeys(fleet), `Fleet from ${file} has a secret-shaped field`).toEqual([]);
    });
  }

  it('all fixtures merged together (every secret in one process) still produce a clean Fleet', async () => {
    const all = files.map((f) => path.join(dir, f)).join(path.delimiter);
    const fleet = await loadFleet({ env: { KUBECONFIG: all }, cwd: dir });
    expectNoCanary('merged Fleet', fleet);
    expect(fleet.contexts.length).toBeGreaterThan(0); // the harness is not passing on an empty result
    expect(fleet.errors.length).toBeGreaterThan(0); // and it did exercise the error paths
  });

  it('read errors from the OS never surface a message', async () => {
    const fleet = await loadFleet({
      env: { KUBECONFIG: '/x' },
      cwd: '/',
      readFile: () => Promise.reject(new Error(`EACCES reading ${CANARIES[0]}`)),
    });
    expectNoCanary('read-failed error', fleet);
  });
});
