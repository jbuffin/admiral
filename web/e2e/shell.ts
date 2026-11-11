// Shared setup for the new shell's specs (shell.spec.ts, shell-phone.spec.ts).
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

/**
 * Two fleets, first one `zulu` (Westwind: claude + codex docks), then `alpha` (Homeport: codex + copilot).
 * Only claude.svg is installed under the home's logos, so the codex and copilot flags show their placeholders.
 */
export function seedFleets(a: Admiral) {
  a.writeConfig({
    version: 1,
    fleets: {
      zulu: { name: WORK, search_roots: [a.home], claude: dock(a, 'zulu', 'claude'), codex: dock(a, 'zulu', 'codex') },
      alpha: { name: HOME, search_roots: [a.home], codex: dock(a, 'alpha', 'codex'), copilot: dock(a, 'alpha', 'copilot') },
    },
  });
  mkdirSync(join(a.home, 'logos'), { recursive: true });
  writeFileSync(
    join(a.home, 'logos', 'claude.svg'),
    '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10" fill="#D97757"/></svg>',
  );
}

/** Every URL path the page requests, so a test can check that none went to an unversioned API. */
export function watchRequests(page: Page) {
  const paths: string[] = [];
  page.on('request', (r) => paths.push(new URL(r.url()).pathname));
  return {
    paths,
    unversioned: () => paths.filter((p) => /^\/(api|ws)(\/|$)/.test(p) && !/^\/(api|ws)\/v1(\/|$)/.test(p)),
  };
}
