import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { appendFileSync, chmodSync, existsSync, mkdirSync, readdirSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
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
  const judgeFile = join(a.home, 'fake-judge');
  const dir = join(a.home, 'fake-bin');
  mkdirSync(dir, { recursive: true });
  const wrapper = join(dir, 'claude');
  writeFileSync(wrapper, `#!/bin/sh\n[ -f '${probeFile}' ] && export FAKE_PROBE=$(cat '${probeFile}')\n[ -f '${judgeFile}' ] && export FAKE_JUDGE=$(cat '${judgeFile}')\nexec '${fakeClaude}' "$@"\n`);
  chmodSync(wrapper, 0o755);
  return {
    dock: {
      executable: wrapper,
      env: { CLAUDE_CONFIG_DIR: `${a.home}/identity/zulu-claude`, FAKE_RECORD_DIR: join(a.home, 'fake-record'), FAKE_VERSION_FILE: versionFile },
    },
    /** What the next probe does: pass, skip-hook, run-anyway, no-tool or different-command (docs/agents/testing.md). */
    probe: (mode: string) => writeFileSync(probeFile, mode),
    /** What the next judge probes do: `pass` (default) or `leak`, which makes each one fail (docs/agents/testing.md). */
    judge: (mode: string) => writeFileSync(judgeFile, mode),
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
  // The browser logs each refused launch, ending or work-repo save as a console error, in either order against its response: the form or row showing the refusal is the journey's own check.
  const failedLoads: string[] = [];
  let refusedLaunches = 0;
  page.on('response', (r) => {
    const refusable = { POST: /^\/api\/v1\/(ships|campaigns\/[^/]+\/launch|ships\/[^/]+\/(dismiss|unfollow))$/, PUT: /^\/api\/v1\/campaigns\/[^/]+\/work-repo$/ }[r.request().method()];
    if (refusable?.test(new URL(r.url()).pathname) && r.status() >= 400) refusedLaunches++;
  });
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    (/^Failed to load resource: .* status of [45]\d\d/.test(m.text()) ? failedLoads : errors).push(m.text());
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

const LONG_TITLE = 'Supercalifragilisticexpialidocious_unbroken_title_with_no_spaces_at_all_to_wrap_on_a_narrow_screen_1234567890';
const LONG_REPO = 'an-extraordinarily-long-repository-name-with-many-hyphens-in-it';
export const ORDERS = [
  { id: 'ord-login', title: 'Fix the login redirect', text: 'Fix it.' },
  { id: 'ord-billing', title: 'Port the billing export', text: 'Port it.' },
  // proj is a checkout of acme/api; widget has no checkout, and gadget's clone target is taken.
  { id: 'ord-api', title: 'Tighten the api limits', text: 'Tighten them.', repo: 'https://github.com/acme/api' },
  { id: 'ord-widget', title: 'Ship the widget fix', text: 'Fix the widget.', repo: 'https://github.com/acme/widget' },
  { id: 'ord-gadget', title: 'Retire the gadget', text: 'Retire it.', repo: 'https://github.com/acme/gadget' },
  // Long content the launch form must wrap or clip, never pan: a title with no break and a repo whose clone path is long.
  { id: 'ord-longtitle', title: LONG_TITLE, text: 'Wrap it.' },
  { id: 'ord-longrepo', title: 'Port the long repo', text: 'Port it.', repo: `https://github.com/acme-corporation-international/${LONG_REPO}` },
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

/** The cue makes the harness exit (0 sails off, 3 anchors); `cues` are cue lines the harness runs instead; without either it idles. */
export async function launchShip(
  a: Admiral,
  req: { fleet: 'zulu' | 'alpha'; harness: Harness; order?: string; exit?: 0 | 3; cue?: string; cues?: string[]; dir?: string },
): Promise<LaunchedShip> {
  const cue = join(a.home, 'cue');
  if (req.cue !== undefined) writeFileSync(cue, req.cue);
  else if (req.cues) writeFileSync(cue, req.cues.join('\n') + '\n');
  else if (req.exit === undefined) rmSync(cue, { force: true });
  else writeFileSync(cue, `exit ${req.exit}\n`);
  const body = {
    fleet: req.fleet,
    harness: req.harness,
    dir: req.dir ?? join(a.home, 'proj'),
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
  // The harness reads its cue file once, so the file stays until it is ready for input.
  if (req.cues) await expect.poll(() => existsSync(join(a.home, 'rec', ship.session, 'cues-done'))).toBe(true);
  if (req.cue === undefined) rmSync(cue, { force: true });
  return ship;
}

export const PR_BASE = 'https://github.com/acme/api/pull';

/** What the fake gh's batched poll answers for a PR: any of the words may be '' for none. */
export function prAnswer(a: Admiral, number: number, p: { state: string; review?: string; checks?: string; mergeable?: string }) {
  const key = `${PR_BASE}/${number}`.replace(/[/:]/g, '_');
  for (const [dir, v] of Object.entries({ pr_state: p.state, pr_review: p.review ?? '', pr_checks: p.checks ?? '', pr_mergeable: p.mergeable ?? '' })) {
    mkdirSync(join(a.home, 'fakegh', dir), { recursive: true });
    if (v) writeFileSync(join(a.home, 'fakegh', dir, key), v + '\n');
    else rmSync(join(a.home, 'fakegh', dir, key), { force: true });
  }
}

/** The fleet gets a built-in GitHub source, so a clean exit with an open PR moors; call before `start`, after seedFleets. */
export function addGitHubSource(a: Admiral) {
  const path = join(a.home, 'config.json');
  const config = JSON.parse(readFileSync(path, 'utf8'));
  config.sources = { ...config.sources, gh: { plugin: 'github', fleet: 'zulu', name: 'GitHub', settings: { views: [] } } };
  a.writeConfig(config);
  a.fakeGh().token('', 'fake-token-work-1');
}

/** Start environment for a moored journey: LIVE_ENV's hour-long sail-off delay would also keep the ship from mooring. */
export const mooringEnv = (a: Admiral) => ({ ...withFakeGh(a), ADMIRAL_SAIL_OFF_DELAY: '200ms' });

/** The resume text a moored ship's report carries unless a test says otherwise: a skill's own resume invocation. */
export const RESUME = '/commodore resume run-7';

/** Launches a ship whose harness waits, then exits 0 with a report naming the PR: Admiral moors it. `resume` is the report's resume text; `lines` are transcript lines round 0 holds when it moors. */
export async function launchMooredShip(a: Admiral, prNumber: number, opts: { resume?: string; lines?: TranscriptLine[] } = {}): Promise<LaunchedShip> {
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', cue: 'wait go\nexit 0\n' });
  const rec = join(a.home, 'rec', ship.session);
  await expect.poll(() => existsSync(join(rec, 'env'))).toBe(true);
  if (opts.lines) await appendLines(a, ship, opts.lines);
  const report = /^ADMIRAL_REPORT=(.*)$/m.exec(readFileSync(join(rec, 'env'), 'utf8'))?.[1];
  expect(report, 'the harness env names a report path').toBeTruthy();
  writeFileSync(report!, JSON.stringify({ version: 2, result: 'done', why: 'PR opened', prs: [{ url: `${PR_BASE}/${prNumber}`, closes: [] }], resume: opts.resume ?? RESUME }));
  writeFileSync(join(rec, 'go'), '');
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status, { timeout: 30_000 }).toBe('moored');
  return ship;
}

/**
 * Records a later round in a moored ship's history, the file `$ADMIRAL_HOME/moored/rounds/<name>.json` the server reads on every request,
 * with a transcript holding `lines`. The mooring itself recorded round 0.
 */
export function recordRound(a: Admiral, ship: LaunchedShip, round: number, lines: TranscriptLine[]) {
  const id = randomUUID();
  // The server refuses a transcript with a symlink on its path, and the temp dir on a Mac is one.
  const dir = join(realpathSync(a.home), 'identity', 'zulu-claude', 'projects', 'zz');
  mkdirSync(dir, { recursive: true });
  const transcript = join(dir, `${id}.jsonl`);
  writeFileSync(transcript, lines.map((l) => JSON.stringify(l) + '\n').join(''));
  const file = join(a.home, 'moored', 'rounds', `${ship.name}.json`);
  const rounds = existsSync(file) ? (JSON.parse(readFileSync(file, 'utf8')) as unknown[]) : [];
  const now = new Date();
  rounds.push({ round, session_id: id, fleet: 'zulu', transcript, started: new Date(now.getTime() - 60_000).toISOString(), ended: now.toISOString() });
  writeFileSync(file, JSON.stringify(rounds, null, 2) + '\n');
}

/** Readies a moored ship's harness for its wake: the cue that made it exit is gone, and so are the old recordings the fake harness would reuse. */
export function readyToWake(a: Admiral, ship: LaunchedShip) {
  rmSync(join(a.home, 'cue'), { force: true });
  rmSync(join(a.home, 'rec', ship.session), { recursive: true, force: true });
}

/** The prompt a woken ship's harness started with: its last argument, which the fake harness records once it is up. */
export async function wokenPrompt(a: Admiral, ship: LaunchedShip): Promise<string> {
  await expect.poll(() => existsSync(join(a.home, 'rec', ship.session, 'recorded')), { timeout: 30_000 }).toBe(true);
  return readFileSync(join(a.home, 'rec', ship.session, 'argv'), 'utf8').split('\0').slice(0, -1).pop()!;
}

/** The ship view's Wake button, and the box's placeholder before and after a wake. */
export const wakeButton = (page: Page) => page.getByRole('main').getByRole('button', { name: 'Wake', exact: true });
export const WAKE_PLACEHOLDER = 'Wake the ship with this message';

/**
 * Wake by hand from the Ships screen: Wake on a moored row starts a round with the resume text alone, the row leaves the Moored group
 * and the same ship is underway. `press` is the click or tap.
 */
export async function wakeFromTheShell(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  const main = page.getByRole('main');
  const group = main.getByRole('region', { name: /^Moored/ });
  const row = group.getByRole('listitem');

  prAnswer(a, 7, { state: 'OPEN' });
  const ship = await launchMooredShip(a, 7);
  await page.goto(a.url + 'app/ships?fleet=zulu');
  const wake = row.filter({ hasText: ship.name }).getByRole('button', { name: 'Wake', exact: true });
  await expect(wake).toBeEnabled();
  expect((await wake.boundingBox())!.height, 'Wake height').toBeGreaterThanOrEqual(44);
  await expect(wake).toHaveText('Wake');

  readyToWake(a, ship);
  const woke = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/v1/ships/${ship.session}/wake`);
  await press(wake);
  expect((await woke).status()).toBe(201);

  await expect(group).toHaveCount(0);
  await expect(rowLink(page, ship)).toBeVisible();
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status).toMatch(/^(underway|needs_you)$/);
  expect(await wokenPrompt(a, ship)).toBe(RESUME);
}

/**
 * Wake by hand from the ship view: the moored row opens the view, which says the ship is moored and calm about its tabs; a message
 * typed at its box and sent wakes it with the text after the resume text, and the view then shows the live ship.
 */
export async function wakeFromTheShipView(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  prAnswer(a, 7, { state: 'OPEN' });
  const ship = await launchMooredShip(a, 7, { lines: [line.user('Fix the parser'), line.assistant('Round zero reply')] });
  recordRound(a, ship, 1, [line.user('Also handle tabs'), line.assistant('Round one reply')]);
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await press(rowLink(page, ship));
  await expect(page).toHaveURL(shipViewUrl(a, ship));

  const main = page.getByRole('main');
  await expect(main).toContainText(/moored/i);
  await expect(main).toContainText('api#7');
  await expect(wakeButton(page)).toBeEnabled();
  await expect(viewPanel(page)).toContainText(/moored/i);
  await expect(page.getByRole('alert')).toHaveCount(0);
  await viewTab(page, 'Terminal').click();
  await expect(viewPanel(page)).toContainText(/woken/i);
  await expect(page.getByRole('alert')).toHaveCount(0);
  await viewTab(page, 'Changes').click();
  await expect(viewPanel(page).getByText('Loading…')).toHaveCount(0);
  await expect(page.getByRole('alert')).toHaveCount(0);
  await viewTab(page, 'PR').click();
  await expect(viewPanel(page)).toContainText('api#7');
  await viewTab(page, 'Conversation').click();

  // Both rounds, in order, split by a divider with its time, and the moored card last.
  const rounds = viewPanel(page).getByRole('separator');
  await expect(rounds).toHaveCount(1);
  await expect(rounds).toContainText(/^Feedback round 1 · /);
  await expect(viewPanel(page)).not.toContainText('No conversation for this round');
  await expect.poll(async () => viewPanel(page).innerText()).toMatch(/Round zero reply[\s\S]*Feedback round 1[\s\S]*Round one reply[\s\S]*Moored, waiting on review/);
  await expect(viewPanel(page).getByText('Moored, waiting on review')).toBeInViewport();

  await expect(messageBox(page)).toBeEnabled();
  await expect(messageBox(page)).toHaveAttribute('placeholder', WAKE_PLACEHOLDER);
  await messageBox(page).fill('   ');
  await expect(sendButton(page)).toBeDisabled();

  readyToWake(a, ship);
  const woke = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/ships/${ship.session}/wake`);
  await messageBox(page).fill('Also check the docs');
  await press(sendButton(page));
  expect((await woke).postDataJSON()).toEqual({ text: 'Also check the docs' });

  await expect(wakeButton(page)).toHaveCount(0);
  await expect(messageBox(page)).toHaveAttribute('placeholder', /^Message the ship/);
  await expect(messageBox(page)).toHaveValue('');
  await expect(sentBubble(page, 'Also check the docs')).toHaveCount(0);
  await expect(page).toHaveURL(shipViewUrl(a, ship));
  expect(await wokenPrompt(a, ship)).toBe(`${RESUME} Also check the docs`);

  // The woken round reuses the seeded round's number, so the live round replaces it; the view keeps round 0 and the divider shows no state line.
  await expect(viewPanel(page)).toContainText('Round zero reply');
  await expect(viewPanel(page).getByRole('separator').filter({ hasText: 'Feedback round 1' })).toHaveCount(1);
  await expect.poll(async () => viewPanel(page).innerText()).not.toMatch(/Feedback round 1[^\n]*\s*No conversation for this round/);
}

/** A moored ship whose report has no valid resume text cannot be woken: Wake and the box are off, with the reason, on the row and in the view. */
export async function noWakeWithoutResume(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  prAnswer(a, 9, { state: 'OPEN' });
  const ship = await launchMooredShip(a, 9, { resume: 'resume it' });
  await page.goto(a.url + 'app/ships?fleet=zulu');
  const row = page.getByRole('main').getByRole('region', { name: /^Moored/ }).getByRole('listitem').filter({ hasText: ship.name });
  await expect(row.getByRole('button', { name: 'Wake', exact: true })).toBeDisabled();
  await expect(row).toContainText(/resume/i);

  await press(rowLink(page, ship));
  await expect(page).toHaveURL(shipViewUrl(a, ship));
  await expect(wakeButton(page)).toBeDisabled();
  await expect(messageBox(page)).toBeDisabled();
  await expect(viewPanel(page)).toContainText(/resume/i);
  await expect(page.getByRole('alert')).toHaveCount(0);
}

/**
 * Acceptance criterion 3 on the Ships screen of Westwind: the Moored group shows each PR's chips from the batched poll,
 * a later poll's chips arrive by push, and Don't follow reviews and Dismiss each take a ship out. `press` is the click or tap.
 */
export async function mooredFromTheShell(page: Page, a: Admiral, press: (l: Locator) => Promise<void>, opts: { phone: boolean }) {
  const main = page.getByRole('main');
  const group = main.getByRole('region', { name: /^Moored/ });
  const rowOf = (s: LaunchedShip) => group.getByRole('listitem').filter({ hasText: s.name });

  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  prAnswer(a, 8, { state: 'OPEN', review: 'CHANGES_REQUESTED', checks: 'FAILURE', mergeable: 'CONFLICTING' });
  const first = await launchMooredShip(a, 7);
  const second = await launchMooredShip(a, 8);

  // Opening Ships kicks one poll, fire and forget.
  const kicks: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/moored/refresh') kicks.push(r.url());
  });
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await expect(main.locator('.ship-group').getByRole('heading', { level: 2 })).toHaveText([/^Moored\s*2$/]);
  await expect.poll(() => kicks.length).toBe(1);

  // Each row: its PR as a link opening in a new tab, the chips from the poll, and how long it has waited.
  const link = rowOf(first).getByRole('link', { name: 'api#7' });
  await expect(link).toHaveAttribute('href', `${PR_BASE}/7`);
  await expect(link).toHaveAttribute('target', '_blank');
  for (const chip of ['Approved', 'Checks passing', 'Mergeable']) await expect(rowOf(first)).toContainText(chip);
  for (const chip of ['Changes requested', 'Checks failing', 'Conflicts']) await expect(rowOf(second)).toContainText(chip);
  await expect(rowOf(first)).not.toContainText('Conflicts');
  expect(kicks, 'one refresh for opening Ships, not one per render').toHaveLength(1); // the page has settled by now
  await expect(rowOf(first).locator('.ship-age')).toHaveText(/^(now|\d+m)$/);
  await expect(rowOf(first)).not.toContainText(/undefined|null/);
  if (opts.phone) expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);
  for (const name of ['Dismiss', "Don't follow reviews"]) {
    expect((await rowOf(first).getByRole('button', { name }).boundingBox())!.height, `${name} height`).toBeGreaterThanOrEqual(44);
  }

  // A later poll's chips arrive by push, with no reload.
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'PENDING', mergeable: 'MERGEABLE' });
  const refresh = await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
  expect(refresh.status).toBe(202);
  await expect(rowOf(first)).toContainText('Checks pending');
  await expect(rowOf(first)).not.toContainText('Checks passing');

  // A record that cannot be removed leaves the ship in place with the reason on its row, and no path in it.
  const moored = join(a.home, 'moored');
  chmodSync(moored, 0o500);
  try {
    await press(rowOf(first).getByRole('button', { name: 'Dismiss' }));
    await expect(rowOf(first).getByRole('alert')).toContainText('its moored record cannot be removed');
    await expect(rowOf(first).getByRole('alert')).not.toContainText(a.home);
    await expect(rowOf(first)).toBeVisible();
  } finally {
    chmodSync(moored, 0o700);
  }

  // Don't follow reviews takes the ship out of the list.
  const unfollowed = page.waitForResponse((r) => new URL(r.url()).pathname === `/api/v1/ships/${first.session}/unfollow`);
  await press(rowOf(first).getByRole('button', { name: "Don't follow reviews" }));
  expect((await unfollowed).status()).toBe(200);
  await expect(rowOf(first)).toHaveCount(0);
  await expect(rowOf(second)).toBeVisible();

  // Dismiss takes the other out by keyboard, with no confirm dialog, and the empty group goes with it.
  const dismissed = page.waitForResponse((r) => new URL(r.url()).pathname === `/api/v1/ships/${second.session}/dismiss`);
  await rowOf(second).getByRole('button', { name: 'Dismiss' }).focus();
  await page.keyboard.press('Enter');
  expect((await dismissed).status()).toBe(200);
  await expect(rowOf(second)).toHaveCount(0);
  await expect(group).toHaveCount(0);
  expect((await listShips(a)).filter((s) => s.status === 'moored')).toEqual([]);
}

