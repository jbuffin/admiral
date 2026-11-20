import { spawnSync } from 'node:child_process';
import { appendFileSync, chmodSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
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

/** What zulu's fake claude lists in the launch journey: fable and opus are built in, claude-opus-4-8 is in no built-in list. */
export const LAUNCH_MODELS = 'opus,sonnet,fable,haiku,claude-opus-4-8';

/** `models` is what the fake claude lists for the dock (FAKE_MODELS, comma-separated); unset, the dock serves the built-in list. */
const withModels = <D extends { env: Record<string, string> }>(d: D, models?: string): D => (models ? { ...d, env: { ...d.env, FAKE_MODELS: models } } : d);

/** Zulu (Westwind) comes first, alpha (Homeport) second; `live` makes the docks launchable and gives zulu the orders; `claude` replaces zulu's claude dock. */
export function seedFleets(a: Admiral, opts: { userHome?: string; live?: boolean; chat?: boolean; versioned?: boolean; holdChat?: ChatFleet; turnChat?: ChatFleet; mcpChat?: ChatFleet; claude?: { executable: string; env: Record<string, string> }; models?: string } = {}) {
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
      zulu: { name: WORK, search_roots: [a.home], claude: opts.claude ?? withModels(dock(a, 'zulu', 'claude', live), opts.models), codex: codexDock('zulu', 'codex') },
      alpha: { name: HOME, search_roots: [a.home], codex: codexDock('alpha', 'codex'), copilot: codexDock('alpha', 'copilot') },
    },
  };
  if (opts.chat) {
    const fleets = config.fleets as Record<string, unknown>;
    for (const f of CHAT_FLEETS) fleets[f.key] = { name: f.name, search_roots: [a.home], claude: chatDock(a, f.key, opts.holdChat === f.key, opts.turnChat === f.key, opts.mcpChat === f.key) };
  }
  if (live) {
    config.sources = { orders: a.ordersSource('orders', 'Orders', { fleet: 'zulu' }) };
    a.flags('orders').orders({ orders: ORDERS });
    gitCheckout(join(a.home, 'proj'), 'https://github.com/acme/api.git');
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

function chatDock(a: Admiral, fleet: string, hold: boolean, turn = false, mcp = false) {
  const cue = join(a.home, `chat-cue-${fleet}`);
  const startup = JSON.stringify({ transcript_path: chatTranscriptPath(a, fleet, 't1'), source: 'startup' });
  const cleared = JSON.stringify({ transcript_path: chatTranscriptPath(a, fleet, 't2'), source: 'clear' });
  // An mcp chat makes the launch_ship call the test hands it, as AdmiralBot does; the ships it launches play a cue of their own, not the chat's.
  const shipCue = join(a.home, `ship-cue-${fleet}`);
  if (mcp) writeFileSync(shipCue, `event SessionStart ${JSON.stringify({ transcript_path: chatTranscriptPath(a, fleet, 'ship'), source: 'startup' })}\nwait go\nexit 0\n`);
  // A New chat clears the context: the second transcript starts once the test touches go-clear.
  // A held chat stays before its first SessionStart, as one stopped on Claude's folder-trust dialog does, until chatStarts.
  writeFileSync(cue, ['record', 'bracketedpaste', ...(hold ? ['wait go-start'] : []), `event SessionStart ${startup}`, ...(mcp ? ['mcp launch'] : []), ...(turn ? ['hook working', 'wait go-turn-end', 'event Stop {"background_tasks":[]}', 'wait go-report-start', 'hook working', 'wait go-report-end', 'event Stop {"background_tasks":[]}'] : []), 'wait go-clear', `event SessionStart ${cleared}`].join('\n') + '\n');
  mkdirSync(join(chatConfigDir(a, fleet), 'projects', 'p'), { recursive: true });
  return { executable: fakeClaude, env: { FAKE_RECORD_DIR: join(a.home, 'rec'), FAKE_CUE: cue, CLAUDE_CONFIG_DIR: chatConfigDir(a, fleet), ...(mcp && { FAKE_SHIP_CUE: shipCue }) } };
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

/** Stamps a new prompt on the chat's pane, as its UserPromptSubmit hook does, which the fake Claude Code does not run: a ship launched for the earlier prompt no longer holds this one. */
export const chatPrompts = (a: Admiral, fleet: ChatFleet) =>
  spawnSync('tmux', ['-S', join(a.home, 'tmux.sock'), 'set-option', '-p', '-t', `${chatSession(fleet)}:`, '@admiral-chat-prompt', String(BigInt(Date.now()) * 1000000n)], { encoding: 'utf8' });

/** Has a chat seeded with `mcpChat` call launch_ship with these arguments, as its model does on the engineer's request. */
export function chatLaunches(a: Admiral, fleet: ChatFleet, args: Record<string, unknown>) {
  mkdirSync(chatRecord(a, fleet), { recursive: true });
  writeFileSync(join(chatRecord(a, fleet), 'launch'), JSON.stringify({ name: 'launch_ship', arguments: args }));
}

/** The reply the chat's launch_ship call got, once it has one. */
export const chatLaunchReply = (a: Admiral, fleet: ChatFleet) => {
  const path = join(chatRecord(a, fleet), 'launch.reply');
  return existsSync(path) ? readFileSync(path, 'utf8') : '';
};

/** Ends the engineer's turn in a chat seeded with `turnChat`, which works from its start until then, as Claude Code does while it answers. */
export function chatTurnEnds(a: Admiral, fleet: ChatFleet, step: 'turn-end' | 'report-start' | 'report-end' = 'turn-end') {
  mkdirSync(chatRecord(a, fleet), { recursive: true });
  writeFileSync(join(chatRecord(a, fleet), `go-${step}`), '');
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
  // The browser logs each refused launch, ending, merge, Re-review or work-repo save, and each PR detail or stack shape GitHub cannot give, and each skills read a journey fails on purpose, as a console error, in either order against its response: the form or row showing the refusal is the journey's own check.
  const failedLoads: string[] = [];
  let refusedLaunches = 0;
  page.on('response', (r) => {
    const refusable = { GET: /^\/api\/v1\/(ships\/[^/]+\/(pr|stack)|launch-orders\/skills)$/, POST: /^\/api\/v1\/(ships|campaigns\/[^/]+\/launch|ships\/[^/]+\/(dismiss|unfollow|merge|rereview|stack|wake)|fleets\/[^/]+\/crew\/(members(\/[^/]+\/rename)?|draft))$/, PUT: /^\/api\/v1\/(campaigns\/[^/]+\/work-repo|fleets\/[^/]+\/crew\/members\/[^/]+)$/ }[r.request().method()];
    const path = new URL(r.url()).pathname;
    if (refusable?.test(path) && r.status() >= 400) refusedLaunches++;
    // A search GitHub cannot run is a 422 on its count, and a host that is not a hostname is a 400 on the source's save; each row or field shows its own.
    if (r.request().method() === 'POST' && /^\/api\/v1\/sources\/[^/]+\/search-count$/.test(path) && r.status() === 422) refusedLaunches++;
    if (r.request().method() === 'PUT' && /^\/api\/v1\/sources\/[^/]+$/.test(path) && r.status() === 400) refusedLaunches++;
    // Moor's refusal is a 409; any other status on an ending stays an error.
    if (r.request().method() === 'POST' && /^\/api\/v1\/ships\/[^/]+\/(stop|return)$/.test(path) && r.status() === 409) refusedLaunches++;
  });
  // WebKit logs a same-origin fetch cancelled by navigation as an access-control error, sometimes as a page error.
  const cancelledFetch = (text: string) => /due to access control checks\.?$/.test(text) && text.includes(host);
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    if (cancelledFetch(m.text())) return;
    (/^Failed to load resource: .* status of [45]\d\d/.test(m.text()) ? failedLoads : errors).push(m.text());
  });
  page.on('pageerror', (e) => {
    if (!cancelledFetch(e.message)) errors.push(e.message);
  });
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

/** Makes dir a checkout with a commit on main and a fetchable origin: a local bare repo, which the GitHub URL (when given) is redirected to, since a no-skill launch fetches origin. */
export function gitCheckout(dir: string, githubUrl?: string) {
  const git = (cwd: string, ...args: string[]) => {
    const r = spawnSync('git', ['-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false', ...args], {
      cwd,
      encoding: 'utf8',
      env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1', GIT_AUTHOR_NAME: 'Test', GIT_AUTHOR_EMAIL: 'test@example.com', GIT_COMMITTER_NAME: 'Test', GIT_COMMITTER_EMAIL: 'test@example.com' },
    });
    expect(r.status, r.stderr).toBe(0);
  };
  const bare = mkdtempSync(join(tmpdir(), 'admiral-ui-origin-'));
  mkdirSync(dir, { recursive: true });
  git(bare, 'init', '-q', '--bare', '-b', 'main');
  git(dir, 'init', '-q', '-b', 'main');
  git(dir, 'remote', 'add', 'origin', githubUrl ?? bare);
  if (githubUrl) git(dir, 'config', `url.${bare}.insteadOf`, githubUrl);
  git(dir, 'commit', '-q', '--allow-empty', '-m', 'first');
  git(dir, 'push', '-q', 'origin', 'main');
}

/** Undoes gitCheckout's redirect, so the checkout's GitHub origin reads as it is: `git remote get-url` expands insteadOf, and a hand names the repo from it. Returns what puts it back, for a launch that still needs to fetch. */
export function dropFetchRedirect(dir: string): () => void {
  const [key, value] = spawnSync('git', ['config', '--get-regexp', '^url\\..*\\.insteadof$'], { cwd: dir, encoding: 'utf8' }).stdout.trim().split(' ');
  expect(key).toBeTruthy();
  const git = (...args: string[]) => expect(spawnSync('git', ['config', ...args], { cwd: dir, encoding: 'utf8' }).status).toBe(0);
  git('--unset-all', key);
  return () => git(key, value);
}

/** The cue makes the harness exit (0 sails off, 3 anchors); `cues` are cue lines the harness runs instead; without either it idles. */
export async function launchShip(
  a: Admiral,
  req: { fleet: 'zulu' | 'alpha'; harness: Harness; source?: string; order?: string; anyway?: boolean; text?: string; exit?: 0 | 3; cue?: string; cues?: string[]; dir?: string; skill?: 'commodore' },
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
    skill: { kind: req.skill ?? 'none' },
    ...(req.order ? { source: req.source ?? 'orders', order_id: req.order, ...(req.anyway && { anyway: true }) } : { text: req.text ?? 'Go.' }),
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

/** Launches a ship the way AdmiralBot does: with the chat's own actor token, read from the MCP config Admiral wrote when it started the chat, so the ship carries `via: admiralbot`. The fleet's only dock is the chat's, so for the launch the dock's cue file holds the ship's own cue (a SessionStart on a transcript of its own) and the chat's cue, which its running Claude has already read, is put back after. With `sailOff` the ship's cue ends in a clean exit, so the ship sails off. */
export async function launchAsAdmiralBot(a: Admiral, fleet: ChatFleet, text = 'Look at the harbour log', reason = 'to look at the harbour log', sailOff = false, scout = false): Promise<LaunchedShip> {
  const dir = join(a.home, 'proj');
  mkdirSync(dir, { recursive: true });
  spawnSync('git', ['init', '-q'], { cwd: dir });
  const mcp = join(a.home, 'chat', fleet, 'mcp.json');
  await expect.poll(() => existsSync(mcp), { message: 'the chat has started and written its MCP config' }).toBe(true);
  const servers = JSON.parse(readFileSync(mcp, 'utf8')).mcpServers as Record<string, { env: Record<string, string> }>;
  const token = Object.values(servers)[0].env.ADMIRAL_ACTOR_TOKEN;
  const cue = join(a.home, `chat-cue-${fleet}`);
  const chatCue = readFileSync(cue, 'utf8');
  const transcript = chatTranscriptPath(a, fleet, 'ship');
  writeFileSync(transcript, '');
  writeFileSync(cue, `event SessionStart ${JSON.stringify({ transcript_path: transcript, source: 'startup' })}\n${sailOff ? 'exit 0\n' : ''}`);
  let ship!: LaunchedShip;
  await expect
    .poll(async () => {
      const res = await fetch(`${a.url}api/v1/ships`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Admiral-Actor': token },
        body: JSON.stringify({ fleet, harness: 'claude', dir, skill: { kind: 'none' }, text, reason, ...(scout && { autopilot: true, scout: true }) }),
      });
      if (res.status === 201) {
        const got = (await res.json()) as LaunchedShip & { ship?: LaunchedShip };
        ship = got.ship ?? got;
      }
      return res.status === 503 ? 'not known yet' : `${res.status} ${ship ? 'launched' : await res.text()}`;
    })
    .toMatch(/^201 launched$/);
  await expect.poll(() => existsSync(join(a.home, 'rec', ship.session, 'cues-done'))).toBe(true);
  writeFileSync(cue, chatCue);
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session) !== undefined).toBe(true);
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

