import { LineCounter, parseDocument, isMap, isSeq, isScalar } from 'yaml';
import type { KubeconfigError } from './types';
import type { RawCluster, RawContext, RawKubeconfig, RawNamed, RawUser } from './raw';

/** A kubeconfig is a few KB; anything this large is not one, and parsing it would only burn memory. */
export const MAX_KUBECONFIG_BYTES = 5 * 1024 * 1024;

export type ParseResult = { ok: true; value: RawKubeconfig } | { ok: false; error: KubeconfigError };

/**
 * Parses one kubeconfig. On failure returns a typed error that contains NO text derived from the file: the YAML
 * library's messages quote the offending source line, and in a kubeconfig that line can be a credential. Only the
 * parser's closed error code is kept.
 */
export function parseKubeconfig(file: string, content: string): ParseResult {
  if (Buffer.byteLength(content, 'utf8') > MAX_KUBECONFIG_BYTES) {
    return { ok: false, error: { file, code: 'too-large' } };
  }
  const lineCounter = new LineCounter();
  const doc = parseDocument(content, { prettyErrors: false, uniqueKeys: false, lineCounter });
  const first = doc.errors[0];
  if (first) {
    return { ok: false, error: { file, code: 'parse-failed', parserCode: first.code } };
  }
  // toJS throws (a ReferenceError) on exponential alias expansion. An unreadable config must never take the whole
  // fleet down, and the thrown message is not safe to surface, so it becomes a typed error.
  let data: unknown;
  try {
    data = doc.toJS({ maxAliasCount: 100 });
  } catch {
    return { ok: false, error: { file, code: 'too-complex' } };
  }
  // An empty file is valid (kubectl treats it as an empty config); anything else must be a mapping.
  if (data === null || data === undefined) {
    return { ok: true, value: { file, contexts: [], clusters: [], users: [] } };
  }
  if (!isRecord(data)) return { ok: false, error: { file, code: 'invalid-shape' } };

  const contextLines = entryLines(doc.contents, 'contexts', lineCounter);
  const contexts = namedList(
    data.contexts,
    (v): RawContext | undefined => {
      if (!isRecord(v)) return undefined;
      const cluster = str(v.cluster);
      const user = str(v.user);
      if (cluster === undefined || user === undefined) return undefined;
      const namespace = str(v.namespace);
      return namespace === undefined ? { cluster, user } : { cluster, user, namespace };
    },
    'context',
    contextLines,
  );
  const clusters = namedList(
    data.clusters,
    (v): RawCluster => {
      const server = isRecord(v) ? str(v.server) : undefined;
      return server === undefined ? {} : { server };
    },
    'cluster',
  );
  const users = namedList(data.users, (v): RawUser => (isRecord(v) ? v : {}), 'user');

  const current = str(data['current-context']);
  return {
    ok: true,
    value: { file, ...(current ? { currentContext: current } : {}), contexts, clusters, users },
  };
}

function namedList<T>(
  value: unknown,
  pick: (inner: unknown) => T | undefined,
  innerKey: string,
  lines: readonly (number | undefined)[] = [],
): RawNamed<T>[] {
  if (!Array.isArray(value)) return [];
  const out: RawNamed<T>[] = [];
  for (const [index, item] of (value as unknown[]).entries()) {
    if (!isRecord(item)) continue;
    const name = str(item.name);
    if (name === undefined || name === '') continue;
    const picked = pick(item[innerKey]);
    // A user entry with no body is legal (anonymous); contexts without cluster/user are not usable and are skipped.
    if (picked === undefined) continue;
    const line = lines[index];
    out.push(line === undefined ? { name, value: picked } : { name, value: picked, line });
  }
  return out;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function str(v: unknown): string | undefined {
  return typeof v === 'string' ? v : undefined;
}

/**
 * 1-based line of each entry in the top-level sequence `key`, indexed like the JS array. Positions only: nothing from
 * the entries' content is read. Anything unexpected yields no lines, never an error.
 */
function entryLines(contents: unknown, key: string, lineCounter: LineCounter): (number | undefined)[] {
  try {
    if (!isMap(contents)) return [];
    const seq = contents.items.find((pair) => isScalar(pair.key) && pair.key.value === key)?.value;
    if (!isSeq(seq)) return [];
    return seq.items.map((node) => {
      const range = (node as { range?: [number, number, number] | null } | null)?.range;
      return range ? lineCounter.linePos(range[0]).line : undefined;
    });
  } catch {
    return [];
  }
}
