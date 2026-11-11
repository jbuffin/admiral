import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import type { Admiral } from './fixtures';

// Fleet names are unusual words so a text assertion can't match anything else on the page.
export const WORK = 'Westwind';
export const HOME = 'Homeport';

const identityVar = { claude: 'CLAUDE_CONFIG_DIR', codex: 'CODEX_HOME', copilot: 'COPILOT_HOME' } as const;
type Harness = keyof typeof identityVar;

function dock(a: Admiral, fleet: string, harness: Harness) {
  return { executable: a.fakeHarness(harness), env: { [identityVar[harness]]: `${a.home}/identity/${fleet}-${harness}` } };
}

/** Two fleets, first `zulu` (Westwind: claude and codex docks), then `alpha` (Homeport: codex and copilot), and a logo for each harness. */
export function seedFleets(a: Admiral) {
  a.writeConfig({
    version: 1,
    fleets: {
      zulu: { name: WORK, search_roots: [a.home], claude: dock(a, 'zulu', 'claude'), codex: dock(a, 'zulu', 'codex') },
      alpha: { name: HOME, search_roots: [a.home], codex: dock(a, 'alpha', 'codex'), copilot: dock(a, 'alpha', 'copilot') },
    },
  });
  mkdirSync(join(a.home, 'logos'), { recursive: true });
  for (const h of Object.keys(identityVar)) {
    writeFileSync(join(a.home, 'logos', `${h}.svg`), '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>');
  }
}

/** What the page asks for and what it logs: Admiral serves everything itself, over /api/v1, without errors. */
export function watchPage(page: Page, server: string) {
  const host = new URL(server).host;
  const paths: string[] = [];
  const foreign: string[] = [];
  const errors: string[] = [];
  page.on('console', (m) => m.type() === 'error' && errors.push(m.text()));
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (['data:', 'blob:', 'about:'].includes(u.protocol)) return;
    if (u.host !== host) foreign.push(r.url());
    paths.push(u.pathname);
  });
  return {
    paths,
    foreign,
    errors,
    unversioned: () => paths.filter((p) => /^\/(api|ws)(\/|$)/.test(p) && !/^\/(api|ws)\/v1(\/|$)/.test(p)),
  };
}
