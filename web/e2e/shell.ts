import { spawnSync } from 'node:child_process';
import { appendFileSync, chmodSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { type Locator, type Page, expect } from '@playwright/test';
import type { Admiral } from './fixtures';

// Fleet names are unusual words so a text assertion can't match anything else on the page.
export const WORK = 'Westwind';
export const HOME = 'Homeport';

const BIN = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'bin', 'admiral');

const identityVar = { claude: 'CLAUDE_CONFIG_DIR', codex: 'CODEX_HOME', copilot: 'COPILOT_HOME' } as const;
type Harness = keyof typeof identityVar;

/** Live docks idle in their pane so a launched ship stays afloat. */
function dock(a: Admiral, fleet: string, harness: Harness, live: boolean) {
  const env = { [identityVar[harness]]: `${a.home}/identity/${fleet}-${harness}` };
  if (!live) return { executable: a.fakeHarness(harness), env };
  const executable = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness', harness);
  return { executable, env: { ...env, FAKE_RECORD_DIR: join(a.home, 'rec'), FAKE_CUE: join(a.home, 'cue') } };
}

const fakeClaude = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness', 'claude');

/**
 * A claude dock that answers `--version` and headless probe runs like the Go e2e fake harness (e2e/fakeharness).
 * The fake reads FAKE_PROBE from its environment, which a dock fixes at config time, so a wrapper script reads it
 * from a file instead: a test changes what the next probe does between two Check again taps.
 */
export function probeableClaude(a: Admiral) {
  const probeFile = join(a.home, 'fake-probe');
  const versionFile = join(a.home, 'fake-version');
  const dir = join(a.home, 'fake-bin');
  mkdirSync(dir, { recursive: true });
  const wrapper = join(dir, 'claude');
  writeFileSync(wrapper, `#!/bin/sh\n[ -f '${probeFile}' ] && export FAKE_PROBE=$(cat '${probeFile}')\nexec '${fakeClaude}' "$@"\n`);
  chmodSync(wrapper, 0o755);
  return {
    dock: {
      executable: wrapper,
      env: { CLAUDE_CONFIG_DIR: `${a.home}/identity/zulu-claude`, FAKE_RECORD_DIR: join(a.home, 'fake-record'), FAKE_VERSION_FILE: versionFile },
    },
    /** What the next probe does: pass, skip-hook, run-anyway, no-tool or different-command (docs/agents/testing.md). */
    probe: (mode: string) => writeFileSync(probeFile, mode),
    /** What `--version` prints; `FAIL` makes it exit 1. */
    version: (text: string) => writeFileSync(versionFile, text),
  };
}

/** Zulu (Westwind) comes first, alpha (Homeport) second; `live` makes the docks launchable and gives zulu the orders; `claude` replaces zulu's claude dock. */
export function seedFleets(a: Admiral, opts: { live?: boolean; claude?: { executable: string; env: Record<string, string> } } = {}) {
  const live = !!opts.live;
  const config: Record<string, unknown> = {
    version: 1,
    fleets: {
      zulu: { name: WORK, search_roots: [a.home], claude: opts.claude ?? dock(a, 'zulu', 'claude', live), codex: dock(a, 'zulu', 'codex', live) },
      alpha: { name: HOME, search_roots: [a.home], codex: dock(a, 'alpha', 'codex', live), copilot: dock(a, 'alpha', 'copilot', live) },
    },
  };
  if (live) {
    config.sources = { orders: a.ordersSource('orders', 'Orders', { fleet: 'zulu' }) };
    a.flags('orders').orders({ orders: ORDERS });
    mkdirSync(join(a.home, 'proj'), { recursive: true });
    spawnSync('git', ['init', '-q'], { cwd: join(a.home, 'proj') });
    spawnSync('git', ['remote', 'add', 'origin', 'https://github.com/acme/api.git'], { cwd: join(a.home, 'proj') });
    // Taken by a ship that names it, which the launch form lists beside the order's other fields.
    config.resources = { db: {} };
    // A folder where a clone would land, so cloning there clashes.
    mkdirSync(join(a.home, 'gadget'), { recursive: true });
  }
  a.writeConfig(config);
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
  // The browser logs each refused launch as a console error, in either order against its response: the form showing the refusal is the journey's own check.
  const failedLoads: string[] = [];
  let refusedLaunches = 0;
  page.on('response', (r) => {
    if (r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships' && r.status() >= 400) refusedLaunches++;
  });
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    (/^Failed to load resource: .* status of 4\d\d/.test(m.text()) ? failedLoads : errors).push(m.text());
  });
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (['data:', 'blob:', 'about:'].includes(u.protocol)) return;
    if (u.host !== host) foreign.push(r.url());
    paths.push(u.pathname);
  });
  // Playwright reports a WebSocket apart from requests.
  page.on('websocket', (ws) => {
    const u = new URL(ws.url());
    if (u.host !== host) foreign.push(ws.url());
    paths.push(u.pathname);
  });
  return {
    paths,
    foreign,
    get errors() {
      return [...errors, ...failedLoads.slice(refusedLaunches)];
    },
    unversioned: () => paths.filter((p) => /^\/(api|ws)(\/|$)/.test(p) && !/^\/(api|ws)\/v1(\/|$)/.test(p)),
  };
}

