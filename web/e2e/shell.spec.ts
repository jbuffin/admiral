import type { Page } from '@playwright/test';
import { expect, test, type Admiral } from './fixtures';
import { HOME, WORK, holds, boatOf, boatReceived, boatSays, chip, expectSeededTurn, flotilla, interruptButton, launchCrew, launchFromTheShell, launchShip, line, appendLines, messageBox, outcomeEvents, probeableClaude, received, releaseBoat, rowLink, say, seedBoatTranscript, seedFleets, seedTurn, sendButton, sentBubble, setBoatState, setState, shipOption, shipViewUrl, stopDialog, stopShipButton, viewPanel, viewTab, watchPage, withFakeGh, HOSTILE, seedInboxItem, setReason, chatAnswerFile, chatAsks, chatCleared, chatReceived, chatSays, chatStarts, chatEnds, chatPrompts, chatTurnEnds, chatSession, launchAsAdmiralBot, mooringEnv, gitCheckout, dropFetchRedirect } from './shell';
import { decryptPush, newSubscriber, pushStub, startFakePush, stubSeen } from './push';
import { spawnSync } from 'node:child_process';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const TABLE = ['| Ship | Status |', '|:-----|-------:|', '| Alpha | afloat |', '| Bravo | anchored |'].join('\n');
const WIDE_TABLE = ['| ' + Array.from({ length: 14 }, (_, i) => `column-number-${i}-with-a-long-heading`).join(' | ') + ' |', '|' + '---|'.repeat(14), '| ' + Array.from({ length: 14 }, (_, i) => `cell-${i}-with-long-unbroken-content`).join(' | ') + ' |'].join('\n');
const pageFits = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth);

const surface = (page: Page, title: string) => page.getByRole('heading', { name: title, exact: true });
const nav = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });
const group = (page: Page, title: string) => page.getByRole('main').getByRole('region', { name: new RegExp(`^${title}`) });
const row = (page: Page, ship: { name: string }) => page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name });
const switcher = (page: Page, fleet: string) => page.getByRole('navigation').getByRole('button', { name: fleet, exact: true });

// What the page asserts about its own traffic, for every journey that opens it.
function expectCleanTraffic(watch: ReturnType<typeof watchPage>) {
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
}

test('the shell navigates its surfaces, lists the chosen fleet\'s ships live and scopes them to that fleet', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  // Ships that end are launched first: their cue file is read by any harness that starts while it is there.
  const moored = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', exit: 3 });
  const sailed = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', exit: 0 });

  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  setState(admiral, login, 'needs_you');
  await say(admiral, login, 'Which branch should I rebase onto?');
  const billing = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-billing' });
  setState(admiral, billing, 'working');
  await say(admiral, billing, 'Reading billing/export.go');
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  // After the last cue is cleared, so the idling crew boat cannot read an exit cue.
  await launchCrew(admiral, billing, 'mate');

  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(surface(page, 'Inbox')).toBeVisible();
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Inbox', 'Ships', 'Orders', 'Chat', 'Campaigns', 'Routines', 'Crew', 'Preferences']);
  // The build's version sits at the foot of the side nav.
  await expect(page.getByRole('navigation').getByText('dev', { exact: true })).toBeVisible();

  // With no choice yet, the first fleet is active.
  const logo = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/logos/claude');
  await nav(page, 'Ships').click();
  await expect(surface(page, 'Ships')).toBeVisible();
  await expect(page.getByRole('main')).toContainText(WORK);
  expect((await logo).status()).toBe(200);

  // The fleet's ships, grouped Needs you, Underway, Anchored; a ship sailing off and another fleet's ship are not listed.
  const main = page.getByRole('main');
  await expect(main.locator('.ship-group').getByRole('heading', { level: 2 })).toHaveText([/^Needs you\s*1$/, /^Underway\s*1$/, /^Anchored\s*1$/]);
  await expect(group(page, 'Needs you').getByRole('listitem')).toHaveCount(1);
  await expect(group(page, 'Needs you')).toContainText(login.name);
  await expect(group(page, 'Underway')).toContainText(billing.name);
  await expect(group(page, 'Anchored')).toContainText(moored.name);
  await expect(main).not.toContainText(sailed.name);
  await expect(main).not.toContainText(visitor.name);

  // A row carries its harness logo, its order and its conversation tail; a ship with no conversation has no tail.
  await expect(row(page, login)).toContainText('ord-login');
  await expect(row(page, login)).toContainText('Fix the login redirect');
  await expect(row(page, login)).toContainText('Which branch should I rebase onto?');
  await expect(row(page, login).locator('img[src="/api/v1/logos/claude"]')).toBeVisible();
  await expect(row(page, billing)).toContainText('Reading billing/export.go');
  await expect(row(page, moored).locator('img[src="/api/v1/logos/codex"]')).toBeVisible();
  await expect(row(page, moored)).not.toContainText(/undefined|null|Reading|Which branch/);
  // A row names the repo its ship launched in.
  await expect(row(page, login).getByText('Repo proj')).toBeVisible();
  await expect(row(page, billing)).toContainText('1 crew');
  await expect(row(page, billing).locator('.ship-age')).toHaveText(/^(<1m|\d+m)$/);
  await expect(row(page, login)).not.toContainText('crew');
  expect((await row(page, login).boundingBox())!.height).toBeGreaterThanOrEqual(44);

  // Server-side changes reach the open page: a ship moves group when its status changes and its tail updates.
  setState(admiral, login, 'working');
  await expect(group(page, 'Underway')).toContainText(login.name);
  await expect(main.locator('.ship-group').getByRole('heading', { level: 2 })).toHaveText([/^Underway\s*2$/, /^Anchored\s*1$/]);
  await say(admiral, login, 'Rebasing onto main');
  await expect(row(page, login)).toContainText('Rebasing onto main', { timeout: 15_000 });
  await expect(row(page, login)).not.toContainText('Which branch should I rebase onto?');

  // Opening a ship is a navigation in the page: Back returns to the list without a reload, and the keyboard opens a row too.
  await page.evaluate(() => ((window as unknown as { stayed: boolean }).stayed = true));
  await rowLink(page, login).tap();
  await expect(page).toHaveURL(shipViewUrl(admiral, login));
  await expect(page.getByRole('heading', { name: login.name, exact: true })).toBeVisible();
  await page.goBack();
  await expect(surface(page, 'Ships')).toBeVisible();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=zulu/);
  await rowLink(page, login).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(shipViewUrl(admiral, login));
  await page.goBack();
  expect(await page.evaluate(() => (window as unknown as { stayed?: boolean }).stayed)).toBe(true);

  await switcher(page, WORK).click();
  const menu = page.getByRole('menu');
  await expect(menu.getByRole('menuitemradio', { name: WORK })).toHaveAttribute('aria-checked', 'true');
  await menu.getByRole('menuitemradio', { name: HOME }).click();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('main')).toContainText(HOME);
  await expect(page.getByRole('main')).not.toContainText(WORK);
  await expect(group(page, 'Underway')).toContainText(visitor.name);
  for (const other of [login, billing, moored]) await expect(row(page, other)).toHaveCount(0);

  // Links navigate in the page and keep the fleet; Back and Forward walk the history.
  await page.evaluate(() => ((window as unknown as { noReload: boolean }).noReload = true));
  await nav(page, 'Crew').click();
  await expect(surface(page, 'Crew')).toBeVisible();
  await expect(page).toHaveURL(/\/app\/crew\?(.*&)?fleet=alpha/);
  await page.goBack();
  await expect(surface(page, 'Ships')).toBeVisible();
  await page.goForward();
  await expect(surface(page, 'Crew')).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { noReload?: boolean }).noReload)).toBe(true);

  // A fresh load remembers the choice, and an unknown ?fleet= falls back to it.
  await page.goto(admiral.url + 'app/ships?fleet=nope');
  await expect(switcher(page, HOME)).toBeVisible();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);

  expect(watch.paths.some((p) => p.startsWith('/api/v1/config'))).toBe(true);
  expect(watch.paths).toContain('/ws/v1');
  expectCleanTraffic(watch);
});

