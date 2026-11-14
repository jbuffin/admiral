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

/** Like `fakeHarness`, but answers `--version`, so the server detects a version for it. */
function versionedHarness(a: Admiral, name: string) {
  const path = a.fakeHarness(name);
  writeFileSync(path, '#!/bin/sh\n[ "$1" = "--version" ] && echo "1.2.3 (fake)"\nexit 0\n');
  return path;
}

/** A copilot dock whose probes really pass: it answers `--version` and hands every other run to the e2e fake; its login is the Keychain's. */
function probeableCopilot(a: Admiral) {
  const path = a.fakeHarness('copilot');
  const fake = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness', 'copilot');
  writeFileSync(path, `#!/bin/sh\n[ "$1" = "--version" ] && { echo "1.2.3 (fake)"; exit 0; }\nexec ${JSON.stringify(fake)} "$@"\n`);
  return path;
}

/** A codex dock whose probes really pass: it answers `--version`, hands every other run to the e2e fake, and the engineer's login exists. */
function probeableCodex(a: Admiral, fleet: string) {
  const path = a.fakeHarness('codex');
  const fake = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'fakeharness', 'codex');
  writeFileSync(path, `#!/bin/sh\n[ "$1" = "--version" ] && { echo "1.2.3 (fake)"; exit 0; }\nexec ${JSON.stringify(fake)} "$@"\n`);
  const identity = join(a.home, 'identity', `${fleet}-codex`);
  mkdirSync(identity, { recursive: true });
  writeFileSync(join(identity, 'auth.json'), 'fake-codex-login', { mode: 0o600 });
  return path;
}

/** Zulu (Westwind) comes first, alpha (Homeport) second; `live` makes the docks launchable and gives zulu the orders; `claude` replaces zulu's claude dock. */
export function seedFleets(a: Admiral, opts: { userHome?: string; live?: boolean; chat?: boolean; versioned?: boolean; holdChat?: ChatFleet; claude?: { executable: string; env: Record<string, string> } } = {}) {
  const live = !!opts.live;
  // A version the server can detect, which Check again needs to keep a seeded probe result.
  const codexDock = (fleet: string, harness: Harness) => {
    const d = dock(a, fleet, harness, live);
    if (!opts.versioned || live) return d;
    return { ...d, executable: harness === 'codex' ? probeableCodex(a, fleet) : harness === 'copilot' ? probeableCopilot(a) : versionedHarness(a, harness) };
  };
  const config: Record<string, unknown> = {
    version: 1,
    fleets: {
      zulu: { name: WORK, search_roots: [a.home], claude: opts.claude ?? dock(a, 'zulu', 'claude', live), codex: codexDock('zulu', 'codex') },
      alpha: { name: HOME, search_roots: [a.home], codex: codexDock('alpha', 'codex'), copilot: codexDock('alpha', 'copilot') },
    },
  };
  if (opts.chat) {
    const fleets = config.fleets as Record<string, unknown>;
    for (const f of CHAT_FLEETS) fleets[f.key] = { name: f.name, search_roots: [a.home], claude: chatDock(a, f.key, opts.holdChat === f.key) };
  }
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
  // A dock's HOME is where codex and copilot look for user skills; a test that lists skills points it away from the engineer's.
  if (opts.userHome) {
    for (const fleet of Object.values(config.fleets as Record<string, Record<string, { env?: Record<string, string> }>>)) {
      for (const h of Object.keys(identityVar)) if (fleet[h]) fleet[h] = { ...fleet[h], env: { ...fleet[h].env, HOME: opts.userHome } };
    }
  }
  a.writeConfig(config);
  mkdirSync(join(a.home, 'logos'), { recursive: true });
  for (const h of Object.keys(identityVar)) {
    writeFileSync(join(a.home, 'logos', `${h}.svg`), '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>');
  }
}

/** More, then Preferences, then the section named in its group: a fleet's by the fleet's name, else the all-fleets group. On a phone the nav is a list that a page replaces, so a page already open is left through its Back. */
export async function openPreferences(page: Page, opts: { fleet?: string; section: string }) {
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Preferences', exact: true }).tap();
  const nav = page.getByRole('navigation', { name: 'Preferences' });
  await expect(page).toHaveURL(/\/app\/preferences/);
  if (!(await nav.isVisible())) {
    await preferencesBack(page).tap();
    await expect(nav).toBeVisible();
  }
  const group = nav.getByRole('group', { name: opts.fleet ? `${opts.fleet} fleet` : 'Admiral · all fleets' });
  await group.getByRole('link', { name: opts.section, exact: true }).tap();
}

/** The phone Preferences page's way back to the list. */
export const preferencesBack = (page: Page) => page.getByRole('main').getByRole('button', { name: /^Back/ }).or(page.getByRole('main').getByRole('link', { name: /^Back/ })).first();

/** Fully on screen, and the topmost thing at its centre is the control itself, so nothing (the tab bar) covers it. */
export async function expectReachable(page: Page, control: Locator) {
  await expect(control).toBeInViewport({ ratio: 1 });
  const box = (await control.boundingBox())!;
  const covered = await control.evaluate((el, [x, y]) => {
    const top = document.elementFromPoint(x, y);
    return !top || !(el === top || el.contains(top));
  }, [box.x + box.width / 2, box.y + box.height / 2]);
  expect(covered, 'nothing covers the control').toBe(false);
}

/** A finger's tap at the control's centre: unlike `locator.tap()` it does not scroll anything into view first. */
export async function touchTap(page: Page, control: Locator) {
  const box = (await control.boundingBox())!;
  await page.touchscreen.tap(box.x + box.width / 2, box.y + box.height / 2);
}

/** The fleets `seedFleets(a, { chat: true })` adds after zulu and alpha: each has a fake Claude Code dock of its own for its AdmiralBot, so two chats never share a transcript or a cue. */
export const CHAT_FLEETS = [
  { key: 'harbor', name: 'Harborlight' },
  { key: 'lagoon', name: 'Lagoonside' },
] as const;
export type ChatFleet = (typeof CHAT_FLEETS)[number]['key'];

const chatConfigDir = (a: Admiral, fleet: string) => join(a.home, 'identity', `${fleet}-claude`);

/** A chat's transcript: Claude Code records it under the dock's config dir, which the SessionStart cue names to Admiral. */
const chatTranscriptPath = (a: Admiral, fleet: string, name: string) => join(chatConfigDir(a, fleet), 'projects', 'p', `${name}.jsonl`);

function chatDock(a: Admiral, fleet: string, hold: boolean) {
  const cue = join(a.home, `chat-cue-${fleet}`);
  const startup = JSON.stringify({ transcript_path: chatTranscriptPath(a, fleet, 't1'), source: 'startup' });
  const cleared = JSON.stringify({ transcript_path: chatTranscriptPath(a, fleet, 't2'), source: 'clear' });
  // A New chat clears the context: the second transcript starts once the test touches go-clear.
  // A held chat stays before its first SessionStart, as one stopped on Claude's folder-trust dialog does, until chatStarts.
  writeFileSync(cue, ['record', 'bracketedpaste', ...(hold ? ['wait go-start'] : []), `event SessionStart ${startup}`, 'wait go-clear', `event SessionStart ${cleared}`].join('\n') + '\n');
  mkdirSync(join(chatConfigDir(a, fleet), 'projects', 'p'), { recursive: true });
  return { executable: fakeClaude, env: { FAKE_RECORD_DIR: join(a.home, 'rec'), FAKE_CUE: cue, CLAUDE_CONFIG_DIR: chatConfigDir(a, fleet) } };
}

/** Writes lines to a fleet's chat transcript before or after the chat starts; `second` is the transcript a New chat moves to. */
export function chatSays(a: Admiral, fleet: ChatFleet, lines: TranscriptLine[], second = false) {
  const path = chatTranscriptPath(a, fleet, second ? 't2' : 't1');
  mkdirSync(dirname(path), { recursive: true });
  appendFileSync(path, lines.map((l) => JSON.stringify(l) + '\n').join(''));
}

/** The chat's session and the directory its pane records in. */
export const chatSession = (fleet: ChatFleet) => `_chat-${fleet}`;
const chatRecord = (a: Admiral, fleet: ChatFleet) => join(a.home, 'rec', chatSession(fleet));

/** Lets the chat's fake Claude Code carry on after a New chat's /clear, as the real one starts its next transcript. */
export function chatCleared(a: Admiral, fleet: ChatFleet) {
  mkdirSync(chatRecord(a, fleet), { recursive: true });
  writeFileSync(join(chatRecord(a, fleet), 'go-clear'), '');
}

/** Lets a held chat's fake Claude Code fire its first SessionStart, as the engineer's answer to the trust dialog does. */
export function chatStarts(a: Admiral, fleet: ChatFleet) {
  mkdirSync(chatRecord(a, fleet), { recursive: true });
  writeFileSync(join(chatRecord(a, fleet), 'go-start'), '');
}

/** Ends the fleet's chat session, as quitting Claude Code in its terminal does. */
export const chatEnds = (a: Admiral, fleet: ChatFleet) => spawnSync('tmux', ['-S', join(a.home, 'tmux.sock'), 'kill-session', '-t', chatSession(fleet)], { encoding: 'utf8' });

/** What was typed into the fleet's chat pane, as the harness received it. */
export const chatReceived = (a: Admiral, fleet: ChatFleet) => {
  try {
    return readFileSync(join(chatRecord(a, fleet), 'stdin'), 'utf8').replaceAll('\x1b[200~', '').replaceAll('\x1b[201~', '');
  } catch {
    return '';
  }
};

/** A question AdmiralBot asked, as `admiral chat ask` leaves it. */
export function chatAsks(a: Admiral, fleet: ChatFleet, id: string, title: string, options: string[]) {
  const dir = join(a.home, 'chat', fleet, 'questions');
  mkdirSync(dir, { recursive: true });
  const question = { questions: [{ id: '1', text: title, multi: false, options: options.map((label) => ({ label })) }] };
  writeFileSync(join(dir, `${id}.json`), JSON.stringify({ version: 1, id, kind: 'question', created: new Date().toISOString(), title, question }));
}

/** The answer file the chat's question was answered with, if any. */
export const chatAnswerFile = (a: Admiral, fleet: ChatFleet, id: string) => {
  try {
    return JSON.parse(readFileSync(join(a.home, 'chat', fleet, 'questions', `${id}.answer.json`), 'utf8')) as { answers: Record<string, { choice?: string }> };
  } catch {
    return undefined;
  }
};