/**
 * Launches a crew boat for the ship, which counts in its `crew_live`. `role` is the boat's `--role`. With no cues it idles;
 * `cues` are the boat's cue lines, which share the ship's cue file, so launch it after the ship's own cues are read.
 */
export async function launchCrew(a: Admiral, ship: LaunchedShip, name: string, role?: string, cues?: string[]) {
  const cue = join(a.home, 'cue');
  if (cues) writeFileSync(cue, cues.join('\n') + '\n');
  const env = { ...process.env, ADMIRAL_HOME: a.home, ADMIRAL_SHIP: ship.session, FAKE_RECORD_DIR: join(a.home, 'rec'), FAKE_CUE: cue };
  const args = ['crew', 'launch', '--name', name, ...(role ? ['--role', role] : []), '--harness', 'claude', '--dir', join(a.home, 'proj'), '--', 'go'];
  const r = spawnSync(BIN, args, { env, encoding: 'utf8' });
  expect(r.status, r.stderr).toBe(0);
  // The harness reads its cue file once, so the file stays until it is ready.
  if (cues) await expect.poll(() => existsSync(join(a.home, 'rec', ship.session, 'crew', name, cues.includes('record') ? 'recording' : 'cues-done'))).toBe(true);
  rmSync(cue, { force: true });
}