/** Launches a ship whose harness waits, then exits 0 with a report naming the PR (and the `more` PRs, a stack): Admiral moors it. `resume` is the report's resume text; `lines` are transcript lines round 0 holds when it moors, `findings` is markdown it writes to ADMIRAL_FINDINGS. */
export async function launchMooredShip(a: Admiral, prNumber: number, opts: { resume?: string; lines?: TranscriptLine[]; more?: number[]; findings?: string } = {}): Promise<LaunchedShip> {
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', cue: 'wait go\nexit 0\n' });
  await mooreWith(a, ship, [prNumber, ...(opts.more ?? [])], opts);
  return ship;
}

async function mooreWith(a: Admiral, ship: LaunchedShip, prs: number[], opts: { resume?: string; lines?: TranscriptLine[]; findings?: string }) {
  const rec = join(a.home, 'rec', ship.session);
  // The fake writes `recorded` last; `env` can exist while it is still half written.
  await expect.poll(() => existsSync(join(rec, 'recorded'))).toBe(true);
  if (opts.lines) await appendLines(a, ship, opts.lines);
  const report = /^ADMIRAL_REPORT=(.*)$/m.exec(readFileSync(join(rec, 'env'), 'utf8'))?.[1];
  expect(report, 'the harness env names a report path').toBeTruthy();
  writeFileSync(report!, JSON.stringify({ version: 2, result: 'done', why: 'PR opened', prs: prs.map((n) => ({ url: `${PR_BASE}/${n}`, closes: [] })), resume: opts.resume ?? RESUME }));
  if (opts.findings !== undefined) {
    const findings = /^ADMIRAL_FINDINGS=(.*)$/m.exec(readFileSync(join(rec, 'env'), 'utf8'))?.[1];
    expect(findings, 'the harness env names a findings path').toBeTruthy();
    writeFileSync(findings!, opts.findings);
  }
  writeFileSync(join(rec, 'go'), '');
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status, { timeout: 30_000 }).toBe('moored');
}