/** What the page asks for and what it logs: Admiral serves everything itself, over /api/v1, without errors. */
export function watchPage(page: Page, server: string) {
  const host = new URL(server).host;
  const paths: string[] = [];
  const foreign: string[] = [];
  const errors: string[] = [];
  // The browser logs each refused launch, ending, merge or work-repo save, and each PR detail GitHub cannot give, as a console error, in either order against its response: the form or row showing the refusal is the journey's own check.
  const failedLoads: string[] = [];
  let refusedLaunches = 0;
  page.on('response', (r) => {
    const refusable = { GET: /^\/api\/v1\/ships\/[^/]+\/pr$/, POST: /^\/api\/v1\/(ships|campaigns\/[^/]+\/launch|ships\/[^/]+\/(dismiss|unfollow|merge)|fleets\/[^/]+\/crew\/(members(\/[^/]+\/rename)?|draft))$/, PUT: /^\/api\/v1\/(campaigns\/[^/]+\/work-repo|fleets\/[^/]+\/crew\/members\/[^/]+)$/ }[r.request().method()];
    const path = new URL(r.url()).pathname;
    if (refusable?.test(path) && r.status() >= 400) refusedLaunches++;
    // Moor's refusal is a 409; any other status on an ending stays an error.
    if (r.request().method() === 'POST' && /^\/api\/v1\/ships\/[^/]+\/(stop|return)$/.test(path) && r.status() === 409) refusedLaunches++;
  });
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    // WebKit logs a same-origin fetch cancelled by navigation as an access-control error.
    if (/due to access control checks\.?$/.test(m.text()) && m.text().includes(host)) return;
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

/** Launches a ship whose harness waits, then exits 0 with a report naming the PR (and the `more` PRs, a stack): Admiral moors it. `resume` is the report's resume text; `lines` are transcript lines round 0 holds when it moors. */
export async function launchMooredShip(a: Admiral, prNumber: number, opts: { resume?: string; lines?: TranscriptLine[]; more?: number[] } = {}): Promise<LaunchedShip> {
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', cue: 'wait go\nexit 0\n' });
  const rec = join(a.home, 'rec', ship.session);
  // The fake writes `recorded` last; `env` can exist while it is still half written.
  await expect.poll(() => existsSync(join(rec, 'recorded'))).toBe(true);
  if (opts.lines) await appendLines(a, ship, opts.lines);
  const report = /^ADMIRAL_REPORT=(.*)$/m.exec(readFileSync(join(rec, 'env'), 'utf8'))?.[1];
  expect(report, 'the harness env names a report path').toBeTruthy();
  writeFileSync(report!, JSON.stringify({ version: 2, result: 'done', why: 'PR opened', prs: [prNumber, ...(opts.more ?? [])].map((n) => ({ url: `${PR_BASE}/${n}`, closes: [] })), resume: opts.resume ?? RESUME }));
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
  await expect(prSummaryButton(page)).toHaveText(/1 PR · #7 waiting on review/);
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
  await expect(viewPanel(page)).toContainText('#7');
  // A moored ship that is not ready: the tab has Don't follow reviews and no Merge or Dismiss, and the head's Wake is the only one.
  await expect(viewPanel(page).getByRole('button', { name: "Don't follow reviews" })).toBeEnabled();
  await expect(viewPanel(page).getByRole('button', { name: /^Merge/ })).toHaveCount(0);
  await expect(viewPanel(page).getByRole('button', { name: 'Dismiss' })).toHaveCount(0);
  await expect(wakeButton(page)).toHaveCount(1);
  await expect(page.getByRole('alert')).toHaveCount(0);
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
  await readyOnTheServer(a, first);

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
  await expectChipTone(page, rowOf(first).getByText('Approved', { exact: true }), 'ok');
  await expectChipTone(page, rowOf(second).getByText('Checks failing', { exact: true }), 'err');
  // Ready to merge is a status on the moored row, which stays in its group; a PR that is not ready shows none.
  await expect(rowOf(first)).toContainText('Ready to merge');
  await expect(rowOf(second)).not.toContainText('Ready to merge');
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
  await expect(rowOf(first)).not.toContainText('Ready to merge');

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

/** The repository's default merge method as the fake gh answers it; call before the ship moors, since Admiral asks once per repository. */
export function seedMergeMethod(a: Admiral, method: 'merge' | 'squash' | 'rebase') {
  writeFileSync(join(a.home, 'fakegh', 'default_merge_method'), method.toUpperCase() + '\n');
}

/** The fake gh fails every `gh pr merge` with this message on stderr, merging nothing. */
export function ghMergeFails(a: Admiral, message: string) {
  writeFileSync(join(a.home, 'fakegh', 'pr_merge.fail'), message + '\n');
}

/** The fake gh sleeps this many seconds inside `gh pr merge`, so the button stays busy while a test looks. */
export function ghMergeSlow(a: Admiral, seconds: number) {
  writeFileSync(join(a.home, 'fakegh', 'pr_merge.sleep'), `${seconds}\n`);
}

/** The fake gh sleeps this many seconds inside `gh stack merge`, so the button stays busy while a test looks. */
export function ghStackMergeSlow(a: Admiral, seconds: number) {
  writeFileSync(join(a.home, 'fakegh', 'stack_merge.sleep'), `${seconds}\n`);
}

/** The argv (after `gh`) of every merge call the fake gh recorded: `pr merge` and `stack merge`. */
export function mergeCalls(a: Admiral): string[][] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .map((d) => join(dir, d, 'argv'))
    .filter((f) => existsSync(f))
    .map((f) => readFileSync(f, 'utf8').split('\0').filter((x) => x !== ''))
    .filter((argv) => (argv[0] === 'pr' || argv[0] === 'stack') && argv[1] === 'merge');
}

/** GitHub's stack number n of these PRs, bottom first: each PR's detail holds the whole stack, each PR based on the head branch of the one below. */
export function seedGitHubStack(a: Admiral, numbers: number[], stackNumber = 4) {
  const layers = numbers.map((n) => ({ number: n, state: 'OPEN', head: `branch-${n}` }));
  numbers.forEach((n, i) =>
    prDetail(a, n, { title: `PR ${n}`, head: `branch-${n}`, base: i === 0 ? 'main' : `branch-${numbers[i - 1]}`, stack: layers, stackNumber }),
  );
}

/** Every merge call the fake gh recorded with the directory it ran in (resolved, as the fake's `pwd -P` prints it). */
export function mergeCallsIn(a: Admiral): { argv: string[]; cwd: string }[] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .filter((d) => existsSync(join(dir, d, 'argv')) && existsSync(join(dir, d, 'cwd')))
    .map((d) => ({ argv: readFileSync(join(dir, d, 'argv'), 'utf8').split('\0').filter((x) => x !== ''), cwd: readFileSync(join(dir, d, 'cwd'), 'utf8').trim() }))
    .filter(({ argv }) => (argv[0] === 'pr' || argv[0] === 'stack') && argv[1] === 'merge');
}

/** Whether a gh call ran in the checkout a journey's ships launch in, or under it: a stack merge reads no checkout and must not. */
export const ranInTheCheckout = (a: Admiral, cwd: string) => {
  const proj = realpathSync(join(a.home, 'proj'));
  return cwd === proj || cwd.startsWith(proj + '/');
};

/** Waits until the server serves the moored ship as ready to merge, with the repository's method when given, so a red journey is the UI's and not the seeding's. */
export async function readyOnTheServer(a: Admiral, ship: LaunchedShip, method?: string) {
  await expect
    .poll(async () => {
      // The poller waits minutes between rounds; the shell asks for one when Ships opens.
      await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
      const res = await fetch(`${a.url}api/v1/ships`);
      const got = ((await res.json()) as { ships: (ShipJSON & { stack_ready?: boolean; merge_method?: string })[] }).ships.find((s) => s.session === ship.session);
      return { ready: got?.stack_ready === true, method: method === undefined ? undefined : got?.merge_method };
    }, { timeout: 30_000 })
    .toEqual({ ready: true, method });
}

const peekOf = (page: Page) => page.getByRole('region', { name: 'Peek' });
const inboxRow = (page: Page, ship: LaunchedShip) => page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name });
const mergeButton = (page: Page) => peekOf(page).getByRole('button', { name: /^Merge/ });
const noHorizontalScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth));

/**
 * Acceptance criteria 1 and 4 for a single PR that is approved, green and mergeable: the Inbox raises an entry, Peek shows the PR and Merge
 * with the repo's method, nothing merges before the tap, one tap records one `gh pr merge <url> --squash`, and the ship sails off.
 */
export async function mergeFromPeek(page: Page, a: Admiral, opts: { phone: boolean }) {
  seedMergeMethod(a, 'squash');
  ghMergeSlow(a, 2);
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  await page.goto(a.url + 'app/inbox?fleet=zulu');
  const main = page.getByRole('main');
  await expect(main.getByRole('region', { name: /^Needs an answer/ }).getByRole('listitem').filter({ hasText: ship.name })).toContainText('ready to merge');
  await rowLinkIn(page, ship).tap();
  const peek = peekOf(page);
  await expect(peek).toBeVisible();
  await expect(peek.getByRole('link', { name: 'api#7' })).toHaveAttribute('href', `${PR_BASE}/7`);
  for (const chip of ['Approved', 'Checks passing', 'Mergeable']) await expect(peek).toContainText(chip);
  const merge = peek.getByRole('button', { name: 'Merge', exact: true });
  await expect(merge).toBeInViewport();
  expect((await merge.boundingBox())!.height, 'Merge height').toBeGreaterThanOrEqual(44);
  await expect(peek).toContainText('squash');
  await expect(peek.getByRole('button', { name: /Merge up to/ })).toHaveCount(0);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);

  // Opening the entry and looking at it merges nothing.
  await holds(() => mergeCalls(a).length === 0, 'no merge before the tap');

  const refreshes: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/moored/refresh') refreshes.push(r.url());
  });
  await merge.tap();
  await expect(mergeButton(page)).toBeDisabled();
  await expect.poll(() => mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/7`, '--squash']]);
  await expect(inboxRow(page, ship)).toContainText('Merged');
  await expect.poll(() => refreshes.length, 'a poll is asked for after the 202').toBeGreaterThan(0);
  await expect
    .poll(async () => (await (await fetch(`${a.url}api/v1/ships`)).json() as { ships: ShipJSON[] }).ships.find((s) => s.session === ship.session)?.status ?? 'gone', { timeout: 30_000 })
    .not.toBe('moored');
  await expect(inboxRow(page, ship)).toHaveCount(0, { timeout: 15_000 });
  expect(mergeCalls(a), 'one merge, not one per render or poll').toHaveLength(1);
}

/** The Peek link of a ship's row in the Inbox. */
const rowLinkIn = (page: Page, ship: LaunchedShip) => inboxRow(page, ship).getByRole('link', { name: new RegExp(ship.name) });

/** A merge gh refuses shows its message in Peek, and the ship stays moored with its entry. */
export async function mergeFailsInPeek(page: Page, a: Admiral) {
  seedMergeMethod(a, 'squash');
  ghMergeFails(a, 'GraphQL: Pull request is not mergeable (mergePullRequest)');
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  await page.goto(a.url + 'app/inbox?fleet=zulu');
  await expect(inboxRow(page, ship)).toContainText('ready to merge');
  await rowLinkIn(page, ship).tap();
  const peek = peekOf(page);
  await peek.getByRole('button', { name: 'Merge', exact: true }).tap();
  await expect(peek.getByRole('alert')).toContainText('Pull request is not mergeable');
  expect(mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/7`, '--squash']]);
  await expect(mergeButton(page)).toBeEnabled();
  // On a narrow screen Peek covers the list, so go back to it.
  await peek.getByRole('link', { name: 'Back' }).tap();
  await expect(inboxRow(page, ship)).toContainText('ready to merge');
  const got = ((await (await fetch(`${a.url}api/v1/ships`)).json()) as { ships: ShipJSON[] }).ships.find((s) => s.session === ship.session);
  expect(got?.status).toBe('moored');
}

/** A Merge the server answers 403, which a page that outlived an Admiral restart gets, says to reload, and the ship is not merged. */
export async function staleMergeTokenInPeek(page: Page, a: Admiral) {
  seedMergeMethod(a, 'squash');
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  await page.route(/\/api\/(v1\/)?ships\/[^/]+\/merge$/, (route) => route.fulfill({ status: 403, contentType: 'application/json', body: '{"error":"forbidden"}' }));
  await page.goto(a.url + 'app/inbox?fleet=zulu');
  await expect(inboxRow(page, ship)).toContainText('ready to merge');
  await rowLinkIn(page, ship).tap();
  const peek = peekOf(page);
  await peek.getByRole('button', { name: 'Merge', exact: true }).tap();
  await expect(peek.getByRole('alert')).toContainText('Admiral restarted, reload to merge');
  await expect(peek.getByRole('button', { name: /reload/i })).toBeVisible();
  expect(mergeCalls(a)).toEqual([]);
}

