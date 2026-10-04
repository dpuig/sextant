import { inspect } from 'node:util';

/**
 * Canary secrets. Every secret-bearing field in test/fixtures/canary/*.yaml holds one of these unique strings. If any
 * appears in ANY output surface (a return value, an error, a log line, a tree label, a tooltip, a report), the code
 * leaked a credential. This is the single guard behind "the extension never shows or logs secret values".
 *
 * Later tasks call `expectNoCanary(surface, value)` on every new output they add; the unit suite is a required CI check.
 */
export const CANARIES = [
  'CANARY-static-token-3f9a1c7e5b2d',
  'CANARY-client-key-data-8d4e6a0f1c93',
  'CANARY-client-cert-data-5b7a2e9d4c18',
  'CANARY-ca-data-1e3c5a7b9d02',
  'CANARY-basic-password-6f8a0b2c4d71',
  'CANARY-exec-env-secret-2a4c6e8f0b35',
  'CANARY-exec-arg-secret-9b1d3f5a7c46',
  'CANARY-oidc-id-token-7c9e1a3b5d28',
  'CANARY-oidc-refresh-token-4e6a8c0b2d59',
  'CANARY-oidc-client-secret-0a2c4e6b8d13',
  'CANARY-userinfo-password-3c5e7a9b1d84',
  'CANARY-impersonate-token-8a0c2e4b6d97',
] as const;

/** Forms a leak can take after a naive transformation. */
function variants(secret: string): string[] {
  return [
    secret,
    Buffer.from(secret).toString('base64'),
    Buffer.from(secret).toString('base64url'),
    Buffer.from(secret).toString('hex'),
    encodeURIComponent(secret),
    secret.toLowerCase(),
    secret.slice(0, 14), // truncation: "CANARY-" + a distinctive stretch
    secret.slice(-12),
  ];
}

/** Everything a value can show: JSON, util.inspect with hidden and getter members, and String(). */
function render(value: unknown): string {
  const parts: string[] = [];
  try {
    // JSON.stringify(undefined) returns undefined at runtime although it is typed as string.
    const json = JSON.stringify(value, (_k, v: unknown) =>
      v instanceof Error ? { name: v.name, message: v.message, stack: v.stack } : v,
    ) as string | undefined;
    parts.push(json ?? '');
  } catch {
    /* cyclic: inspect below still covers it */
  }
  parts.push(
    inspect(value, {
      depth: null,
      showHidden: true,
      getters: true,
      maxArrayLength: null,
      maxStringLength: null,
    }),
  );
  parts.push(String(value));
  return parts.join('\n');
}

export function findCanaries(value: unknown): string[] {
  const text = render(value);
  const hits: string[] = [];
  for (const secret of CANARIES) {
    for (const v of variants(secret)) {
      if (text.includes(v)) {
        hits.push(`${secret} (as ${v === secret ? 'plain text' : v})`);
        break;
      }
    }
  }
  return hits;
}

/** Throws, naming the surface and the leaked canary, if any secret appears in `value`. */
export function expectNoCanary(surface: string, value: unknown): void {
  const hits = findCanaries(value);
  if (hits.length > 0) throw new Error(`secret leaked into ${surface}: ${hits.join('; ')}`);
}

const SUSPICIOUS_KEY =
  /(token|password|passwd|secret|private|key-?data|certificate-?data|passphrase|bearer)/i;

/**
 * Structural guard: the public model must not even have a field that could hold a secret. `credential` (the
 * classification) is the one allowed name; everything else is checked by pattern.
 */
export function suspiciousKeys(value: unknown, path = '$'): string[] {
  const out: string[] = [];
  if (Array.isArray(value)) {
    value.forEach((v: unknown, i) => out.push(...suspiciousKeys(v, `${path}[${i}]`)));
  } else if (value !== null && typeof value === 'object' && !(value instanceof Date)) {
    for (const [k, v] of Object.entries(value)) {
      if (SUSPICIOUS_KEY.test(k)) out.push(`${path}.${k}`);
      out.push(...suspiciousKeys(v, `${path}.${k}`));
    }
  }
  return out;
}