/** What `gh api repos/acme/api/stacks/<n>` answers: the stack's layers, bottom first, each open or closed. */
export function stackAnswer(a: Admiral, stack: number, layers: { number: number; state: 'open' | 'closed' }[]) {
  const dir = join(a.home, 'fakegh', 'rest');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'stack.endpoint'), `repos/acme/api/stacks/${stack}`);
  writeFileSync(join(dir, 'stack.method'), 'GET');
  writeFileSync(join(dir, 'stack.status'), '200');
  writeFileSync(join(dir, 'stack.json'), JSON.stringify({ number: stack, open: true, pull_requests: layers }));
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
/** The repository's default merge method as the fake gh answers it; call before the ship moors, since Admiral asks once per repository. */
export function seedMergeMethod(a: Admiral, method: 'merge' | 'squash' | 'rebase') {
  writeFileSync(join(a.home, 'fakegh', 'default_merge_method'), method.toUpperCase() + '\n');
}

/** The fake gh sleeps this many seconds inside `gh pr merge`, so the button stays busy while a test looks. */
export function ghMergeSlow(a: Admiral, seconds: number) {
  writeFileSync(join(a.home, 'fakegh', 'pr_merge.sleep'), `${seconds}\n`);
}

/** The argv (after `gh`) of every merge call the fake gh recorded: `pr merge` and `stack merge`. */
export function mergeCalls(a: Admiral): string[][] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  // The fake names each call dir at random, so the order is the dirs' creation time.
  return readdirSync(dir)
    .sort((x, y) => statSync(join(dir, x)).birthtimeMs - statSync(join(dir, y)).birthtimeMs)
    .map((d) => join(dir, d, 'argv'))
    .filter((f) => existsSync(f))
    .map((f) => readFileSync(f, 'utf8').split('\0').filter((x) => x !== ''))
    .filter((argv) => (argv[0] === 'pr' || argv[0] === 'stack') && argv[1] === 'merge');
}