/** A ready 2-PR stack that GitHub holds as stack 4, moored and served ready, with the repository's method squash. */
async function readyStack(a: Admiral): Promise<LaunchedShip> {
  seedMergeMethod(a, 'squash');
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  prAnswer(a, 8, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  seedGitHubStack(a, [7, 8]);
  const ship = await launchMooredShip(a, 7, { more: [8] });
  await readyOnTheServer(a, ship, 'squash');
  return ship;
}

/** What the whole stack's one merge looks like: GitHub's stack number, the repository's method, and a folder of gh's own rather than the ship's checkout. */
async function expectOneStackMerge(a: Admiral) {
  await expect.poll(() => mergeCallsIn(a).map((c) => c.argv)).toEqual([['stack', 'merge', '4', '--yes', '--merge-method', 'squash']]);
  const [call] = mergeCallsIn(a);
  expect(ranInTheCheckout(a, call.cwd), `gh stack merge ran in ${call.cwd}, the ship's checkout`).toBe(false);
}

/** Merge as the stack's one act: the button, with the method and that it merges the whole stack, and none of the old gh stack note. */
async function expectWholeStackMerge(scope: Locator) {
  const merge = scope.getByRole('button', { name: 'Merge', exact: true });
  await expect(merge).toBeInViewport();
  expect((await merge.boundingBox())!.height, 'Merge height').toBeGreaterThanOrEqual(44);
  await expect(scope).toContainText(/squash/);
  await expect(scope).toContainText(/whole stack/i);
  await expect(scope).not.toContainText('gh stack');
  await expect(scope.getByRole('button', { name: /Merge up to/ })).toHaveCount(0);
  return merge;
}

/** A ready stack shows Ready to merge, and Peek offers Merge for the whole stack: nothing merges before the tap, and one tap runs one gh stack merge by GitHub's stack number. */
export async function readyStackMergesFromPeek(page: Page, a: Admiral) {
  const ship = await readyStack(a);
  ghStackMergeSlow(a, 2);

  await page.goto(a.url + 'app/ships?fleet=zulu');
  await expect(page.getByRole('main').getByRole('region', { name: /^Moored/ }).getByRole('listitem').filter({ hasText: ship.name })).toContainText('Ready to merge');
  await page.goto(a.url + 'app/inbox?fleet=zulu');
  await rowLinkIn(page, ship).tap();
  const peek = peekOf(page);
  await expect(peek).toBeVisible();
  for (const chip of ['api#7', 'api#8']) await expect(peek.getByRole('link', { name: chip })).toBeVisible();
  const merge = await expectWholeStackMerge(peek);
  await holds(() => mergeCalls(a).length === 0, 'a stack is merged only by the tap');

  await merge.tap();
  await expect(merge).toBeDisabled();
  await expectOneStackMerge(a);
  await expect(inboxRow(page, ship)).toContainText('Merged');
  await expect
    .poll(async () => (await (await fetch(`${a.url}api/v1/ships`)).json() as { ships: ShipJSON[] }).ships.find((s) => s.session === ship.session)?.status ?? 'gone', { timeout: 30_000 })
    .not.toBe('moored');
  expect(mergeCalls(a), 'one merge, not one per layer').toHaveLength(1);
}

/** The PR tab of a ship's view, opened from the Ships screen by touch. */
async function openPRTab(page: Page, a: Admiral, ship: LaunchedShip) {
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();
  await expect(page).toHaveURL(shipViewUrl(a, ship));
  await viewTab(page, 'PR').tap();
  return viewPanel(page);
}

/**
 * The PR tab of a ready ship shows the state and the actions: the PR and its chips, Ready to merge above, Merge with the method hint
 * and Don't follow reviews in the tab, no Dismiss, and the head's one Wake. Nothing merges before the tap; one tap records
 * one `gh pr merge <url> --squash` and the ship sails off.
 */
export async function mergeFromThePRTab(page: Page, a: Admiral, opts: { phone: boolean }) {
  seedMergeMethod(a, 'squash');
  ghMergeSlow(a, 2);
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  const panel = await openPRTab(page, a, ship);
  await expect(page.getByRole('main')).toContainText('Ready to merge');
  await expect(panel).toContainText('#7');
  for (const chip of ['Approved', 'Checks passing', 'Mergeable']) await expectChipTone(page, panel.getByText(chip, { exact: true }), 'ok');
  // The moored journeys seed no PR detail, so the header falls back to the number and a muted line, with no sub-tab row and no alert.
  await expect(panel).toContainText("Couldn't load this PR from GitHub.");
  await expect(panel.getByRole('tablist')).toHaveCount(0);
  const merge = panel.getByRole('button', { name: 'Merge', exact: true });
  const unfollow = panel.getByRole('button', { name: "Don't follow reviews" });
  for (const [name, b] of [['Merge', merge], ["Don't follow reviews", unfollow]] as const) {
    await expect(b).toBeInViewport();
    expect((await b.boundingBox())!.height, `${name} height`).toBeGreaterThanOrEqual(44);
  }
  await expect(panel).toContainText('squash');
  await expect(panel.getByRole('button', { name: /Merge up to/ })).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Dismiss' })).toHaveCount(0);
  await expect(wakeButton(page)).toHaveCount(1);
  await expect(page.getByRole('alert')).toHaveCount(0);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);

  // Opening the tab, and the pushes and polls that follow, merge nothing.
  await holds(() => mergeCalls(a).length === 0, 'no merge before the tap');

  await merge.tap();
  await expect(merge).toBeDisabled();
  await expect(unfollow).toBeDisabled();
  await expect.poll(() => mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/7`, '--squash']]);
  await expect
    .poll(async () => (await (await fetch(`${a.url}api/v1/ships`)).json() as { ships: ShipJSON[] }).ships.find((s) => s.session === ship.session)?.status ?? 'gone', { timeout: 30_000 })
    .not.toBe('moored');
  expect(mergeCalls(a), 'one merge, not one per render or poll').toHaveLength(1);
}

/** A ready stack in the PR tab: Merge with the whole-stack hint, nothing merged before the tap, and one gh stack merge by GitHub's stack number after it. */
export async function readyStackMergesFromThePRTab(page: Page, a: Admiral, opts: { phone: boolean }) {
  const ship = await readyStack(a);
  ghStackMergeSlow(a, 2);

  const panel = await openPRTab(page, a, ship);
  await expect(page.getByRole('main')).toContainText('Ready to merge');
  await expect(panel.getByRole('tablist').getByRole('tab')).toHaveText([/#7/, /#8/]);
  const merge = await expectWholeStackMerge(panel);
  await expect(panel.getByRole('button', { name: "Don't follow reviews" })).toBeEnabled();
  await expect(wakeButton(page)).toHaveCount(1);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);
  await holds(() => mergeCalls(a).length === 0, 'a stack is merged only by the tap');

  await merge.tap();
  await expect(merge).toBeDisabled();
  await expectOneStackMerge(a);
  await expect
    .poll(async () => (await (await fetch(`${a.url}api/v1/ships`)).json() as { ships: ShipJSON[] }).ships.find((s) => s.session === ship.session)?.status ?? 'gone', { timeout: 30_000 })
    .not.toBe('moored');
  expect(mergeCalls(a), 'one merge, not one per layer').toHaveLength(1);
}

/** Don't follow reviews in the PR tab ends the mooring the way the Ships row's button does: the same route, and the ship leaves. */
export async function unfollowFromThePRTab(page: Page, a: Admiral) {
  prAnswer(a, 7, { state: 'OPEN' });
  const ship = await launchMooredShip(a, 7);
  const panel = await openPRTab(page, a, ship);
  const unfollow = panel.getByRole('button', { name: "Don't follow reviews" });
  expect((await unfollow.boundingBox())!.height, "Don't follow reviews height").toBeGreaterThanOrEqual(44);

  const unfollowed = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/v1/ships/${ship.session}/unfollow`);
  await unfollow.tap();
  expect((await unfollowed).status()).toBe(200);
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status ?? 'gone').not.toBe('moored');
  expect(mergeCalls(a)).toEqual([]);
}

/** A merge gh refuses shows its message in the tab's one alert, and the ship stays moored with both actions enabled. */
export async function mergeFailsInThePRTab(page: Page, a: Admiral) {
  seedMergeMethod(a, 'squash');
  ghMergeFails(a, 'GraphQL: Pull request is not mergeable (mergePullRequest)');
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  const panel = await openPRTab(page, a, ship);
  await panel.getByRole('button', { name: 'Merge', exact: true }).tap();
  await expect(panel.getByRole('alert')).toContainText('Pull request is not mergeable');
  await expect(page.getByRole('alert'), 'the head shows no second alert').toHaveCount(1);
  expect(mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/7`, '--squash']]);
  await expect(panel.getByRole('button', { name: 'Merge', exact: true })).toBeEnabled();
  await expect(panel.getByRole('button', { name: "Don't follow reviews" })).toBeEnabled();
  expect((await listShips(a)).find((s) => s.session === ship.session)?.status).toBe('moored');
}

/** A Merge the server answers 403 says to reload in the tab, and the ship is not merged. */
export async function staleMergeTokenInThePRTab(page: Page, a: Admiral) {
  seedMergeMethod(a, 'squash');
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  await page.route(/\/api\/(v1\/)?ships\/[^/]+\/merge$/, (route) => route.fulfill({ status: 403, contentType: 'application/json', body: '{"error":"forbidden"}' }));
  const panel = await openPRTab(page, a, ship);
  await panel.getByRole('button', { name: 'Merge', exact: true }).tap();
  await expect(panel.getByRole('alert')).toContainText('Admiral restarted, reload to merge');
  await expect(panel.getByRole('button', { name: /reload/i })).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(1);
  expect(mergeCalls(a)).toEqual([]);
}

/** PRs GitHub does not hold as one stack (PR 8 is based on another branch) are refused by the server (409), and the tab shows that message and merges nothing. */
export async function stackMismatchInThePRTab(page: Page, a: Admiral) {
  const ship = await readyStack(a);
  prDetail(a, 8, { title: 'PR 8', head: 'branch-8', base: 'develop' });

  const panel = await openPRTab(page, a, ship);
  const refused = page.waitForResponse((r) => r.request().method() === 'POST' && /\/ships\/[^/]+\/merge$/.test(new URL(r.url()).pathname));
  await panel.getByRole('button', { name: 'Merge', exact: true }).tap();
  const res = await refused;
  expect(res.status()).toBe(409);
  const message = ((await res.json()) as { error: string }).error;
  expect(message, 'GitHub is the record of a stack, so the refusal names no checkout').not.toMatch(/checkout/i);
  await expect(panel.getByRole('alert')).toContainText(message);
  await expect(page.getByRole('alert')).toHaveCount(1);
  expect(mergeCalls(a)).toEqual([]);
  expect((await listShips(a)).find((s) => s.session === ship.session)?.status).toBe('moored');
}

/** The ship view's one-line PR summary button, which opens the PR tab. */
export const prSummaryButton = (page: Page) => page.getByRole('main').getByRole('button', { name: /\d+ PRs? · / });

/** What the fake gh's poll reads of a PR's author and creation time: a running ship carries a PR its fleet's user opened at or after the ship's launch. */
export function prAuthored(a: Admiral, number: number, p: { login?: string; at?: Date } = {}) {
  const dir = join(a.home, 'fakegh', 'pr_events');
  mkdirSync(dir, { recursive: true });
  const createdAt = (p.at ?? new Date(Date.now() + 3_600_000)).toISOString().replace(/\.\d+Z$/, 'Z');
  const event = { author: { __typename: 'User', login: p.login ?? 'fake-viewer' }, createdAt };
  writeFileSync(join(dir, `${PR_BASE}/${number}`.replace(/[/:]/g, '_')), JSON.stringify(event) + '\n');
}

/** An author in GitHub's shape: a bot has `__typename: "Bot"`, and no author at all is a deleted user. */
type GqlAuthor = { login: string; bot?: boolean } | null;
const gqlActor = (a: GqlAuthor) => (a ? { __typename: a.bot ? 'Bot' : 'User', login: a.login } : null);
type GqlComment = { author: GqlAuthor; body: string; at: Date };
const gqlComment = (c: GqlComment, n: number) => ({ author: gqlActor(c.author), body: c.body, url: `${PR_BASE}/8#comment-${n}`, createdAt: c.at.toISOString() });

/** What a PR's detail carries beyond its header: the body, the check contexts, the review history, the requests, the threads and the comments. */
export interface PRSections {
  body?: string;
  /** The rollup state: SUCCESS, FAILURE, PENDING, ''. */
  checks?: string;
  /** Check contexts that failed, as a CheckRun or a StatusContext does; `url` is the details or target URL. */
  failing?: { name: string; url: string; status?: boolean }[];
  reviews?: { author: GqlAuthor; state: string }[];
  requested?: { login: string; bot?: boolean }[];
  threads?: { path: string; line: number; resolved?: boolean; comments: GqlComment[] }[];
  comments?: GqlComment[];
}

/** What the fake gh answers the PR tab's single-PR query with, in GitHub's shape: `stack` lists every layer, bottom first. The fake's `stack merge N` finds the stack by the compact `"stack":{"number":N,` this writes. */
export function prDetail(a: Admiral, number: number, d: { title: string; head: string; base: string; state?: string; draft?: boolean; stack?: { number: number; state: string; head: string }[]; stackNumber?: number } & PRSections) {
  const contexts = (d.failing ?? []).map((f) =>
    f.status ? { __typename: 'StatusContext', context: f.name, state: 'FAILURE', targetUrl: f.url } : { __typename: 'CheckRun', name: f.name, conclusion: 'FAILURE', detailsUrl: f.url },
  );
  const node = {
    number,
    url: `${PR_BASE}/${number}`,
    title: d.title,
    body: d.body ?? '',
    state: d.state ?? 'OPEN',
    isDraft: d.draft ?? false,
    headRefName: d.head,
    baseRefName: d.base,
    baseRepository: { defaultBranchRef: { name: 'main' } },
    statusCheckRollup: d.checks ? { state: d.checks } : null,
    commits: { nodes: [{ commit: { statusCheckRollup: { contexts: { nodes: contexts } } } }] },
    reviews: { nodes: (d.reviews ?? []).map((r) => ({ author: gqlActor(r.author), state: r.state })) },
    reviewRequests: { nodes: (d.requested ?? []).map((r) => ({ requestedReviewer: gqlActor(r) })) },
    reviewThreads: { nodes: (d.threads ?? []).map((t, i) => ({ isResolved: t.resolved ?? false, path: t.path, line: t.line, comments: { nodes: t.comments.map((c, j) => gqlComment(c, i * 10 + j)) } })) },
    comments: { nodes: (d.comments ?? []).map((c, i) => gqlComment(c, 100 + i)) },
    stack: d.stack
      ? { number: d.stackNumber ?? 4, size: d.stack.length, baseRefName: 'main', entries: { nodes: d.stack.map((l, i) => ({ position: i + 1, pullRequest: { number: l.number, url: `${PR_BASE}/${l.number}`, state: l.state, headRefName: l.head } })) } }
      : null,
  };
  const dir = join(a.home, 'fakegh', 'pr_detail');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `${PR_BASE}/${number}`.replace(/[/:]/g, '_')), JSON.stringify(node) + '\n');
}

