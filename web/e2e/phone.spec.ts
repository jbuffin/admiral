import type { Page } from '@playwright/test';
import { SUB_START, crewOf, launchCrew, release, releaseCrew, wireOf } from './bridge';
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

// Crew boats on the phone Ships list (#174). The names it is built to, from the prototype (scene ships) and the brief:
//
//   a crew boat is an indented button under its ship, "Board <crew>, teammate, crew of <ship>" plus ", needs you" or
//     ", anchored", with the second line "Teammate · working" | "Teammate · needs you" | "Teammate · anchored, exit N"
//   a ship that needs you through a crew boat sorts into the needs-you group (anchored or not), counts in the Ships tab
//     badge, and reads "Needs you · <crew> · waiting …"
//   subagents: one note under the ship (role "note", not a button) "Subagents · N · no terminal", then "<type> · <age>"
//   tapping a crew row boards it ("<ship> › <crew>"); Back returns to Ships; the phone bar has "More for <crew>" with
//     "Close crew boat"

const escape = (t: string) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const crewRows = (page: Page) => shipsTab(page).getByRole('button', { name: /, teammate, crew of / });
const crewRow = (page: Page, crew: string, ship: string, tail = '') =>
  shipsTab(page).getByRole('button', { name: `Board ${crew}, teammate, crew of ${ship}${tail}`, exact: true });
const shipRows = (page: Page) => shipsTab(page).getByRole('button', { name: /^Board [^,]+, Claude in Work, / });

test('#174 S1, S2: crew rows sit under their ship; a boat that needs you moves its older ship first and counts', async ({ page, admiral, sea }) => {
  test.setTimeout(60_000);
  // Every boat parks on `needs`; releasing `end` after it makes the boat exit 1. The ships' own cues are never released.
  const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait needs', 'hook needs_you', 'wait end', 'exit 1'] } });
  await admiral.start();
  const old = await s.launch('work', 'alpha', 'recorded');
  // `launched` has one-second resolution; a tie would leave the order to the server's.
  await page.waitForTimeout(1100);
  const young = await s.launch('work', 'beta', 'recorded');
  await launchCrew(s, old.session, 'api');
  await launchCrew(s, old.session, 'web');
  releaseCrew(s, old.session, 'web', 'needs');
  releaseCrew(s, old.session, 'web', 'end');
  await expect.poll(async () => (await crewOf(s, old.session)).find((c) => c.name === 'web')?.status).toBe('anchored');
  await page.goto(admiral.url);
  await tab(page, 'Ships').tap();

  // S1: two rows under alpha, the newest ship first.
  await expect(crewRows(page)).toHaveCount(2);
  await expect(crewRow(page, 'api', old.name)).toContainText('Teammate · working');
  await expect(crewRow(page, 'web', old.name, ', anchored')).toContainText('Teammate · anchored, exit 1');
  await expect(shipRows(page).nth(0)).toHaveAccessibleName(new RegExp(`^Board ${escape(young.name)}, `));
  await expect(tab(page, /^Ships/)).toHaveAccessibleName('Ships');

  // S2: the boat of the older ship needs you.
  releaseCrew(s, old.session, 'api', 'needs');
  await expect(crewRow(page, 'api', old.name, ', needs you')).toContainText('Teammate · needs you');
  await expect(tab(page, 'Ships, 1 needing you')).toBeVisible();
  await expect(shipsTab(page)).toContainText('2 ships, 1 needs you');
  await expect(shipRows(page).nth(0)).toHaveAccessibleName(new RegExp(`^Board ${escape(old.name)}, Claude in Work, Needs you · api · waiting `));
  await expect(shipRows(page).nth(1)).toHaveAccessibleName(new RegExp(`^Board ${escape(young.name)}, Claude in Work, Underway`));
});

test('#174 S3: an anchored ship whose crew boat needs you sorts into the needs-you group and counts', async ({ page, admiral, sea }) => {
  test.setTimeout(60_000);
  const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait needs', 'hook needs_you', 'wait end', 'exit 2'] } });
  await admiral.start();
  const old = await s.launch('work', 'alpha', 'recorded');
  const young = await s.launch('work', 'beta', 'recorded');
  await launchCrew(s, old.session, 'api');
  // The ship runs through its cue and anchors; its boat stays parked until it is released.
  release(s, old.session, 'needs');
  release(s, old.session, 'end');
  await expect.poll(async () => (await s.ship(old.session))?.status).toBe('anchored');
  releaseCrew(s, old.session, 'api', 'needs');
  await expect.poll(async () => (await crewOf(s, old.session)).find((c) => c.name === 'api')?.status).toBe('needs_you');
  expect((await s.ship(old.session))?.status).toBe('anchored');
  await page.goto(admiral.url);

  await expect(tab(page, 'Ships, 1 needing you')).toBeVisible();
  await tab(page, 'Ships, 1 needing you').tap();
  await expect(shipsTab(page)).toContainText('2 ships, 1 needs you');
  await expect(shipRows(page).nth(0)).toHaveAccessibleName(new RegExp(`^Board ${escape(old.name)}, Claude in Work, Needs you · api · waiting `));
  await expect(shipRows(page).nth(1)).toHaveAccessibleName(new RegExp(`^Board ${escape(young.name)}, `));
  await expect(crewRow(page, 'api', old.name, ', needs you')).toContainText('Teammate · needs you');
});

