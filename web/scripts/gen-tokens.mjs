// Generates src/tokens.css (CSS custom properties) from designs/tokens.json.
// font.googleFontsUrl is skipped: nothing may reference Google Fonts.
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const tokens = JSON.parse(readFileSync(join(root, '..', 'designs', 'tokens.json'), 'utf8'));

const kebab = (s) => s.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase();
const vars = [];

function walk(value, path) {
  if (path[0] === 'font' && path[1] === 'googleFontsUrl') return;
  if (path.some((p) => p.startsWith('$'))) return;
  if (Array.isArray(value)) {
    value.forEach((v, i) => walk(v, [...path, v && typeof v === 'object' && v.name ? v.name : String(i + 1)]));
  } else if (value && typeof value === 'object') {
    for (const [k, v] of Object.entries(value)) walk(v, [...path, k]);
  } else {
    // Sizes are CSS px; motion entries are prose, so they stay out of CSS.
    if (path[0] === 'motion') return;
    const px = typeof value === 'number';
    vars.push(`  --${path.map(kebab).join('-')}: ${px ? `${value}px` : value};`);
  }
}

walk(tokens, []);
writeFileSync(join(root, 'src', 'tokens.css'), `/* Generated from designs/tokens.json by scripts/gen-tokens.mjs. Do not edit. */\n:root {\n${vars.join('\n')}\n}\n`);