/**
 * Launches a running ship that carries these PRs: one tool result names them all (cues read in one pass collapse to the last), the fake gh
 * says the fleet's user opened them, and the poll is kicked until the server serves them on the ship. The caller seeds each PR's answer first.
 */
export async function launchShipCarrying(a: Admiral, numbers: number[]): Promise<LaunchedShip> {
  for (const n of numbers) prAuthored(a, n);
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', cues: [`toolresult ${numbers.map((n) => `${PR_BASE}/${n}`).join(' ')}`] });
  await expect
    .poll(
      async () => {
        await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
        const got = (await listShips(a)).find((s) => s.session === ship.session) as (ShipJSON & { prs?: { url: string }[] }) | undefined;
        return (got?.prs ?? []).map((p) => p.url).sort();
      },
      { timeout: 30_000 },
    )
    .toEqual(numbers.map((n) => `${PR_BASE}/${n}`).sort());
  return ship;
}

/** The colour a design token paints, as the browser computes it, so a test names the token and not an RGB. */
async function tokenColor(page: Page, token: '--color-ok' | '--color-err') {
  return page.evaluate((t) => {
    const probe = document.createElement('span');
    probe.style.color = `var(${t})`;
    document.body.appendChild(probe);
    const color = getComputedStyle(probe).color;
    probe.remove();
    return color;
  }, token);
}

/** A PR chip paints its text in the ok (green) or err (red) colour, not the muted one a shared chip rule would give it. */
export async function expectChipTone(page: Page, chip: Locator, tone: 'ok' | 'err') {
  await expect(chip).toHaveCSS('color', await tokenColor(page, tone === 'ok' ? '--color-ok' : '--color-err'));
}

const HOUR = 3_600_000;
const ago = (ms: number) => new Date(Date.now() - ms);
const prSection = (panel: Locator, name: string) => panel.locator('section.pr-section').filter({ has: panel.page().getByRole('heading', { name, exact: true }) });

/** The top PR's detail once its checks failed and people have reviewed and talked: every section has something to show. */
function prSectionsFixture(): PRSections {
  const filler = Array.from({ length: 24 }, (_, i) => `Line ${i + 1} of the notes explains one more detail of the token cache.`).join('\n\n');
  return {
    body: `## Why\n\nThe cache keeps token buckets warm.\n\n- keeps one bucket per key\n- evicts after five minutes\n\n<!-- reviewer checklist, never shown -->\n\n${filler}`,
    checks: 'FAILURE',
    failing: [
      { name: 'build / unit', url: 'https://github.com/acme/api/actions/runs/9' },
      { name: 'lint', url: 'javascript:alert(1)', status: true },
    ],
    reviews: [
      { author: { login: 'carol' }, state: 'APPROVED' },
      { author: { login: 'bob' }, state: 'CHANGES_REQUESTED' },
      { author: { login: 'carol' }, state: 'COMMENTED' },
      { author: { login: 'github-copilot', bot: true }, state: 'COMMENTED' },
    ],
    requested: [{ login: 'bob' }, { login: 'dave' }],
    threads: [
      { path: 'src/limiter.ts', line: 42, comments: [{ author: { login: 'bob' }, body: 'This drops the lock too early.', at: ago(73 * HOUR) }, { author: { login: 'alice' }, body: 'Good catch, fixing it.', at: ago(49 * HOUR) }] },
      { path: 'old/path.ts', line: 7, resolved: true, comments: [{ author: { login: 'bob' }, body: 'Rename this.', at: ago(80 * HOUR) }] },
      { path: 'old/other.ts', line: 0, resolved: true, comments: [{ author: { login: 'carol' }, body: 'Remove this file.', at: ago(79 * HOUR) }] },
    ],
    comments: [
      { author: { login: 'carol' }, body: 'Can we add a test for the eviction?', at: ago(5 * HOUR + 60_000) },
      { author: { login: 'github-actions', bot: true }, body: 'Coverage 91%\nlines: 120\nbranches: 40', at: ago(30 * 60_000) },
    ],
  };
}

/** AC2 and AC3 for the top PR: checks, description, reviewers and conversation, in two columns beside each other or folded to one on a phone. */
async function expectPRSections(page: Page, panel: Locator, subtab: Locator, opts: { phone: boolean }) {
  await expect(subtab).toHaveAttribute('aria-selected', 'true');
  const checks = prSection(panel, 'Checks');
  const description = prSection(panel, 'Description');
  const reviewers = panel.locator('section.pr-section').filter({ has: panel.page().locator('.reviewers') });
  const conversation = prSection(panel, 'Conversation');
  const tall = async (l: Locator, what: string) => expect((await l.boundingBox())!.height, what).toBeGreaterThanOrEqual(44);

  // Checks: the failing one with a Log link that opens elsewhere; a link GitHub sent that is not http(s) is not a link.
  await expect(checks).toContainText('2 failing');
  await expect(checks.getByText('Failed', { exact: true })).toHaveCount(2);
  await expect(checks).toContainText('build / unit');
  await expect(checks).toContainText('lint');
  const logs = checks.getByRole('link', { name: 'Log' });
  await expect(logs).toHaveCount(1);
  await expect(logs).toHaveAttribute('href', 'https://github.com/acme/api/actions/runs/9');
  await expect(logs).toHaveAttribute('target', '_blank');
  await expect(logs).toHaveAttribute('rel', /noopener/);
  await expect(logs).toHaveAttribute('rel', /noreferrer/);
  await tall(logs, 'Log height');

  // Description: Markdown rendered, the template's comment gone, clamped until Show more.
  const desc = description.locator('.desc');
  await expect(desc.getByRole('heading', { name: 'Why' })).toBeVisible();
  await expect(desc.getByRole('listitem')).toHaveText(['keeps one bucket per key', 'evicts after five minutes']);
  await expect(description).not.toContainText('reviewer checklist');
  expect(await desc.getByRole('heading', { name: 'Why' }).evaluate((h) => getComputedStyle(h).fontSize), 'a heading in the description is body size').toBe(await desc.locator('p').first().evaluate((p) => getComputedStyle(p).fontSize));
  await expect(desc).toHaveClass(/\bclamped\b/);
  const clampedHeight = (await desc.boundingBox())!.height;
  const more = description.getByRole('button', { name: 'Show more' });
  await expect(more).toHaveAttribute('aria-expanded', 'false');
  await tall(more, 'Show more height');

  // Reviewers: one row per person, the latest verdict, waiting ones last.
  const rows = reviewers.getByRole('listitem');
  if (opts.phone) {
    const summary = panel.locator('.rev-summary');
    await expect(summary).toHaveText('1 approved · 1 changes requested · 1 commented · 1 waiting');
    await expect(summary).toHaveAttribute('aria-expanded', 'false');
    await expect(rows.first()).toBeHidden();
    await tall(summary, 'Reviewers summary height');
    expect((await summary.boundingBox())!.y, 'the summary sits above Checks').toBeLessThan((await checks.boundingBox())!.y);
    await summary.tap();
    await expect(summary).toHaveAttribute('aria-expanded', 'true');
  } else {
    await expect(panel.locator('.rev-summary')).toBeHidden();
  }
  await expect(rows).toHaveText([/carol\s*Approved/, /bob\s*Changes requested/, /github-copilot\s*bot\s*Commented/, /dave\s*Waiting/]);
  await expectChipTone(page, rows.nth(0).getByText('Approved', { exact: true }), 'ok');
  await expectChipTone(page, rows.nth(1).getByText('Changes requested', { exact: true }), 'err');
  await expect(rows.nth(0).getByText('bot', { exact: true })).toHaveCount(0);
  await expect(rows.nth(2).locator('.avatar')).toHaveClass(/\bbot\b/);
  await expect(rows.nth(0).locator('.avatar')).toHaveText('CA');

  // The layout: two columns beside each other, or one column.
  const box = async (l: Locator) => (await l.boundingBox())!;
  const [c, d, r, v] = [await box(checks), await box(description), await box(reviewers), await box(conversation)];
  expect(d.y, 'Description is under Checks').toBeGreaterThan(c.y + c.height - 1);
  expect(Math.abs(d.x - c.x), 'Description shares Checks\' column').toBeLessThan(2);
  expect(v.y, 'Conversation is below the columns').toBeGreaterThan(Math.max(d.y + d.height, r.y + r.height) - 1);
  if (opts.phone) {
    expect(r.y + r.height, 'Reviewers is above Checks').toBeLessThanOrEqual(c.y + 1);
    expect(Math.abs(r.x - c.x)).toBeLessThan(2);
    expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth)), 'no sideways scroll').toBe(true);
  } else {
    expect(r.x, 'Reviewers is right of Checks').toBeGreaterThan(c.x + c.width - 1);
    expect(Math.abs(r.y - c.y), 'Reviewers and Checks start on one row').toBeLessThan(2);
    expect(v.width, 'Conversation spans both columns').toBeGreaterThan(c.width + r.width - 2);
  }

  // Conversation: the open thread and the comments in time order, oldest first, each with its time.
  const items = conversation.locator('.timeline .item');
  await expect(items).toHaveCount(3);
  await expect(items.nth(0)).toContainText('bob');
  await expect(items.nth(0)).toContainText('src/limiter.ts:42');
  await expect(items.nth(0)).toContainText('3d ago');
  await expect(items.nth(0)).toContainText('This drops the lock too early.');
  await expect(items.nth(0).locator('.reply')).toContainText('alice');
  await expect(items.nth(0).locator('.reply')).toContainText('Good catch, fixing it.');
  await expect(items.nth(0).locator('.reply')).toContainText('2d ago');
  await expect(items.nth(0).getByText('bot', { exact: true })).toHaveCount(0);
  await expect(items.nth(1)).toContainText('carol');
  await expect(items.nth(1)).toContainText('5h ago');
  await expect(items.nth(1)).toContainText('Can we add a test for the eviction?');
  await expect(items.nth(1)).not.toContainText('Resolved');

  // A bot's comment is marked, folded to its first line, and opens on Show all.
  const bot = items.nth(2);
  await expect(bot).toHaveClass(/\bfolded\b/);
  await expect(bot.getByText('bot', { exact: true })).toBeVisible();
  await expect(bot).toContainText('github-actions');
  await expect(bot).toContainText(/\d+m ago/);
  await expect(bot).toContainText('Coverage 91%');
  await expect(bot).not.toContainText('lines: 120');
  const showAll = bot.getByRole('button', { name: 'Show all' });
  await expect(showAll).toHaveAttribute('aria-expanded', 'false');
  await tall(showAll, 'Show all height');
  await showAll.tap();
  await expect(bot).not.toHaveClass(/\bfolded\b/);
  await expect(bot).toContainText('lines: 120');
  await expect(bot).toContainText('branches: 40');
  await expect(bot.getByRole('button', { name: 'Show less' })).toHaveAttribute('aria-expanded', 'true');

  // Resolved threads leave the list for one fold, which opens on a tap.
  const fold = conversation.getByRole('button', { name: '2 resolved threads' });
  await expect(fold).toHaveAttribute('aria-expanded', 'false');
  await tall(fold, 'resolved fold height');
  await expect(conversation).not.toContainText('Rename this.');
  await fold.tap();
  await expect(fold).toHaveAttribute('aria-expanded', 'true');
  await expect(items).toHaveCount(5);
  await expect(conversation).toContainText('old/path.ts:7');
  await expect(conversation).toContainText('Rename this.');
  await expect(conversation).toContainText('old/other.ts');
  await expect(conversation).not.toContainText('old/other.ts:0');
  await expect(conversation.locator('.item.resolved').first().getByText('Resolved', { exact: true })).toBeVisible();

  // Show more opens the description.
  await more.tap();
  await expect(description.getByRole('button', { name: 'Show less' })).toHaveAttribute('aria-expanded', 'true');
  await expect(desc).not.toHaveClass(/\bclamped\b/);
  expect((await desc.boundingBox())!.height, 'the open description is taller').toBeGreaterThan(clampedHeight);
  await expect(desc).toContainText('Line 24 of the notes');

  // Show less clamps it again, and Show more opens it for the refresh that follows.
  await description.getByRole('button', { name: 'Show less' }).tap();
  await expect(desc).toHaveClass(/\bclamped\b/);
  await expect(more).toHaveAttribute('aria-expanded', 'false');
  await more.tap();
  await expect(desc).not.toHaveClass(/\bclamped\b/);
  await expect(page.getByRole('alert')).toHaveCount(0);
}

