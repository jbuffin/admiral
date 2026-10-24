import type { Browser, Locator, Page, TestInfo } from '@playwright/test';
import { spawnSync } from 'node:child_process';
import { existsSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { type Admiral, expect, test as base } from './fixtures';
import { World } from './world';

// Helpers for the ships-in-open-water scenarios (u10). A Sea is one Admiral
// whose fleets each have their own cue file, so every ship can be told what to
// do: u5's shared-cue pitfall, and a fake that reports no hook is needs you
// after 10 s. Every cue list starts with `hook working`.

const fakeharness = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness');

export interface SeaFleet {
  name: string;
  color: string;
  /** Cue lines of the fake claude: `hook working` is added in front. */
  cue?: string[];
}

export interface LaunchedShip {
  session: string;
  name: string;
  launched: string;
  status: string;
  position: { x: number; y: number } | null;
}

export class Sea {
  readonly world: World;
  private readonly keys: string[];

  constructor(
    readonly a: Admiral,
    fleets: Record<string, SeaFleet>,
  ) {
    this.world = new World(a);
    this.keys = Object.keys(fleets);
    const config: { [key: string]: unknown } = {};
    for (const [key, f] of Object.entries(fleets)) {
      writeFileSync(join(this.world.root, `cue-${key}`), ['hook working', ...(f.cue ?? [])].join('\n') + '\n');
      config[key] = {
        name: f.name,
        color: f.color,
        search_roots: [this.world.work],
        claude: {
          executable: join(fakeharness, 'claude'),
          env: {
            FAKE_RECORD_DIR: join(this.world.root, 'rec'),
            FAKE_CUE: join(this.world.root, `cue-${key}`),
            HOME: join(this.world.root, 'fh'),
            CLAUDE_CONFIG_DIR: this.world.identity(key, 'claude'),
          },
        },
      };
    }
    a.writeConfig({ version: 1, fleets: config });
  }

  /** Launches straight through the API (no source) and returns the ship, once it is underway and its cues have run. */
  async launch(fleet: string, repo: string, until: 'cues-done' | 'recorded' = 'cues-done'): Promise<LaunchedShip> {
    const text = 'sail';
    const dir = this.world.checkout(repo);
    const res = await fetch(`${this.a.url}api/ships`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ fleet, harness: 'claude', dir, skill: { kind: 'none' }, text }),
    });
    const body = await res.json();
    if (res.status !== 201) throw new Error(`launch failed: ${res.status} ${JSON.stringify(body)}`);
    const ship = (body.session ? body : body.ship) as LaunchedShip;
    await expect.poll(() => existsSync(join(this.world.root, 'rec', ship.session, until)), { timeout: 15_000 }).toBe(true);
    return ship;
  }

  async ships(): Promise<LaunchedShip[]> {
    const res = await fetch(`${this.a.url}api/ships`);
    const body = await res.json();
    return (Array.isArray(body) ? body : body.ships) as LaunchedShip[];
  }

  async ship(session: string) {
    return (await this.ships()).find((s) => s.session === session);
  }

  /** What a harness hook does: `tmux set -p @admiral-state <state>` on the ship's own pane. */
  setState(session: string, state: 'needs_you' | 'working') {
    const sock = join(this.world.root, 'tmux.sock');
    const r = spawnSync('tmux', ['-S', sock, 'set-option', '-p', '-t', session, '@admiral-state', state]);
    if (r.status !== 0) throw new Error(`tmux set-option failed: ${r.stderr}`);
  }

  /** Ends every fake ship, so a test stays under the CI pty limit. */
  end() {
    this.world.endShips();
  }
}

export const test = base.extend<{ sea: (fleets: Record<string, SeaFleet>) => Sea }>({
  sea: async ({ admiral }, use) => {
    const made: Sea[] = [];
    await use((fleets) => {
      const s = new Sea(admiral, fleets);
      made.push(s);
      return s;
    });
    for (const s of made) s.end();
  },
});

export { expect };

// ---- what the page offers the tests ----
//
//   window.__admiral.boarded   the board(session) function pushes the session here when this object exists
//   window.__chime.notes       the chime's oscillators started so far: one chime is 3 (the prototype's three notes)

