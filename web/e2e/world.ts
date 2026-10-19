import type { Locator, Page } from '@playwright/test';
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, realpathSync, renameSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Admiral } from './fixtures';

// Helpers for the launch scenarios (u9). A World is one Admiral's fleets,
// fake checkouts, fake skills and fake harnesses, all under the temp
// ADMIRAL_HOME of the per-test fixture. The shapes follow u6's Go helpers
// (e2e/launchdata_helpers_test.go): real `git init` checkouts under a search
// root, `<name>/SKILL.md` folders under each dock's own identity directory.

const fakeharness = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness');

const identityVar = { claude: 'CLAUDE_CONFIG_DIR', codex: 'CODEX_HOME', copilot: 'COPILOT_HOME' } as const;
export type Harness = keyof typeof identityVar;

export interface Recorded {
  session: string;
  argv: string[];
  cwd: string;
}

export interface WorldOptions {
  /** Adds fleet "bare" (no search roots) and fleet "gone" (its claude executable is missing). */
  refusals?: boolean;
  /** `favorite_skills` in config.json. */
  favorites?: string[];
}

export class World {
  /** The real path of the temp ADMIRAL_HOME: the fakes record `pwd -P`. */
  readonly root: string;
  readonly work: string;
  private readonly rec: string;
  private ended = 0;
  private readonly cue: string;

  constructor(
    readonly a: Admiral,
    private readonly opts: WorldOptions = {},
  ) {
    this.root = realpathSync(a.home);
    this.work = join(this.root, 'work');
    this.rec = join(this.root, 'rec');
    this.cue = join(this.root, 'cue');
    for (const d of [this.work, this.rec]) mkdirSync(d, { recursive: true });
    // Every fake ship reports `working` first, so it counts as underway.
    writeFileSync(this.cue, 'hook working\n');
  }

  // ---- config ----

  /** The identity directory of one dock. No two docks share one. */
  identity(fleet: string, harness: Harness) {
    return join(this.root, 'id', `${fleet}-${harness}`);
  }

  private dock(fleet: string, harness: Harness, executable = join(fakeharness, harness)) {
    const home = join(this.root, 'fh');
    mkdirSync(home, { recursive: true });
    return {
      executable,
      env: {
        FAKE_RECORD_DIR: this.rec,
        FAKE_CUE: this.cue,
        HOME: home,
        [identityVar[harness]]: this.identity(fleet, harness),
      },
    };
  }

  /** Writes config.json: fleets work (Claude, Codex) and home (Claude, Codex), searching `work/`. */
  writeConfig() {
    const fleets: { [key: string]: unknown } = {
      work: {
        name: 'Work',
        search_roots: [this.work],
        claude: this.dock('work', 'claude'),
        codex: this.dock('work', 'codex'),
      },
      home: {
        name: 'Home',
        search_roots: [this.work],
        claude: this.dock('home', 'claude'),
        codex: this.dock('home', 'codex'),
      },
    };
    if (this.opts.refusals) {
      fleets.bare = { name: 'Bare', search_roots: [], claude: this.dock('bare', 'claude') };
      fleets.gone = {
        name: 'Gone',
        search_roots: [this.work],
        claude: this.dock('gone', 'claude', this.a.missingHarness('claude')),
      };
    }
    const config: { [key: string]: unknown } = { version: 1, fleets };
    if (this.opts.favorites) config.favorite_skills = this.opts.favorites;
    this.a.writeConfig(config);
  }

  // ---- fake checkouts and skills ----