/** What the engineer opened stays open across a refresh: the description, the bot's text and the resolved threads. */
async function expectPRSectionsStayOpen(panel: Locator, opts: { phone: boolean }) {
  if (opts.phone) await expect(panel.locator('.rev-summary')).toHaveAttribute('aria-expanded', 'true');
  await expect(prSection(panel, 'Description').getByRole('button', { name: 'Show less' })).toHaveAttribute('aria-expanded', 'true');
  await expect(prSection(panel, 'Description').locator('.desc')).not.toHaveClass(/\bclamped\b/);
  const conversation = prSection(panel, 'Conversation');
  await expect(conversation.getByRole('button', { name: '2 resolved threads' })).toHaveAttribute('aria-expanded', 'true');
  await expect(conversation.locator('.timeline .item')).toHaveCount(5);
  await expect(conversation.locator('.item.bot')).not.toHaveClass(/\bfolded\b/);
}

/**
 * Acceptance criteria 1, 4 and 5 for a running ship carrying a 2-PR stack whose bottom layer is merged (the ship listed the top one first):
 * the tab counts the open PR, the summary line opens the tab, the sub-tabs follow the stack with the merged one marked, a tap on a sub-tab
 * switches the header and the chips, an approved passing PR's chips are green and a failing one's red, and the tab re-reads GitHub every 30s
 * only while it is open.
 */