/**
 * What the PR tab reads for PR `number`: alice's review stands and nobody is requested, then, once GitHub takes a review request
 * (a `requestReviews` call), alice is requested again. The nodes carry the ids the request needs.
 */
export function seedReviewedPR(a: Admiral, number: number, author = 'octocat') {
  const url = `${PR_BASE}/${number}`;
  const node = (requested: unknown[]) => ({
    id: 'PR_fake1', number, url, title: 'Rename the helper', body: '', state: 'OPEN', isDraft: false, author: { __typename: 'User', login: author, id: 'U_author' },
    headRefName: 'feature/rename', baseRefName: 'main', reviewDecision: 'CHANGES_REQUESTED', mergeable: 'MERGEABLE', mergeStateStatus: 'CLEAN', statusCheckRollup: null,
    reviews: { nodes: [{ id: 'R1', author: { __typename: 'User', login: 'alice', id: 'U_fake1' }, state: 'CHANGES_REQUESTED', url: `${url}#pullrequestreview-1` }] },
    reviewRequests: { nodes: requested }, reviewThreads: { nodes: [] }, comments: { nodes: [] }, commits: { nodes: [] }, stack: null,
  });
  const requestedAlice = [{ requestedReviewer: { __typename: 'User', login: 'alice', id: 'U_fake1' } }];
  for (const [dir, requested] of [['pr_detail', []], ['pr_detail_after', requestedAlice]] as const) {
    mkdirSync(join(a.home, 'fakegh', dir), { recursive: true });
    writeFileSync(join(a.home, 'fakegh', dir, url.replace(/[/:]/g, '_')), JSON.stringify(node([...requested])) + '\n');
  }
}