const tmux = (a: Admiral, ...args: string[]) => spawnSync('tmux', ['-S', join(a.home, 'tmux.sock'), ...args], { encoding: 'utf8' });

/** What a harness hook does: `needs_you` makes the ship ask for you, `working` puts it underway. */
export function setState(a: Admiral, ship: LaunchedShip, state: 'needs_you' | 'working') {
  const r = tmux(a, 'set', '-p', '-t', ship.session, '@admiral-state', state);
  expect(r.status, r.stderr).toBe(0);
}

/** What a harness hook records beside the state: why the pane needs you (`permission:Bash`, `question`, `stop`…). With `boat`, on that crew boat's pane. */
export function setReason(a: Admiral, ship: LaunchedShip, reason: string, boat?: string) {
  const target = boat === undefined ? ship.session : boatPane(a, ship, boat);
  const r = tmux(a, 'set', '-p', '-t', target, '@admiral-reason', reason);
  expect(r.status, r.stderr).toBe(0);
}

type TranscriptLine = Record<string, unknown>;

const stamp = () => ({ uuid: `l-${Date.now()}-${Math.random().toString(36).slice(2)}`, timestamp: new Date().toISOString() });

/** Transcript lines in the shapes Claude Code writes and the conversation adapter reads. */
export const line = {
  assistant: (text: string): TranscriptLine => ({ type: 'assistant', ...stamp(), message: { role: 'assistant', id: `msg_${Date.now()}`, content: [{ type: 'text', text }] } }),
  user: (text: string): TranscriptLine => ({ type: 'user', ...stamp(), message: { role: 'user', content: text } }),
  toolUse: (id: string, name: string, input: Record<string, unknown>): TranscriptLine => ({
    type: 'assistant',
    ...stamp(),
    message: { role: 'assistant', id: `msg_${Date.now()}`, content: [{ type: 'tool_use', id, name, input }] },
  }),
  toolResult: (id: string, output: string): TranscriptLine => ({
    type: 'user',
    ...stamp(),
    message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: id, content: output }] },
  }),
  /** An AskUserQuestion call with no result yet: a pending question item per entry of `questions`. */
  ask: (id: string, questions: { question: string; options: string[]; multiSelect?: boolean }[]): TranscriptLine =>
    line.toolUse(id, 'AskUserQuestion', {
      questions: questions.map((q) => ({ question: q.question, header: 'Q', multiSelect: q.multiSelect ?? false, options: q.options.map((label) => ({ label, description: '' })) })),
    }),
  turnEnd: (durationMs: number): TranscriptLine => ({ type: 'system', subtype: 'turn_duration', durationMs, ...stamp() }),
  compaction: (): TranscriptLine => ({ type: 'system', subtype: 'compact_boundary', ...stamp() }),
};