test('a ship\'s view opens on its conversation, follows the transcript live, joins the bridge only on Terminal, and says when the ship is read only or gone', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  // Ships that end are launched first: their cue file is read by any harness that starts while it is there.
  const moored = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', exit: 3 });
  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  setState(admiral, login, 'working');

  // The view opens on Conversation and shows its turn.
  await seedTurn(admiral, login);
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, login).tap();
  await expect(page).toHaveURL(shipViewUrl(admiral, login));
  await expect(page.getByRole('heading', { name: login.name, exact: true })).toBeVisible();
  await expect(page.getByLabel(/^Running for (<1m|\d+m)$/)).toBeVisible();
  await expect(page.getByRole('tab')).toHaveText(['Conversation', 'Terminal', 'PR']);
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await expectSeededTurn(page);
  await expect(viewPanel(page)).toContainText('Working');

  // A line appended to the transcript shows without a reload.
  await appendLines(admiral, login, [line.assistant('Appended while the view was open')]);
  await expect(viewPanel(page)).toContainText('Appended while the view was open', { timeout: 15_000 });

  // A table in a reply shows as a table, and a table wider than the column scrolls inside its own box.
  await appendLines(admiral, login, [line.assistant(`Fleet status\n\n${TABLE}\n\n${WIDE_TABLE}`)]);
  const tables = viewPanel(page).locator('table');
  await expect(tables).toHaveCount(2, { timeout: 15_000 });
  await expect(tables.first().locator('th')).toHaveText(['Ship', 'Status']);
  await expect(tables.first().locator('td')).toHaveText(['Alpha', 'afloat', 'Bravo', 'anchored']);
  const box = tables.nth(1).locator('xpath=..');
  expect(await box.evaluate((e) => e.scrollWidth > e.clientWidth && ['auto', 'scroll'].includes(getComputedStyle(e).overflowX))).toBe(true);
  expect(await pageFits(page)).toBe(true);

  // PR is a placeholder here, and reading it never joins the bridge.
  await viewTab(page, 'PR').tap();
  await expect(viewTab(page, 'PR')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page)).toContainText('No pull request yet. A PR shows here as soon as the ship or its crew opens one.');
  expect(watch.paths.filter((p) => /^\/ws\/(v1\/)?bridge\//.test(p))).toEqual([]);

  // Only the Terminal tab joins the bridge, and what is typed there reaches the pane. Leaving it closes the socket.
  const joined = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await viewTab(page, 'Terminal').tap();
  const bridge = await joined;
  expect(new URL(bridge.url()).pathname).toBe(`/ws/v1/bridge/${encodeURIComponent(login.session)}`);
  await expect(viewPanel(page).locator('.xterm')).toBeVisible();
  // Safari zooms the page when a focused input's font is under 16px.
  expect(await viewPanel(page).locator('.xterm-helper-textarea').evaluate((el) => parseFloat(getComputedStyle(el).fontSize))).toBeGreaterThanOrEqual(16);
  // The fix is the font size, not a page that cannot be zoomed.
  expect(await page.locator('meta[name=viewport]').getAttribute('content')).not.toMatch(/maximum-scale|user-scalable\s*=\s*(no|0)/);
  const recorded = join(admiral.home, 'rec', login.session);
  await expect.poll(() => existsSync(join(recorded, 'cues-done'))).toBe(true);
  await viewPanel(page).locator('.xterm').tap();
  await page.keyboard.type('typed-on-the-bridge');
  await expect.poll(() => readFileSync(join(recorded, 'stdin'), 'utf8')).toContain('typed-on-the-bridge');
  // A touch keyboard has no Esc, so the key bar sends it.
  await viewPanel(page).getByRole('button', { name: 'Send Esc' }).tap();
  await expect.poll(() => readFileSync(join(recorded, 'stdin'), 'utf8')).toContain('\x1b');
  await viewTab(page, 'Conversation').tap();
  await expect.poll(() => bridge.isClosed()).toBe(true);

  // A Codex ship has no conversation Admiral can read: a card says so and points to the terminal.
  await page.goBack();
  await expect(surface(page, 'Ships')).toBeVisible();
  await rowLink(page, moored).tap();
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page)).toContainText(/Starting|No conversation/);
  await expect(viewPanel(page).getByRole('button', { name: 'Open terminal' })).toBeVisible();

  // An anchored ship exited with an error: its box is read only and says so.
  await expect(messageBox(page)).toBeDisabled();
  await expect(viewPanel(page)).toContainText('Exited with error, read only');
  await expect(interruptButton(page)).toHaveCount(0);
  // The box sits at the bottom of the panel however short the conversation is.
  const panelBox = (await viewPanel(page).boundingBox())!;
  const inputBox = (await messageBox(page).boundingBox())!;
  expect(panelBox.y + panelBox.height - (inputBox.y + inputBox.height)).toBeLessThan(40);

  // A ship that leaves the list while its view is open: the view says it is gone and offers Back.
  const stopped = await fetch(`${admiral.url}api/v1/ships/${encodeURIComponent(moored.session)}/stop`, { method: 'POST' });
  expect(stopped.status).toBe(200);
  await expect(page.getByRole('main')).toContainText(/gone|no longer/i);
  const back = page.getByRole('link', { name: 'Back' }).or(page.getByRole('button', { name: 'Back' }));
  await back.tap();
  await expect(surface(page, 'Ships')).toBeVisible();
  await expect(row(page, moored)).toHaveCount(0);

  expectCleanTraffic(watch);
});