export const ORDERS = [
  { id: 'ord-login', title: 'Fix the login redirect', text: 'Fix it.' },
  { id: 'ord-billing', title: 'Port the billing export', text: 'Port it.' },
  // proj is a checkout of acme/api; widget has no checkout, and gadget's clone target is taken.
  { id: 'ord-api', title: 'Tighten the api limits', text: 'Tighten them.', repo: 'https://github.com/acme/api' },
  { id: 'ord-widget', title: 'Ship the widget fix', text: 'Fix the widget.', repo: 'https://github.com/acme/widget' },
  { id: 'ord-gadget', title: 'Retire the gadget', text: 'Retire it.', repo: 'https://github.com/acme/gadget' },
];

/** Admiral's start environment for a test that launches ships: no ship goes idle or sails off on its own. */
export const LIVE_ENV = { ADMIRAL_NO_HOOK_WAIT: '1h', ADMIRAL_SAIL_OFF_DELAY: '1h' };

/** LIVE_ENV plus the fake gh first on PATH, so an uncloned repo can be cloned. */
export function withFakeGh(a: Admiral): Record<string, string> {
  const dir = join(a.home, 'fakegh');
  mkdirSync(dir, { recursive: true });
  const gh = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'testdata', 'fakegh');
  return { ...LIVE_ENV, FAKEGH_DIR: dir, PATH: `${gh}:${process.env.PATH}` };
}

export interface LaunchedShip {
  session: string;
  name: string;
}

type ShipJSON = LaunchedShip & { status: string };

async function listShips(a: Admiral): Promise<ShipJSON[]> {
  const res = await fetch(`${a.url}api/v1/ships`);
  return ((await res.json()) as { ships: ShipJSON[] }).ships;
}

/** The cue makes the harness exit (0 sails off, 3 anchors); without one it idles. */
export async function launchShip(
  a: Admiral,
  req: { fleet: 'zulu' | 'alpha'; harness: Harness; order?: string; exit?: 0 | 3 },
): Promise<LaunchedShip> {
  const cue = join(a.home, 'cue');
  if (req.exit === undefined) rmSync(cue, { force: true });
  else writeFileSync(cue, `exit ${req.exit}\n`);
  const body = {
    fleet: req.fleet,
    harness: req.harness,
    dir: join(a.home, 'proj'),
    skill: { kind: 'none' },
    ...(req.order ? { source: 'orders', order_id: req.order } : { text: 'Go.' }),
  };
  // Admiral answers 503 until it has read the tmux server's sessions.
  let ship!: LaunchedShip;
  await expect
    .poll(async () => {
      const res = await fetch(`${a.url}api/v1/ships`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
      if (res.status === 201) {
        const got = (await res.json()) as LaunchedShip & { ship?: LaunchedShip };
        ship = got.ship ?? got;
      }
      return res.status === 503 ? 'not known yet' : `${res.status} ${ship ? 'launched' : await res.text()}`;
    })
    .toMatch(/^201 launched$/);
  if (req.exit !== undefined) {
    const want = req.exit === 0 ? 'sailing_off' : 'anchored';
    await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status).toBe(want);
  }
  rmSync(cue, { force: true });
  return ship;
}

/** Launches an idling crew boat for the ship, which counts in its `crew_live`. */
export function launchCrew(a: Admiral, ship: LaunchedShip, name: string) {
  const env = { ...process.env, ADMIRAL_HOME: a.home, ADMIRAL_SHIP: ship.session, FAKE_RECORD_DIR: join(a.home, 'rec'), FAKE_CUE: join(a.home, 'cue') };
  const r = spawnSync(BIN, ['crew', 'launch', '--name', name, '--harness', 'claude', '--dir', join(a.home, 'proj'), '--', 'go'], { env, encoding: 'utf8' });
  expect(r.status, r.stderr).toBe(0);
}