/** Appends lines to a Claude ship's transcript, which becomes the ship's conversation and its tail. */
export async function appendLines(a: Admiral, ship: LaunchedShip, lines: TranscriptLine[], fleet: 'zulu' | 'alpha' = 'zulu') {
  const argv = join(a.home, 'rec', ship.session, 'argv');
  await expect.poll(() => existsSync(join(a.home, 'rec', ship.session, 'recorded'))).toBe(true);
  const args = readFileSync(argv, 'utf8').split('\0');
  const id = args[args.indexOf('--session-id') + 1];
  // Admiral records the transcript's path once the ship is launched; reads find the file by session id.
  await expect.poll(() => tmux(a, 'show-options', '-pv', '-t', ship.session, '@admiral-transcript').stdout.trim()).not.toBe('');
  const dir = join(a.home, 'identity', `${fleet}-claude`, 'projects', 'zz');
  mkdirSync(dir, { recursive: true });
  appendFileSync(join(dir, `${id}.jsonl`), lines.map((l) => JSON.stringify(l) + '\n').join(''));
}

/** Appends an assistant message to a Claude ship's transcript, which becomes the ship's conversation tail. */
export const say = (a: Admiral, ship: LaunchedShip, text: string, fleet: 'zulu' | 'alpha' = 'zulu') => appendLines(a, ship, [line.assistant(text)], fleet);