export async function prTabOfARunningStack(page: Page, a: Admiral, opts: { phone?: boolean } = {}) {
  const stack = [
    { number: 7, state: 'MERGED', head: 'feat/limiter-core' },
    { number: 8, state: 'OPEN', head: 'feat/limiter-cache' },
  ];
  const passing: PRSections = { checks: 'SUCCESS', reviews: [{ author: { login: 'carol' }, state: 'APPROVED' }] };
  const detail8 = (title: string, sections: PRSections = passing) => prDetail(a, 8, { title, head: 'feat/limiter-cache', base: 'feat/limiter-core', draft: true, stack, ...sections });
  prAnswer(a, 7, { state: 'MERGED', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  prAnswer(a, 8, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  prDetail(a, 7, { title: 'Add the limiter', head: 'feat/limiter-core', base: 'main', state: 'MERGED', stack, body: 'The core limiter.', checks: 'SUCCESS', reviews: [{ author: { login: 'ann' }, state: 'APPROVED' }] });
  detail8('Cache tokens');
  const ship = await launchShipCarrying(a, [8, 7]);

  await page.clock.install();
  const reads: string[] = [];
  page.on('request', (r) => {
    const u = new URL(r.url());
    if (u.pathname === `/api/v1/ships/${ship.session}/pr`) reads.push(u.searchParams.get('url') ?? '');
  });
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();
  await expect(page).toHaveURL(shipViewUrl(a, ship));
  expect(reads, 'the PR tab is closed: nothing reads a PR from GitHub').toEqual([]);

  // The tab counts the open PRs, and the count is part of its name.
  await expect(page.getByRole('tab', { name: /^PR\s*1$/ })).toBeVisible();

  // The summary sits above the tabs and opens the PR tab.
  const summary = prSummaryButton(page);
  await expect(summary).toHaveText(/1 PR · Ready to merge/);
  expect((await summary.boundingBox())!.y).toBeLessThan((await viewTab(page, 'PR').boundingBox())!.y);
  await expect(viewTab(page, 'PR')).toHaveAttribute('aria-selected', 'false');
  await summary.tap();
  await expect(viewTab(page, 'PR')).toHaveAttribute('aria-selected', 'true');

  // The sub-tabs follow the stack, bottom first, though the ship listed the top layer first; the merged one keeps its tab with its style.
  const panel = viewPanel(page);
  await expect(panel).toContainText('Stacked on GitHub · 2 PRs, bottom to top.');
  const subtabs = panel.getByRole('tablist');
  const tabs = subtabs.getByRole('tab');
  await expect(tabs).toHaveText([/#7\s*Add the limiter/, /#8\s*Cache tokens/]);
  await expect(tabs.nth(0)).toHaveClass(/\bmerged\b/);
  await expect(tabs.nth(1)).not.toHaveClass(/\bmerged\b/);
  await expect(subtabs.locator('.stack-arrow')).toHaveCount(1);
  await expect(tabs.nth(1), 'the tab opens on the first open PR, not the merged bottom one').toHaveAttribute('aria-selected', 'true');
  await expect(panel.getByRole('heading', { name: 'Cache tokens' })).toBeVisible();
  await expect(prSection(panel, 'Description')).toContainText('No description.');
  expect(await subtabs.evaluate((e) => ['auto', 'scroll'].includes(getComputedStyle(e).overflowX)), 'the row scrolls sideways').toBe(true);
  await expect.poll(() => [...new Set(reads)].sort(), 'the tab reads every PR the ship carries').toEqual([`${PR_BASE}/7`, `${PR_BASE}/8`]);
  await expect(page.getByRole('alert')).toHaveCount(0);

  // Tapping a sub-tab switches the header and the chips.
  await tabs.nth(1).tap();
  await expect(tabs.nth(1)).toHaveAttribute('aria-selected', 'true');
  await expect(panel.getByRole('heading', { name: 'Cache tokens' })).toBeVisible();
  await expect(panel).toContainText('#8');
  await expect(panel).toContainText('feat/limiter-cache → feat/limiter-core');
  await expect(panel).toContainText('Draft');
  await expect(panel.getByRole('link', { name: 'Open on GitHub' })).toHaveAttribute('href', `${PR_BASE}/8`);
  for (const chip of ['Approved', 'Checks passing', 'Mergeable']) await expectChipTone(page, panel.locator('.pr-status').getByText(chip, { exact: true }), 'ok');
  await tabs.nth(0).tap();
  await expect(tabs.nth(0)).toHaveAttribute('aria-selected', 'true');
  await expect(panel.getByRole('heading', { name: 'Add the limiter' })).toBeVisible();
  await expect(panel).toContainText('feat/limiter-core → main');
  await expect(panel).toContainText('Merged');
  await expect(panel.locator('.pr-status').getByText('Approved', { exact: true })).toHaveCount(0);

  // A merged PR keeps its sections, with a muted line where it has nothing to show.
  await expect(prSection(panel, 'Checks')).toContainText('All checks passed');
  await expect(prSection(panel, 'Description')).toContainText('The core limiter.');
  await expect(prSection(panel, 'Description').getByRole('button'), 'a description that fits has nothing to unclamp').toHaveCount(0);
  await expect(prSection(panel, 'Conversation')).toContainText('No reviews or comments yet.');
  if (opts.phone) await panel.locator('.rev-summary').tap();
  await expect(panel.locator('.reviewers').getByRole('listitem')).toHaveText([/ann\s*Approved/]);

  // A failing PR's chips are red, and the summary and dot follow the later poll.
  prAnswer(a, 8, { state: 'OPEN', review: 'CHANGES_REQUESTED', checks: 'FAILURE', mergeable: 'CONFLICTING' });
  expect((await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' })).status).toBe(202);
  await expect(summary).toHaveText(/1 PR · #8 changes requested/);
  await tabs.nth(1).tap();
  for (const chip of ['Changes requested', 'Checks failing', 'Conflicts']) await expectChipTone(page, panel.locator('.pr-status').getByText(chip, { exact: true }), 'err');
  await expect(panel.locator('.pr-status').getByText('Approved', { exact: true })).toHaveCount(0);

  // Every 30s the open tab reads GitHub again; a tab left behind does not.
  detail8('Cache tokens, renamed', prSectionsFixture());
  await page.clock.fastForward(30_000);
  await expect(panel.getByRole('heading', { name: 'Cache tokens, renamed' })).toBeVisible();
  await expectPRSections(page, panel, tabs.nth(1), { phone: opts.phone ?? false });

  // The next refresh keeps what the engineer opened.
  detail8('Cache tokens, refreshed', prSectionsFixture());
  await page.clock.fastForward(30_000);
  await expect(panel.getByRole('heading', { name: 'Cache tokens, refreshed' })).toBeVisible();
  await expectPRSectionsStayOpen(panel, { phone: opts.phone ?? false });
  const unbroken = 'Cache-tokens-' + 'x'.repeat(150);
  detail8(unbroken);
  await page.clock.fastForward(30_000);
  await expect(panel.getByRole('heading', { name: unbroken })).toBeVisible();
  expect(await noHorizontalScroll(page), 'an unbroken long title wraps inside the header').toBe(true);
  // A refresh GitHub fails keeps the last good detail, so the stack and its order hold.
  rmSync(join(a.home, 'fakegh', 'pr_detail', `${PR_BASE}/7`.replace(/[/:]/g, '_')));
  const failed = reads.length;
  await page.clock.fastForward(30_000);
  await expect.poll(() => reads.length, 'the refresh reads again').toBeGreaterThan(failed);
  await expect(tabs).toHaveText([/#7\s*Add the limiter/, /#8/]);
  await viewTab(page, 'Conversation').tap();
  const before = reads.length;
  detail8('Cache tokens, renamed again');
  await page.clock.fastForward(90_000);
  await holds(() => reads.length === before, 'a closed PR tab reads nothing', 500);
  await viewTab(page, 'PR').tap();
  await tabs.nth(1).tap();
  await expect(panel.getByRole('heading', { name: 'Cache tokens, renamed again' })).toBeVisible();
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
  ask: (id: string, questions: { question: string; options: string[]; multiSelect?: boolean; header?: string; descriptions?: string[] }[]): TranscriptLine =>
    line.toolUse(id, 'AskUserQuestion', {
      questions: questions.map((q) => ({
        question: q.question,
        header: q.header ?? 'Q',
        multiSelect: q.multiSelect ?? false,
        options: q.options.map((label, i) => ({ label, description: q.descriptions?.[i] ?? '' })),
      })),
    }),
  /** The result of an AskUserQuestion call: Claude records one answer string per question text. */
  askResult: (id: string, answers: Record<string, string>): TranscriptLine => ({
    ...line.toolResult(id, 'User has answered your questions'),
    toolUseResult: { answers },
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
/** A ship's own `@admiral-<name>` session option, such as where Return to port has got to. */
export const shipOption = (a: Admiral, ship: LaunchedShip, name: string) => tmux(a, 'show-options', '-v', '-t', ship.session, `@admiral-${name}`).stdout.trim();
export const paneSize = (a: Admiral, ship: LaunchedShip) => tmux(a, 'display', '-p', '-t', ship.session, '#{window_width}x#{window_height}').stdout.trim();

/** Sizes the ship's window as a terminal on a small screen leaves it. */
export const resizeWindow = (a: Admiral, ship: LaunchedShip, cols: number, rows: number) => {
  const r = tmux(a, 'resize-window', '-t', ship.session, '-x', String(cols), '-y', String(rows));
  if (r.status !== 0) throw new Error(`resize-window: ${r.stderr}`);
};

/** Asserts that `ok` stays true for `ms`: the way to show that something does not happen. */
export async function holds(ok: () => boolean, what: string, ms = 1500) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    expect(ok(), what).toBe(true);
    await new Promise((r) => setTimeout(r, 100));
  }
}

/** The ship view's tabs, and the link that opens the view from the ship's row. */
export const viewTab = (page: Page, name: 'Conversation' | 'Terminal' | 'Changes' | 'PR') => page.getByRole('tab', { name: new RegExp(`^${name}(\\s*\\d+)?$`) });
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
  const send = async (button = submit) => {
    const req = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
    const res = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
    await press(button);
    return { body: (await req).postDataJSON() as Post, res: await res };
  };
  const launched = async (sent: { res: { status(): number; json(): Promise<unknown> } }) => {
    expect(sent.res.status()).toBe(201);
    return ((await sent.res.json()) as { ship: LaunchedShip }).ship;
  };
  /** A launch boards its ship; Back returns to the list. */
  const boarded = async (ship: LaunchedShip, opts: { waitsOnMessage?: boolean } = {}) => {
    await expect(form).toBeHidden();
    await expect(page).toHaveURL(shipViewUrl(a, ship));
    if (opts.waitsOnMessage) {
      // A new ship with no conversation yet waits on the engineer's first message, so the box has focus and no card stands over it.
      await expect(messageBox(page)).toBeFocused();
      await expect(viewPanel(page).locator('.convo-card')).toHaveCount(0);
    }
    await press(page.getByRole('link', { name: 'Back', exact: true }));
    await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=zulu/);
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
  await boarded(apiShip, { waitsOnMessage: true });
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
  await boarded(reviewer);
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
  await expect(form.getByRole('radio', { name: /codex/i })).toBeChecked();
  // The form re-renders after the harness switch and can drop the first click.
  await expect(async () => {
    await form.getByRole('checkbox', { name: /autopilot/i }).check();
  }).toPass();
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
  const justLaunch = form.getByRole('button', { name: 'Just launch', exact: true });
  await form.getByLabel('Skill').selectOption({ label: 'Just launch' });
  await expect(crewGroup).toBeHidden();
  await expect(justLaunch).toBeHidden();
  await form.getByLabel('Skill').selectOption({ label: 'Commodore' });
  await crewGroup.getByRole('checkbox', { name: /scout/ }).check();
  // The Just launch button skips the Skill pick: whatever the select holds, it sends no skill and no pins.
  await form.getByLabel('Text').fill('just launched');
  const plain = await send(justLaunch);
  expect(plain.body).toMatchObject({ skill: { kind: 'none' }, text: 'just launched' });
  expect(plain.body).not.toHaveProperty('crew');
  const plainShip = await launched(plain);
  await boarded(plainShip, { waitsOnMessage: true });
  await expect(main.getByRole('listitem').filter({ hasText: plainShip.name })).toBeVisible();
  setState(a, plainShip, 'working');
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form).toBeVisible();
  await expect(form.getByLabel('Skill')).toHaveValue('commodore');
  await crewGroup.getByRole('checkbox', { name: /scout/ }).check();
  await form.getByLabel('Text').fill('with a pinned scout');
  const pinned = await send();
  expect(pinned.body).toMatchObject({ skill: { kind: 'commodore' }, crew: ['scout'] });
  const pinnedShip = await launched(pinned);
  await boarded(pinnedShip);
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
  await boarded(memberShip);
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
  await boarded(widgetShip);
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
export function outcomeEvents(a: Admiral, ship: LaunchedShip, keep = ['launched', 'ended', 'stopped']): string[] {
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
    .filter((e) => keep.includes(e));
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

/** An item file a ship leaves in its inbox; the server tags it with the ship's name and fleet. `session` `_admiral` is a notice. */
export function seedInboxItem(a: Admiral, session: string, item: { id: string; kind: 'question' | 'review' | 'findings' | 'notice' | 'crew_notes' | 'crew_member' | 'routine_proposal' | 'event_proposal'; title: string } & Record<string, unknown>) {
  const dir = join(a.home, 'inbox', session);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `${item.id}.json`), JSON.stringify({ version: 1, created: new Date().toISOString(), ...item }));
}

/** Scripts the fake gh: a REST call with this method and endpoint (no leading slash) gets this status and body. */
export function rest(a: Admiral, key: string, method: string, endpoint: string, status: number, body: unknown = {}, stderr = '') {
  const dir = join(a.home, 'fakegh', 'rest');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `${key}.endpoint`), endpoint);
  writeFileSync(join(dir, `${key}.method`), method);
  writeFileSync(join(dir, `${key}.status`), String(status));
  writeFileSync(join(dir, `${key}.json`), JSON.stringify(body));
  if (stderr) writeFileSync(join(dir, `${key}.stderr`), stderr);
}

/** Every REST call the fake gh was run for, as "METHOD endpoint", sorted. */
export function restCalls(a: Admiral): string[] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  const read = (d: string, f: string) => (existsSync(join(dir, d, f)) ? readFileSync(join(dir, d, f), 'utf8').trim() : '');
  return readdirSync(dir)
    .filter((d) => read(d, 'endpoint') !== '')
    .map((d) => `${read(d, 'method')} ${read(d, 'endpoint')}`)
    .sort();
}

/** A review item's target: PR 12 of acme/widgets and its pending review 123, with the fake gh answering Submit and Discard. */
export function seedReviewTarget(a: Admiral) {
  rest(a, 'review-get', 'GET', 'repos/acme/widgets/pulls/12/reviews/123', 200, { state: 'PENDING' });
  rest(a, 'review-events', 'POST', 'repos/acme/widgets/pulls/12/reviews/123/events', 200, { state: 'COMMENTED' });
  rest(a, 'review-delete', 'DELETE', 'repos/acme/widgets/pulls/12/reviews/123', 200, {});
  return { pr_url: 'https://github.com/acme/widgets/pull/12', review_id: 123 };
}

export const TS_NAME = 'fake-mac.tailnet-fake.ts.net';
export const TS_LOGIN = 'alice@example.com';
const fakeTailscaleDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'testdata', 'faketailscale');

/** The fake `tailscale` (e2e/testdata/faketailscale): pass `env` to `start`; the files steer its answers (docs/agents/testing.md). */
export function fakeTailscale(a: Admiral) {
  const dir = join(a.home, 'fakets');
  mkdirSync(dir, { recursive: true });
  const put = (name: string, text: string) => writeFileSync(join(dir, name), text);
  const status = (state = 'Running', certDomains: string[] = [TS_NAME]) =>
    put(
      'status.json',
      JSON.stringify({
        BackendState: state,
        Self: { DNSName: `${TS_NAME}.`, UserID: 1001 },
        CurrentTailnet: { MagicDNSEnabled: true },
        CertDomains: certDomains,
        User: { '1001': { LoginName: TS_LOGIN } },
      }),
    );
  /** Serve proxies HTTPS 443 to the Admiral on `port`, or nothing at all. */
  const serve = (port?: string) =>
    put(
      'serve.json',
      JSON.stringify(
        port
          ? { TCP: { '443': { HTTPS: true } }, Web: { [`${TS_NAME}:443`]: { Handlers: { '/': { Proxy: `http://127.0.0.1:${port}` } } } } }
          : {},
      ),
    );
  status();
  serve();
  return {
    dir,
    env: { FAKETS_DIR: dir, PATH: `${fakeTailscaleDir}:${process.env.PATH ?? ''}`, ADMIRAL_TAILSCALE_SERVE_TIMEOUT: '2s' },
    put,
    status,
    serve,
    calls: () => (existsSync(join(dir, 'calls')) ? readFileSync(join(dir, 'calls'), 'utf8').split('\n').filter(Boolean) : []),
    serveCalls: () => (existsSync(join(dir, 'calls')) ? readFileSync(join(dir, 'calls'), 'utf8').split('\n').filter((l) => l.startsWith('serve --bg')) : []),
  };
}

/** Replaces the clipboard so a tap on Copy can be read back; `copied` is what the page last wrote. */
export async function stubClipboard(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as { __copied?: string };
    Object.defineProperty(navigator, 'clipboard', { value: { writeText: async (t: string) => void (w.__copied = t) }, configurable: true });
  });
  return () => page.evaluate(() => (window as unknown as { __copied?: string }).__copied);
}

/** `remote` in config.json, merged into what is there. */
export function setRemote(a: Admiral, remote: { tailscale: boolean; allowedLogins: string[] }) {
  const cfg = existsSync(join(a.home, 'config.json')) ? JSON.parse(readFileSync(join(a.home, 'config.json'), 'utf8')) : { version: 1 };
  a.writeConfig({ ...cfg, remote });
}

/** A pull request of acme/api that My PRs lists: its number, title and the state both the search and the poll answer. */
export interface MyPR {
  number: number;
  title: string;
  review: string;
  checks: string;
  mergeable: string;
}

export const READY_PR: MyPR = { number: 7, title: 'Add the rate limiter', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' };
export const WAITING_PR: MyPR = { number: 8, title: 'Cache the token lookups', review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE' };
export const SHIPLESS_PR: MyPR = { number: 9, title: 'Fix the flaky retry', review: 'REVIEW_REQUIRED', checks: 'FAILURE', mergeable: 'CONFLICTING' };

/**
 * What the fake gh's search answers for My PRs (the one query with `author:@me`): these open PRs of acme/api, routed to the zulu fleet.
 * The poll answers the same state, so an order's chips and its moored ship's chips agree. Call before `start`, after addGitHubSource.
 */
export function seedMyPRs(a: Admiral, prs: MyPR[]) {
  const path = join(a.home, 'config.json');
  const config = JSON.parse(readFileSync(path, 'utf8'));
  config.fleets.zulu.owners = ['acme'];
  a.writeConfig(config);
  const dir = join(a.home, 'fakegh', 'search');
  mkdirSync(dir, { recursive: true });
  const node = (p: MyPR) => ({
    number: p.number, title: p.title, url: `${PR_BASE}/${p.number}`,
    repository: { nameWithOwner: 'acme/api', url: 'https://github.com/acme/api' }, labels: { nodes: [] },
    state: 'OPEN', reviewDecision: p.review, mergeable: p.mergeable,
    mergeStateStatus: p.review === 'APPROVED' && p.checks === 'SUCCESS' && p.mergeable === 'MERGEABLE' ? 'CLEAN' : 'BLOCKED',
    statusCheckRollup: { state: p.checks },
  });
  writeFileSync(join(dir, 'a-mine.match'), 'author:@me');
  writeFileSync(join(dir, 'a-mine.json'), JSON.stringify({ data: { search: { nodes: prs.map(node) } } }));
  // The other default views ask for something else and get nothing.
  writeFileSync(join(dir, 'z-rest.match'), 'is:open');
  writeFileSync(join(dir, 'z-rest.json'), JSON.stringify({ data: { search: { nodes: [] } } }));
  for (const p of prs) prAnswer(a, p.number, { state: 'OPEN', review: p.review, checks: p.checks, mergeable: p.mergeable });
}

/** A My PRs row: the Orders list's item for this PR. */
const myPRRow = (page: Page, p: MyPR) => page.getByRole('region', { name: 'Orders' }).getByRole('listitem').filter({ hasText: p.title });

/** Opens the Ships screen and waits for the PR's row in My PRs. */
async function openMyPRs(page: Page, a: Admiral, p: MyPR) {
  await page.goto(a.url + 'app/ships?fleet=zulu');
  const row = myPRRow(page, p);
  await expect(row).toBeVisible();
  return row;
}

const SHIPLESS_NOTE = /Ready to merge|Waiting on|Has conflicts/;

/**
 * A ready PR a ship carries: its row in My PRs has the chips, the ship's link and Ready to merge, and Merge with the method, with no Launch.
 * Nothing merges before the tap; one tap records one `gh pr merge <url> --squash` and the ship sails off.
 */
export async function myPRsMergeACarriedPR(page: Page, a: Admiral, opts: { phone: boolean }) {
  seedMergeMethod(a, 'squash');
  ghMergeSlow(a, 2);
  const ship = await launchMooredShip(a, READY_PR.number);
  await readyOnTheServer(a, ship, 'squash');

  const row = await openMyPRs(page, a, READY_PR);
  for (const chip of ['Approved', 'Checks passing', 'Mergeable']) await expect(row).toContainText(chip);
  await expect(row).toContainText('Ready to merge');
  await expect(row.getByRole('link', { name: ship.name })).toBeVisible();
  await expect(row.getByRole('button', { name: /^Launch/ })).toHaveCount(0);
  const merge = row.getByRole('button', { name: 'Merge', exact: true });
  // The row sits under the moored ships, so it takes a scroll to reach, as on a real screen.
  await merge.scrollIntoViewIfNeeded();
  await expect(merge).toBeInViewport();
  expect((await merge.boundingBox())!.height, 'Merge height').toBeGreaterThanOrEqual(44);
  await expect(row).toContainText('squash');
  await expect(row.getByRole('button', { name: /Merge up to/ })).toHaveCount(0);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);
  await holds(() => mergeCalls(a).length === 0, 'opening My PRs merges nothing');

  await merge.tap();
  await expect(merge).toBeDisabled();
  await expect.poll(() => mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/${READY_PR.number}`, '--squash']]);
  await expect
    .poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status ?? 'gone', { timeout: 30_000 })
    .not.toBe('moored');
  expect(mergeCalls(a), 'one merge').toHaveLength(1);
  // Whether the row stays or the refresh removes it, a merged PR is never offered for launch.
  await expect(row.getByRole('button', { name: /^Launch|^Merge/ })).toHaveCount(0);
  if (await row.isVisible()) await expect(row).toContainText('Merged');
}

/** A PR a ship carries that is not ready: its row has the chips, the ship and a next step that is not Ready to merge, and offers no Merge and no Launch. */
export async function myPRsShowANotReadyCarriedPR(page: Page, a: Admiral, opts: { phone: boolean }) {
  const ship = await launchMooredShip(a, WAITING_PR.number);
  await expect
    .poll(async () => {
      await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
      const got = ((await (await fetch(`${a.url}api/v1/ships`)).json()) as { ships: (ShipJSON & { prs?: { review?: string }[]; waiting?: string; no_resume?: boolean })[] }).ships.find((s) => s.session === ship.session);
      // A waiting text or a missing resume would put its own words ahead of the chips' next step.
      return { review: got?.prs?.[0]?.review, waiting: got?.waiting ?? '', noResume: got?.no_resume ?? false };
    }, { timeout: 30_000 })
    .toEqual({ review: 'REVIEW_REQUIRED', waiting: '', noResume: false });

  const row = await openMyPRs(page, a, WAITING_PR);
  for (const chip of ['Review required', 'Checks passing', 'Mergeable']) await expect(row).toContainText(chip);
  await expect(row.getByRole('link', { name: ship.name })).toBeVisible();
  await expect(row).toContainText('Waiting on review');
  await expect(row).not.toContainText('Ready to merge');
  await expect(row.getByRole('button', { name: /^Merge/ })).toHaveCount(0);
  await expect(row.getByRole('button', { name: /^Launch/ })).toHaveCount(0);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);
}

/**
 * A PR no ship carries: its row has the chips and no ship, no next step and no Merge, and Launch Commodore, which opens the form
 * with Commodore and the PR's URL as its text. Nothing is launched.
 */
export async function myPRsOfferCommodoreOnAShiplessPR(page: Page, a: Admiral, opts: { phone: boolean }) {
  // The server has no Commodore; the form falls back to another skill unless the claude dock's identity has one.
  const commodore = join(a.home, 'identity', 'zulu-claude', 'skills', 'commodore');
  mkdirSync(commodore, { recursive: true });
  writeFileSync(join(commodore, 'SKILL.md'), '---\nname: commodore\ndescription: a fake skill\n---\n');

  const row = await openMyPRs(page, a, SHIPLESS_PR);
  for (const chip of ['Review required', 'Checks failing', 'Conflicts']) await expect(row).toContainText(chip);
  await expect(row).not.toContainText(SHIPLESS_NOTE);
  await expect(row.getByRole('button', { name: /^Merge/ })).toHaveCount(0);
  await expect(row.locator('a[href*="/app/ships/"]'), 'no ship carries it').toHaveCount(0);
  const launch = row.getByRole('button', { name: 'Launch Commodore', exact: true });
  await expect(launch).toHaveText('Launch Commodore');
  // Only a PR order says Commodore: the other orders keep Launch.
  await expect(page.getByRole('region', { name: 'Orders' }).getByRole('button', { name: /^Launch Tighten the api limits/ })).toHaveText('Launch');
  await expect(launch).toBeInViewport();
  expect((await launch.boundingBox())!.height, 'Launch Commodore height').toBeGreaterThanOrEqual(44);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);

  await launch.tap();
  const form = page.getByRole('dialog', { name: 'Launch', exact: true });
  await expect(form).toBeVisible();
  await expect(form.getByLabel('Skill')).toHaveValue('commodore');
  await expect(form.getByLabel('Text')).toHaveValue(`${PR_BASE}/${SHIPLESS_PR.number}`);
  await form.getByRole('button', { name: 'Cancel', exact: true }).tap();
  await expect(form).toBeHidden();
  expect(await listShips(a), 'nothing was launched').toEqual([]);
}

/** A merge gh refuses shows in the row's one alert, merges nothing and leaves the ship moored. */
export async function myPRsMergeFailsOnTheRow(page: Page, a: Admiral) {
  seedMergeMethod(a, 'squash');
  ghMergeFails(a, 'GraphQL: Pull request is not mergeable (mergePullRequest)');
  const ship = await launchMooredShip(a, READY_PR.number);
  await readyOnTheServer(a, ship, 'squash');

  const row = await openMyPRs(page, a, READY_PR);
  await row.getByRole('button', { name: 'Merge', exact: true }).tap();
  await expect(row.getByRole('alert')).toContainText('Pull request is not mergeable');
  await expect(page.getByRole('alert'), 'no second alert').toHaveCount(1);
  expect(mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/${READY_PR.number}`, '--squash']]);
  await expect(row.getByRole('button', { name: 'Merge', exact: true })).toBeEnabled();
  expect((await listShips(a)).find((s) => s.session === ship.session)?.status).toBe('moored');
}

/**
 * Writes passing AdmiralBot probe results for a fleet's harness, as the store keeps them, so the picker offers it.
 * They are keyed by the version the server detected (Check again detects it again), so this waits for the startup
 * detection to land first: a later write of it could not overwrite the seed.
 */
export async function proveAdmiralBot(a: Admiral, fleet: string, harness: string): Promise<void> {
  const detected = async () => {
    const res = await fetch(a.url + 'api/v1/harness-capabilities');
    const body = (await res.json()) as { fleets: { key: string; harnesses: { harness: string; version: string }[] }[] };
    return body.fleets.find((f) => f.key === fleet)?.harnesses.find((h) => h.harness === harness)?.version ?? '';
  };
  await expect.poll(detected).not.toBe('');
  const version = await detected();
  const file = join(a.home, 'capabilities.json');
  const store = existsSync(file) ? JSON.parse(readFileSync(file, 'utf8')) : { fleets: {} };
  const at = '2026-01-02T03:04:05Z';
  const pass = { passed: true, at };
  store.fleets[fleet] ??= {};
  store.fleets[fleet][harness] = {
    ...store.fleets[fleet][harness],
    version,
    detected_at: at,
    versions: { [version]: { 'bot.mcp_fence': pass, 'bot.config_home': pass } },
    probed_version: version,
    probed_at: at,
  };
  writeFileSync(file, JSON.stringify(store));
}

/** The fake gh answers the review-requested search with one PR from `sender` (newer than the baseline poll), plus the viewer and the REST item the Lookout reads. */
export function seedReviewRequest(a: Admiral, opts: { sender: string; repo?: string; number?: number; title?: string; body?: string }) {
  const repo = opts.repo ?? 'acme/api';
  const number = opts.number ?? 7;
  const title = opts.title ?? 'Fix the quarry lantern';
  const [owner, name] = repo.split('/') as [string, string];
  const node = {
    __typename: 'PullRequest', number, title, url: `https://github.com/${repo}/pull/${number}`, updatedAt: '2026-10-18T09:00:00Z',
    repository: { nameWithOwner: repo, name, owner: { login: owner }, url: `https://github.com/${repo}` },
    labels: { nodes: [] },
    author: { login: opts.sender },
    timelineItems: { nodes: [{ __typename: 'ReviewRequestedEvent', createdAt: new Date(Date.now() + 3_600_000).toISOString(), actor: { login: opts.sender }, requestedReviewer: { login: 'fake-viewer' } }] },
  };
  const dir = a.fakeGh().dir;
  mkdirSync(join(dir, 'search'), { recursive: true });
  mkdirSync(join(dir, 'rest'), { recursive: true });
  writeFileSync(join(dir, 'search', 'review.match'), 'review-requested:@me');
  writeFileSync(join(dir, 'search', 'review.json'), JSON.stringify({ data: { search: { issueCount: 1, nodes: [node] } } }));
  writeFileSync(join(dir, 'viewer'), 'fake-viewer');
  writeFileSync(join(dir, 'rest', 'item.endpoint'), `repos/${repo}/issues/${number}`);
  writeFileSync(join(dir, 'rest', 'item.json'), JSON.stringify({ title, body: opts.body ?? 'Please review my lantern fix.' }));
}

/** Outcome events a Moor or a Return to port can leave. */
const ENDINGS = ['launched', 'moored', 'returning', 'ended', 'stopped'];

/**
 * Launches a live ship whose report (result `partial`, so it does not moor on its own) names the PRs, all open on the fake gh, and waits
 * for the server to list them as `open_prs`. `needs_you` ends its turn (reason `stop`); `working` leaves it underway.
 */
export async function launchShipWithOpenPRs(a: Admiral, numbers: number[], opts: { state?: 'working' | 'needs_you' } = {}): Promise<LaunchedShip> {
  for (const n of numbers) prAnswer(a, n, { state: 'OPEN' });
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  const rec = join(a.home, 'rec', ship.session);
  await expect.poll(() => existsSync(join(rec, 'recorded'))).toBe(true);
  await reportOpenPRs(a, ship, numbers);
  if (opts.state === 'needs_you') {
    setReason(a, ship, 'stop');
    setState(a, ship, 'needs_you');
  } else setState(a, ship, 'working');
  await expectOpenPRs(a, ship, numbers);
  return ship;
}

/** Writes the ship's report (result `partial`, so it does not moor on its own) naming the PRs. */
async function reportOpenPRs(a: Admiral, ship: LaunchedShip, numbers: number[]) {
  const report = /^ADMIRAL_REPORT=(.*)$/m.exec(readFileSync(join(a.home, 'rec', ship.session, 'env'), 'utf8'))?.[1];
  expect(report, 'the harness env names a report path').toBeTruthy();
  writeFileSync(report!, JSON.stringify({ version: 2, result: 'partial', why: 'Waiting on review', prs: numbers.map((n) => ({ url: `${PR_BASE}/${n}`, closes: [] })), resume: RESUME }));
}

async function expectOpenPRs(a: Admiral, ship: LaunchedShip, numbers: number[]) {
  await expect
    .poll(async () => {
      const res = await fetch(`${a.url}api/v1/ships`);
      return ((await res.json()) as { ships: { session: string; open_prs?: string[] }[] }).ships.find((s) => s.session === ship.session)?.open_prs;
    })
    .toEqual(numbers.map((n) => `${PR_BASE}/${n}`));
}

/** The ship's status in the API, or undefined when it is not listed. */
async function statusOf(a: Admiral, ship: LaunchedShip): Promise<string | undefined> {
  const res = await fetch(`${a.url}api/v1/ships`);
  const ships = ((await res.json()) as { ships: { session: string; name: string; status: string }[] }).ships;
  return ships.find((s) => s.session === ship.session || (s.status === 'moored' && s.name === ship.name))?.status;
}

type SheetName = 'Stop ship' | 'Return to port';

/** Opens the ship's view and its sheet, and checks the sheet's open-PR copy, the buttons in order at touch size and the focus on Cancel. */
async function openPRSheet(page: Page, a: Admiral, ship: LaunchedShip, sheet: SheetName, numbers: number[], press: (l: Locator) => Promise<void>): Promise<Locator> {
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await press(rowLink(page, ship));
  await press(page.getByRole('main').getByRole('button', { name: sheet, exact: true }));
  const dialog = page.getByRole('dialog', { name: sheet });
  await expect(dialog).toBeVisible();
  const count = numbers.length === 1 ? '1 open PR' : `${numbers.length} open PRs`;
  await expect(dialog).toContainText(`${ship.name} carries ${count}`);
  for (const n of numbers) await expect(dialog.getByRole('link', { name: `api#${n}` })).toHaveAttribute('href', `${PR_BASE}/${n}`);
  await expect(dialog.getByRole('link')).toHaveCount(numbers.length);
  await expect(dialog.getByRole('listitem')).toHaveCount(numbers.length);
  for (const n of numbers) await expect(dialog.getByRole('listitem').filter({ has: page.getByRole('link', { name: `api#${n}` }) })).toHaveCount(1);
  expect(await dialog.getByRole('list').innerText(), 'the PRs read as separate items').not.toMatch(/api#\d+api#\d+/);
  if (sheet === 'Stop ship') {
    await expect(dialog).toContainText('Moor ends the session now and keeps the PRs; the ship wakes when reviews come in. Stop for good ends it and forgets them.');
  } else {
    await expect(dialog).toContainText('Moor ends the session now with no clean-up prompt and keeps the PRs. Return to port tells it to clean up, then stops it and forgets the PRs.');
  }
  const other = sheet === 'Stop ship' ? 'Stop for good' : 'Return to port';
  await expect(dialog.getByRole('button')).toHaveText(['Cancel', other, 'Moor']);
  await expect(dialog.getByRole('button', { name: 'Moor', exact: true })).toHaveClass(/\bprimary\b/);
  await expect(dialog.getByRole('button', { name: other, exact: true })).not.toHaveClass(/\bprimary\b/);
  if (sheet === 'Stop ship') await expect(dialog.getByRole('button', { name: other, exact: true })).toHaveClass(/\bdanger\b/);
  for (const b of await dialog.getByRole('button').all()) expect((await b.boundingBox())!.height, `${await b.textContent()} height`).toBeGreaterThanOrEqual(44);
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
  return dialog;
}

/** The POST to a ship's stop or return route that the page sends next. */
const endRequest = (page: Page, ship: LaunchedShip, action: 'stop' | 'return') =>
  page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/v1/ships/${ship.session}/${action}`);

/** The Ships screen holds the moored ship's row, with its PR, and no live row. */
async function expectMooredRow(page: Page, ship: LaunchedShip, pr: number) {
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  const rows = page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name });
  await expect(rows).toHaveCount(1);
  await expect(page.getByRole('main').getByRole('region', { name: /^Moored/ }).getByRole('listitem').filter({ hasText: ship.name }).getByRole('link', { name: `api#${pr}` })).toHaveAttribute('href', `${PR_BASE}/${pr}`);
}

/** Moor on the Stop ship sheet of a ship at the end of its turn with two open PRs: it moors, with no body on Stop for good's route to compare. */
export async function stopShipMoors(page: Page, a: Admiral, press: (l: Locator) => Promise<void>, opts: { phone: boolean }) {
  const ship = await launchShipWithOpenPRs(a, [7, 8], { state: 'needs_you' });
  const dialog = await openPRSheet(page, a, ship, 'Stop ship', [7, 8], press);
  if (opts.phone) expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  // Both buttons wait while the request is out.
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route(`**/api/v1/ships/${ship.session}/stop`, async (route) => {
    await gate;
    await route.continue();
  });
  const sent = endRequest(page, ship, 'stop');
  await press(dialog.getByRole('button', { name: 'Moor', exact: true }));
  await expect(dialog.getByRole('button', { name: 'Moor', exact: true })).toBeDisabled();
  await expect(dialog.getByRole('button', { name: 'Stop for good', exact: true })).toBeDisabled();
  release();
  const res = await sent;
  expect(JSON.parse(res.request().postData() ?? 'null')).toEqual({ moor: true });
  expect(res.status()).toBe(200);
  await page.unroute(`**/api/v1/ships/${ship.session}/stop`);

  await expectMooredRow(page, ship, 7);
  await expect(page.getByRole('main').getByRole('region', { name: /^Moored/ }).getByRole('listitem').filter({ hasText: ship.name }).getByRole('link', { name: 'api#8' })).toBeVisible();
  await expect.poll(() => statusOf(a, ship)).toBe('moored');
  await expect.poll(() => outcomeEvents(a, ship, ENDINGS)).toEqual(['launched', 'moored', 'ended']);
}

/** Stop for good on the same sheet posts no body, and the ship ends and is not moored. */
export async function stopForGoodEnds(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  const ship = await launchShipWithOpenPRs(a, [7], { state: 'working' });
  const dialog = await openPRSheet(page, a, ship, 'Stop ship', [7], press);
  const sent = endRequest(page, ship, 'stop');
  await press(dialog.getByRole('button', { name: 'Stop for good', exact: true }));
  const res = await sent;
  expect(res.request().postData() ?? '').toBe('');
  expect(res.status()).toBe(200);
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  await expect(page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name })).toHaveCount(0);
  await expect.poll(() => outcomeEvents(a, ship, ENDINGS)).toEqual(['launched', 'ended', 'stopped']);
  await expect.poll(() => statusOf(a, ship)).toBeUndefined();
}

