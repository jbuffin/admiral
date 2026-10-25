import type { Locator, Page } from '@playwright/test';
import type { Admiral } from './fixtures';
import { expect, test } from './fixtures';
import { World } from './world';

// Launching from the phone's Orders tab (issue #119, u3), at the `phone` project's 402x874. Names it is
// built to, from the prototype (designs/canvas/Main.dc.html:933-960, :2002) and the brief:
//
//   Orders tab: a focusable button in each order row, named "<source> <id>, <title>. Tap to pick a dock and launch it."
//     (a freeform order has a hex id the UI never shows, so the id is left out)
//   dock sheet: dialog, named by its heading "Launch <order id>", or "Launch order" when the order shows no id
//     the order's title and a button "Cancel"
//     one group per fleet, named by the fleet's name, with one button per dock:
//       "Launch order on <Fleet>, <Harness> dock" (plus ". <full text>" for a dock with a config problem)
//       whose text carries the status line: "All in port", "N at sea", "No search roots", "Not found", "Shared config dir"
//   a dock button closes the sheet and opens the dialog "Launch orders" ("<Fleet> · <Harness> dock") for that order
//   Cancel, Escape and a tap on the backdrop close the sheet; focus goes back to the order's button, also when
//     "Launch orders" closes with "Stay in port"
//   after "Hoist sails" the app shows the Ships tab and focus is on the new ship's row ("Board <name>, ...")

const TITLE = 'Fix the flaky test';

test.beforeEach(({}, info) => test.skip(info.project.name !== 'phone', 'the phone launch is the phone project only'));

const tab = (page: Page, name: string) => page.getByRole('navigation', { name: 'Sections' }).getByRole('button', { name, exact: true });
const ordersPanel = (page: Page) => page.getByRole('complementary', { name: 'Orders', exact: true });
const sheet = (page: Page) => page.getByRole('dialog', { name: 'Launch order', exact: true });
const launchDialog = (page: Page) => page.getByRole('dialog', { name: 'Launch orders', exact: true });
const shipsTab = (page: Page) => page.getByRole('region', { name: 'Ships', exact: true });
const hoist = (d: Locator) => d.getByRole('button', { name: 'Hoist sails' });
const dockButton = (page: Page, fleet: string, harness: string) =>
  sheet(page).getByRole('group', { name: fleet, exact: true }).getByRole('button', { name: new RegExp(`^Launch order on ${fleet}, ${harness} dock`) });

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** The seeded order as the API lists it, once it is there: the source's name, the order's id and its title. */
async function listedOrder(admiral: Admiral, title: string) {
  let found: { source: string; id: string; title: string } | undefined;
  await expect
    .poll(async () => {
      const res = await fetch(`${admiral.url}api/sources`);
      const body = await res.json();
      const list = (Array.isArray(body) ? body : body.sources) as { key: string; name: string; panels?: { orders?: { id: string; title: string }[] }[] }[];
      for (const s of list ?? []) {
        for (const p of s.panels ?? []) {
          const o = p.orders?.find((x) => x.title.split('\n')[0] === title);
          if (o) found = { source: s.name, id: o.id, title };
        }
      }
      return found !== undefined;
    })
    .toBe(true);
  return found!;
}

/** Fleets work, home, bare (no search roots) and gone (missing executable); one order; the page on the phone's Orders tab. */
async function onOrdersTab(admiral: Admiral, page: Page) {
  const w = new World(admiral, { refusals: true });
  w.writeConfig();
  w.checkout('alpha');
  await admiral.start();
  await w.seedOrder(`${TITLE}\nIt fails on CI only.`);
  const order = await listedOrder(admiral, TITLE);
  await page.goto(admiral.url);
  await tab(page, 'Orders').tap();
  const orderButton = ordersPanel(page).getByRole('button', {
    name: new RegExp(`^${escape(order.source)}\\s*, ${escape(TITLE)}\\. Tap to pick a dock and launch it\\.$`),
  });
  await expect(orderButton).toBeVisible();
  return { w, order, orderButton };
}

test('S1: a tap on an order opens the dock sheet with a group per fleet and a button per dock', async ({ page, admiral }) => {
  const { w, orderButton } = await onOrdersTab(admiral, page);
  try {
    await orderButton.tap();

    const s = sheet(page);
    await expect(s).toBeVisible();
    await expect(s.getByRole('heading', { name: 'Launch order', exact: true })).toBeVisible();
    await expect(s.getByText(TITLE, { exact: true })).toBeVisible();
    await expect(s.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
    // Focus moves into the sheet.
    await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest('[role="dialog"]'))).toBe(true);

    await expect(s.getByRole('group')).toHaveCount(4);
    for (const fleet of ['Work', 'Home', 'Bare', 'Gone']) await expect(s.getByRole('group', { name: fleet, exact: true })).toBeVisible();
    await expect(s.getByRole('button', { name: /^Launch order on / })).toHaveCount(6);

    // The marina's unarmed status: nothing at sea, and the short text of a dock with a config problem.
    await expect(s.getByRole('button', { name: 'Launch order on Work, Claude dock', exact: true })).toContainText('All in port');
    await expect(s.getByRole('button', { name: 'Launch order on Work, Codex dock', exact: true })).toContainText('All in port');
    await expect(s.getByRole('button', { name: 'Launch order on Home, Codex dock', exact: true })).toContainText('All in port');
    await expect(s.getByRole('button', { name: 'Launch order on Bare, Claude dock. No search roots', exact: true })).toContainText('No search roots');
    await expect(s.getByRole('button', { name: /^Launch order on Gone, Claude dock\. Executable not found: / })).toContainText('Not found');

    // The sheet does not arm the marinas' docks: nothing is selected behind it.
    await expect(ordersPanel(page).locator('.order.selected')).toHaveCount(0);
  } finally {
    w.endShips();
  }
});

