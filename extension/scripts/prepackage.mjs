// The VSIX must carry the licence texts; keep one source of truth at the repository root.
import { copyFileSync } from 'node:fs';
for (const f of ['LICENSE', 'NOTICE'])
  copyFileSync(new URL(`../../${f}`, import.meta.url), new URL(`../${f}`, import.meta.url));