test('talking to a ship: the box sends, the message shows dimmed until the conversation has it, Interrupt presses Esc, Return to port and Stop ship end the ship', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  const chat = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'replydelay 2', 'reply Aye, rebasing now'] });
  setState(admiral, chat, 'needs_you');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, chat).tap();
  await expect(messageBox(page)).toBeEnabled();
  // A draft survives a visit to another tab.
  await messageBox(page).fill('A draft to keep');
  // Leaving Terminal before the bridge joined, or letting its socket outlive the ship, logs a socket error.
  const joined = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await viewTab(page, 'Terminal').tap();
  const bridge = await joined;
  await bridge.waitForEvent('framereceived');
  const closed = bridge.waitForEvent('close');
  await viewTab(page, 'Conversation').tap();
  await closed;
  await expect(messageBox(page)).toHaveValue('A draft to keep');
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route('**/api/v1/ships/*/messages', async (route) => {
    await gate;
    await route.continue();
  });
  setState(admiral, chat, 'working');
  await expect(interruptButton(page)).toBeEnabled();
  await messageBox(page).fill('Please rebase onto main');
  await sendButton(page).tap();
  // While the send is out the box keeps its text read-only, Send spins and Interrupt waits; no bubble yet.
  await expect(sendButton(page)).toHaveAttribute('aria-busy', 'true');
  await expect(messageBox(page)).toHaveValue('Please rebase onto main');
  await expect(messageBox(page)).not.toBeEditable();
  await expect(interruptButton(page)).toBeDisabled();
  await expect(sentBubble(page, 'Please rebase onto main')).toHaveCount(0);
  const accepted = page.waitForResponse((r) => r.url().includes('/messages') && r.status() === 200);
  release();
  await accepted;
  await expect(messageBox(page)).toHaveValue('');
  await expect(messageBox(page)).toBeEditable();
  await expect(sendButton(page)).not.toHaveAttribute('aria-busy', 'true');
  await expect(interruptButton(page)).toBeEnabled();
  // The bubble waits for the harness's record, which the fake writes two seconds after the server accepted the message, not for the 200.
  await expect(sentBubble(page, 'Please rebase onto main')).toBeVisible();
  await expect.poll(() => recordedInTranscript(admiral, chat, 'Please rebase onto main'), { timeout: 15_000 }).toBe(true);
  await expect(sentBubble(page, 'Please rebase onto main')).toHaveCount(0);
  await expect(viewPanel(page).getByText('Please rebase onto main', { exact: true })).toHaveCount(1);
  await expect(viewPanel(page)).toContainText('Aye, rebasing now', { timeout: 15_000 });
  expect(received(admiral, chat)).toContain('Please rebase onto main');
  await page.unroute('**/api/v1/ships/*/messages');

  // A hardware keyboard sends with Enter.
  await messageBox(page).fill('And run the tests');
  await messageBox(page).press('Enter');
  await expect(viewPanel(page).getByText('And run the tests', { exact: true })).toHaveCount(1);
  await expect(messageBox(page)).toHaveValue('');

  // The box is one line, with Send inside it, and grows with its text.
  const oneLine = (await messageBox(page).boundingBox())!;
  const send = (await sendButton(page).boundingBox())!;
  expect(send.x).toBeGreaterThan(oneLine.x + oneLine.width - send.width - 1);
  expect(send.y + send.height).toBeGreaterThan(oneLine.y);
  await messageBox(page).fill('First line\nSecond line\nThird line');
  await expect.poll(async () => (await messageBox(page).boundingBox())!.height).toBeGreaterThan(oneLine.height + 20);
  await messageBox(page).fill('');
  await expect.poll(async () => (await messageBox(page).boundingBox())!.height).toBe(oneLine.height);

  await expect(interruptButton(page)).toBeVisible();
  // Interrupt sits on the tabs' line, at its far right.
  const tabBox = (await viewTab(page, 'Conversation').boundingBox())!;
  const stop = (await interruptButton(page).boundingBox())!;
  expect(Math.abs(stop.y + stop.height / 2 - (tabBox.y + tabBox.height / 2))).toBeLessThan(4);
  expect(stop.x + stop.width).toBeGreaterThan(oneLine.x + oneLine.width - 8);
  expect(received(admiral, chat)).not.toContain('\x1b');
  await interruptButton(page).tap();
  await expect.poll(() => received(admiral, chat)).toContain('\x1b');

  const nativeDialogs: string[] = [];
  page.on('dialog', (d) => {
    nativeDialogs.push(d.message());
    void d.dismiss();
  });
  await stopShipButton(page).tap();
  await expect(stopDialog(page)).toBeVisible();
  expect((await stopDialog(page).boundingBox())!.width).toBeLessThanOrEqual(520);
  await stopDialog(page).getByRole('button', { name: 'Stop ship' }).tap();
  await expect(surface(page, 'Ships')).toBeVisible();
  await expect(row(page, chat)).toHaveCount(0);
  expect(nativeDialogs).toEqual([]);
  await expect.poll(() => outcomeEvents(admiral, chat)).toEqual(['launched', 'ended', 'stopped']);

  // Return to port waits for the turn to end, tells the ship to clean up, and stops it once that turn ends.
  const done = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setState(admiral, done, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, done).tap();
  await page.getByRole('button', { name: 'Return to port', exact: true }).tap();
  await page.getByRole('dialog', { name: 'Return to port' }).getByRole('button', { name: 'Return to port' }).tap();
  await expect(page.getByRole('button', { name: 'Returning to port' })).toHaveAttribute('aria-busy', 'true');
  setReason(admiral, done, 'stop');
  setState(admiral, done, 'needs_you');
  await expect.poll(() => received(admiral, done)).toContain('Return to port: this run is over');
  await expect.poll(() => shipOption(admiral, done, 'returning')).toBe('sent');
  await expect(page.getByRole('button', { name: 'Returning to port' })).toBeVisible();
  setReason(admiral, done, 'stop');
  setState(admiral, done, 'needs_you');
  await expect.poll(() => outcomeEvents(admiral, done)).toEqual(['launched', 'ended', 'stopped']);

  expectCleanTraffic(watch);
});