  /** A real git checkout at work/<rel>. */
  checkout(rel: string) {
    const dir = join(this.work, rel);
    mkdirSync(dir, { recursive: true });
    // A clean environment: the engineer's git config must not reach the fake.
    const env = { PATH: process.env.PATH ?? '', HOME: join(this.root, 'gh'), GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null' };
    execFileSync('git', ['init', '-q'], { cwd: dir, env });
    return dir;
  }

  removeCheckout(dir: string) {
    rmSync(dir, { recursive: true, force: true });
  }

  /** <base>/<name>/SKILL.md */
  skillIn(base: string, name: string) {
    const dir = join(base, name);
    mkdirSync(dir, { recursive: true });
    writeFileSync(join(dir, 'SKILL.md'), `---\nname: ${name}\ndescription: a fake skill\n---\nbody\n`);
  }

  /** An installed skill of one dock (in its identity directory's skills folder). */
  skill(fleet: string, harness: Harness, name: string) {
    this.skillIn(join(this.identity(fleet, harness), 'skills'), name);
  }

  /** A project skill of a Claude checkout. */
  projectSkill(checkoutDir: string, name: string) {
    this.skillIn(join(checkoutDir, '.claude', 'skills'), name);
  }

  // ---- the server's API, read straight from it ----

  private async json(method: string, path: string, body?: unknown) {
    const res = await fetch(`${this.a.url}${path}`, {
      method,
      headers: body ? { 'content-type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const text = await res.text();
    let parsed: unknown = text;
    try {
      parsed = JSON.parse(text);
    } catch {
      // not JSON: keep the text
    }
    return { status: res.status, body: parsed as { [key: string]: any } };
  }

  /** The directory list for a dock, asked the way the dialog asks it before an order exists. */
  dirs(fleet: string, harness: Harness, text = '') {
    return this.json('POST', 'api/launch-orders/dirs', { fleet, harness, text });
  }

  skills(fleet: string, harness: Harness, dir: string) {
    const q = new URLSearchParams({ fleet, harness, dir });
    return this.json('GET', `api/launch-orders/skills?${q}`);
  }

  /** The server answers the data the dialog is built on (landed in u6). A failure here is the fixture, not u9. */
  async guardData() {
    const probe = this.checkout('guard');
    const dirs = await this.dirs('work', 'claude');
    if (dirs.status !== 200 || !dirs.body.entries.some((e: { path: string }) => e.path === probe)) {
      throw new Error(`launch-orders dirs is not serving the fixture: ${dirs.status} ${JSON.stringify(dirs.body)}`);
    }
    const skills = await this.skills('work', 'claude', probe);
    if (skills.status !== 200 || typeof skills.body.commodore?.found !== 'boolean') {
      throw new Error(`launch-orders skills is not serving the fixture: ${skills.status} ${JSON.stringify(skills.body)}`);
    }
    this.removeCheckout(probe);
  }

  /** Puts an order into the freeform source (u7's API) and returns when the server has taken it. */
  async seedOrder(text: string) {
    // The freeform plugin starts a moment after the server does: ask until it lists its panel.
    let sources = await this.json('GET', 'api/sources');
    const panelOf = () => {
      const list = (Array.isArray(sources.body) ? sources.body : sources.body.sources) as { key: string; panels: { id: string }[] }[] | undefined;
      return list?.find((s) => s.key === 'orders')?.panels?.[0];
    };
    for (let i = 0; i < 100 && !panelOf(); i++) {
      await new Promise((r) => setTimeout(r, 100));
      sources = await this.json('GET', 'api/sources');
    }
    const orders = { panels: [panelOf()] };
    if (sources.status !== 200 || !orders.panels[0]) {
      throw new Error(`no freeform "orders" source with a panel to seed: ${sources.status} ${JSON.stringify(sources.body)}`);
    }
    const added = await this.json('POST', 'api/sources/orders/orders', { panel: orders.panels[0]!.id, text });
    if (added.status >= 300) throw new Error(`seeding an order failed: ${added.status} ${JSON.stringify(added.body)}`);
  }

  /** Launches straight through the API (no source, so the text is the client's), as a ship that is already at sea. */
  async launchDirect(fleet: string, harness: Harness, dir: string, text: string) {
    const res = await this.json('POST', 'api/ships', { fleet, harness, dir, skill: { kind: 'none' }, text });
    if (res.status !== 201) throw new Error(`direct launch failed: ${res.status} ${JSON.stringify(res.body)}`);
  }

  /** What POST /api/ships answers for a launch that must be refused, as {status, error}. */
  async launchRefusal(fleet: string, harness: Harness, dir: string) {
    const res = await this.json('POST', 'api/ships', { fleet, harness, dir, skill: { kind: 'none' }, text: 'probe' });
    return { status: res.status, error: String(res.body.error ?? res.body) };
  }

  // ---- what the fake harnesses recorded ----

  records(): Recorded[] {
    if (!existsSync(this.rec)) return [];
    return readdirSync(this.rec)
      .filter((s) => existsSync(join(this.rec, s, 'recorded')))
      .sort((x, y) => statSync(join(this.rec, x, 'recorded')).mtimeMs - statSync(join(this.rec, y, 'recorded')).mtimeMs)
      .map((session) => {
        const raw = readFileSync(join(this.rec, session, 'argv'), 'utf8');
        return {
          session,
          argv: raw === '' ? [] : raw.replace(/\0$/, '').split('\0'),
          cwd: readFileSync(join(this.rec, session, 'cwd'), 'utf8').trim(),
        };
      });
  }

  /** The prompt a harness was given: its last argument on Claude Code. */
  static prompt(r: Recorded) {
    return r.argv[r.argv.length - 1];
  }

  /** Ends every fake ship, so a test stays under the CI pty limit. */
  endShips() {
    const sock = join(this.root, 'tmux.sock');
    const list = spawnSync('tmux', ['-S', sock, 'list-sessions', '-F', '#{session_name}'], { encoding: 'utf8' });
    for (const name of list.stdout.split('\n')) {
      if (!name || name === '_admiral') continue;
      spawnSync('tmux', ['-S', sock, 'kill-session', '-t', name]);
      // Ship names are picked at random and a free one can come round again: keep what this one recorded under its own name.
      if (existsSync(join(this.rec, name))) renameSync(join(this.rec, name), join(this.rec, `${name}~${this.ended++}`));
    }
  }
}

// ---- locators ----

export const sidebar = (page: Page) => page.getByRole('complementary', { name: 'Orders', exact: true });
export const marinas = (page: Page) => page.getByRole('region', { name: / marina$/ });
export const marina = (page: Page, fleetName: string) => page.getByRole('region', { name: `${fleetName} marina` });
export const dock = (page: Page, fleetName: string, harness: string) =>
  marina(page, fleetName).getByRole('button', { name: new RegExp(`^${harness} dock, `) });
export const dialog = (page: Page) => page.getByRole('dialog', { name: 'Launch orders' });

export const center = (box: { x: number; y: number; width: number; height: number }) => ({
  x: box.x + box.width / 2,
  y: box.y + box.height / 2,
});

export async function centerOf(l: Locator) {
  const box = await l.boundingBox();
  if (!box) throw new Error('not visible, no box');
  return center(box);
}

/** The text of the page's selection. */
export const selectedText = (page: Page) => page.evaluate(() => String(getSelection()));

// ---- touch ----

/**
 * Playwright's touchscreen only taps (and a tap is real: pointer events, touch
 * events, then a click). A drag is driven here by dispatching pointer events
 * (pointerType "touch", one pointer id) on the element that was pressed, as a
 * pointer-capturing page would receive them. Page code that listens on the
 * document instead gets them by bubbling. Page code hit-tests with
 * elementFromPoint at clientX/clientY, which are real positions.
 */
export class TouchPointer {
  private at = { x: 0, y: 0 };
  private constructor(
    private readonly page: Page,
    private readonly el: Awaited<ReturnType<Locator['elementHandle']>>,
  ) {}

  static async on(target: Locator) {
    const el = await target.elementHandle();
    if (!el) throw new Error('nothing to press');
    return new TouchPointer(target.page(), el);
  }

  private fire(type: 'pointerdown' | 'pointermove' | 'pointerup' | 'pointercancel', x: number, y: number) {
    this.at = { x, y };
    return this.el!.evaluate(
      (el, a) =>
        el.dispatchEvent(
          new PointerEvent(a.type, {
            bubbles: true,
            cancelable: true,
            composed: true,
            pointerId: 7,
            pointerType: 'touch',
            isPrimary: true,
            clientX: a.x,
            clientY: a.y,
            buttons: a.type === 'pointerup' || a.type === 'pointercancel' ? 0 : 1,
            width: 1,
            height: 1,
            pressure: 0.5,
          }),
        ),
      { type, x, y },
    );
  }

  down(p: { x: number; y: number }) {
    return this.fire('pointerdown', p.x, p.y);
  }

  async moveTo(p: { x: number; y: number }, steps = 8) {
    const from = this.at;
    for (let i = 1; i <= steps; i++) {
      await this.fire('pointermove', from.x + ((p.x - from.x) * i) / steps, from.y + ((p.y - from.y) * i) / steps);
    }
  }

  up() {
    return this.fire('pointerup', this.at.x, this.at.y);
  }

  /** What an iPad edge swipe or a system gesture does to a touch in progress. */
  cancel() {
    return this.fire('pointercancel', this.at.x, this.at.y);
  }
}

/**
 * Call before page.goto in a test that drives TouchPointer. A synthetic pointer
 * id is not an active pointer, so WebKit throws NotFoundError from
 * setPointerCapture; a real touch never does. This makes the call a no-op for
 * the synthetic id, so a page that captures the pointer is not failed by the driver.
 */
export function touchSetup(page: Page) {
  return page.addInitScript(() => {
    const real = Element.prototype.setPointerCapture;
    Element.prototype.setPointerCapture = function (id: number) {
      try {
        real.call(this, id);
      } catch {
        // synthetic pointer
      }
    };
  });
}