/** What the raw HTML in the reply must show as text: an image that runs a script when it fails to load, and a script tag. */
export const HOSTILE = '<img src=x onerror="window.__xss=1"> <script>window.__xss=1</script>';

/**
 * A finished turn: a user message, Markdown with a heading, a list and hostile HTML, two reads and two commands,
 * a six-minute turn end and a compaction.
 */
export function seedTurn(a: Admiral, ship: LaunchedShip) {
  return appendLines(a, ship, [
    line.user('Rebase onto main please'),
    line.assistant('## Plan\n\n- rebase the branch\n- run the tests'),
    line.toolUse('t1', 'Read', { file_path: '/src/a.go' }),
    line.toolResult('t1', 'contents of a'),
    line.toolUse('t2', 'Read', { file_path: '/src/b.go' }),
    line.toolResult('t2', 'contents of b'),
    line.toolUse('t3', 'Bash', { command: 'git rebase main' }),
    line.toolResult('t3', 'rebased cleanly'),
    line.toolUse('t4', 'Bash', { command: 'go test ./...' }),
    line.toolResult('t4', 'all green'),
    line.assistant(`Done. ${HOSTILE}`),
    line.turnEnd(360_000),
    line.compaction(),
  ]);
}

/** The pane's size as tmux has it, `<cols>x<rows>`. */
export const paneSize = (a: Admiral, ship: LaunchedShip) => tmux(a, 'display', '-p', '-t', ship.session, '#{window_width}x#{window_height}').stdout.trim();

/** Asserts that `ok` stays true for `ms`: the way to show that something does not happen. */
export async function holds(ok: () => boolean, what: string, ms = 1500) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    expect(ok(), what).toBe(true);
    await new Promise((r) => setTimeout(r, 100));
  }
}

/** The ship view's tabs, and the link that opens the view from the ship's row. */
export const viewTab = (page: Page, name: 'Conversation' | 'Terminal' | 'Changes' | 'PR') => page.getByRole('tab', { name, exact: true });
export const rowLink = (page: Page, ship: LaunchedShip) =>
  page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name }).getByRole('link', { name: new RegExp(ship.name) });
export const viewPanel = (page: Page) => page.getByRole('tabpanel');
export const shipViewUrl = (a: Admiral, ship: LaunchedShip) => new RegExp(`/app/ships/${encodeURIComponent(ship.session)}\\?(.*&)?fleet=zulu`);

