import type { Page } from '@playwright/test';
import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { type LaunchedShip, type Sea } from './sea';

// Helpers for the bridge scenarios (u11): launching a ship the way the dialog
// does (from a freeform order, with a skill, so the header has a title and the
// lead row a skill), and reaching what the fake harness saw.

/** The order text used by the helpers; its first line is the ship's `title`. */
export const orderTitle = 'Fix the flaky test';
const orderText = `${orderTitle}\nIt fails on CI only.`;

interface SourceJSON {
  key: string;
  panels: { id: string; orders: { id: string; title: string }[] }[];
}

/**
 * Launches a Claude ship from a freeform order with the skill `review` (the
 * fleet must have it installed: `world.skill(fleet, 'claude', 'review')`), as
 * the launch dialog does through POST /api/ships. Resolves once the fake has
 * recorded (`until: 'recorded'`) or has run its cues (`'cues-done'`).
 */
export async function launchFromOrder(sea: Sea, fleet: string, repo: string, until: 'cues-done' | 'recorded' = 'cues-done'): Promise<LaunchedShip> {
  await sea.world.seedOrder(orderText);
  // The freeform source refreshes a moment after the add: ask until it lists the order.
  let sources: unknown;
  let order: { id: string } | undefined;
  const sourcesBy = Date.now() + 10_000;
  for (;;) {
    sources = await (await fetch(`${sea.a.url}api/sources`)).json();
    const list = (Array.isArray(sources) ? sources : (sources as { sources: SourceJSON[] }).sources) as SourceJSON[];
    order = list.find((s) => s.key === 'orders')?.panels.flatMap((p) => p.orders).find((o) => o.title === orderTitle);
    if (order || Date.now() > sourcesBy) break;
    await new Promise((r) => setTimeout(r, 100));
  }
  if (!order) throw new Error(`the seeded order is not in the freeform source: ${JSON.stringify(sources)}`);
  const res = await fetch(`${sea.a.url}api/ships`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      fleet,
      harness: 'claude',
      dir: sea.world.checkout(repo),
      skill: { kind: 'skill', name: 'review' },
      source: 'orders',
      order_id: order.id,
      text: orderText,
    }),
  });
  const body = await res.json();
  if (res.status !== 201) throw new Error(`launch failed: ${res.status} ${JSON.stringify(body)}`);
  const ship = (body.session ? body : body.ship) as LaunchedShip;
  const dir = join(sea.world.root, 'rec', ship.session);
  const deadline = Date.now() + 15_000;
  while (!existsSync(join(dir, until))) {
    if (Date.now() > deadline) throw new Error(`the fake of ${ship.session} never wrote ${until}`);
    await new Promise((r) => setTimeout(r, 50));
  }
  return ship;
}

/** The bytes the fake harness has read from its terminal so far (it records `stdin` once its cues are done). */
export function stdinOf(sea: Sea, session: string) {
  const p = join(sea.world.root, 'rec', session, 'stdin');
  return existsSync(p) ? readFileSync(p, 'latin1') : '';
}

/** The ship's pane size as tmux reports it, "COLSxROWS". */
export function paneSize(sea: Sea, session: string) {
  const sock = join(sea.world.root, 'tmux.sock');
  const r = spawnSync('tmux', ['-S', sock, 'display-message', '-p', '-t', `=${session}:`, '#{window_width}x#{window_height}'], { encoding: 'utf8' });
  return r.stdout.trim();
}

/** Lets a fake that is blocked in a `wait <name>` cue go on. */
export function release(sea: Sea, session: string, name = 'go') {
  writeFileSync(join(sea.world.root, 'rec', session, name), '');
}

/** Writes to the ship's pane terminal: output from the program in it, as far as tmux and the bridge can tell. */
export function paneWrite(sea: Sea, session: string, data: string) {
  const sock = join(sea.world.root, 'tmux.sock');
  const r = spawnSync('tmux', ['-S', sock, 'display-message', '-p', '-t', `=${session}:`, '#{pane_tty}'], { encoding: 'utf8' });
  const tty = r.stdout.trim();
  if (r.status !== 0 || !tty) throw new Error(`no pane terminal for ${session}: ${r.stderr}`);
  writeFileSync(tty, data);
}