test('a ship launched in a checkout names its folder as its repo, and a flotilla chip takes the conversation, drafts, messages, Interrupt and terminal of its boat', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  // A ship launched in a checkout names its folder as its repo, on its row and in the boarded ship's header.
  const repo = join(admiral.home, 'harbor-web');
  gitCheckout(repo);
  const dev = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', dir: repo, cues: ['bracketedpaste'] });
  setState(admiral, dev, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await expect(row(page, dev).getByText('Repo harbor-web')).toBeVisible();
  await rowLink(page, dev).tap();
  await expect(page.locator('.ship-view-head').getByText('Repo harbor-web')).toBeVisible();

  // The flotilla: a chip for the ship and one per boat, by role (Teammate when it has none). A picked boat takes the conversation,
  // the terminal, the message box and Interrupt; Stop ship stays the ship's.
  const lead = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setState(admiral, lead, 'working');
  await say(admiral, lead, 'The ship is waiting on its crew');
  await launchCrew(admiral, lead, 'rev', 'reviewer', ['bracketedpaste', 'record', 'wait done', 'exit 0']);
  await launchCrew(admiral, lead, 'mate');
  await seedBoatTranscript(admiral, lead, 'rev', [line.user('Review the parser'), line.assistant('Boat found two parser bugs')]);
  setBoatState(admiral, lead, 'rev', 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, lead).tap();
  await expect(chip(page, new RegExp(lead.name))).toBeChecked();
  await expect(chip(page, /reviewer/)).toBeVisible();
  await expect(chip(page, /Teammate/)).toBeVisible();
  await expect(flotilla(page).getByRole('radio')).toHaveCount(3);
  await expect(viewPanel(page)).toContainText('The ship is waiting on its crew');

  await chip(page, /reviewer/).tap();
  await expect(chip(page, /reviewer/)).toBeChecked();
  await expect(viewPanel(page)).toContainText('Boat found two parser bugs');
  await expect(viewPanel(page)).not.toContainText('The ship is waiting on its crew');

  // A draft belongs to the chip it was typed under: the boat's stays with the boat and the ship's with the ship.
  await expect(messageBox(page)).toHaveValue('');
  await messageBox(page).fill('A draft for the boat');
  await chip(page, new RegExp(lead.name)).tap();
  await expect(messageBox(page)).toHaveValue('');
  await messageBox(page).fill('A draft for the ship');
  await chip(page, /reviewer/).tap();
  await expect(messageBox(page)).toHaveValue('A draft for the boat');
  await chip(page, new RegExp(lead.name)).tap();
  await expect(messageBox(page)).toHaveValue('A draft for the ship');
  await messageBox(page).fill('');
  await chip(page, /reviewer/).tap();
  await expect(messageBox(page)).toHaveValue('A draft for the boat');

  // The boat's subscription pushes a line appended to its transcript.
  boatSays(admiral, lead, 'rev', [line.assistant('Appended to the boat while the view was open')]);
  await expect(viewPanel(page)).toContainText('Appended to the boat while the view was open', { timeout: 15_000 });

  // A message from the box reaches the boat's pane and not the ship's.
  await messageBox(page).fill('Check the lexer too');
  await sendButton(page).tap();
  await expect(sentBubble(page, 'Check the lexer too')).toBeVisible();
  await expect.poll(() => boatReceived(admiral, lead, 'rev')).toContain('Check the lexer too');
  expect(received(admiral, lead)).not.toContain('Check the lexer too');
  boatSays(admiral, lead, 'rev', [line.user('Check the lexer too')]);
  await expect(sentBubble(page, 'Check the lexer too')).toHaveCount(0, { timeout: 15_000 });
  await expect(viewPanel(page).getByText('Check the lexer too', { exact: true })).toHaveCount(1);

  // Interrupt presses Esc in the boat's pane.
  await interruptButton(page).tap();
  await expect.poll(() => boatReceived(admiral, lead, 'rev')).toContain('\x1b');
  expect(received(admiral, lead)).not.toContain('\x1b');

  // Stop ship stays the ship's.
  await stopShipButton(page).tap();
  await expect(stopDialog(page)).toContainText(lead.name);
  await stopDialog(page).getByRole('button', { name: 'Cancel' }).tap();
  await expect(stopDialog(page)).toBeHidden();

  // The terminal is the boat's pane, over the versioned bridge path.
  const boatBridge = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await viewTab(page, 'Terminal').tap();
  expect(new URL((await boatBridge).url()).pathname).toBe(`/ws/v1/bridge/${encodeURIComponent(lead.session)}/crew/rev`);
  await viewTab(page, 'Conversation').tap();

  // A boat with no transcript says so and points to its terminal.
  await chip(page, /Teammate/).tap();
  await expect(chip(page, /Teammate/)).toBeChecked();
  await expect(viewPanel(page)).toContainText(/No conversation/);
  await expect(viewPanel(page)).not.toContainText('Boat found two parser bugs');
  await viewPanel(page).getByRole('button', { name: 'Open terminal' }).tap();
  await expect(viewTab(page, 'Terminal')).toHaveAttribute('aria-selected', 'true');
  await viewTab(page, 'Conversation').tap();

  // A boat that exits 0 anchors: its chip stays, reads as finished and not as an error, and its box is read only.
  await chip(page, /reviewer/).tap();
  releaseBoat(admiral, lead, 'rev');
  await expect.poll(async () => (await boatOf(admiral, lead, 'rev'))?.status).toBe('anchored');
  expect((await boatOf(admiral, lead, 'rev'))?.exit_code).toBe(0);
  await expect(chip(page, /reviewer/)).toBeVisible();
  await expect(chip(page, /reviewer/)).toContainText(/finished/i);
  await expect(chip(page, /reviewer/)).not.toContainText(/error/i);
  await expect(chip(page, /Teammate/)).not.toContainText(/finished|error/i);
  await expect(messageBox(page)).toBeDisabled();
  await expect(viewPanel(page)).not.toContainText('Exited with error');
  await expect(interruptButton(page)).toHaveCount(0);
  await expect(viewPanel(page)).toContainText('Boat found two parser bugs');

  expectCleanTraffic(watch);
});

test('a ship launches from the header and from an order on the Ships screen and shows in the list', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await expect(switcher(page, WORK)).toBeVisible();
  await launchFromTheShell(page, admiral, (l) => l.click(), { phone: false });
  expectCleanTraffic(watch);
});

// The user line the fake harness appends to the transcript after its reply delay: the record the sent bubble waits for.
function recordedInTranscript(a: Admiral, ship: { session: string }, text: string): boolean {
  // The fake writes argv only once its pane has started, after the launch call returned.
  const argv = join(a.home, 'rec', ship.session, 'argv');
  if (!existsSync(argv)) return false;
  const args = readFileSync(argv, 'utf8').split('\0');
  const id = args[args.indexOf('--session-id') + 1];
  const file = join(a.home, 'identity', 'zulu-claude', 'projects', 'zz', `${id}.jsonl`);
  return existsSync(file) && readFileSync(file, 'utf8').includes(text);
}

function box(b: { width: number; height: number } | null) {
  if (!b) throw new Error('the element has no box');
  return b;
}

test('a Copilot ship shows its conversation in the Conversation tab, shows a line it records live, and answers its question card in place', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const hook = (event: string, extra: object) => `copilothook ${event} ${JSON.stringify({ sessionId: 'x', cwd: '.', hook_event_name: event, ...extra })}`;
  const hooks = [hook('preToolUse', { toolName: 'ask_user', toolCallId: 'fake-ask-call', toolArgs: {} }), hook('notification', { notification_type: 'elicitation_dialog', title: 'Input needed', message: 'Information requested' })].join('\n');
  const pilot = await launchShip(admiral, { fleet: 'alpha', harness: 'copilot', cue: `record\nevents Copilot reads events.jsonl\nwait more\nevents Appended while the view was open\nask Which colour?|Red|Green|Blue\n${hooks}\nwait echo\nprompted Please rebase onto main\n` });
  await page.goto(admiral.url + 'app/ships?fleet=alpha');
  await rowLink(page, pilot).tap();
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page)).toContainText('Question 1');
  await expect(viewPanel(page)).toContainText('Copilot reads events.jsonl');
  await expect(viewPanel(page)).not.toContainText('No conversation for this harness');

  writeFileSync(join(admiral.home, 'rec', pilot.session, 'more'), '');
  await expect(viewPanel(page)).toContainText('Appended while the view was open', { timeout: 15_000 });

  const choice = (name: string) => viewPanel(page).getByRole('button', { name, exact: true });
  await expect(choice('Blue')).toBeVisible({ timeout: 15_000 });
  await choice('Green').tap();
  await expect.poll(() => received(admiral, pilot)).toBe('\x1b[B\r');
  await expect(choice('Green')).toHaveCount(0);

  // The sent bubble stands until the conversation shows the echo, then the message shows once.
  await messageBox(page).fill('Please rebase onto main');
  await sendButton(page).tap();
  await expect(sentBubble(page, 'Please rebase onto main')).toBeVisible();
  await expect.poll(() => received(admiral, pilot)).toContain('Please rebase onto main');
  writeFileSync(join(admiral.home, 'rec', pilot.session, 'echo'), '');
  await expect(sentBubble(page, 'Please rebase onto main')).toHaveCount(0, { timeout: 15_000 });
  await expect(viewPanel(page).getByText('Please rebase onto main')).toHaveCount(1);

  // Hand to Commodore asks the ship for an issue, then relaunches it as Commodore under the same name; a Codex ship shows why it cannot.
  const commodoreSkill = join(admiral.home, 'identity', 'zulu-claude', 'skills', 'commodore');
  mkdirSync(commodoreSkill, { recursive: true });
  writeFileSync(join(commodoreSkill, 'SKILL.md'), '---\nname: commodore\ndescription: a fake skill\n---\n');
  const design = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  const restoreRedirect = dropFetchRedirect(join(admiral.home, 'proj'));
  setState(admiral, design, 'working');
  await say(admiral, design, 'The design is settled');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, design).tap();
  await page.getByRole('button', { name: 'Hand to Commodore', exact: true }).tap();
  const handSheet = page.getByRole('dialog', { name: 'Hand to Commodore' });
  await expect(handSheet.getByRole('radio', { name: /GitHub issue/ })).toBeChecked();
  await handSheet.getByRole('radio', { name: 'Other' }).tap();
  await expect(handSheet.getByRole('button', { name: 'Hand to Commodore' })).toBeDisabled();
  await handSheet.getByRole('radio', { name: /GitHub issue/ }).tap();
  await handSheet.getByRole('button', { name: 'Hand to Commodore' }).tap();
  await expect(page.getByRole('button', { name: 'Handing to Commodore' })).toHaveAttribute('aria-busy', 'true');
  setReason(admiral, design, 'stop');
  setState(admiral, design, 'needs_you');
  let ref = '';
  await expect.poll(() => (ref = new RegExp(`\\S*/ships/${design.session}/ref`).exec(received(admiral, design))?.[0] ?? '')).not.toBe('');
  writeFileSync(ref, 'https://github.com/acme/api/issues/7\n');
  await expect.poll(() => shipOption(admiral, design, 'handing')).toBe('sent');
  setReason(admiral, design, 'stop');
  setState(admiral, design, 'needs_you');
  await expect.poll(() => outcomeEvents(admiral, design, ['stopped', 'handed'])).toEqual(['stopped', 'handed']);
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, design).tap();
  await expect(page.locator('.ship-view-head').getByText(design.name)).toBeVisible();
  await expect(page.getByRole('button', { name: 'Return to port', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /Hand(ing)? to Commodore/ })).toHaveCount(0);

  // A second hand started from elsewhere while the sheet is open is the route's 409, shown in the sheet, which stays open.
  restoreRedirect();
  const raced = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  setState(admiral, raced, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, raced).tap();
  await page.getByRole('button', { name: 'Hand to Commodore', exact: true }).tap();
  expect((await page.request.post(admiral.url + `api/ships/${raced.session}/commodore`, { data: { kind: 'issue' } })).status()).toBe(202);
  await handSheet.getByRole('button', { name: 'Hand to Commodore' }).tap();
  await expect(handSheet.getByRole('alert')).toContainText('already being handed');
  await expect(handSheet).toBeVisible();

  const other = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', cues: ['bracketedpaste'] });
  setState(admiral, other, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, other).tap();
  await expect(page.getByRole('button', { name: 'Hand to Commodore', exact: true })).toBeDisabled();
  await expect(page.getByText(/only a Claude ship can be handed to Commodore/)).toBeVisible();
});