const tmux = (a: Admiral, ...args: string[]) => spawnSync('tmux', ['-S', join(a.home, 'tmux.sock'), ...args], { encoding: 'utf8' });

/** What a harness hook does: `needs_you` makes the ship ask for you, `working` puts it underway. */
export function setState(a: Admiral, ship: LaunchedShip, state: 'needs_you' | 'working') {
  const r = tmux(a, 'set', '-p', '-t', ship.session, '@admiral-state', state);
  expect(r.status, r.stderr).toBe(0);
}

/** Appends an assistant message to a Claude ship's transcript, which becomes the ship's conversation tail. */
export async function say(a: Admiral, ship: LaunchedShip, text: string, fleet: 'zulu' | 'alpha' = 'zulu') {
  const argv = join(a.home, 'rec', ship.session, 'argv');
  await expect.poll(() => existsSync(join(a.home, 'rec', ship.session, 'recorded'))).toBe(true);
  const args = readFileSync(argv, 'utf8').split('\0');
  const id = args[args.indexOf('--session-id') + 1];
  // Admiral records the transcript's path once the ship is launched; reads find the file by session id.
  await expect.poll(() => tmux(a, 'show-options', '-pv', '-t', ship.session, '@admiral-transcript').stdout.trim()).not.toBe('');
  const dir = join(a.home, 'identity', `${fleet}-claude`, 'projects', 'zz');
  mkdirSync(dir, { recursive: true });
  const line = {
    type: 'assistant',
    uuid: `a-${Date.now()}-${Math.random().toString(36).slice(2)}`,
    timestamp: new Date().toISOString(),
    message: { role: 'assistant', id: `msg_${Date.now()}`, content: [{ type: 'text', text }] },
  };
  appendFileSync(join(dir, `${id}.jsonl`), JSON.stringify(line) + '\n');
}

type Post = Record<string, unknown>;

/**
 * The launch journey on the Ships screen of the active fleet (Westwind): launching from an order and from the header with
 * freeform text, a skill, a model and effort, resources, autopilot, and the clone of an uncloned repo, each refusal showing
 * in the form. `press` is the click or tap of the device.
 */