test('S2, C8: a dock opens Launch orders for that order, Hoist sails starts a ship and focus lands on its row in Ships', { tag: '@critical' }, async ({ page, admiral }) => {
  const { w, orderButton } = await onOrdersTab(admiral, page);
  try {
    // A dock with a config problem still opens Launch orders, as it does from a marina; Stay in port returns focus to the order.
    await orderButton.tap();
    await dockButton(page, 'Bare', 'Claude').tap();
    await expect(sheet(page)).toHaveCount(0);
    const refused = launchDialog(page);
    await expect(refused).toBeVisible();
    await expect(refused.getByText('Bare · Claude dock')).toBeVisible();
    await expect(refused.getByText('This fleet has no search roots')).toBeVisible();
    await refused.getByRole('button', { name: 'Stay in port' }).tap();
    await expect(refused).toHaveCount(0);
    await expect(orderButton).toBeFocused();

    // A working dock: the dialog is for this order on this dock, and the launch is real (the fake harness).
    await orderButton.tap();
    await dockButton(page, 'Work', 'Claude').tap();
    await expect(sheet(page)).toHaveCount(0);
    const d = launchDialog(page);
    await expect(d).toBeVisible();
    await expect(d.getByText('Work · Claude dock')).toBeVisible();
    await expect(d.getByText(TITLE, { exact: true })).toBeVisible();
    await expect(d.getByRole('combobox', { name: 'Directory' })).toHaveValue('alpha');
    await hoist(d).tap();
    await expect(d).toHaveCount(0);

    await expect.poll(() => w.records().length).toBe(1);
    const listed = async () => ((await (await fetch(`${admiral.url}api/ships`)).json()) as { ships: { name: string }[] }).ships;
    await expect.poll(async () => (await listed()).length).toBe(1);
    const ships = await listed();

    // The app moves to the Ships tab, and the new ship's row has focus.
    await expect(tab(page, 'Ships')).toHaveAttribute('aria-current', 'page');
    const row = shipsTab(page).getByRole('button', { name: new RegExp(`^Board ${escape(ships[0].name)}, Claude in Work`) });
    await expect(row).toBeVisible();
    await expect(row).toBeFocused();
  } finally {
    w.endShips();
  }
});

test('S3: Cancel, Escape and the backdrop each close the sheet and return focus to the order', async ({ page, admiral }) => {
  const { w, orderButton } = await onOrdersTab(admiral, page);
  try {
    // Cancel
    await orderButton.tap();
    await expect(sheet(page)).toBeVisible();
    await sheet(page).getByRole('button', { name: 'Cancel', exact: true }).tap();
    await expect(sheet(page)).toHaveCount(0);
    await expect(launchDialog(page)).toHaveCount(0);
    await expect(orderButton).toBeFocused();

    // Escape
    await orderButton.tap();
    await expect(sheet(page)).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(sheet(page)).toHaveCount(0);
    await expect(launchDialog(page)).toHaveCount(0);
    await expect(orderButton).toBeFocused();

    // The backdrop: a tap above the sheet, which is bottom-aligned.
    await orderButton.tap();
    await expect(sheet(page)).toBeVisible();
    const box = (await sheet(page).boundingBox())!;
    expect(box.y).toBeGreaterThan(40);
    await page.touchscreen.tap(200, 20);
    await expect(sheet(page)).toHaveCount(0);
    await expect(launchDialog(page)).toHaveCount(0);
    await expect(orderButton).toBeFocused();

    // The row's menu button is not the row's main button: it opens the menu, not the sheet.
    await ordersPanel(page).getByRole('button', { name: `Actions for ${TITLE}` }).tap();
    await expect(sheet(page)).toHaveCount(0);
  } finally {
    w.endShips();
  }
});

test('S4: the sheet scrolls at 874x402, every dock is reachable and Launch orders is usable', async ({ page, admiral }) => {
  const { w, orderButton } = await onOrdersTab(admiral, page);
  try {
    await page.setViewportSize({ width: 874, height: 402 });
    await expect(tab(page, 'Orders')).toHaveAttribute('aria-current', 'page');
    await expect(orderButton).toBeVisible();
    await orderButton.tap();

    const s = sheet(page);
    await expect(s).toBeVisible();
    // Six docks do not fit in 402px: the sheet or its body scrolls, and the last dock comes into view.
    const gone = s.getByRole('button', { name: /^Launch order on Gone, Claude dock/ });
    await gone.scrollIntoViewIfNeeded();
    await expect(gone).toBeInViewport();
    const first = dockButton(page, 'Work', 'Claude');
    await first.scrollIntoViewIfNeeded();
    await expect(first).toBeInViewport();
    // Something in the sheet scrolls: some element is taller than its box.
    expect(await s.evaluate((el) => [el, ...el.querySelectorAll('*')].some((c) => c.scrollHeight > c.clientHeight + 1))).toBe(true);

    await first.tap();
    const d = launchDialog(page);
    await expect(d).toBeVisible();
    await expect(d.getByRole('heading', { name: 'Launch orders' })).toBeInViewport();
    // The card's body scrolls: the footer's buttons are reachable.
    await hoist(d).scrollIntoViewIfNeeded();
    await expect(hoist(d)).toBeInViewport();
    await expect(d.getByRole('button', { name: 'Stay in port' })).toBeInViewport();
    await d.getByRole('button', { name: 'Stay in port' }).tap();
    await expect(d).toHaveCount(0);
    await expect(orderButton).toBeFocused();
  } finally {
    w.endShips();
  }
});