test('#174 S4: a ship with a subagent has a note under it that is not a button and boards nothing', async ({ page, admiral, sea }) => {
  test.setTimeout(60_000);
  const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait sub', `event SubagentStart ${SUB_START}`, 'wait go'] } });
  await admiral.start();
  const ship = await s.launch('work', 'alpha', 'recorded');
  release(s, ship.session, 'sub');
  await expect.poll(async () => (await wireOf(admiral.url, ship.session))?.subagents.length).toBe(1);
  await page.goto(admiral.url);
  await tab(page, 'Ships').tap();

  const note = shipsTab(page).getByRole('note');
  await expect(note).toHaveCount(1);
  await expect(note).toContainText('Subagents · 1 · no terminal');
  await expect(note).toContainText('Explore · just started');
  await expect(shipsTab(page).getByRole('button', { name: /Subagents|Explore/ })).toHaveCount(0);
  await note.tap();
  await expect(terminal(page)).toHaveCount(0);
  await expect(shipsTab(page)).toBeVisible();
});

test('#174 S5, S6, S7: a crew row boards the boat full screen and Back returns to Ships, in portrait and landscape; ⋯ closes an anchored boat', async ({ page, admiral, sea }) => {
  test.setTimeout(90_000);
  // The boats exit 1 on `end`; the ship never gets it and stays underway.
  const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait end', 'exit 1'] } });
  await admiral.start();
  const ship = await s.launch('work', 'alpha', 'recorded');
  await launchCrew(s, ship.session, 'api');
  await launchCrew(s, ship.session, 'web');
  releaseCrew(s, ship.session, 'api', 'end');
  releaseCrew(s, ship.session, 'web', 'end');
  await expect.poll(async () => (await crewOf(s, ship.session)).map((c) => c.status)).toEqual(['anchored', 'anchored']);
  await page.goto(admiral.url);
  await tab(page, 'Ships').tap();

  const board = async (crew: string) => {
    await crewRow(page, crew, ship.name, ', anchored').tap();
    await expect(terminal(page)).toBeVisible();
    await expect(terminal(page).getByText(`${ship.name} › ${crew}`, { exact: true })).toBeVisible();
    await expect(tabs(page)).toHaveCount(0);
  };

  // S5: portrait.
  await board('web');
  await back(page).tap();
  await expect(terminal(page)).toHaveCount(0);
  await expect(shipsTab(page)).toBeVisible();
  await expect(tab(page, 'Ships')).toHaveAttribute('aria-current', 'page');

  // S7: the same at 874x402.
  await page.setViewportSize({ width: 874, height: 402 });
  await board('web');
  await back(page).tap();
  await expect(shipsTab(page)).toBeVisible();

  // At 320 px the anchored pill ellipsizes rather than run under ⋯.
  await page.setViewportSize({ width: 320, height: 640 });
  await board('web');
  const pill = (await terminal(page).locator('.phone-line .term-pill').boundingBox())!;
  const more = (await terminal(page).getByRole('button', { name: 'More for web' }).boundingBox())!;
  expect(pill.x + pill.width).toBeLessThanOrEqual(more.x);
  await back(page).tap();
  await expect(shipsTab(page)).toBeVisible();
  await page.setViewportSize({ width: 402, height: 874 });

  // S6: ⋯ on the phone bar closes the anchored boat and returns to the ship.
  await board('api');
  await terminal(page).getByRole('button', { name: 'More for api' }).tap();
  const close = page.getByRole('menuitem', { name: 'Close crew boat' });
  await expect(close).toBeEnabled();
  await close.tap();
  await expect(terminal(page).getByText(`${ship.name} › api`)).toHaveCount(0);
  await expect(terminal(page).getByText(ship.name, { exact: true })).toBeVisible();
  await expect.poll(async () => (await crewOf(s, ship.session)).map((c) => c.name)).toEqual(['web']);
  await back(page).tap();
  await expect(crewRows(page)).toHaveCount(1);
});
