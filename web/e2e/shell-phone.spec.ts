import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { HOME, HOSTILE, TS_LOGIN, TS_NAME, WORK, fakeTailscale, setRemote, stubClipboard, boatReceived, changeFile, changesOf, chip, diffOf, expectSeededTurn, gitCheckout, holds, interruptButton, launchCrew, launchFromTheShell, launchShip, line, messageBox, outcomeEvents, paneSize, received, rowLink, say, seedBoatTranscript, seedFleets, seedInboxItem, seedTurn, sendButton, sentBubble, setReason, setState, shipViewUrl, stopDialog, stopShipButton, viewPanel, viewTab, watchPage, withFakeGh, chatReceived, chatSays, chatSession, chatStarts, expectReachable, openPreferences, preferencesBack, touchTap } from './shell';

const tab = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });

test('on the phone, the tab bar and More reach every surface, the Ships tab lists the fleet\'s ships and the Fleets sheet switches fleets', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  setState(admiral, login, 'needs_you');
  await say(admiral, login, 'Which branch should I rebase onto?');
  const billing = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-billing' });
  setState(admiral, billing, 'working');
  // A tail near the 200-character cap, to guard the no-scroll check below.
  await say(admiral, billing, 'Reading billing/export.go '.repeat(7).trim());
  const moored = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', exit: 3 });

  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Inbox', 'Chat', 'Ships']);

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
  await expectSeededTurn(page);
  await viewTab(page, 'Changes').tap();
  await expect(viewTab(page, 'Changes')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page)).toContainText('No git history to compare');
  await viewTab(page, 'PR').tap();
  await expect(viewPanel(page)).toContainText('No pull request yet. A PR shows here as soon as the ship or its crew opens one.');
  await viewTab(page, 'Conversation').tap();
  await expect(viewPanel(page)).toContainText('Rebase onto main please');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await holds(() => bridges().length === 0 && paneSize(admiral, login) === size, 'no bridge socket opens and the pane keeps its size');

  const joined = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await viewTab(page, 'Terminal').tap();
  const bridge = await joined;
  expect(new URL(bridge.url()).pathname).toBe(`/ws/v1/bridge/${encodeURIComponent(login.session)}`);
  await expect.poll(() => paneSize(admiral, login)).not.toBe(size);
  await viewTab(page, 'Conversation').tap();
  await expect.poll(() => bridge.isClosed()).toBe(true);
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();

  // Changes in a checkout: the files and their diffs fit the screen, and a long line scrolls inside its diff, not the page. Reading them opens no bridge.
  const bridgesBefore = bridges().length;
  const dev = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', dir: gitCheckout(admiral, 'repo') });
  await expect.poll(async () => (await changesOf(admiral, dev)).files.map((f) => `${f.path} ${f.status}`).sort()).toEqual(['a.txt modified', 'b.txt added', 'c.txt untracked']);
  await rowLink(page, dev).tap();
  await viewTab(page, 'Changes').tap();
  await expect(changeFile(page, 'a.txt')).toContainText('modified');
  await expect(changeFile(page, 'b.txt')).toContainText('added');
  await expect(changeFile(page, 'c.txt')).toContainText('untracked');
  await expect(diffOf(page, 'a.txt')).toContainText('TWO');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  expect(await diffOf(page, 'a.txt').evaluate((el) => [el, ...el.querySelectorAll('*')].some((e) => e.scrollWidth > e.clientWidth))).toBe(true);
  await holds(() => bridges().length === bridgesBefore, 'reading Changes opens no bridge socket');
  expect(watch.unversioned()).toEqual([]);
  await page.goBack();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();

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
  await expect(page.getByRole('heading', { name: 'Order sources', exact: true })).toBeHidden();

  // Back in Westwind, the launch form is usable at the phone's width.
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await expect(page.getByRole('main')).toContainText(WORK);
  await launchFromTheShell(page, admiral, (l) => l.tap(), { phone: true });

  // On the phone the box sends with its button, Interrupt and Stop ship stay reachable, and Stop ship asks first.
  const chat = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'reply Aye, rebasing now'] });
  setState(admiral, chat, 'working');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, chat).tap();
  await expect(messageBox(page)).toBeEnabled();
  await messageBox(page).fill('Please rebase onto main');
  await sendButton(page).tap();
  await expect(viewPanel(page).getByText('Please rebase onto main', { exact: true })).toHaveCount(1);
  await expect(sentBubble(page, 'Please rebase onto main')).toHaveCount(0);
  await expect(viewPanel(page)).toContainText('Aye, rebasing now', { timeout: 15_000 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  // The flotilla fits the phone, and a picked boat takes the conversation and the box.
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

test('on the phone, Preferences lists its pages, each page opens fully on screen with a Back to the list, and its controls can be tapped', async ({ page, admiral }) => {
  // Four fleets put the Harborlight group well down the list, where a page used to be squeezed out.
  seedFleets(admiral, { chat: true });
  await admiral.start();
  await page.goto(admiral.url + 'app/inbox');
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
  await touchTap(page, newCode);
  await expect.poll(() => code.textContent()).not.toBe(first);

  // Back returns to the list from a page.
  const back = preferencesBack(page);
  const size = (await back.boundingBox())!;
  expect(size.height).toBeGreaterThanOrEqual(44);
  expect(size.width).toBeGreaterThanOrEqual(44);
  await back.tap();
  await expect(page.getByRole('navigation', { name: 'Preferences' })).toBeInViewport();
  await expect(heading('Remote access')).toBeHidden();

  await openPreferences(page, { fleet: 'Harborlight', section: 'Fleet' });
  await expect(page).toHaveURL(/\/app\/preferences\/harbor\/fleet/);
  await expect(heading('Fleet')).toBeInViewport({ ratio: 1 });
  await expect(page.getByRole('textbox', { name: 'Search root 1' })).toBeInViewport({ ratio: 1 });

  // Harborlight has no order source, so its page offers Add source.
  await openPreferences(page, { fleet: 'Harborlight', section: 'Order sources' });
  await expect(page).toHaveURL(/\/app\/preferences\/harbor\/sources/);
  await expect(heading('Order sources')).toBeInViewport({ ratio: 1 });
  const add = page.getByRole('button', { name: 'Add source', exact: true });
  await expectReachable(page, add);
  await touchTap(page, add);
  await expect(page.getByRole('group', { name: 'Add source' })).toBeVisible();
});

test('on the phone, the Inbox lists a question item and a needs-you stop from two fleets, each with its fleet, and the stop leaves live once its ship is back underway', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('main')).toContainText('Whatever needs you, from every fleet, will land here.');

  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  setReason(admiral, login, 'permission:Bash');
  setState(admiral, login, 'needs_you');
  seedInboxItem(admiral, visitor.session, {
    id: 'q-port',
    kind: 'question',
    title: 'Which port should the visitor use?',
    question: { questions: [{ id: 'p', text: 'Which port should the visitor use?', options: [{ label: '80' }, { label: '8080' }] }] },
  });
  seedInboxItem(admiral, '_admiral', { id: 'n-hostile', kind: 'notice', title: 'Heads up', notice: { body: HOSTILE } });

  // Both rows arrive without a reload; the empty line is gone.
  const main = page.getByRole('main');
  const entry = (text: string) => main.getByRole('listitem').filter({ hasText: text });
  await expect(entry(visitor.name)).toContainText(HOME, { timeout: 15_000 });
  await expect(entry(visitor.name)).toContainText('asked');
  await expect(entry(visitor.name)).toContainText('Which port should the visitor use?');
  await expect(entry(login.name)).toContainText(WORK);
  await expect(entry(login.name)).toContainText(/wants to run bash/i);
  await expect(entry(login.name)).toContainText(/^.*\b(now|\d+m)\b/);
  await expect(main).not.toContainText('Whatever needs you, from every fleet, will land here.');
  await expect(entry('Heads up')).toBeVisible();
  await expect(entry('Heads up')).not.toContainText(HOME);
  await expect(entry('Heads up')).not.toContainText(WORK);
  for (const n of [login.name, visitor.name, 'Heads up']) expect((await entry(n).boundingBox())!.height).toBeGreaterThanOrEqual(44);
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  // A notice shows its body as text, and nothing in it runs.
  await entry('Heads up').getByRole('button').tap();
  await expect(main).toContainText(HOSTILE);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();

  // The stop folds to its confirmation when its ship goes back underway, then leaves; the item stays.
  setState(admiral, login, 'working');
  await expect(entry(login.name)).toContainText('Back underway', { timeout: 15_000 });
  await expect(entry(login.name)).toHaveCount(0, { timeout: 15_000 });
  await expect(entry(visitor.name)).toHaveCount(1);

  // A ship-backed row opens Peek on its item.
  await entry(visitor.name).getByRole('link').tap();
  await expect(page).toHaveURL(new RegExp(`/app/inbox/${encodeURIComponent(visitor.session)}/item/q-port`));
  await expect(page.getByRole('region', { name: 'Peek' })).toBeVisible();
});

