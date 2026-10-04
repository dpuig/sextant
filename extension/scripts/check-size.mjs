// Fails if the packaged VSIX exceeds the size budget (spec: bundle under 2 MB; the E1 scaffold must stay far below).
import { readdirSync, statSync } from 'node:fs';
const budget = Number(process.env.VSIX_BUDGET_BYTES ?? 2 * 1024 * 1024);
const dir = new URL('../dist-vsix/', import.meta.url);
const files = readdirSync(dir).filter((f) => f.endsWith('.vsix'));
if (files.length !== 1) {
  console.error(`expected exactly one .vsix in dist-vsix, found ${files.length}`);
  process.exit(1);
}
const size = statSync(new URL(files[0], dir)).size;
console.log(`${files[0]}: ${size} bytes (budget ${budget})`);
if (size > budget) {
  console.error('VSIX exceeds the size budget');
  process.exit(1);
}