/** The one-string argv of every `requestReviews` call the fake gh recorded. */
export function reRequestCalls(a: Admiral): string[] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .map((d) => join(dir, d, 'argv'))
    .filter((f) => existsSync(f))
    .map((f) => readFileSync(f, 'utf8').split('\0').filter((x) => x !== '').join(' '))
    .filter((argv) => argv.includes('requestReviews'));
}

/** The argv (after `gh`) of every `stack link` call the fake gh recorded. */
export function stackLinkCalls(a: Admiral): string[][] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .map((d) => join(dir, d, 'argv'))
    .filter((f) => existsSync(f))
    .map((f) => readFileSync(f, 'utf8').split('\0').filter((x) => x !== ''))
    .filter((argv) => argv[0] === 'stack' && argv[1] === 'link');
}

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

/** Asserts that `ok` stays true for `ms`: the way to show that something does not happen. */
export async function holds(ok: () => boolean, what: string, ms = 1500) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    expect(ok(), what).toBe(true);
    await new Promise((r) => setTimeout(r, 100));
  }
}

/** The ship view's tabs, and the link that opens the view from the ship's row. */
export const viewTab = (page: Page, name: 'Conversation' | 'Terminal' | 'PR') => page.getByRole('tab', { name: new RegExp(`^${name}(\\s*\\d+)?$`) });
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

/** Installs a skill for a dock: its SKILL.md in the dock's identity, which Claude Code and Codex both read as user skills. */
export function installSkill(a: Admiral, fleet: string, harness: 'claude' | 'codex', name: string, description: string) {
  const dir = join(a.home, 'identity', `${fleet}-${harness}`, 'skills', name);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'SKILL.md'), `---\nname: ${name}\ndescription: ${description}\n---\n`);
}

