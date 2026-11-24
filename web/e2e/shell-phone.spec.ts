import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { seedOrders } from './orders';
import { join } from 'node:path';
import { AGENT_CUES, HOME, WORK, boatReceived, chip, expectSeededTurn, interruptButton, launchCrew, LAUNCH_MODELS, launchFromTheShell, launchShip, line, messageBox, outcomeEvents, paneSize, received, rowLink, say, seedBoatTranscript, seedFleets, seedTurn, sendButton, sentBubble, setState, shipViewUrl, stopDialog, stopShipButton, viewPanel, viewTab, watchPage, withFakeGh, chatReceived, chatSays, chatSession, chatStarts, expectReachable, openPreferences, preferencesBack, touchTap } from './shell';

const tab = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });

test('on the phone, the tab bar and More reach every surface, the Ships tab lists the fleet\'s ships and the Fleets sheet switches fleets', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: { ...withFakeGh(admiral), ADMIRAL_VSCODE_TEST: 'missing' } });
  const watch = watchPage(page, admiral.url);

  // Ships that end are launched first: their cue file is read by any harness that starts while it is there.
  const moored = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', exit: 3 });
  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  setState(admiral, login, 'needs_you');
  await say(admiral, login, 'Which branch should I rebase onto?');
  const billing = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-billing' });
  setState(admiral, billing, 'working');
  // A tail near the 200-character cap, to guard the no-scroll check below.
  await say(admiral, billing, 'Reading billing/export.go '.repeat(7).trim());

  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Inbox', 'Chat', 'Ships', 'Orders']);
  // The needs-you stop fits the screen and is tall enough to tap.
  const stop = page.getByRole('main').getByRole('listitem').filter({ hasText: login.name });
  await expect(stop).toContainText('Fix the login redirect');
  expect((await stop.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  await tab(page, 'Ships').tap();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toContainText(WORK);

  // The same groups and rows as the wide shell, inside the screen's width.
  const main = page.getByRole('main');
  await expect(main.locator('.ship-group').getByRole('heading', { level: 2 })).toHaveText([/^Needs you\s*1$/, /^Underway\s*1$/, /^Anchored\s*1$/]);
  const row = (ship: { name: string }) => main.getByRole('listitem').filter({ hasText: ship.name });
  await expect(row(login)).toContainText('Fix the login redirect');
  await expect(row(login)).toContainText('Which branch should I rebase onto?');
  await expect(row(login).locator('img[src="/api/v1/logos/claude"]')).toBeVisible();
  await expect(row(moored).locator('img[src="/api/v1/logos/codex"]')).toBeVisible();
  for (const ship of [login, billing, moored]) expect((await row(ship).boundingBox())!.height).toBeGreaterThanOrEqual(44);
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  // Reading a ship on the phone never resizes the Mac's pane: only the Terminal tab joins the bridge.
  await seedTurn(admiral, login);
  const bridges = () => watch.paths.filter((p) => /^\/ws\/(v1\/)?bridge\//.test(p));
  const size = paneSize(admiral, login);
  expect(size).toMatch(/^\d+x\d+$/);
  await rowLink(page, login).tap();
  await expect(page).toHaveURL(shipViewUrl(admiral, login));
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  // The phone header orders its children by hand: the note sits below the ship's name, not above Back.
  const note = page.getByText("VS Code isn't installed on this Mac.");
  await expect(note).toBeVisible();
  const [nameBox, noteBox] = [await page.locator('.ship-view-head').getByRole('heading').first().boundingBox(), await note.boundingBox()];
  expect(noteBox!.y).toBeGreaterThan(nameBox!.y + nameBox!.height - 1);
  await expectSeededTurn(page);
  await viewTab(page, 'PR').tap();
  await expect(viewPanel(page)).toContainText('No pull request yet. A PR shows here as soon as the ship or its crew opens one.');
  await viewTab(page, 'Conversation').tap();
  await expect(viewPanel(page)).toContainText('Rebase onto main please');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  expect(bridges(), 'no bridge socket opens').toEqual([]);
  expect(paneSize(admiral, login), 'the pane keeps its size').toBe(size);

  const joined = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await viewTab(page, 'Terminal').tap();
  const bridge = await joined;
  expect(new URL(bridge.url()).pathname).toBe(`/ws/v1/bridge/${encodeURIComponent(login.session)}`);
  await expect.poll(() => paneSize(admiral, login)).not.toBe(size);
  await viewTab(page, 'Conversation').tap();
  await expect.poll(() => bridge.isClosed()).toBe(true);
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  expect(watch.unversioned()).toEqual([]);

  await page.getByRole('banner').getByRole('button', { name: WORK, exact: true }).tap();
  const sheet = page.getByRole('dialog', { name: 'Fleets' });
  await expect(sheet.getByRole('menuitemradio', { name: WORK })).toHaveAttribute('aria-checked', 'true');
  await sheet.getByRole('menuitemradio', { name: HOME }).tap();
  await expect(sheet).toBeHidden();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('main')).toContainText(HOME);
  await expect(page.getByRole('main')).not.toContainText(WORK);
  // Homeport has no ship afloat: the empty state, not the other fleet's rows.
  await expect(page.getByRole('main')).toContainText('The ships sailing from this fleet will appear here.');
  await expect(page.getByRole('main').getByRole('listitem')).toHaveCount(0);

  await page.getByRole('button', { name: 'More', exact: true }).tap();
  const more = page.getByRole('dialog', { name: 'More' });
  await expect(more.getByRole('link')).toHaveText(['Campaigns', 'Routines', 'Crew', 'Preferences']);
  await more.getByRole('link', { name: 'Crew', exact: true }).tap();
  await expect(more).toBeHidden();
  await expect(page).toHaveURL(/\/app\/crew\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('heading', { name: 'Crew', exact: true })).toBeVisible();

  // Preferences opens as a list of its pages, not a page.
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Preferences', exact: true }).tap();
  await expect(page).toHaveURL(/\/app\/preferences\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('navigation', { name: 'Preferences' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Plugin sources', exact: true })).toBeHidden();

  // Each Preferences page opens fully on screen with a Back to the list, and its controls take a finger.
  const heading = (name: string) => page.getByRole('heading', { name, exact: true });
  await openPreferences(page, { section: 'Remote access' });
  await expect(page).toHaveURL(/\/app\/preferences\/admiral\/remote/);
  await expect(heading('Remote access')).toBeInViewport({ ratio: 1 });
  await expect(page.getByRole('group', { name: 'Tailscale' }).getByRole('button').first()).toBeInViewport({ ratio: 1 });
  // New code sits below Tailscale and the allowed logins: scrolled in, it must be uncovered and take a finger.
  const newCode = page.getByRole('button', { name: 'New code', exact: true });
  await newCode.scrollIntoViewIfNeeded();
  await expectReachable(page, newCode);
  const code = page.locator('.pair-code');
  const first = await code.textContent();
  expect(first).toBeTruthy();
  await expect(newCode).not.toHaveAttribute('aria-busy', 'true');
  await touchTap(page, newCode);
  await expect.poll(() => code.textContent()).not.toBe(first);
  const back = preferencesBack(page);
  const backBox = (await back.boundingBox())!;
  expect(backBox.height).toBeGreaterThanOrEqual(44);
  expect(backBox.width).toBeGreaterThanOrEqual(44);
  await back.tap();
  await expect(page.getByRole('navigation', { name: 'Preferences' })).toBeInViewport();
  await expect(heading('Remote access')).toBeHidden();
  await openPreferences(page, { fleet: HOME, section: 'Fleet' });
  await expect(page).toHaveURL(/\/app\/preferences\/alpha\/fleet/);
  await expect(heading('Fleet')).toBeInViewport({ ratio: 1 });
  await expect(page.getByRole('textbox', { name: 'Search root 1' })).toBeInViewport({ ratio: 1 });

  expect(watch.paths).toContain('/ws/v1');
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});


test('on the phone, the launch form fits, a ship takes a message by button, its crew boats take the box, and Stop ship asks first', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, models: LAUNCH_MODELS });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  // The launch form is usable at the phone's width.
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await expect(page.getByRole('main')).toContainText(WORK);
  await launchFromTheShell(page, admiral, (l) => l.tap(), { phone: true });

  // On the phone the box sends with its button, Interrupt and Stop ship stay reachable, and Stop ship asks first.
  const chat = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', ...AGENT_CUES, 'reply Aye, rebasing now'] });
  await expect.poll(() => existsSync(join(admiral.home, 'rec', chat.session, 'cues-done'))).toBe(true);
  setState(admiral, chat, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, chat).tap();
  await expect(messageBox(page)).toBeEnabled();
  // Every view tab and the Agents count are on screen at first sight, with no swipe.
  const agentsTab = viewTab(page, 'Agents');
  await expect(agentsTab.locator('.tab-count')).toBeVisible();
  for (const target of [agentsTab, agentsTab.locator('.tab-count')]) {
    const box = (await target.boundingBox())!;
    expect(box.x + box.width, 'the Agents tab ends on screen').toBeLessThanOrEqual(page.viewportSize()!.width);
  }
  await messageBox(page).fill('Please rebase onto main');
  await sendButton(page).tap();
  await expect(viewPanel(page).getByText('Please rebase onto main', { exact: true })).toHaveCount(1);
  await expect(sentBubble(page, 'Please rebase onto main')).toHaveCount(0);
  await expect(viewPanel(page)).toContainText('Aye, rebasing now', { timeout: 15_000 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  for (const name of ['Return to port', 'Hand to Commodore', 'Stop ship']) {
    const box = (await page.getByRole('button', { name, exact: true }).boundingBox())!;
    expect(box.x, `${name} starts on screen`).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width, `${name} ends on screen`).toBeLessThanOrEqual(page.viewportSize()!.width);
    expect(box.width, `${name} is wide enough to tap`).toBeGreaterThanOrEqual(44);
  }

  // The Agents tab fits the phone and shows the subagents and background shells.
  await viewTab(page, 'Agents').tap();
  await expect(viewPanel(page).getByRole('listitem').filter({ hasText: 'Plan' })).toContainText(/running/i);
  await expect(viewPanel(page).getByRole('listitem').filter({ hasText: 'bdone001' })).toContainText(/finished/i);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await viewTab(page, 'Conversation').tap();

  // The flotilla fits the phone, and a picked boat takes the conversation and the box.
  // A boat without cues of its own plays the cue file left by the ship, so put back the one it needs.
  writeFileSync(join(admiral.home, 'cue'), 'bracketedpaste\nreply Aye, rebasing now\n');
  await launchCrew(admiral, chat, 'rev', 'reviewer');
  await seedBoatTranscript(admiral, chat, 'rev', [line.user('Review the parser'), line.assistant('Boat found two parser bugs')]);
  await expect(chip(page, /reviewer/)).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  expect((await chip(page, /reviewer/).boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await chip(page, /reviewer/).tap();
  await expect(chip(page, /reviewer/)).toBeChecked();
  await expect(viewPanel(page)).toContainText('Boat found two parser bugs');
  await expect(viewPanel(page)).not.toContainText('Aye, rebasing now');
  await messageBox(page).fill('Check the lexer too');
  await sendButton(page).tap();
  await expect.poll(() => boatReceived(admiral, chat, 'rev')).toContain('Check the lexer too');
  expect(received(admiral, chat)).not.toContain('Check the lexer too');
  await chip(page, new RegExp(chat.name)).tap();
  await expect(viewPanel(page)).toContainText('Aye, rebasing now');
  await interruptButton(page).tap();
  await expect.poll(() => received(admiral, chat)).toContain('\x1b');
  await stopShipButton(page).tap();
  await stopDialog(page).getByRole('button', { name: 'Stop ship' }).tap();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  await expect(page.getByRole('main').getByRole('listitem').filter({ hasText: chat.name })).toHaveCount(0);
  await expect.poll(() => outcomeEvents(admiral, chat)).toEqual(['launched', 'ended', 'stopped']);

  expect(watch.paths).toContain('/ws/v1');
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test("on the phone, the Chat tab shows the fleet's AdmiralBot conversation and its composer above the tab bar without the Inbox rail", async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true });
  chatSays(admiral, 'harbor', [line.user('Where are the ships'), line.assistant('Two are **afloat**.')]);
  await admiral.start();
  const watch = watchPage(page, admiral.url);
  await page.goto(admiral.url + 'app/inbox?fleet=harbor');
  await tab(page, 'Chat').tap();
  const main = page.getByRole('main');
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Harborlight fleet' })).toBeVisible();
  // The title keeps the fleet's name, with the header's two buttons beside it.
  expect(await page.locator('.chat-title-text').evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await expect(main.getByRole('button', { name: 'Open terminal', exact: true }).first()).toBeVisible();
  await expect(main.getByRole('button', { name: 'New chat', exact: true })).toBeVisible();
  await expect(main).toContainText('Where are the ships');
  await expect(main.locator('strong', { hasText: 'afloat' })).toBeVisible();
  await expect(main.getByText('Inbox · All fleets · oldest first')).toHaveCount(0);

  const box = main.getByRole('textbox', { name: 'Message AdmiralBot' });
  const field = (await box.boundingBox())!;
  const tabBar = (await page.getByRole('navigation', { name: 'Main' }).boundingBox())!;
  expect(tabBar.y - (field.y + field.height), 'the box sits on the tab bar').toBeLessThanOrEqual(10);
  await box.fill('How many are underway?');
  const send = main.getByRole('button', { name: 'Send', exact: true });
  expect((await send.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await send.tap();
  await expect.poll(() => chatReceived(admiral, 'harbor')).toContain('How many are underway?');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  // The Inbox keeps its own tab.
  await tab(page, 'Inbox').tap();
  await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();
  expect(watch.errors).toEqual([]);
});

test("on the phone, a chat held before its first SessionStart opens its terminal over the Chat tab, takes touch keys, survives a reload, and sends after Back to chat once it starts", async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true, holdChat: 'lagoon' });
  await admiral.start();
  const main = page.getByRole('main');
  await page.goto(admiral.url + 'app/chat?fleet=lagoon');
  await expect(main).toContainText('Open the terminal to finish starting the chat');
  await main.getByRole('button', { name: 'Open terminal', exact: true }).first().tap();
  await expect(page).toHaveURL(/\/app\/chat\/lagoon\/terminal/);
  await expect(main.locator('.xterm')).toBeVisible();
  await expect(main.getByText('Inbox · All fleets · oldest first')).toHaveCount(0);
  const back = main.getByRole('button', { name: 'Back to chat', exact: true });
  expect((await back.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await expect.poll(() => existsSync(join(admiral.home, 'rec', chatSession('lagoon'), 'recording'))).toBe(true);
  await main.getByRole('button', { name: 'Send Enter' }).tap();
  await expect.poll(() => chatReceived(admiral, 'lagoon')).toContain('\r');

  await page.reload();
  await expect(main.locator('.xterm')).toBeVisible();
  await back.tap();
  await expect(page).not.toHaveURL(/\/terminal/);
  await expect(main.locator('.xterm')).toHaveCount(0);
  await expect(main).toContainText('Open the terminal to finish starting the chat');

  chatStarts(admiral, 'lagoon');
  await expect(main).not.toContainText('Open the terminal to finish starting the chat');
  await main.getByRole('textbox', { name: 'Message AdmiralBot' }).fill('Now that it started');
  await main.getByRole('button', { name: 'Send', exact: true }).tap();
  await expect.poll(() => chatReceived(admiral, 'lagoon')).toContain('Now that it started');
});

test('on the phone, the New view card fits the screen, its controls are tall enough to tap, and Add view saves', async ({ page, admiral }) => {
  seedOrders(admiral);
  const gh = admiral.fakeGh();
  gh.count('z-open', 'is:open', 3);
  await admiral.start({ env: withFakeGh(admiral) });
  const stored = () => (JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8')).sources.gh.settings.views ?? []) as { title?: string; query?: string }[];
  await expect.poll(() => stored().length).toBe(6);

  await page.goto(admiral.url + 'app/');
  await openPreferences(page, { fleet: WORK, section: 'GitHub' });
  await expect(page.getByRole('region', { name: 'Connection', exact: true })).toContainText('Connected');
  const open = page.getByRole('button', { name: '+ New view', exact: true });
  await open.scrollIntoViewIfNeeded();
  await expectReachable(page, open);
  await touchTap(page, open);

  const card = page.getByRole('region', { name: 'New view', exact: true });
  await expect(card).toBeVisible();
  const viewport = page.viewportSize()!;
  for (const control of await card.getByRole('button').all()) {
    const box = (await control.boundingBox())!;
    expect(box.height).toBeGreaterThanOrEqual(44);
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(viewport.width);
  }
  await expect(card.getByRole('group', { name: 'Search', exact: true })).toContainText('is:open is:pr review-requested:@me draft:false -review:approved');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  await touchTap(page, card.getByRole('button', { name: 'Assigned issues', exact: true }));
  const add = card.getByRole('button', { name: 'Add view', exact: true });
  await add.scrollIntoViewIfNeeded();
  await expectReachable(page, add);
  await touchTap(page, add);
  await expect.poll(() => stored().at(-1)).toEqual({ title: 'Assigned issues', query: 'is:open is:issue assignee:@me' });
});