export function spyOnPage(page: Page) {
  return page.addInitScript(() => {
    const w = window as unknown as { __admiral: { boarded: string[] }; __chime: { notes: number } };
    w.__admiral = { boarded: [] };
    w.__chime = { notes: 0 };
    const proto = window.OscillatorNode?.prototype;
    if (proto) {
      const start = proto.start;
      proto.start = function (...args: Parameters<typeof start>) {
        w.__chime.notes++;
        return start.apply(this, args);
      };
    }
  });
}

export const boarded = (page: Page) =>
  page.evaluate(() => (window as unknown as { __admiral?: { boarded: string[] } }).__admiral?.boarded ?? []);
export const notes = (page: Page) => page.evaluate(() => (window as unknown as { __chime?: { notes: number } }).__chime?.notes ?? 0);

// ---- locators ----

export const water = (page: Page) => page.getByRole('main', { name: 'Open water' });
export const header = (page: Page) => page.getByRole('banner');
/** The header's Needs You inbox button: "Needs you, <n> item(s)" or "Needs you, nothing waiting". */
export const inboxButton = (page: Page) => header(page).getByRole('button', { name: /^Needs you, / });

/** All ships in open water (not their crew boats, "Board <crew>, teammate, crew of <ship>"), or one by a part of its
 *  accessible name: "Board <name>, <Harness> in <Fleet>, <status in words>". */
export const ships = (page: Page, name: RegExp = /^Board (?!.+, teammate, crew of ).+, .+ in .+, /) => water(page).getByRole('button', { name });

// The status words in a ship's name: underway, needs you, "anchored, exited 3", sailing off.
export const underwayIn = (fleet: string) => new RegExp(`^Board .+, Claude in ${fleet}, underway$`);
export const needsYouIn = (fleet: string) => new RegExp(`^Board .+, Claude in ${fleet}, needs you$`);

/** The phone project has no header button: its Inbox tab carries the count ("Inbox, <n> item(s)", "Inbox" when empty). */
export const phone = (info: TestInfo) => info.project.name === 'phone';
export const badge = (page: Page, info: TestInfo) =>
  phone(info) ? page.getByRole('navigation', { name: 'Sections' }).getByRole('button', { name: /^Inbox/ }) : inboxButton(page);
/** A header button name ("Needs you, 2 items", "Needs you, nothing waiting") as the badge is named on this project. */
export const badgeName = (info: TestInfo, name: string) =>
  phone(info) ? name.replace(/^Needs you, nothing waiting$/, 'Inbox').replace(/^Needs you, /, 'Inbox, ') : name;

export const soundToggle = (page: Page) => header(page).getByRole('button', { name: /^(Mute chime|Turn chime on)$/ });

/** The ship's top-left as fractions of open water, as the server records it. */
export async function placedAt(ship: Locator, page: Page) {
  const [s, w] = [await ship.boundingBox(), await water(page).boundingBox()];
  if (!s || !w) throw new Error('not visible, no box');
  return { x: (s.x - w.x) / w.width, y: (s.y - w.y) / w.height };
}

export const isTouch = (info: TestInfo) => !!info.project.use.hasTouch;

/** A first gesture anywhere, as the engineer's first tap arms audio on iPad. It taps the header's title, not a ship. */
export async function gesture(page: Page, touch: boolean) {
  const title = header(page).getByText('Admiral', { exact: true });
  if (touch) await title.tap();
  else await title.click();
}

/** A second browser looking at the same Admiral, with the project's viewport and input. */
export async function otherBrowser(browser: Browser, info: TestInfo) {
  const { viewport, hasTouch, isMobile } = info.project.use;
  const context = await browser.newContext({ viewport, hasTouch, isMobile });
  return { context, page: await context.newPage() };
}

/** Animations running on a ship and on the header: bobbing, shaking, sailing, flag and pill animations. */
export const animationsOn = (l: Locator) => l.evaluate((el) => el.getAnimations({ subtree: true }).length);

export function sameSpot(a: { x: number; y: number }, b: { x: number; y: number }, tol = 0.012) {
  return Math.abs(a.x - b.x) <= tol && Math.abs(a.y - b.y) <= tol;
}

export const hex = (c: string) => {
  const n = parseInt(c.slice(1), 16);
  return `rgb(${n >> 16}, ${(n >> 8) & 255}, ${n & 255})`;
};