const cachedPaths = (page: Page) =>
  page.evaluate(async () => {
    const paths: string[] = [];
    for (const name of await caches.keys()) {
      for (const r of await (await caches.open(name)).keys()) paths.push(new URL(r.url).pathname);
    }
    return paths;
  });
const controlled = (page: Page) => page.evaluate(() => navigator.serviceWorker.controller !== null);

test.describe('installed app', () => {
  test.use({ serviceWorkers: 'allow' });

  test('the shell is installable and a worker controls /app/, and with the server down the cached shell says it cannot reach the Mac and Retry returns once it answers', { tag: '@critical' }, async ({ page, admiral }) => {
    await admiral.start();
    await page.goto(admiral.url + 'app/');
    await expect(surface(page, 'Inbox')).toBeVisible();

    const res = await page.request.get(admiral.url + (await page.locator('link[rel="manifest"]').getAttribute('href'))!.replace(/^\//, ''));
    expect(res.status()).toBe(200);
    expect(res.headers()['content-type']).toContain('json');
    const manifest = await res.json();
    expect(manifest).toMatchObject({ name: 'Admiral', display: 'standalone', scope: '/', start_url: '/app/' });
    const sized = (size: string, purpose?: string) =>
      manifest.icons.find((i: { sizes: string; purpose?: string }) => i.sizes === size && (i.purpose ?? 'any').split(' ').includes(purpose ?? 'any'));
    for (const icon of [sized('192x192'), sized('512x512'), sized('512x512', 'maskable')]) {
      expect(icon).toBeTruthy();
      const png = await page.request.get(admiral.url + icon.src.replace(/^\//, ''));
      expect(png.status()).toBe(200);
      expect(png.headers()['content-type']).toBe('image/png');
    }

    const touch = await page.request.get(admiral.url + (await page.locator('link[rel="apple-touch-icon"]').getAttribute('href'))!.replace(/^\//, ''));
    expect(touch.headers()['content-type']).toBe('image/png');
    await expect(page.locator('meta[name="theme-color"][media="(prefers-color-scheme: light)"]')).toHaveAttribute('content', /^#F4F5F7$/i);
    await expect(page.locator('meta[name="theme-color"][media="(prefers-color-scheme: dark)"]')).toHaveAttribute('content', /^#0B1118$/i);

    // The worker caches the shell when it installs, and never an API or socket response.
    const scope = await page.evaluate(async () => (await navigator.serviceWorker.ready).scope);
    expect(new URL(scope).pathname).toBe('/app/');
    await expect.poll(() => cachedPaths(page)).toContain('/app/');
    const paths = await cachedPaths(page);
    expect(paths.some((p) => p.startsWith('/assets/'))).toBe(true);
    expect(paths.filter((p) => p.startsWith('/api/') || p.startsWith('/ws/'))).toEqual([]);
    expect(paths).not.toContain('/');
    await page.reload();
    await expect.poll(() => controlled(page)).toBe(true);

    await admiral.stopServer();
    await page.reload();
    await expect(page.getByRole('heading', { name: "Can't reach your Mac" })).toBeVisible();
    for (const check of ['Tailscale on here?', 'Mac awake?', 'Admiral running?']) await expect(page.getByText(check)).toBeVisible();
    await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible();
    // The shell stays mounted under the screen, so a blip keeps what the person typed.
    await expect(surface(page, 'Inbox')).toBeAttached();

    await page.getByRole('button', { name: 'Retry' }).click();
    await expect(page.getByRole('heading', { name: "Can't reach your Mac" })).toBeVisible();

    await admiral.resumeServer();
    await page.getByRole('button', { name: 'Retry' }).click();
    await expect(surface(page, 'Inbox')).toBeVisible();
    await expect(page.getByRole('heading', { name: "Can't reach your Mac" })).toBeHidden();
  });

  test('turning notifications on makes this Mac a push device: a watched stop only toasts, an unwatched one pushes a message that decrypts to the ship, and turning it off drops it', async ({ page, context, admiral }) => {
    seedFleets(admiral, { live: true });
    const fake = await startFakePush(admiral.home);
    try {
      const sub = newSubscriber();
      await admiral.start({ env: { ...withFakeGh(admiral), ADMIRAL_PUSH_TEST_ROOTS: fake.rootsFile } });
      await context.addInitScript(pushStub, { endpoint: `${fake.url}/sub/mac`, p256dh: sub.p256dh, auth: sub.authB64 });
      const devices = async () => ((await (await fetch(`${admiral.url}api/v1/devices`)).json()) as { devices: { name: string; thisMac?: boolean; notifications: boolean }[] }).devices;
      const macRow = () => page.getByRole('list', { name: 'Devices' }).getByRole('listitem').filter({ hasText: 'This Mac' });

      const socketClosed = new Promise<void>((done) => page.on('websocket', (ws) => ws.on('close', () => done())));

      // On: the tap asks permission, subscribes with the Mac's key, and the Mac's own browser becomes the This Mac device.
      await page.goto(admiral.url + 'app/preferences/admiral/notifications');
      const toggle = page.getByRole('switch', { name: 'Notifications on this device' });
      await expect(toggle).not.toBeChecked();
      await toggle.tap();
      await expect(toggle).toBeChecked();
      await expect(macRow()).toContainText('push on');
      const seen = await page.evaluate(stubSeen);
      expect(seen.asked).toBe(true);
      expect(seen.key).toBe(((await (await fetch(`${admiral.url}api/v1/push/key`)).json()) as { key: string }).key);
      expect((await devices()).find((d) => d.thisMac)?.notifications).toBe(true);

      // Watched: the window shows the toast and the Mac sends no push.
      await nav(page, 'Inbox').tap();
      await expect(page.getByRole('main')).toContainText('Whatever needs you, from every fleet, will land here.');
      await nav(page, 'Ships').tap();
      await nav(page, 'Inbox').tap();
      const rec = (ship: { session: string }, file: string) => join(admiral.home, 'rec', ship.session, file);
      const permission = 'event PermissionRequest {"hook_event_name":"PermissionRequest","tool_name":"Bash"}';
      const first = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cue: `wait go\n${permission}\n` });
      await expect.poll(() => existsSync(rec(first, 'recorded'))).toBe(true);
      writeFileSync(rec(first, 'go'), '');
      await expect(page.getByRole('status').getByRole('button', { name: new RegExp(first.name) })).toBeVisible({ timeout: 15_000 });
      await holds(() => fake.requests.length === 0, 'a push went out while the window was watching');

      // Unwatched: the page goes away, and the next stop pushes once, encrypted to the subscription, naming the ship.
      await page.goto('about:blank');
      await socketClosed;
      const second = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cue: `wait go\n${permission}\n` });
      await expect.poll(() => existsSync(rec(second, 'recorded'))).toBe(true);
      writeFileSync(rec(second, 'go'), '');
      await expect.poll(() => fake.requests.length, { timeout: 15_000 }).toBe(1);
      const push = fake.requests[0];
      expect(push.method).toBe('POST');
      expect(push.path).toBe('/sub/mac');
      expect(push.headers['content-encoding']).toBe('aes128gcm');
      expect(JSON.parse(decryptPush(sub, push.body))).toMatchObject({ name: second.name, session: second.session });

      // Off: the subscription is dropped on the Mac and the row says so.
      await page.goto(admiral.url + 'app/preferences/admiral/notifications');
      await expect(toggle).toBeChecked();
      await toggle.tap();
      await expect(toggle).not.toBeChecked();
      await expect(macRow()).toContainText('push off');
      expect((await page.evaluate(stubSeen)).subscribed).toBe(false);
      expect((await devices()).find((d) => d.thisMac)?.notifications).toBe(false);
    } finally {
      await fake.close();
    }
  });
});

// The fleets: harbor (Harborlight) and lagoon (Lagoonside) have their own AdmiralBot; alpha (Homeport) has no Claude Code dock.
test("the Chat surface shows the fleet's AdmiralBot history beside the Inbox rail, starts a new chat that keeps the history, answers a question in place, and shows only the shown fleet's chat", { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  chatSays(admiral, 'harbor', [line.user('Where are the ships'), line.assistant('Two are **afloat**.'), line.assistant(HOSTILE), line.assistant(`AdmiralBot status\n\n${TABLE}`)]);
  chatSays(admiral, 'lagoon', [line.user('Is the lagoon calm'), line.assistant('Glass flat.')]);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);
  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  seedInboxItem(admiral, login.session, {
    id: 'q-port',
    kind: 'question',
    title: 'Which port should the login use?',
    question: { questions: [{ id: 'p', text: 'Which port should the login use?', options: [{ label: '80' }, { label: '8080' }] }] },
  });

  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  const main = page.getByRole('main');
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Harborlight fleet' })).toBeVisible();
  // Opening the chat starts it; the history arrives from its transcript, oldest first, AdmiralBot's text as markdown and never as HTML.
  await expect(main).toContainText('Where are the ships');
  await expect(main.locator('strong', { hasText: 'afloat' })).toBeVisible();
  await expect(main).toContainText(HOSTILE);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  await expect(main.locator('table th')).toHaveText(['Ship', 'Status']);
  expect(await pageFits(page)).toBe(true);
  await expect(main).not.toContainText('Is the lagoon calm');
  // A running chat has Open terminal beside New chat, and no not-started note.
  await expect(main.getByRole('button', { name: 'Open terminal', exact: true })).toHaveCount(1);
  await expect(main).not.toContainText('Open the terminal to finish starting the chat');

  // The Inbox rail sits beside the conversation with the ships' items from every fleet.
  await expect(main.getByText('Inbox · All fleets · oldest first')).toBeVisible();
  await expect(main.getByRole('listitem').filter({ hasText: login.name })).toContainText('Which port should the login use?');

  // The composer sends to this fleet's chat only.
  const box = main.getByRole('textbox', { name: 'Message AdmiralBot' });
  await expect(box).toHaveAttribute('placeholder', 'Ask AdmiralBot');
  await box.fill('How many are underway?');
  await main.getByRole('button', { name: 'Send', exact: true }).tap();
  await expect.poll(() => chatReceived(admiral, 'harbor')).toContain('How many are underway?');
  expect(chatReceived(admiral, 'lagoon')).not.toContain('How many are underway?');
  // Claude Code records the sent line in its transcript, and it joins the thread live.
  chatSays(admiral, 'harbor', [line.user('How many are underway?')]);
  await expect(main).toContainText('How many are underway?');

  // A question AdmiralBot asked is a card in the conversation, answered in place.
  chatAsks(admiral, 'harbor', 'q1', 'Which panel?', ['Freeform', 'Work queue']);
  await expect(main).toContainText('Which panel?');
  await main.getByText('Freeform', { exact: true }).tap();
  await main.getByRole('button', { name: 'Send answers' }).tap();
  await expect(main).toContainText('You answered: Freeform');
  await expect.poll(() => chatAnswerFile(admiral, 'harbor', 'q1')?.answers['1']?.choice).toBe('Freeform');

  // A question still open when the chat starts anew is withdrawn: shown, quiet, with no controls.
  chatAsks(admiral, 'harbor', 'q2', 'Which branch?', ['main', 'next']);
  await expect(main).toContainText('Which branch?');

  // New chat adds a marker; the earlier messages stay above it, and what comes after lands below.
  await main.getByRole('button', { name: 'New chat', exact: true }).tap();
  await expect(main.getByText(/^New chat · /)).toHaveCount(1);
  await expect(main).toContainText('Where are the ships');
  await expect(main).toContainText('Withdrawn');
  await expect(main.getByRole('button', { name: 'Send answers' })).toHaveCount(0);
  chatCleared(admiral, 'harbor');
  chatSays(admiral, 'harbor', [line.user('After the reset')], true);
  await expect(main).toContainText('After the reset');
  const order = await main.innerText();
  expect(order.indexOf('Where are the ships')).toBeLessThan(order.search(/New chat · /));
  expect(order.search(/New chat · /)).toBeLessThan(order.indexOf('After the reset'));
  await page.reload();
  await expect(main.getByText(/^New chat · /)).toHaveCount(1);
  await expect(main).toContainText('Where are the ships');

  // Another fleet's chat shows its own messages and none of Harborlight's.
  await switcher(page, 'Harborlight').click();
  await page.getByRole('menu').getByRole('menuitemradio', { name: 'Lagoonside' }).click();
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Lagoonside fleet' })).toBeVisible();
  await expect(main).toContainText('Is the lagoon calm');
  await expect(main).toContainText('Glass flat.');
  await expect(main).not.toContainText('Where are the ships');
  await expect(main).not.toContainText('After the reset');
  await expect(main.getByText(/^New chat · /)).toHaveCount(0);

  // A fleet with no Claude Code dock has its chat off: the reason and a link to its settings, no composer.
  await switcher(page, 'Lagoonside').click();
  await page.getByRole('menu').getByRole('menuitemradio', { name: HOME }).click();
  await expect(main).toContainText(/no Claude Code dock/i);
  await expect(main.getByRole('textbox', { name: 'Message AdmiralBot' })).toHaveCount(0);
  // Leaving before the page's loads land makes WebKit log each one as an error.
  const loads = ['instructions', 'notes', 'commitments'].map((part) => page.waitForResponse((r) => new URL(r.url()).pathname === `/api/v1/chat/alpha/${part}`));
  await main.getByRole('link', { name: /AdmiralBot/ }).tap();
  await expect(page).toHaveURL(/\/app\/preferences\/alpha\/admiralbot/);
  await Promise.all(loads);

  // A rail row opens the item in Peek beside the chat, without leaving it.
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  await main.getByRole('listitem').filter({ hasText: login.name }).getByRole('link').tap();
  await expect(page).toHaveURL(new RegExp(`/app/chat/harbor/item/${encodeURIComponent(login.session)}/q-port`));
  await expect(page.getByRole('region', { name: 'Peek' })).toContainText('Which port should the login use?');
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Harborlight fleet' })).toBeVisible();

  // A ship AdmiralBot launched is a link in its reply, and tapping it opens the ship's screen in this page.
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  const launchText = 'Read the harbour log and report the three newest errors.';
  const launched = await launchAsAdmiralBot(admiral, 'harbor', launchText, 'to look at the harbour log');
  // The launch line says what was sent and why; its launch text opens by touch.
  await expect(main).toContainText('Sent a quick ship to look at the harbour log');
  await expect(main.getByText(launchText, { exact: true })).toHaveCount(0);
  await main.getByRole('button', { name: /launch text/i }).tap();
  await expect(main.getByText(launchText, { exact: true })).toBeVisible();
  chatSays(admiral, 'harbor', [line.assistant(`I launched **${launched.name}** to look at the harbour log.`)], true);
  await expect(main).toContainText(`I launched ${launched.name} to look at the harbour log.`);
  const shipLink = main.locator('.msg.assistant').getByRole('link', { name: launched.name, exact: true });
  await expect(shipLink).toHaveCount(1);
  await expect(shipLink).not.toHaveAttribute('target', /.+/);
  await shipLink.tap();
  await expect(page).toHaveURL(new RegExp(`/app/ships/${encodeURIComponent(launched.session)}`));
  await expect(page.getByRole('heading', { name: launched.name, exact: true })).toBeVisible();
  expect(page.context().pages()).toHaveLength(1);

  expect(watch.errors).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
});

test("a ship that sails off gets AdmiralBot's reply to its report turn threaded under its ship card, the turn behind a closed disclosure", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  chatSays(admiral, 'harbor', [line.user('Send a ship to read the harbour log')]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  const main = page.getByRole('main');
  await expect(main).toContainText('Send a ship to read the harbour log');
  chatPrompts(admiral, 'harbor');
  const sailing = await launchAsAdmiralBot(admiral, 'harbor', 'Read the harbour log and report the newest error.', 'to look at the harbour log', true);
  await expect(main.getByText('Sailed off', { exact: true })).toHaveCount(1, { timeout: 30_000 });
  await expect.poll(() => new RegExp(`\\[Report: ${sailing.name}\\][^\\r]*\\r`).test(chatReceived(admiral, 'harbor')), { timeout: 30_000 }).toBe(true);
  const turn = chatReceived(admiral, 'harbor').split('\r').find((x) => x.includes('[Report: ')) ?? '';
  chatSays(admiral, 'harbor', [line.user(turn.trim()), line.assistant('The newest error is a **tide gauge** timeout.')]);
  const thread = main.locator('[data-thread]');
  await expect(thread).toHaveCount(1);
  await expect(thread.getByText('Sailed off', { exact: true })).toBeVisible();
  await expect(thread.locator('.msg.assistant')).toContainText('The newest error is a tide gauge timeout.');
  const sent = thread.getByRole('button', { name: 'Report sent to AdmiralBot' });
  await expect(sent).toHaveAttribute('aria-expanded', 'false');
  await expect(thread.locator('.msg.user')).toHaveCount(0);
  await expect(thread).not.toContainText('A ship you launched has reported back');
  await sent.tap();
  await expect(thread).toContainText('A ship you launched has reported back');
  expect(await pageFits(page)).toBe(true);

  expect(watch.errors).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
});

test("scouts launched together report in one turn naming each, while a quick ship from the same prompt reports in its own", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true, turnChat: 'harbor' });
  chatSays(admiral, 'harbor', [line.user('Look into the harbour log with two scouts and a quick ship')]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  const main = page.getByRole('main');
  await expect(main).toContainText('Look into the harbour log with two scouts and a quick ship');
  const checked = await page.request.post(admiral.url + 'api/v1/harness-capabilities/check', { data: { fleet: 'harbor', harness: 'claude' } });
  expect(checked.ok()).toBe(true);
  chatPrompts(admiral, 'harbor');
  const first = await launchAsAdmiralBot(admiral, 'harbor', 'Read the harbour log and report the newest error.', 'to read the harbour log', true, true);
  const second = await launchAsAdmiralBot(admiral, 'harbor', 'Read the tide table and report the newest error.', 'to read the tide table', true, true);
  const quick = await launchAsAdmiralBot(admiral, 'harbor', 'Fix the harbour log typo.', 'to fix the typo', true);
  // Each scout and the quick ship get a card as they end; none of their report turns reaches the chat while the engineer's turn is open.
  await expect(main.getByText(/^(Sailed off|Failed)$/)).toHaveCount(3, { timeout: 30_000 });
  expect(chatReceived(admiral, 'harbor')).not.toContain('[Report: ');
  chatTurnEnds(admiral, 'harbor');
  const turns = () => chatReceived(admiral, 'harbor').split('\r').filter((x) => x.includes('[Report: '));
  await expect.poll(() => turns().length).toBe(1);
  chatTurnEnds(admiral, 'harbor', 'report-start');
  chatTurnEnds(admiral, 'harbor', 'report-end');
  await expect.poll(() => turns().length).toBe(2);
  const group = turns().filter((x) => x.includes(`ship: ${first.session}`));
  expect(group).toHaveLength(1);
  expect(group[0]).toContain(`ship: ${second.session}`);
  expect(group[0]).not.toContain(`ship: ${quick.session}`);
  const own = turns().filter((x) => x.includes(`ship: ${quick.session}`));
  expect(own).toHaveLength(1);
  expect(own[0]).not.toContain(`ship: ${first.session}`);

  expect(watch.errors).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
});

test("AdmiralBot's own waiting question is in the chat's rail, opens in Peek beside the chat, and answering there marks the card answered", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  await admiral.start({ env: withFakeGh(admiral) });
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  const main = page.getByRole('main');
  // Opening the chat starts it, and a start withdraws the questions waiting: ask once the start is over.
  await expect
    .poll(async () => ((await (await page.request.get(admiral.url + 'api/v1/chat/harbor/session')).json()) as { running?: boolean }).running)
    .toBe(true);
  chatAsks(admiral, 'harbor', 'q1', 'Which panel?', ['Freeform', 'Work queue']);
  const rail = main.getByRole('listitem').filter({ hasText: 'AdmiralBot' }).filter({ hasText: 'Which panel?' });
  await expect(rail).toHaveCount(1, { timeout: 15_000 });
  await rail.getByRole('link').tap();
  await expect(page).toHaveURL(new RegExp(`/app/chat/harbor/item/${encodeURIComponent('_chat-harbor')}/q1`));
  const peek = page.getByRole('region', { name: 'Peek' });
  await peek.getByRole('radio', { name: 'Work queue' }).tap();
  await peek.getByRole('button', { name: 'Send answers' }).tap();
  await expect.poll(() => chatAnswerFile(admiral, 'harbor', 'q1')?.answers['1']?.choice).toBe('Work queue');
  await expect(peek).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Harborlight fleet' })).toBeVisible();
  await expect(main).toContainText('You answered: Work queue');
  await expect(rail).toHaveCount(0);
});