test('on the phone, Remote access shows the Tailscale checks and copies the tailnet URL', async ({ page, admiral }) => {
  const ts = fakeTailscale(admiral);
  setRemote(admiral, { tailscale: true, allowedLogins: [TS_LOGIN] });
  await admiral.start({ env: ts.env });
  ts.serve(new URL(admiral.url).port);
  const copied = await stubClipboard(page);
  await page.goto(admiral.url + 'app/preferences/admiral/remote');
  const section = page.getByRole('group', { name: 'Tailscale' });
  await expect(section.getByRole('listitem')).toHaveCount(4);
  await section.getByRole('button', { name: 'Copy' }).tap();
  await expect(section.getByRole('status').filter({ hasText: /copied/i })).toBeVisible();
  expect(await copied()).toBe(`https://${TS_NAME}/`);
});

test('on the phone, Turn off Tailscale mode asks first', async ({ page, admiral }) => {
  const ts = fakeTailscale(admiral);
  setRemote(admiral, { tailscale: true, allowedLogins: [TS_LOGIN] });
  await admiral.start({ env: ts.env });
  ts.serve(new URL(admiral.url).port);
  await page.goto(admiral.url + 'app/preferences/admiral/remote');
  const section = page.getByRole('group', { name: 'Tailscale' });
  await section.getByRole('button', { name: 'Turn off', exact: true }).tap();
  await expect(page.getByRole('group', { name: 'Turn off Tailscale mode' }).getByRole('button', { name: 'Turn off Tailscale mode' })).toBeVisible();

});