/** Moor on the Return to port sheet moors a working ship at once, and the clean-up prompt is never typed. */
export async function returnToPortMoors(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  const ship = await launchShipWithOpenPRs(a, [7], { state: 'working' });
  const dialog = await openPRSheet(page, a, ship, 'Return to port', [7], press);
  const sent = endRequest(page, ship, 'return');
  await press(dialog.getByRole('button', { name: 'Moor', exact: true }));
  const res = await sent;
  expect(JSON.parse(res.request().postData() ?? 'null')).toEqual({ moor: true });
  expect(res.status()).toBe(200);
  await expectMooredRow(page, ship, 7);
  await expect.poll(() => statusOf(a, ship)).toBe('moored');
  await expect.poll(() => outcomeEvents(a, ship, ENDINGS)).toEqual(['launched', 'moored', 'ended']);
  expect(received(a, ship)).not.toContain('Return to port: this run is over');
}

/** Return to port on the same sheet posts no body, types the clean-up prompt, and stops the ship once its turn ends. */
export async function returnToPortCleansUp(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  const ship = await launchShipWithOpenPRs(a, [7], { state: 'working' });
  const dialog = await openPRSheet(page, a, ship, 'Return to port', [7], press);
  const sent = endRequest(page, ship, 'return');
  await press(dialog.getByRole('button', { name: 'Return to port', exact: true }));
  const res = await sent;
  expect(res.request().postData() ?? '').toBe('');
  expect(res.status()).toBe(200);
  await expect(page.getByRole('button', { name: 'Returning to port' })).toBeDisabled();
  setReason(a, ship, 'stop');
  setState(a, ship, 'needs_you');
  await expect.poll(() => received(a, ship)).toContain('Return to port: this run is over');
  await expect.poll(() => shipOption(a, ship, 'returning')).toBe('sent');
  setReason(a, ship, 'stop');
  setState(a, ship, 'needs_you');
  await expect.poll(() => outcomeEvents(a, ship, ENDINGS)).toContain('stopped');
  expect(outcomeEvents(a, ship, ENDINGS)).toContain('returning');
  expect(outcomeEvents(a, ship, ENDINGS)).not.toContain('moored');
  await expect.poll(() => statusOf(a, ship)).toBeUndefined();
}