/** The launch form's Skill picker as a person finds it: a combobox labelled Skill and the options of its open list. */
export function skillPicker(page: Page, form: Locator, press: (l: Locator) => Promise<void>) {
  const box = form.getByRole('combobox', { name: 'Skill', exact: true });
  const list = page.getByRole('listbox');
  const option = (name: string) => page.getByRole('option', { name: new RegExp(`^[/$]?${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(\\s|$)`, 'i') });
  // The list opens only once the dock's skills have answered for the chosen checkout, so it is also the wait for them.
  const open = async () => {
    if (!(await list.isVisible())) await press(box);
    await expect(list).toBeVisible();
  };
  return {
    box,
    list,
    option,
    open,
    /** Types part of a name, which opens the list of what matches. */
    type: async (typed: string) => {
      await box.fill(typed);
      await expect(list).toBeVisible();
    },
    /** Waits for the skills, types `typed` and taps the row named `name`. */
    pick: async (name: string, typed = name) => {
      await open();
      await box.fill(typed);
      await expect(list).toBeVisible();
      await press(option(name));
      await expect(list).toBeHidden();
    },
    /** Clears the box so the list offers Commodore, the favorites and Just launch, and taps one. */
    pickListed: async (name: 'Commodore' | 'Just launch' | string) => {
      await box.fill('');
      await open();
      await press(option(name));
      await expect(list).toBeHidden();
    },
  };
}