test("on the phone, the Chat tab shows the fleet's AdmiralBot conversation and its composer without the Inbox rail, and a fleet with no Claude Code dock shows why its chat is off", async ({ page, admiral }) => {
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
  await box.fill('How many are underway?');
  const send = main.getByRole('button', { name: 'Send', exact: true });
  expect((await send.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await send.tap();
  await expect.poll(() => chatReceived(admiral, 'harbor')).toContain('How many are underway?');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

  // The Inbox keeps its own tab.
  await tab(page, 'Inbox').tap();
  await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();

  await page.goto(admiral.url + 'app/chat?fleet=alpha');
  await expect(main).toContainText(/no Claude Code dock/i);
  await expect(main.getByRole('textbox', { name: 'Message AdmiralBot' })).toHaveCount(0);
  expect(watch.errors).toEqual([]);
});

test("on the phone, a fleet's AdmiralBot page shows its harness, model, effort and refresh threshold, saves a change that survives a reload, and shows why a fleet with no Claude Code dock is off", async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true });
  await admiral.start();
  await page.goto(admiral.url + 'app/inbox');
  await openPreferences(page, { fleet: 'Harborlight', section: 'AdmiralBot' });
  await expect(page).toHaveURL(/\/app\/preferences\/harbor\/admiralbot/);
  const pick = (g: string, name: string) => page.getByRole('radiogroup', { name: g }).getByRole('radio', { name, exact: true });
  await expect(page.getByRole('heading', { name: 'AdmiralBot', exact: true })).toBeInViewport({ ratio: 1 });
  await expectReachable(page, pick('Harness', 'Claude'));
  await expect(pick('Harness', 'Claude')).toBeChecked();
  await expect(pick('Model', 'Sonnet')).toBeChecked();
  await expect(pick('Effort', 'Default')).toBeChecked();
  await expect(pick('Refresh context at', '150K')).toBeChecked();
  expect((await pick('Model', 'Opus').boundingBox())!.height).toBeGreaterThanOrEqual(44);

  await pick('Model', 'Haiku').tap();
  await pick('Effort', 'High').tap();
  await pick('Refresh context at', 'Never').tap();
  await Promise.all([page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().includes('/fleets/harbor')), page.getByRole('button', { name: 'Save', exact: true }).tap()]);
  await page.reload();
  await expect(pick('Model', 'Haiku')).toBeChecked();
  await expect(pick('Effort', 'High')).toBeChecked();
  await expect(pick('Refresh context at', 'Never')).toBeChecked();

  await page.goto(admiral.url + 'app/preferences/alpha/admiralbot');
  await expect(page.getByRole('main')).toContainText(/no Claude Code dock/i);

  await page.goto(admiral.url + 'app/preferences/zulu/admiralbot');
  await expect(pick('Harness', 'Codex')).toBeDisabled();
  await expect(page.getByRole('main')).toContainText(/Codex.*has not been checked/i);
  const again = page.getByRole('main').getByRole('button', { name: /Check again.*Codex/ });
  await expectReachable(page, again);
  expect((await again.boundingBox())!.height).toBeGreaterThanOrEqual(44);
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