/**
 * What the terminal shows, as text. xterm.js draws to a canvas, so a page that
 * has window.__admiral (the tests create it, nothing else does) must offer
 * window.__admiral.screen(): the active buffer's lines, scrollback and screen,
 * joined by "\n" with trailing blanks trimmed.
 */
export const screenText = (page: Page) =>
  page.evaluate(() => (window as unknown as { __admiral?: { screen?: () => string } }).__admiral?.screen?.() ?? '');

/** The non-loopback address answers (a firewall dialog or no network can stop it), within 3 s. */
export async function reachable(url: string) {
  try {
    await fetch(url, { signal: AbortSignal.timeout(3000) });
    return true;
  } catch {
    return false;
  }
}

const bin = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'bin', 'admiral');

/**
 * `admiral crew launch` against this Admiral's home, as a ship's lead would run it. The boat runs the fleet's fake
 * with the fleet's cue file, so it is driven by `wait <name>` cues and `releaseCrew`. Resolves once the boat's fake
 * has recorded and the server lists it in the ship's `crew[]`.
 */
export async function launchCrew(sea: Sea, session: string, name: string) {
  const env: NodeJS.ProcessEnv = { ...process.env, ADMIRAL_HOME: sea.a.home };
  for (const v of ['ADMIRAL_SHIP', 'ADMIRAL_AUTOPILOT', 'ADMIRAL_REPORT', 'ADMIRAL_INBOX']) delete env[v];
  const r = spawnSync(bin, ['crew', 'launch', '--ship', session, '--name', name, '--dir', sea.world.checkout(`crew-${name}`), '--', 'go'], { env, encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`crew launch ${name} exited ${r.status}: ${r.stdout}${r.stderr}`);
  const dir = join(sea.world.root, 'rec', session, 'crew', name);
  const deadline = Date.now() + 15_000;
  while (!existsSync(join(dir, 'recorded'))) {
    if (Date.now() > deadline) throw new Error(`the fake of crew boat ${name} never recorded`);
    await new Promise((res) => setTimeout(res, 50));
  }
  while (!(await crewOf(sea, session)).some((c) => c.name === name)) {
    if (Date.now() > deadline) throw new Error(`GET /api/ships never listed crew boat ${name}`);
    await new Promise((res) => setTimeout(res, 100));
  }
  return dir;
}

/** The ship's `crew[]` as the server lists it. */
export async function crewOf(sea: Sea, session: string): Promise<{ name: string; status: string }[]> {
  const body = await (await fetch(`${sea.a.url}api/ships`)).json();
  const list = (Array.isArray(body) ? body : body.ships) as { session: string; crew?: { name: string; status: string }[] }[];
  return list.find((s) => s.session === session)?.crew ?? [];
}

/** Lets a crew boat's fake that is blocked in a `wait <name>` cue go on. */
export function releaseCrew(sea: Sea, session: string, crew: string, name: string) {
  writeFileSync(join(sea.world.root, 'rec', session, 'crew', crew, name), '');
}

/** Writes to a crew boat's pane terminal, as paneWrite does for the ship's. */
export function crewPaneWrite(sea: Sea, session: string, crew: string, data: string) {
  const sock = join(sea.world.root, 'tmux.sock');
  const r = spawnSync('tmux', ['-S', sock, 'list-panes', '-s', '-t', `=${session}`, '-F', '#{@admiral-crew-name}\t#{pane_tty}'], { encoding: 'utf8' });
  const tty = r.stdout.split('\n').find((l) => l.startsWith(`${crew}\t`))?.split('\t')[1];
  if (r.status !== 0 || !tty) throw new Error(`no pane terminal for crew boat ${crew} of ${session}: ${r.stderr}`);
  writeFileSync(tty, data);
}