/** What the view shows of seedTurn's turn: both ends of it as text, Markdown rendered, hostile HTML inert, tool calls folded. */
export async function expectSeededTurn(page: Page) {
  const panel = viewPanel(page);
  await expect(panel).toContainText('Rebase onto main please');
  await expect(panel.getByRole('heading', { name: 'Plan' })).toBeVisible();
  await expect(panel.getByRole('listitem').filter({ hasText: 'rebase the branch' })).toBeVisible();
  await expect(panel.getByText('<img src=x onerror="window.__xss=1">')).toBeVisible();
  await expect(panel.locator('img[src="x"], script')).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  const folded = panel.getByRole('button', { name: /^Worked for 6m · read 2 files, ran 2 commands/ });
  await expect(folded).toBeVisible();
  await expect(panel).not.toContainText('rebased cleanly');
  await folded.click();
  await expect(panel).toContainText('rebased cleanly');
  await expect(panel.getByRole('separator')).toHaveCount(1);
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
  // The form is fixed, so only its own boxes show an overflow: the dialog and the body that scrolls inside it.
  const fits = () =>
    page.evaluate(() =>
      ['html', 'main', '[role="dialog"].launch-form', '.launch-body'].map((sel) => {
        const e = document.querySelector(sel);
        if (!e) throw new Error(`fits: ${sel} matches nothing`);
        return e.scrollWidth <= e.clientWidth;
      }).every(Boolean),
    );
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

  // Autopilot: the server refuses it on a dock whose guard probe has not passed, and the form shows why. The cap's 409 is pinned in the Go e2e.
  await form.getByRole('radio', { name: /codex/i }).check();
  await form.getByRole('checkbox', { name: /autopilot/i }).check();
  const notClaude = await send();
  expect(notClaude.body).toMatchObject({ harness: 'codex', autopilot: true });
  expect(notClaude.res.status()).toBe(400);
  const notClaudeMsg = ((await notClaude.res.json()) as { error: string }).error;
  await expect(form.getByRole('alert')).toContainText(notClaudeMsg);
  await form.getByRole('radio', { name: /claude/i }).check();
  const unproven = await send();
  expect(unproven.body).toMatchObject({ harness: 'claude', autopilot: true });
  expect(unproven.res.status()).toBe(400);
  const unprovenMsg = ((await unproven.res.json()) as { error: string }).error;
  await expect(form.getByRole('alert')).toContainText(unprovenMsg);
  await expect(form.getByRole('alert')).toContainText('guard probe has not passed');
  await cancel();

  // Pinned crew: the playwright server has no Commodore, so the claude dock's identity gets one with a built-in member.
  const commodoreDir = join(a.home, 'identity', 'zulu-claude', 'skills', 'commodore');
  mkdirSync(join(commodoreDir, 'crew'), { recursive: true });
  writeFileSync(join(commodoreDir, 'SKILL.md'), '---\nname: commodore\ndescription: a fake skill\n---\n');
  const scout = '---\nname: scout\nrole: scribe\npick-me-when: Pick scout.\n---\n\nWork as scout.\n';
  writeFileSync(join(commodoreDir, 'crew', 'scout.md'), scout);
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form).toBeVisible();
  await form.getByLabel('Checkout').selectOption(proj);
  await form.getByLabel('Skill').selectOption({ label: 'Commodore' });
  const crewGroup = form.getByRole('group', { name: /crew/i });
  await crewGroup.getByRole('checkbox', { name: /scout/ }).check();
  // Just launch takes no pins, so the control goes away and nothing is sent for them.
  await form.getByLabel('Skill').selectOption({ label: 'Just launch' });
  await expect(crewGroup).toBeHidden();
  await form.getByLabel('Skill').selectOption({ label: 'Commodore' });
  await crewGroup.getByRole('checkbox', { name: /scout/ }).check();
  await form.getByLabel('Text').fill('with a pinned scout');
  const pinned = await send();
  expect(pinned.body).toMatchObject({ skill: { kind: 'commodore' }, crew: ['scout'] });
  const pinnedShip = await launched(pinned);
  await expect(form).toBeHidden();
  await expect(main.getByRole('listitem').filter({ hasText: pinnedShip.name })).toBeVisible();
  const recorded = join(a.home, 'rec', pinnedShip.session);
  await expect.poll(() => existsSync(join(recorded, 'recorded'))).toBe(true);
  const runCrew = readFileSync(join(recorded, 'env'), 'utf8')
    .split('\n')
    .find((l) => l.startsWith('ADMIRAL_RUN_CREW='))
    ?.slice('ADMIRAL_RUN_CREW='.length);
  expect(runCrew, 'the pinned ship knows its run layer').toBeTruthy();
  expect(readFileSync(join(runCrew!, 'scout.md'), 'utf8')).toBe(scout);
  setState(a, pinnedShip, 'working');
  // Later steps start from Just launch, as before.
  rmSync(commodoreDir, { recursive: true, force: true });

  // Launch as a member: Just launch offers the fleet's members, and a member locked to codex runs there with its instructions and notes.
  const zuluCrew = join(a.home, 'fleets', 'zulu', 'crew');
  mkdirSync(join(zuluCrew, 'memory', 'ada'), { recursive: true });
  writeFileSync(join(zuluCrew, 'ada.md'), '---\nname: ada\nrole: implementer\nharness: codex\npick-me-when: Pick ada.\n---\n\nWork as ada.\n');
  writeFileSync(join(zuluCrew, 'memory', 'ada', 'fleet.md'), '- 2026-01-02 (run-1): ada knows the fleet\n');
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form).toBeVisible();
  await form.getByLabel('Checkout').selectOption(proj);
  await form.getByLabel('Skill').selectOption({ label: 'Just launch' });
  const launchAs = form.getByLabel('Launch as');
  await expect(launchAs).toHaveValue('');
  await launchAs.selectOption({ value: 'ada' });
  await expect(form).toContainText('runs on codex');
  await form.getByLabel('Text').fill('as ada');
  const asMember = await send();
  expect(asMember.body).toMatchObject({ skill: { kind: 'none' }, member: 'ada', harness: 'codex' });
  expect(asMember.body).not.toHaveProperty('crew');
  const memberShip = await launched(asMember);
  await expect(form).toBeHidden();
  const memberRec = join(a.home, 'rec', memberShip.session);
  await expect.poll(() => existsSync(join(memberRec, 'recorded'))).toBe(true);
  const memberArgv = readFileSync(join(memberRec, 'argv'), 'utf8');
  expect(memberArgv).toContain('Work as ada.');
  expect(memberArgv).toContain('ada knows the fleet');
  expect(memberArgv).toContain('as ada');
  setState(a, memberShip, 'working');
  rmSync(zuluCrew, { recursive: true, force: true });

  // An uncloned repo: the form says where it will clone before launch, and launching sends clone with no dir.
  const dirsAnswer = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/launch-orders/dirs');
  await press(orderLaunch('Ship the widget fix'));
  const shown = ((await (await dirsAnswer).json()) as { clone: { repo: string } }).clone.repo;
  const widget = join(a.home, 'widget');
  await expect(form).toContainText(/Clone .*acme\/widget.* into /);
  await expect(form).toContainText(widget);
  if (opts.phone) expect(await fits()).toBe(true);
  const cloned = await send();
  expect(cloned.body).toMatchObject({ fleet: 'zulu', source: 'orders', order_id: 'ord-widget', clone: true, clone_repo: shown });
  expect(shown).toBe('github.com/acme/widget');
  expect(cloned.body).not.toHaveProperty('dir');
  const widgetShip = await launched(cloned);
  await expect(main.getByRole('listitem').filter({ hasText: widgetShip.name })).toBeVisible();
  expect(existsSync(widget)).toBe(true);

  // Long content: the form neither pans sideways nor shrinks a select under the touch target.
  // One Orders heading, and a group heading naming the source alone.
  const orders = page.getByRole('main').locator('section.orders');
  await expect(orders.getByRole('heading', { level: 2 })).toHaveText(['Orders']);
  await expect(orders.getByRole('heading', { level: 3 })).toHaveText(['Orders']);

  for (const title of ['Supercalifragilistic', 'Port the long repo']) {
    await press(orderLaunch(title));
    await expect(form).toBeVisible();
    // The checkout list is in: its longest entry is what can widen the form.
    await expect(form.getByLabel('Checkout').locator('option')).not.toHaveCount(1);
    expect(await fits()).toBe(true);
    for (const name of ['Checkout', 'Skill', 'Model', 'Effort']) {
      expect((await form.getByLabel(name).boundingBox())!.height, `${name} select height`).toBeGreaterThanOrEqual(44);
    }
    // Tab and Shift+Tab ring through the dialog's controls and never reach the page behind it.
    for (const key of ['Tab', 'Shift+Tab']) {
      for (let i = 0; i < 30; i++) {
        await page.keyboard.press(key);
        expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"].launch-form')), `${key} #${i} stays in the dialog`).toBe(true);
      }
    }
    await cancel();
  }

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