const NOT_STARTED = 'Open the terminal to finish starting the chat';

test("a chat held before its first SessionStart shows the not-started note with Open terminal, answers the dialog in the terminal view that survives a reload, and sends once it starts", async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true, holdChat: 'lagoon' });
  chatSays(admiral, 'harbor', [line.user('Where are the ships'), line.assistant('Two are **afloat**.')]);
  await admiral.start();
  const main = page.getByRole('main');
  const open = main.getByRole('button', { name: 'Open terminal', exact: true }).first();
  const rail = main.getByText('Inbox · All fleets · oldest first');
  // A terminal path for another fleet shows the shown fleet's conversation and says so in the address.
  await page.goto(admiral.url + 'app/chat/harbor/terminal?fleet=lagoon');
  await expect(page).toHaveURL(/\/app\/chat\?fleet=lagoon$/);
  await expect(main).toContainText(NOT_STARTED);
  await expect(main.getByRole('button', { name: 'Send', exact: true })).toBeDisabled();

  // The button beside the note opens it as the header's does.
  const joined = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await main.locator('.msg-note').getByRole('button', { name: 'Open terminal', exact: true }).tap();
  const bridge = await joined;
  expect(new URL(bridge.url()).pathname).toBe(`/ws/v1/bridge/${encodeURIComponent(chatSession('lagoon'))}`);
  await expect(page).toHaveURL(/\/app\/chat\/lagoon\/terminal/);
  await expect(main.locator('.xterm')).toBeVisible();
  await expect(main.getByRole('button', { name: 'Back to chat', exact: true })).toBeVisible();
  await expect(main.getByRole('textbox', { name: 'Message AdmiralBot' })).toHaveCount(0);
  await expect(rail).toBeVisible();

  // What is typed there, and the touch keys, reach the held session: the fake records its stdin.
  const recorded = join(admiral.home, 'rec', chatSession('lagoon'));
  await expect.poll(() => existsSync(join(recorded, 'recording'))).toBe(true);
  await main.locator('.xterm').tap();
  await page.keyboard.type('trust-this-folder');
  await expect.poll(() => chatReceived(admiral, 'lagoon')).toContain('trust-this-folder');
  await main.getByRole('button', { name: 'Send Enter' }).tap();
  await expect.poll(() => chatReceived(admiral, 'lagoon')).toContain('\r');

  // Reload on the terminal path reopens the terminal; Back to chat returns to the conversation.
  await page.reload();
  await expect(main.locator('.xterm')).toBeVisible();
  await main.getByRole('button', { name: 'Back to chat', exact: true }).tap();
  await expect(page).not.toHaveURL(/\/terminal/);
  await expect(main.locator('.xterm')).toHaveCount(0);
  await expect(main).toContainText(NOT_STARTED);

  // Another fleet's chat shows its conversation, not the terminal.
  await open.tap();
  await expect(main.locator('.xterm')).toBeVisible();
  await switcher(page, 'Lagoonside').click();
  await page.getByRole('menu').getByRole('menuitemradio', { name: 'Harborlight' }).click();
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Harborlight fleet' })).toBeVisible();
  await expect(main).toContainText('Where are the ships');
  await expect(main.locator('.xterm')).toHaveCount(0);
  await expect(page).not.toHaveURL(/\/terminal/);

  // The first SessionStart clears the note by itself, and the chat sends.
  await switcher(page, 'Harborlight').click();
  await page.getByRole('menu').getByRole('menuitemradio', { name: 'Lagoonside' }).click();
  await expect(main).toContainText(NOT_STARTED);
  chatStarts(admiral, 'lagoon');
  await expect(main).not.toContainText(NOT_STARTED);
  const box = main.getByRole('textbox', { name: 'Message AdmiralBot' });
  await box.fill('Now that it started');
  await main.getByRole('button', { name: 'Send', exact: true }).tap();
  await expect.poll(() => chatReceived(admiral, 'lagoon')).toContain('Now that it started');
});

