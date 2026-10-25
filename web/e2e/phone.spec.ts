import type { Page } from '@playwright/test';
import { expect, test } from './sea';

// The phone shell (issue #119, u2), at the `phone` project's 402x874. Names it is built to, from the
// prototype (designs/canvas/Main.dc.html) and the brief:
//
//   header (banner): the mark, "Admiral", "<n> at sea" and button "Settings"; no Sound pill, no Needs you button
//   tab bar: navigation "Sections" with buttons "Inbox" ("Inbox, <n> item(s)"), "Chat", "Ships"
//     ("Ships, <n> needing you") and "Orders"; the open tab has aria-current="page"; it is gone while a bridge is open
//   Inbox tab: region "Needs you inbox", with no "Close the inbox" button
//   Ships tab: region "Ships" with a heading "Ships" and the text "<n> ships, <n> needs you"; one button per ship,
//     "Board <name>, <Harness> in <Fleet>, <status>" where the status starts "Needs you" or "Underway"
//   bridge: region "Attached terminal" fills the screen; button "Back" (exact) returns to the tab it came from
//   Orders tab: complementary "Orders" in full, with no "Collapse orders" button and no "Orders, collapsed" rail
//   no open water (main "Open water") and no marina (region "<fleet> marina")

const BLUE = '#3B82F6';
const PERMISSION = 'event PermissionRequest {"tool_name":"Bash"}';

const tabs = (page: Page) => page.getByRole('navigation', { name: 'Sections' });
const tab = (page: Page, name: string | RegExp) => tabs(page).getByRole('button', { name });
const shipsTab = (page: Page) => page.getByRole('region', { name: 'Ships', exact: true });
const rows = (page: Page) => shipsTab(page).getByRole('button', { name: /^Board / });
const terminal = (page: Page) => page.getByRole('region', { name: 'Attached terminal' });
const back = (page: Page) => page.getByRole('button', { name: 'Back', exact: true });
const noOpenWater = async (page: Page) => {
  await expect(page.getByRole('main', { name: 'Open water' })).toHaveCount(0);
  await expect(page.getByRole('region', { name: / marina$/ })).toHaveCount(0);
};

test.beforeEach(({}, info) => test.skip(info.project.name !== 'phone', 'the phone shell is the phone project only'));

test('S1, S4, C3, C4: the Ships tab lists the needs-you ship first and badges the tab, in portrait and landscape', { tag: '@critical' }, async ({ page, admiral, sea }) => {
  const s = sea({ work: { name: 'Work', color: BLUE } });
  await admiral.start();
  const first = await s.launch('work', 'alpha');
  const second = await s.launch('work', 'beta');
  // The older ship is the one that needs you: newest first would list the other one first.
  s.setState(first.session, 'needs_you');
  await page.goto(admiral.url);

  await expect(tab(page, 'Ships, 1 needing you')).toBeVisible();
  for (const name of ['Inbox', 'Chat', 'Orders']) await expect(tab(page, name)).toBeVisible();
  await expect(tab(page, 'Inbox')).toHaveAttribute('aria-current', 'page');
  await noOpenWater(page);

  await tab(page, 'Ships, 1 needing you').tap();
  await expect(tab(page, 'Ships, 1 needing you')).toHaveAttribute('aria-current', 'page');
  await expect(shipsTab(page).getByRole('heading', { name: /^Ships/ })).toBeVisible();
  await expect(shipsTab(page)).toContainText('2 ships, 1 needs you');
  await expect(rows(page)).toHaveCount(2);
  await expect(rows(page).nth(0)).toHaveAccessibleName(new RegExp(`^Board ${first.name}, Claude in Work, Needs you`));
  await expect(rows(page).nth(1)).toHaveAccessibleName(new RegExp(`^Board ${second.name}, Claude in Work, Underway`));

  // Rotating to 874x402 is still the phone layout.
  await page.setViewportSize({ width: 874, height: 402 });
  await expect(tabs(page)).toBeVisible();
  await expect(rows(page)).toHaveCount(2);
  await noOpenWater(page);
});

test('S2, C6: a ship row opens the bridge full screen, the tab bar steps aside and Back returns to Ships', async ({ page, admiral, sea }) => {
  const s = sea({ work: { name: 'Work', color: BLUE } });
  await admiral.start();
  const ship = await s.launch('work', 'alpha');
  await page.goto(admiral.url);
  await tab(page, 'Ships').tap();
  await expect(rows(page)).toHaveCount(1);

  await rows(page).first().tap();
  await expect(terminal(page)).toBeVisible();
  await expect(tabs(page)).toHaveCount(0);
  await expect(page.getByText(ship.name).first()).toBeVisible();

  await back(page).tap();
  await expect(terminal(page)).toHaveCount(0);
  await expect(shipsTab(page)).toBeVisible();
  await expect(tab(page, 'Ships')).toHaveAttribute('aria-current', 'page');
});

test('S3, C6: Attach on a needs-you stop opens the bridge, and Back returns to the Inbox tab', async ({ page, admiral, sea }) => {
  const s = sea({ work: { name: 'Work', color: BLUE, cue: [PERMISSION] } });
  await admiral.start();
  const ship = await s.launch('work', 'alpha');
  await page.goto(admiral.url);

  await expect(tab(page, 'Inbox, 1 item')).toBeVisible();
  const inbox = page.getByRole('region', { name: 'Needs you inbox' });
  await expect(inbox).toBeVisible();
  await expect(inbox.getByRole('button', { name: 'Close the inbox' })).toHaveCount(0);
  await expect(inbox.getByText('Wants to run Bash')).toBeVisible();

  await inbox.getByRole('button', { name: new RegExp(`^Attach to ${ship.name}`) }).tap();
  await expect(terminal(page)).toBeVisible();
  await expect(tabs(page)).toHaveCount(0);

  await back(page).tap();
  await expect(terminal(page)).toHaveCount(0);
  await expect(inbox).toBeVisible();
  await expect(tab(page, /^Inbox/)).toHaveAttribute('aria-current', 'page');
});

test('S5, C8: the Orders tab shows the whole Orders panel, not the rail, and nothing collapses it', async ({ page, admiral, sea }) => {
  sea({ work: { name: 'Work', color: BLUE } });
  await admiral.start();
  await page.goto(admiral.url);

  await tab(page, 'Orders').tap();
  const orders = page.getByRole('complementary', { name: 'Orders', exact: true });
  await expect(orders).toBeVisible();
  await expect(page.getByRole('complementary', { name: 'Orders, collapsed' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Collapse orders' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Expand orders' })).toHaveCount(0);
});

test('C2p: the phone header has the mark, the title, "at sea" and Settings, and Sound lives in Settings', async ({ page, admiral, sea }) => {
  sea({ work: { name: 'Work', color: BLUE } });
  await admiral.start();
  await page.goto(admiral.url);

  const banner = page.getByRole('banner');
  await expect(banner.getByText('Admiral', { exact: true })).toBeVisible();
  await expect(banner.getByText(/ at sea$/)).toBeVisible();
  await expect(banner.getByRole('button', { name: 'Settings', exact: true })).toBeVisible();
  await expect(banner.getByRole('button', { name: /chime/i })).toHaveCount(0);
  await expect(banner.getByRole('button', { name: /^Needs you/ })).toHaveCount(0);

  await banner.getByRole('button', { name: 'Settings', exact: true }).tap();
  const dialog = page.getByRole('dialog', { name: 'Settings' });
  await expect(dialog.getByRole('region', { name: 'General' }).getByRole('button', { name: 'Mute attention sounds' })).toBeVisible();
});