export const messageBox = (page: Page) => viewPanel(page).getByRole('textbox', { name: 'Message' });
export const sendButton = (page: Page) => viewPanel(page).getByRole('button', { name: 'Send', exact: true });
export const interruptButton = (page: Page) => page.getByRole('button', { name: 'Interrupt', exact: true });
export const stopShipButton = (page: Page) => page.getByRole('button', { name: 'Stop ship', exact: true });
/** The dimmed bubble that shows a message from the moment it is sent until the conversation shows it. */
export const sentBubble = (page: Page, text: string) => viewPanel(page).getByRole('status').filter({ hasText: text });
export const stopDialog = (page: Page) => page.getByRole('dialog', { name: 'Stop ship' });

/** What the fake harness's pane received, without the bracketed-paste markers a paste wraps around the text. */
export const received = (a: Admiral, ship: LaunchedShip) => readFileSync(join(a.home, 'rec', ship.session, 'stdin'), 'utf8').replaceAll('\x1b[200~', '').replaceAll('\x1b[201~', '');

/** The launched, ended and stopped events the ship's outcome log holds, in order (the log is `$ADMIRAL_HOME/outcomes/<YYYY-MM>.jsonl`). */
export function outcomeEvents(a: Admiral, ship: LaunchedShip): string[] {
  const dir = join(a.home, 'outcomes');
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .filter((f) => f.endsWith('.jsonl'))
    .sort()
    .flatMap((f) => readFileSync(join(dir, f), 'utf8').split('\n'))
    .filter(Boolean)
    .map((l) => JSON.parse(l) as { ship: string; event: string })
    .filter((r) => r.ship === ship.session)
    .map((r) => r.event)
    // The log also notes each status change the server happened to see, which depends on how long a journey takes.
    .filter((e) => ['launched', 'ended', 'stopped'].includes(e));
}

const GIT = ['-c', 'user.name=Admiral Test', '-c', 'user.email=test@example.com', '-c', 'commit.gpgsign=false'];
const LONG_LINE = 'a-very-long-line-in-the-working-copy-that-no-phone-screen-can-show-without-scrolling-sideways-' + 'x'.repeat(200);

/**
 * A checkout (not `proj`, which has no commit) on a branch with its own commit, an uncommitted edit and an untracked file:
 * `a.txt` is modified (+2 −1, one line very long), `b.txt` is added on the branch and `c.txt` is untracked.
 * `clean` leaves it with the branch's commit only.
 */