export async function launchFromTheShell(page: Page, a: Admiral, press: (l: Locator) => Promise<void>, opts: { phone: boolean }) {
  const main = page.getByRole('main');
  const form = page.getByRole('dialog', { name: 'Launch', exact: true });
  const fits = () => page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth));
  const orderLaunch = (title: string) => main.getByRole('button', { name: new RegExp(`^Launch.*${title}`) });
  const submit = form.getByRole('button', { name: 'Launch', exact: true });
  /** Submits the form and answers what the server received and said. */
  const send = async () => {
    const req = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
    const res = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
    await press(submit);
    return { body: (await req).postDataJSON() as Post, res: await res };
  };
  const launched = async (sent: { res: { status(): number; json(): Promise<unknown> } }) => {
    expect(sent.res.status()).toBe(201);
    return ((await sent.res.json()) as { ship: LaunchedShip }).ship;
  };
  const cancel = async () => {
    await press(form.getByRole('button', { name: 'Cancel', exact: true }));
    await expect(form).toBeHidden();
  };
  const proj = join(a.home, 'proj');

  // From an order: its text, the checkout of its repo, and Just launch (no Commodore, no favorites) are preselected.
  await press(orderLaunch('Tighten the api limits'));
  await expect(form).toBeVisible();
  await expect(form.getByLabel('Text')).toHaveValue('Tighten them.');
  await expect(form.getByLabel('Checkout')).toHaveValue(proj);
  await expect(form.getByLabel('Skill')).toHaveValue(/none|Just launch/i);
  await expect(form).not.toContainText('Clone ');
  if (opts.phone) expect(await fits()).toBe(true);
  const fromOrder = await send();
  expect(fromOrder.body).toMatchObject({ fleet: 'zulu', harness: 'claude', source: 'orders', order_id: 'ord-api', dir: proj, skill: { kind: 'none' } });
  const apiShip = await launched(fromOrder);
  await expect(form).toBeHidden();
  await expect(main.getByRole('listitem').filter({ hasText: apiShip.name })).toBeVisible();
  setState(a, apiShip, 'working');

  // Freeform from the header, with a custom skill, a model, an effort and a resource.
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form).toBeVisible();
  await form.getByLabel('Checkout').selectOption(proj);
  await form.getByLabel('Skill').selectOption({ label: 'Custom' });
  await form.getByLabel('Skill name').fill('review');
  await form.getByLabel('Text').fill('  look at the parser  ');
  await form.getByLabel('Model').selectOption('opus');
  // An effort the harness does not offer is not offered.
  const efforts = form.getByLabel('Effort');
  await expect(efforts.locator('option')).toContainText(['Low', 'Medium', 'High', 'Max'], { useInnerText: true });
  await form.getByRole('radio', { name: /codex/i }).check();
  await expect(efforts.locator('option', { hasText: 'Max' })).toHaveCount(0);
  await form.getByRole('radio', { name: /claude/i }).check();
  await form.getByLabel('Effort').selectOption({ label: 'High' });
  await form.getByRole('checkbox', { name: 'db' }).check();
  if (opts.phone) expect(await fits()).toBe(true);
  const freeform = await send();
  expect(freeform.body).toMatchObject({ fleet: 'zulu', harness: 'claude', dir: proj, skill: { kind: 'skill', name: 'review' }, model: 'opus', effort: 'high', resources: ['db'], autopilot: false });
  expect(freeform.body).not.toHaveProperty('order_id');
  expect(String(freeform.body.text).trim()).toBe('look at the parser');
  const reviewer = await launched(freeform);
  await expect(form).toBeHidden();
  await expect(main.getByRole('listitem').filter({ hasText: reviewer.name })).toBeVisible();
  setState(a, reviewer, 'working');

  // The ship holds db, so a second launch naming it is refused in the form, which stays open.
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await form.getByLabel('Text').fill('also the db');
  await form.getByRole('checkbox', { name: 'db' }).check();
  const taken = await send();
  expect(taken.res.status()).toBe(409);
  await expect(form.getByRole('alert')).toContainText('db');
  await expect(form).toBeVisible();
  await form.getByRole('checkbox', { name: 'db' }).uncheck();

  // Autopilot: the server refuses it on a dock that is not Claude, and at the cap of three ships.
  await form.getByRole('radio', { name: /codex/i }).check();
  await form.getByRole('checkbox', { name: /autopilot/i }).check();
  const notClaude = await send();
  expect(notClaude.body).toMatchObject({ harness: 'codex', autopilot: true });
  expect(notClaude.res.status()).toBe(400);
  const notClaudeMsg = ((await notClaude.res.json()) as { error: string }).error;
  await expect(form.getByRole('alert')).toContainText(notClaudeMsg);
  await form.getByRole('radio', { name: /claude/i }).check();
  const capped = await send();
  expect(capped.body).toMatchObject({ harness: 'claude', autopilot: true });
  expect(capped.res.status()).toBe(409);
  await expect(form.getByRole('alert')).toContainText(((await capped.res.json()) as { error: string }).error);
  await expect(form.getByRole('alert')).toContainText('cap');
  await cancel();

  // An uncloned repo: the form says where it will clone before launch, and launching sends clone with no dir.
  await press(orderLaunch('Ship the widget fix'));
  const widget = join(a.home, 'widget');
  await expect(form).toContainText(/Clone .*acme\/widget.* into /);
  await expect(form).toContainText(widget);
  if (opts.phone) expect(await fits()).toBe(true);
  const cloned = await send();
  expect(cloned.body).toMatchObject({ fleet: 'zulu', source: 'orders', order_id: 'ord-widget', clone: true });
  expect(cloned.body).not.toHaveProperty('dir');
  const widgetShip = await launched(cloned);
  await expect(main.getByRole('listitem').filter({ hasText: widgetShip.name })).toBeVisible();
  expect(existsSync(widget)).toBe(true);

  // A folder already where the clone would land: the form warns and does not preselect the clone, and choosing it anyway shows the server's refusal.
  await press(orderLaunch('Retire the gadget'));
  await expect(form).toContainText(join(a.home, 'gadget'));
  await expect(form).toContainText(/already exists.*refused/);
  await expect(form.getByLabel('Checkout')).not.toHaveValue('clone');
  await form.getByLabel('Checkout').selectOption('clone');
  const clash = await send();
  expect(clash.res.status()).toBe(409);
  await expect(form.getByRole('alert')).toContainText(((await clash.res.json()) as { error: string }).error);
  await expect(form.getByRole('alert')).toContainText('gadget');
  await expect(form).toBeVisible();
  await cancel();
}
