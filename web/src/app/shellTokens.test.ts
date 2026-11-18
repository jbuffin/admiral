import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { beforeAll, expect, test } from 'vitest';

const web = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const tokens = JSON.parse(readFileSync(join(web, '..', 'designs', 'redesign', 'tokens.json'), 'utf8'));
let css = '';

beforeAll(() => {
  execFileSync('node', [join(web, 'scripts', 'gen-shell-tokens.mjs')]);
  css = readFileSync(join(web, 'src', 'app', 'shell-tokens.css'), 'utf8');
});

// The declarations of the first rule whose selector is exactly `selector`.
const rule = (selector: string) => {
  const at = css.indexOf(`${selector} {`);
  return at < 0 ? '' : css.slice(at, css.indexOf('}', at));
};

for (const theme of ['dark', 'light'] as const) {
  test(`an explicit ${theme} choice gets the ${theme} colors and fleet colors even when the system says otherwise`, () => {
    const body = rule(`:root[data-theme="${theme}"]`);
    expect(body).toContain(`--color-bg: ${tokens.color[theme].bg};`);
    expect(body).toContain(`--color-panel: ${tokens.color[theme].panel};`);
    expect(body).toContain(`--fleet-0: var(--fleet-0-${theme});`);
  });
}