/** A PR that closed after the server counted it: Moor re-checks, the server refuses with 409, the sheet shows why and stays, and the ship stays live. */
export async function moorRefusedWhenThePRClosed(page: Page, a: Admiral, press: (l: Locator) => Promise<void>, sheet: SheetName) {
  const ship = await launchShipWithOpenPRs(a, [7], { state: 'working' });
  const dialog = await openPRSheet(page, a, ship, sheet, [7], press);
  prAnswer(a, 7, { state: 'CLOSED' });
  const sent = endRequest(page, ship, sheet === 'Stop ship' ? 'stop' : 'return');
  await press(dialog.getByRole('button', { name: 'Moor', exact: true }));
  const res = await sent;
  expect(res.status()).toBe(409);
  await expect(dialog.getByRole('alert')).toContainText('has no open PR to moor');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Moor', exact: true })).toBeEnabled();
  await expect.poll(() => statusOf(a, ship)).not.toBe('moored');
  expect(outcomeEvents(a, ship, ENDINGS)).toEqual(['launched']);
  await press(dialog.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(dialog).toBeHidden();
}

/** The plain Return to port sheet stays plain when the ship gains an open PR while it is open, so a tap on its button still cleans up. */
export async function plainReturnSheetStaysPlain(page: Page, a: Admiral, press: (l: Locator) => Promise<void>) {
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setState(a, ship, 'working');
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await press(rowLink(page, ship));
  await press(page.getByRole('main').getByRole('button', { name: 'Return to port', exact: true }));
  const dialog = page.getByRole('dialog', { name: 'Return to port' });
  await expect(dialog.getByRole('button')).toHaveText(['Cancel', 'Return to port']);

  prAnswer(a, 7, { state: 'OPEN' });
  await reportOpenPRs(a, ship, [7]);
  await expectOpenPRs(a, ship, [7]);
  await expect(dialog.getByRole('button')).toHaveText(['Cancel', 'Return to port']);
  await expect(dialog).not.toContainText('open PR');

  const sent = endRequest(page, ship, 'return');
  await press(dialog.getByRole('button', { name: 'Return to port', exact: true }));
  const res = await sent;
  expect(res.request().postData() ?? '').toBe('');
  expect(res.status()).toBe(200);
  expect(outcomeEvents(a, ship, ENDINGS)).not.toContain('moored');
}