/**
 * The launch journey on the Ships screen of the active fleet (Westwind): launching from an order and from the header with
 * freeform text, a skill, a model, an effort and a resource, and the layout of the form on a long order. The form's own
 * behaviour is in LaunchDialog.test.tsx and the server's refusals are in the Go tests. `press` is the click or tap of the device.
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
  // Orders launch from their row's arrow on the Orders screen, in the plugin source's tab.
  const onOrders = async () => {
    await page.goto(a.url + 'app/orders?fleet=zulu');
    await press(page.getByRole('tab', { name: /^Orders/ }));
    await expect(page.getByRole('tab', { name: /^Orders/ })).toHaveAttribute('aria-selected', 'true');
  };
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
  const picker = skillPicker(page, form, press);
  installSkill(a, 'zulu', 'claude', 'review', 'Review the diff before it merges.');
  installSkill(a, 'zulu', 'claude', 'fable-claude', 'Only Claude Code has this.');

  // From an order: its text and the checkout of its repo are preselected, and the skill picked is what boards.
  await onOrders();
  await press(orderLaunch('Tighten the api limits'));
  await expect(form).toBeVisible();
  await expect(form.getByLabel('Text')).toHaveValue('Tighten them.');
  await expect(form.getByLabel('Checkout')).toHaveValue(proj);
  if (opts.phone) expect(await fits()).toBe(true);
  await picker.pick('review', 'rev');
  await expect(form).toBeVisible();
  const fromOrder = await send();
  expect(fromOrder.body).toMatchObject({ fleet: 'zulu', harness: 'claude', source: 'orders', order_id: 'ord-api', dir: proj, skill: { kind: 'skill', name: 'review' } });
  const apiShip = await launched(fromOrder);
  await boarded(apiShip, { waitsOnMessage: true });
  await expect(main.getByRole('listitem').filter({ hasText: apiShip.name })).toBeVisible();
  setState(a, apiShip, 'working');

  // Freeform from the header, with a typed skill, a model, an effort and a resource.
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form).toBeVisible();
  await form.getByLabel('Checkout').selectOption(proj);
  // The form is anchored to the bottom, so the warning line going away as the name completes moves the box; the open list follows it and never covers it.
  await picker.open();
  await picker.type('fable-claud');
  await expect(form).toContainText('not installed on claude');
  await picker.box.press('e');
  await expect(form).not.toContainText('not installed on');
  await expect(picker.option('fable-claude')).toBeVisible();
  await expect(picker.list).toHaveCSS('visibility', 'visible');
  const [listBox, inputBox] = [(await picker.list.boundingBox())!, (await picker.box.boundingBox())!];
  expect(listBox.y >= inputBox.y + inputBox.height - 1 || listBox.y + listBox.height <= inputBox.y + 1, 'the open list does not cover the box').toBe(true);
  await picker.box.press('Escape');
  await form.getByLabel('Text').fill('  look at the parser  ');
  // The list is the dock's own, asked of the harness: claude-opus-4-8 is in no built-in list.
  await expect(form.getByLabel('Model').locator('option')).toHaveText(['Default', 'opus', 'sonnet', 'fable', 'haiku', 'claude-opus-4-8', 'Other…']);
  await form.getByLabel('Model').selectOption('claude-opus-4-8');
  await form.getByLabel('Effort').selectOption({ label: 'High' });
  await form.getByRole('checkbox', { name: 'db' }).check();
  await picker.pick('review');
  if (opts.phone) expect(await fits()).toBe(true);
  const freeform = await send();
  expect(freeform.body).toMatchObject({ fleet: 'zulu', harness: 'claude', dir: proj, skill: { kind: 'skill', name: 'review' }, model: 'claude-opus-4-8', effort: 'high', resources: ['db'], autopilot: false });
  expect(freeform.body).not.toHaveProperty('order_id');
  expect(String(freeform.body.text).trim()).toBe('look at the parser');
  const reviewer = await launched(freeform);
  await boarded(reviewer);
  await expect(main.getByRole('listitem').filter({ hasText: reviewer.name })).toBeVisible();
  setState(a, reviewer, 'working');

  // Long content: the form neither pans sideways nor shrinks a select under the touch target.
  // Enough skills that the open list outgrows the screen and scrolls.
  for (let i = 1; i <= 12; i++) installSkill(a, 'zulu', 'claude', `filler-${i}`, 'A skill that only fills the list.');
  await onOrders();
  for (const title of ['Supercalifragilistic', 'Port the long repo']) {
    await press(orderLaunch(title));
    await expect(form).toBeVisible();
    // The checkout list is in: its longest entry is what can widen the form.
    await expect(form.getByLabel('Checkout').locator('option')).not.toHaveCount(1);
    expect(await fits()).toBe(true);
    for (const name of ['Checkout', 'Model', 'Effort']) {
      expect((await form.getByLabel(name).boundingBox())!.height, `${name} select height`).toBeGreaterThanOrEqual(44);
    }
    expect((await picker.box.boundingBox())!.height, 'Skill height').toBeGreaterThanOrEqual(44);
    // Skill keeps its place in the Tab order: right after Checkout.
    await form.getByLabel('Checkout').focus();
    await page.keyboard.press('Tab');
    await expect(picker.box).toBeFocused();
    // The open list: every row is 44px or taller and the list lies inside the visual viewport, with few rows and with many.
    for (const typed of ['', 'e', 'filler']) {
      await picker.box.fill(typed);
      await expect(picker.list).toBeVisible();
      const rows = picker.list.getByRole('option');
      expect(await rows.count(), `rows for "${typed}"`).toBeGreaterThan(0);
      for (const [i, row] of (await rows.all()).entries()) {
        await row.scrollIntoViewIfNeeded();
        expect((await row.boundingBox())!.height, `row ${i} for "${typed}"`).toBeGreaterThanOrEqual(44);
      }
      const inside = await picker.list.evaluate((l) => {
        const r = l.getBoundingClientRect();
        const v = window.visualViewport!;
        return { top: r.top - v.offsetTop, left: r.left - v.offsetLeft, bottom: v.offsetTop + v.height - r.bottom, right: v.offsetLeft + v.width - r.right };
      });
      for (const [side, gap] of Object.entries(inside)) expect(gap, `list ${side} edge inside the visual viewport for "${typed}"`).toBeGreaterThanOrEqual(0);
    }
    await picker.box.press('Escape');
    // Tab and Shift+Tab ring through the dialog's controls and never reach the page behind it.
    for (const key of ['Tab', 'Shift+Tab']) {
      for (let i = 0; i < 30; i++) {
        await page.keyboard.press(key);
        expect(await page.evaluate(() => !!document.activeElement?.closest('[role="dialog"].launch-form')), `${key} #${i} stays in the dialog`).toBe(true);
      }
    }
    await cancel();
  }

  // Last, so the steps above ran with no Commodore on zulu's Claude dock. The fake installer writes the plugin into the dock's identity, and the dialog then offers Commodore with no reload.
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await press(main.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form).toBeVisible();
  const install = form.getByRole('button', { name: 'Install Commodore', exact: true });
  await expect(install).toBeVisible();
  await expect(form).not.toContainText('not installed on');
  if (opts.phone) expect(await fits()).toBe(true);
  await page.evaluate(() => ((window as unknown as { __noReload?: boolean }).__noReload = true));
  await install.tap();
  await expect(install).toBeHidden();
  await expect(picker.box).toHaveValue('Commodore');
  expect(await page.evaluate(() => (window as unknown as { __noReload?: boolean }).__noReload), 'the page was not reloaded').toBe(true);
  if (opts.phone) expect(await fits()).toBe(true);
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
export function seedInboxItem(a: Admiral, session: string, item: { id: string; kind: 'question' | 'review' | 'findings' | 'notice' | 'crew_notes' | 'crew_member' | 'mission' | 'routine_proposal' | 'event_proposal' | 'campaign_draft'; title: string } & Record<string, unknown>) {
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

/** The selected order's detail: a column beside the list on the iPad, the sheet on the phone. */
export const orderPane = (page: Page, phone: boolean) => (phone ? page.getByRole('dialog').filter({ hasText: "Why it's here" }) : page.getByRole('complementary', { name: 'Selected order' }));

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

