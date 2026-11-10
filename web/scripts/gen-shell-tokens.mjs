// Generates src/app/shell-tokens.css (CSS custom properties) from
// designs/redesign/tokens.json, for the new shell. The old UI's tokens.css
// comes from gen-tokens.mjs and is not touched.
//
// Kept out: every $-key, prose (shape.touchNote), icon, motion, and status
// labels and icon paths. status.*.color names a color token, so it maps to
// that token's variable.
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const tokens = JSON.parse(readFileSync(join(root, '..', 'designs', 'redesign', 'tokens.json'), 'utf8'));

const kebab = (s) => s.replace(/([a-z0-9])([A-Z])/g, '$1-$2').toLowerCase();
const name = (path) => `--${path.map(kebab).join('-')}`;
// Lengths are px; weights stay unitless.
const PX = [['harness', 'size'], ['font', 'size'], ['shape']];
const isPx = (path) => PX.some((p) => p.every((k, i) => path[i] === k));
const SKIP = new Set(['touchNote']);

function flat(value, path, out) {
  if (path.some((p) => p.startsWith('$') || SKIP.has(p))) return out;
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    for (const [k, v] of Object.entries(value)) flat(v, [...path, k], out);
  } else {
    let v = value;
    if (typeof v === 'number' && isPx(path)) v = `${v}px`;
    // Placeholder letters are CSS strings; tileBorder names a color token.
    else if (path.at(-1) === 'placeholder') v = JSON.stringify(v);
    else if (path.at(-1) === 'tileBorder') v = `var(--color-${kebab(v)})`;
    out.push(`  ${name(path)}: ${v};`);
  }
  return out;
}

const themed = (theme) => flat(tokens.color[theme], ['color'], []);
const terminal = flat(tokens.color.terminal, ['color', 'terminal'], []);

const fleet = tokens.fleet.sequence.flatMap((e, i) => [
  [`--fleet-${i}-light`, e.light],
  [`--fleet-${i}-dark`, e.dark],
  [`--fleet-${i}-ink`, e.ink],
]);
const fleetLines = (theme) => tokens.fleet.sequence.map((_, i) => `  --fleet-${i}: var(--fleet-${i}-${theme});`);
const raw = fleet.map(([k, v]) => `  ${k}: ${v};`);

const crew = tokens.crew.sequence.map((c, i) => `  --crew-${i}: ${c};`);
const harness = flat(tokens.harness, ['harness'], []);
const font = flat(tokens.font, ['font'], []);
const shape = flat(tokens.shape, ['shape'], []);
const status = Object.entries(tokens.status)
  .filter(([k]) => !k.startsWith('$'))
  .map(([k, v]) => `  --status-${kebab(k)}: var(--color-${kebab(v.color)});`);

const common = [...terminal, ...raw, ...crew, ...harness, ...font, ...shape, ...status];
const light = [...themed('light'), ...fleetLines('light')];
const dark = [...themed('dark'), ...fleetLines('dark')];

const css = `/* Generated from designs/redesign/tokens.json by scripts/gen-shell-tokens.mjs. Do not edit. */
:root {
${[...common, ...light].join('\n')}
}
@media (prefers-color-scheme: dark) {
  :root {
${dark.map((l) => `  ${l}`).join('\n')}
  }
}
`;
writeFileSync(join(root, 'src', 'app', 'shell-tokens.css'), css);

// What CSS can't give the shell: the sequence length (to wrap color_index) and the
// placeholder letters as real text.
const placeholders = Object.fromEntries(
  Object.entries(tokens.harness)
    .filter(([k, v]) => v && typeof v === 'object' && 'placeholder' in v)
    .map(([k, v]) => [k, v.placeholder]),
);
writeFileSync(
  join(root, 'src', 'app', 'shell-tokens.ts'),
  `// Generated from designs/redesign/tokens.json by scripts/gen-shell-tokens.mjs. Do not edit.
export const FLEET_COUNT = ${tokens.fleet.sequence.length};
export const HARNESS_PLACEHOLDERS: Record<string, string> = ${JSON.stringify(placeholders)};
`,
);
