import { execFileSync } from 'node:child_process';
import { readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, header, test } from './sea';
import { World, sidebar } from './world';

// Locked resources in the UI (#118, u3). Two autopilot panels of one source need the capacity-1
// resource "phone". One panel's order launches a ship, which holds the phone; the other waits.
//
//   the header shows a chip "phone 1/1" whose accessible label reads in units and names the holder:
//     "phone: 1 of 1 unit in use, held by <ship name>"
//   the panel that waits shows the line "waiting for phone"; the panel that launched does not
//   when the resource's check starts failing, the chip's label reads "phone: missing (its check failed)"

const remoteOf = (name: string) => `https://github.com/acme/${name}`;

type Json = { [key: string]: any };

async function get(url: string, path: string): Promise<Json> {
  const res = await fetch(`${url}${path}`);
  return (await res.json()) as Json;
}

const resources = async (url: string) => ((await get(url, 'api/resources')).resources ?? []) as Json[];

async function panels(url: string) {
  const body = await get(url, 'api/sources');
  const list = (Array.isArray(body) ? body : body.sources) as Json[];
  return (list.find((s) => s.key === 'a')?.panels ?? []) as Json[];
}

test.describe('locked resources in the UI', () => {
  test('a held resource shows as a chip and the other panel says it is waiting; a failing check turns the chip missing', async ({ page, admiral }) => {
    const w = new World(admiral);
    w.writeConfig();
    for (const name of ['r1', 'r2']) {
      const dir = w.checkout(name);
      execFileSync('git', ['remote', 'add', 'origin', remoteOf(name)], { cwd: dir });
    }

    admiral.installTestPlugin();
    const flags = admiral.flags('a');
    flags.panels([
      { id: 'main', title: 'Main' },
      { id: 'other', title: 'Other' },
    ]);
    const order = (id: string, name: string) => ({ id, title: `Order ${id}`, repo: remoteOf(name), text: `Review ${remoteOf(name)}` });
    flags.orders({ main: [order('o1', 'r1')], other: [order('o2', 'r2')] });

    const flag = join(admiral.home, 'phone-flag');
    writeFileSync(flag, '');

    const config = JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8'));
    const needs = { enabled: true, fleet: 'work', harness: 'claude', skill: { kind: 'none' }, max_ships: 1, low_stakes: 'never', resources: ['phone'] };
    config.sources = { a: { plugin: 'testplug', name: 'Test', settings: { flagdir: flags.dir }, autopilot: { main: needs, other: needs } } };
    config.resources = { phone: { capacity: 1, check: ['test', '-f', flag] } };
    admiral.writeConfig(config);
    await admiral.start();

    // The server side holds: the phone is checked and held by one ship, and one panel waits for it.
    await expect
      .poll(async () => (await resources(admiral.url))[0], { timeout: 20_000, message: 'phone held by a ship once its check passed' })
      .toMatchObject({ name: 'phone', capacity: 1, in_use: 1, check: 'ok', holders: [{ kind: 'ship' }] });
    await expect
      .poll(async () => (await panels(admiral.url)).filter((p) => p.autopilot?.waiting_for?.length).map((p) => p.autopilot.waiting_for), {
        timeout: 20_000,
        message: 'exactly one panel waiting for phone',
      })
      .toEqual([['phone']]);

    const holder = (await resources(admiral.url))[0].holders[0].session as string;
    const body = await get(admiral.url, 'api/ships');
    const ships = (Array.isArray(body) ? body : body.ships) as Json[];
    const shipName = ships.find((s) => s.session === holder)?.name as string;
    expect(shipName, 'the holding ship is in the ships list').toBeTruthy();
    const waiting = (await panels(admiral.url)).find((p) => p.autopilot?.waiting_for?.length)!;
    const running = (await panels(admiral.url)).find((p) => !p.autopilot?.waiting_for?.length)!;

    await page.goto(admiral.url);

    // The chip: name and count, and a label in units that names the holder.
    await expect(header(page).getByText('phone 1/1')).toBeVisible();
    await expect(header(page).getByLabel(`phone: 1 of 1 unit in use, held by ${shipName}`)).toBeVisible();

    // The waiting panel says so, and the panel that launched does not.
    const waitingPanel = sidebar(page).getByRole('region', { name: waiting.title as string, exact: true });
    const runningPanel = sidebar(page).getByRole('region', { name: running.title as string, exact: true });
    await expect(waitingPanel.getByText('waiting for phone')).toBeVisible();
    await expect(runningPanel.getByText(/waiting for/)).toHaveCount(0);

    // The check fails: the next run turns the chip missing (checks run every 15 s).
    rmSync(flag);
    await expect.poll(async () => (await resources(admiral.url))[0]?.check, { timeout: 20_000 }).toBe('missing');
    await expect(header(page).getByLabel('phone: missing (its check failed)')).toBeVisible({ timeout: 20_000 });

    w.endShips();
  });
});
