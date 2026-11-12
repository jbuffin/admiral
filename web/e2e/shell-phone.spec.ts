import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, HOSTILE, TS_LOGIN, TS_NAME, WORK, fakeTailscale, setRemote, stubClipboard, boatReceived, changeFile, changesOf, chip, diffOf, expectSeededTurn, gitCheckout, holds, interruptButton, launchCrew, launchFromTheShell, launchShip, line, messageBox, outcomeEvents, paneSize, received, rowLink, say, seedBoatTranscript, seedFleets, seedInboxItem, seedTurn, sendButton, sentBubble, setReason, setState, shipViewUrl, stopDialog, stopShipButton, viewPanel, viewTab, watchPage, withFakeGh } from './shell';

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
  await expect(viewPanel(page)).toContainText('No pull request yet');
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

  // Preferences stacks its nav above the page.
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Preferences', exact: true }).tap();
  await expect(page).toHaveURL(/\/app\/preferences\/alpha\/sources\?(.*&)?fleet=alpha/);
  const prefsNav = await page.getByRole('navigation', { name: 'Preferences' }).boundingBox();
  const prefsPage = await page.getByRole('heading', { name: 'Order sources', exact: true }).boundingBox();
  expect(prefsNav!.y + prefsNav!.height).toBeLessThanOrEqual(prefsPage!.y);
  expect(prefsPage!.x).toBeLessThan(prefsNav!.x + prefsNav!.width);

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

test('on the phone, Remote access opens from More through Preferences', async ({ page, admiral }) => {
  seedFleets(admiral);
  await admiral.start();
  await page.goto(admiral.url + 'app/inbox');
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Preferences', exact: true }).tap();
  await page.getByRole('navigation', { name: 'Preferences' }).getByRole('link', { name: 'Remote access' }).tap();
  await expect(page).toHaveURL(/\/app\/preferences\/admiral\/remote/);
  await expect(page.getByRole('heading', { name: 'Remote access', exact: true })).toBeVisible();
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
