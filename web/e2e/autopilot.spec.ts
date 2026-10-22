import type { Locator, Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, header, isTouch, test } from './sea';
import { World, sidebar } from './world';

// Autopilot in the UI (#114, u3). The names below are the contract the UI is built to:
//
//   a panel with autopilot enabled shows, in its section, an AutopilotBadge: role img named
//     "Autopilot on" while it runs and "Autopilot paused" while paused
//   beside it a button "Pause autopilot" (running) or "Resume autopilot" (paused), at least 44 px square
//   a panel with no autopilot entry, or an entry with enabled: false, shows neither
//   the header's ship pill reads "n / max at sea" while some panel has autopilot enabled and the
//     config has arrived (max is the config's autopilot.max_ships), otherwise "n at sea"
//   the page follows the pushed sources view: a pause the server refuses leaves the badge as it was

const entry = (enabled: boolean) => ({ enabled, fleet: 'work', harness: 'claude', skill: { kind: 'none' } });

/**
 * A claude dock on fleet "work" (fake harness) and the given sources; `cap` is the global
 * autopilot.max_ships. The autopilot panel stays empty: the real engine launches listed orders.
 */
function setup(admiral: { writeConfig(c: unknown): void; home: string }, w: World, sources: Record<string, unknown>, cap?: number) {
  w.writeConfig();
  const config = JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8'));
  config.sources = sources;
  if (cap !== undefined) config.autopilot = { max_ships: cap };
  admiral.writeConfig(config);
}

const orders = (enabled = true) => ({ plugin: 'freeform', name: 'Orders', autopilot: { orders: entry(enabled) } });

const panel = (page: Page, name: string) => sidebar(page).getByRole('region', { name, exact: true });
const badge = (p: Locator) => p.getByRole('img', { name: /^Autopilot (on|paused)$/ });
const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

async function panelAutopilot(url: string) {
  const res = await fetch(`${url}api/sources`);
  const body = await res.json();
  const list = (Array.isArray(body) ? body : body.sources) as { key: string; panels: { autopilot: { enabled: boolean; paused: boolean; max_ships: number } | null }[] }[];
  return list.find((s) => s.key === 'orders')?.panels[0]?.autopilot;
}

test.describe('autopilot in the panel header', () => {
  test('a panel on autopilot shows the badge; pause shows it paused, a reload keeps it, resume runs it again', { tag: '@critical' }, async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    setup(admiral, new World(admiral), { orders: orders() }, 3);
    await admiral.start();
    await page.goto(admiral.url);

    const section = panel(page, 'Orders');
    await expect(badge(section)).toHaveAccessibleName('Autopilot on');
    const pause = section.getByRole('button', { name: 'Pause autopilot' });
    await expect(pause).toBeVisible();
    await expect(section.getByRole('button', { name: 'Resume autopilot' })).toHaveCount(0);

    // Reachable by touch: at least as large as the header's other buttons.
    const box = (await pause.boundingBox())!;
    expect(Math.min(box.width, box.height), 'the pause control is a touch target').toBeGreaterThanOrEqual(44);

    await press(pause, touch);
    await expect(badge(section)).toHaveAccessibleName('Autopilot paused');
    await expect(section.getByRole('button', { name: 'Resume autopilot' })).toBeVisible();
    await expect(section.getByRole('button', { name: 'Pause autopilot' })).toHaveCount(0);
    await expect.poll(() => panelAutopilot(admiral.url)).toMatchObject({ enabled: true, paused: true });

    await page.reload();
    await expect(badge(panel(page, 'Orders'))).toHaveAccessibleName('Autopilot paused');
    await expect(panel(page, 'Orders').getByRole('button', { name: 'Resume autopilot' })).toBeVisible();

    await press(panel(page, 'Orders').getByRole('button', { name: 'Resume autopilot' }), touch);
    await expect(badge(panel(page, 'Orders'))).toHaveAccessibleName('Autopilot on');
    await expect(panel(page, 'Orders').getByRole('button', { name: 'Pause autopilot' })).toBeVisible();
    await expect.poll(() => panelAutopilot(admiral.url)).toMatchObject({ paused: false });
  });

  test('a pause the server refuses leaves the badge running', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    setup(admiral, new World(admiral), { orders: orders() }, 3);
    await admiral.start();
    await page.route('**/api/sources/*/panels/*/autopilot', (route) => route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"no"}' }));
    await page.goto(admiral.url);

    const section = panel(page, 'Orders');
    await press(section.getByRole('button', { name: 'Pause autopilot' }), touch);
    // Give the page the time it would need to flip, then see that it did not.
    await page.waitForTimeout(500);
    await expect(badge(section)).toHaveAccessibleName('Autopilot on');
    await expect(section.getByRole('button', { name: 'Pause autopilot' })).toBeVisible();
    expect((await panelAutopilot(admiral.url))?.paused).toBe(false);
  });

  test('a panel without autopilot, and one whose entry is disabled, show no badge and no control', async ({ page, admiral }) => {
    setup(admiral, new World(admiral), {
      orders: orders(),
      notes: { plugin: 'freeform', name: 'Notes' },
      quiet: { plugin: 'freeform', name: 'Quiet', autopilot: { orders: entry(false) } },
    }, 3);
    await admiral.start();
    await page.goto(admiral.url);

    await expect(badge(panel(page, 'Orders'))).toBeVisible();
    for (const name of ['Notes', 'Quiet']) {
      // A second source's panel is titled "Orders" too, inside the source's group.
      const p = sidebar(page).getByRole('group', { name, exact: true }).getByRole('region', { name: 'Orders', exact: true });
      await expect(p).toBeVisible();
      await expect(badge(p)).toHaveCount(0);
      await expect(p.getByRole('button', { name: /autopilot$/ })).toHaveCount(0);
    }
  });
});

test.describe('autopilot in the header count', () => {
  test('one ship at sea under a cap of 3 reads "1 / 3 at sea"', async ({ page, admiral }) => {
    const w = new World(admiral);
    setup(admiral, w, { orders: orders() }, 3);
    w.checkout('alpha');
    await admiral.start();
    await w.launchDirect('work', 'claude', join(w.work, 'alpha'), 'hand launch');
    await page.goto(admiral.url);

    await expect(header(page).getByText('1 / 3 at sea', { exact: true })).toBeVisible();
    w.endShips();
  });

  test('hand launches over the cap read plainly: "2 / 1 at sea"', async ({ page, admiral }) => {
    const w = new World(admiral);
    setup(admiral, w, { orders: orders() }, 1);
    for (const repo of ['alpha', 'bravo']) w.checkout(repo);
    await admiral.start();
    for (const repo of ['alpha', 'bravo']) await w.launchDirect('work', 'claude', join(w.work, repo), 'hand launch');
    await page.goto(admiral.url);

    await expect(header(page).getByText('2 / 1 at sea', { exact: true })).toBeVisible();
    w.endShips();
  });

  test('with no panel on autopilot the cap is not shown', async ({ page, admiral }) => {
    const w = new World(admiral);
    setup(admiral, w, { orders: orders(false) }, 3);
    w.checkout('alpha');
    await admiral.start();
    await w.launchDirect('work', 'claude', join(w.work, 'alpha'), 'hand launch');
    await page.goto(admiral.url);

    await expect(header(page).getByText('1 at sea', { exact: true })).toBeVisible();
    await expect(header(page).getByText(/\//)).toHaveCount(0);
    w.endShips();
  });
});