test('a chat terminal whose session ends returns to the conversation', async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true });
  await admiral.start();
  const main = page.getByRole('main');
  await page.goto(admiral.url + 'app/chat?fleet=lagoon');
  await main.getByRole('button', { name: 'Open terminal', exact: true }).first().tap();
  await expect(main.locator('.xterm')).toBeVisible();
  chatEnds(admiral, 'lagoon');
  await expect(page).not.toHaveURL(/\/terminal/);
  await expect(main.locator('.xterm')).toHaveCount(0);
  await expect(main.getByRole('textbox', { name: 'Message AdmiralBot' })).toBeVisible();
});

test('the side nav shows an update badge when the update check finds a newer release, and its dialog gives the upgrade commands and the release page', async ({ page, admiral }) => {
  const asked: string[] = [];
  const releases: Server = createServer((req, res) => {
    asked.push(`${req.method} ${req.url}`);
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ tag_name: 'v99.0.0', html_url: 'https://github.com/jbuffin/admiral/releases/tag/v99.0.0' }));
  });
  await new Promise<void>((done) => releases.listen(0, '127.0.0.1', done));
  try {
    seedFleets(admiral, { live: true });
    await admiral.start({
      env: {
        ...withFakeGh(admiral),
        ADMIRAL_UPDATE_TEST_URL: `http://127.0.0.1:${(releases.address() as AddressInfo).port}`,
        ADMIRAL_UPDATE_TEST_VERSION: '0.9.1',
        ADMIRAL_UPDATE_FIRST_CHECK: '200ms',
      },
    });
    await page.goto(admiral.url + 'app/');
    const nav = page.getByRole('navigation', { name: 'Main' });
    await expect(nav.getByText('dev', { exact: true })).toBeVisible();
    await nav.getByRole('button', { name: /update available/i }).tap();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('99.0.0');
    await expect(dialog).toContainText('launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral');
    await expect(dialog.getByRole('link', { name: /release/i })).toHaveAttribute('href', 'https://github.com/jbuffin/admiral/releases/tag/v99.0.0');
    expect(asked[0]).toBe('GET /repos/jbuffin/admiral/releases/latest');
  } finally {
    await new Promise((done) => releases.close(done));
  }
});