/** The fleet's reviewers whose reviews wake a moored ship; call before `start`, after seedFleets. */
export function addWakeReviewers(a: Admiral, fleet: string, logins: string[]) {
  const config = JSON.parse(readFileSync(join(a.home, 'config.json'), 'utf8'));
  config.fleets[fleet].wake_reviewers = logins;
  a.writeConfig(config);
}

/** Start environment for answering a review: a debounce this long keeps the poll from waking the ship on its own. */
export const answeringEnv = (a: Admiral) => ({ ...mooringEnv(a), ADMIRAL_WAKE_DEBOUNCE: '1h' });

/** The fake gh's events for a PR gain one review by `author`, merged into what seedStackView or an earlier call wrote. */
export function prReview(a: Admiral, number: number, r: { author: string; body: string; state?: string; at?: Date }) {
  const dir = join(a.home, 'fakegh', 'pr_events');
  mkdirSync(dir, { recursive: true });
  const file = join(dir, `${PR_BASE}/${number}`.replace(/[/:]/g, '_'));
  const events = existsSync(file) ? JSON.parse(readFileSync(file, 'utf8')) : {};
  const node = {
    id: `R${number}`, author: { __typename: 'User', login: r.author }, state: r.state ?? 'COMMENTED', submittedAt: (r.at ?? new Date()).toISOString(),
    body: r.body, url: `${PR_BASE}/${number}#pullrequestreview-${number}`, comments: { totalCount: 0 },
  };
  writeFileSync(file, JSON.stringify({ ...events, reviews: { nodes: [node] } }) + '\n');
}

/** PR `number` is approved, green and mergeable but BEHIND a base that requires up-to-date branches, with a detail that is in no stack. */
export function prBehindStrictBase(a: Admiral, number: number) {
  prAnswer(a, number, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const url = `${PR_BASE}/${number}`;
  const key = url.replace(/[/:]/g, '_');
  const events = { mergeStateStatus: 'BEHIND', baseRef: { branchProtectionRule: { requiresStrictStatusChecks: true }, rules: { nodes: [] } } };
  mkdirSync(join(a.home, 'fakegh', 'pr_events'), { recursive: true });
  writeFileSync(join(a.home, 'fakegh', 'pr_events', key), JSON.stringify(events) + '\n');
  const detail = { number, url, title: 'Add the thing', body: '', state: 'OPEN', isDraft: false, author: { login: 'fake-viewer' }, headRefName: 'thing', baseRefName: 'main', baseRepository: { defaultBranchRef: { name: 'main' } }, statusCheckRollup: null, commits: { nodes: [] }, reviews: { nodes: [] }, reviewRequests: { nodes: [] }, reviewThreads: { nodes: [] }, comments: { nodes: [] }, stack: null };
  mkdirSync(join(a.home, 'fakegh', 'pr_detail'), { recursive: true });
  writeFileSync(join(a.home, 'fakegh', 'pr_detail', key), JSON.stringify(detail) + '\n');
}

/** The argv (after `gh`) of every `pr update-branch` call the fake gh recorded. */
export function updateBranchCalls(a: Admiral): string[][] {
  const dir = join(a.home, 'fakegh', 'calls');
  if (!existsSync(dir)) return [];
  return readdirSync(dir)
    .sort((x, y) => statSync(join(dir, x)).birthtimeMs - statSync(join(dir, y)).birthtimeMs)
    .map((d) => join(dir, d, 'argv'))
    .filter((f) => existsSync(f))
    .map((f) => readFileSync(f, 'utf8').split('\0').filter((x) => x !== ''))
    .filter((argv) => argv[0] === 'pr' && argv[1] === 'update-branch');
}