export function gitCheckout(a: Admiral, name: string, opts: { clean?: boolean } = {}): string {
  const dir = join(a.home, name);
  mkdirSync(dir, { recursive: true });
  const git = (...args: string[]) => {
    const r = spawnSync('git', [...GIT, ...args], { cwd: dir, encoding: 'utf8', env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_SYSTEM: '/dev/null' } });
    expect(r.status, `git ${args.join(' ')}: ${r.stderr}`).toBe(0);
  };
  git('init', '-q', '-b', 'main');
  writeFileSync(join(dir, 'a.txt'), 'one\ntwo\nthree\n');
  git('add', '.');
  git('commit', '-q', '-m', 'first');
  git('checkout', '-q', '-b', 'feature');
  writeFileSync(join(dir, 'b.txt'), 'new one\nnew two\n');
  git('add', '.');
  git('commit', '-q', '-m', 'second');
  if (!opts.clean) {
    writeFileSync(join(dir, 'a.txt'), `one\nTWO\nthree\n${LONG_LINE}\n`);
    writeFileSync(join(dir, 'c.txt'), 'loose\n');
  }
  return dir;
}

/** What GET /api/v1/ships/<ship>/changes answers. */
export async function changesOf(a: Admiral, ship: LaunchedShip): Promise<{ state: string; files: { path: string; status: string }[] }> {
  const res = await fetch(`${a.url}api/v1/ships/${encodeURIComponent(ship.session)}/changes`);
  return (await res.json()) as { state: string; files: { path: string; status: string }[] };
}

/** The Changes tab: the file list, one entry per file, and the diff of a file as a region named for its path. */
export const changeFile = (page: Page, path: string) => viewPanel(page).getByRole('listitem').filter({ hasText: path });
export const diffOf = (page: Page, path: string) => viewPanel(page).getByRole('region', { name: new RegExp(path.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')) });

/** The flotilla: one radio for the ship and one per crew boat, named by the ship's name, or the boat's role (Teammate when none) and name. */
export const flotilla = (page: Page) => page.getByRole('main').getByRole('radiogroup');
export const chip = (page: Page, name: string | RegExp) => flotilla(page).getByRole('radio', { name });

const boatDir = (a: Admiral, ship: LaunchedShip, name: string) => join(a.home, 'rec', ship.session, 'crew', name);

/** What the boat's pane received, without the bracketed-paste markers a paste wraps around the text. */
export const boatReceived = (a: Admiral, ship: LaunchedShip, name: string) => {
  const f = join(boatDir(a, ship, name), 'stdin');
  return existsSync(f) ? readFileSync(f, 'utf8').replaceAll('\x1b[200~', '').replaceAll('\x1b[201~', '') : '';
};

/** Lets a boat whose cues hold `wait done` go on to its next cue. */
export const releaseBoat = (a: Admiral, ship: LaunchedShip, name: string) => writeFileSync(join(boatDir(a, ship, name), 'done'), '');

type BoatJSON = { name: string; status: string; conversation: boolean; role?: string; exit_code: number | null };

/** The boat's row on the ships API. */
export async function boatOf(a: Admiral, ship: LaunchedShip, name: string): Promise<BoatJSON | undefined> {
  const res = await fetch(`${a.url}api/v1/ships`);
  const list = ((await res.json()) as { ships: (LaunchedShip & { crew: BoatJSON[] })[] }).ships;
  return list.find((s) => s.session === ship.session)?.crew.find((c) => c.name === name);
}

function boatTranscript(a: Admiral, ship: LaunchedShip, name: string): string {
  // The server refuses a transcript with a symlink on its path, and the temp dir on a Mac is one.
  const dir = join(realpathSync(a.home), 'identity', 'zulu-claude', 'projects', 'zz');
  mkdirSync(dir, { recursive: true });
  return join(dir, `boat-${ship.session.replace(/\W/g, '-')}-${name}.jsonl`);
}

function boatPane(a: Admiral, ship: LaunchedShip, name: string): string {
  const panes = tmux(a, 'list-panes', '-s', '-t', ship.session, '-F', '#{pane_id} #{@admiral-crew-name}').stdout.split('\n');
  const pane = panes.find((p) => p.endsWith(` ${name}`))?.split(' ')[0];
  expect(pane, `no pane for boat ${name} in ${panes.join(', ')}`).toBeTruthy();
  return pane!;
}

/**
 * Gives the Claude boat a transcript, the way its hooks would record one: a regular file under the fleet's `projects/`,
 * named on the boat's pane. Waits for the ships API to show the boat's `conversation: true`.
 */
export async function seedBoatTranscript(a: Admiral, ship: LaunchedShip, name: string, lines: TranscriptLine[]) {
  const file = boatTranscript(a, ship, name);
  writeFileSync(file, lines.map((l) => JSON.stringify(l) + '\n').join(''));
  const r = tmux(a, 'set', '-p', '-t', boatPane(a, ship, name), '@admiral-crew-transcript', file);
  expect(r.status, r.stderr).toBe(0);
  await expect.poll(async () => (await boatOf(a, ship, name))?.conversation).toBe(true);
}

/** Appends lines to the boat's transcript, which becomes its conversation tail. */
export const boatSays = (a: Admiral, ship: LaunchedShip, name: string, lines: TranscriptLine[]) =>
  appendFileSync(boatTranscript(a, ship, name), lines.map((l) => JSON.stringify(l) + '\n').join(''));

/** What a harness hook does to a boat: puts it `working` or `needs_you`. */
export function setBoatState(a: Admiral, ship: LaunchedShip, name: string, state: 'needs_you' | 'working') {
  const r = tmux(a, 'set', '-p', '-t', boatPane(a, ship, name), '@admiral-state', state);
  expect(r.status, r.stderr).toBe(0);
}
