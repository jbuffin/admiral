import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, holds, boatOf, boatReceived, boatSays, changeFile, changesOf, chip, diffOf, expectSeededTurn, flotilla, gitCheckout, interruptButton, launchCrew, launchFromTheShell, launchShip, line, appendLines, messageBox, outcomeEvents, probeableClaude, received, releaseBoat, rowLink, say, seedBoatTranscript, seedFleets, seedTurn, sendButton, sentBubble, setBoatState, setState, shipViewUrl, stopDialog, stopShipButton, viewPanel, viewTab, watchPage, withFakeGh, HOSTILE, seedInboxItem, setReason, chatAnswerFile, chatAsks, chatCleared, chatReceived, chatSays, chatStarts, chatEnds, chatSession } from './shell';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const surface = (page: Page, title: string) => page.getByRole('heading', { name: title, exact: true });
const nav = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });
const group = (page: Page, title: string) => page.getByRole('main').getByRole('region', { name: new RegExp(`^${title}`) });
const row = (page: Page, ship: { name: string }) => page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name });
const switcher = (page: Page, fleet: string) => page.getByRole('navigation').getByRole('button', { name: fleet, exact: true });

test('the shell navigates its surfaces, lists the chosen fleet\'s ships live and scopes them to that fleet', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  setState(admiral, login, 'needs_you');
  await say(admiral, login, 'Which branch should I rebase onto?');
  const billing = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-billing' });
  setState(admiral, billing, 'working');
  await say(admiral, billing, 'Reading billing/export.go');
  const moored = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', exit: 3 });
  const sailed = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', exit: 0 });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  // After the last cue is cleared, so the idling crew boat cannot read an exit cue.
  await launchCrew(admiral, billing, 'mate');

  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(surface(page, 'Inbox')).toBeVisible();
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Inbox', 'Ships', 'Chat', 'Campaigns', 'Routines', 'Crew', 'Preferences']);

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
  await expect(row(page, billing)).toContainText('1 crew');
  await expect(row(page, billing).locator('.ship-age')).toHaveText(/^(now|\d+m)$/);
  await expect(row(page, login)).not.toContainText('crew');
  expect((await row(page, login).boundingBox())!.height).toBeGreaterThanOrEqual(44);

  // Server-side changes reach the open page: a ship moves group when its status changes and its tail updates.
  setState(admiral, login, 'working');
  await expect(group(page, 'Underway')).toContainText(login.name);
  await expect(main.locator('.ship-group').getByRole('heading', { level: 2 })).toHaveText([/^Underway\s*2$/, /^Anchored\s*1$/]);
  await say(admiral, login, 'Rebasing onto main');
  await expect(row(page, login)).toContainText('Rebasing onto main', { timeout: 15_000 });
  await expect(row(page, login)).not.toContainText('Which branch should I rebase onto?');

  // Opening a ship: the view opens on Conversation and shows its turn. Back returns to the list.
  await seedTurn(admiral, login);
  await page.evaluate(() => ((window as unknown as { stayed: boolean }).stayed = true));
  await rowLink(page, login).tap();
  await expect(page).toHaveURL(shipViewUrl(admiral, login));
  await expect(page.getByRole('heading', { name: login.name, exact: true })).toBeVisible();
  await expect(page.getByRole('tab')).toHaveText(['Conversation', 'Terminal', 'Changes', 'PR']);
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await expectSeededTurn(page);
  await expect(viewPanel(page)).toContainText('Working');

  // A line appended to the transcript shows without a reload.
  await appendLines(admiral, login, [line.assistant('Appended while the view was open')]);
  await expect(viewPanel(page)).toContainText('Appended while the view was open', { timeout: 15_000 });

  // PR is a placeholder here. login works in proj, which has no commit: there is nothing to compare, and reading Changes never joins the bridge.
  await viewTab(page, 'PR').tap();
  await expect(viewTab(page, 'PR')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page)).toContainText('No pull request yet');
  await viewTab(page, 'Changes').tap();
  await expect(viewTab(page, 'Changes')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page)).toContainText('No git history to compare');
  expect(watch.paths.filter((p) => /^\/ws\/(v1\/)?bridge\//.test(p))).toEqual([]);

  // Only the Terminal tab joins the bridge, and what is typed there reaches the pane. Leaving it closes the socket.
  const joined = page.waitForEvent('websocket', (ws) => /\/bridge\//.test(ws.url()));
  await viewTab(page, 'Terminal').tap();
  const bridge = await joined;
  expect(new URL(bridge.url()).pathname).toBe(`/ws/v1/bridge/${encodeURIComponent(login.session)}`);
  await expect(viewPanel(page).locator('.xterm')).toBeVisible();
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

  // Back to the list, and the view opens on Conversation again from the keyboard: no tab is remembered.
  await viewTab(page, 'Terminal').tap();
  await page.goBack();
  await expect(surface(page, 'Ships')).toBeVisible();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=zulu/);
  await rowLink(page, login).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(shipViewUrl(admiral, login));
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
  await page.goBack();
  expect(await page.evaluate(() => (window as unknown as { stayed?: boolean }).stayed)).toBe(true);

  // A Codex ship has no conversation Admiral can read: a card says so and points to the terminal.
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

  // Back in Westwind, ships launch from its orders and from the header, and show in the list.
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await expect(switcher(page, WORK)).toBeVisible();
  await launchFromTheShell(page, admiral, (l) => l.click(), { phone: false });

  // Talking to a ship: the box sends, the message shows dimmed until the conversation has it, Interrupt presses Esc and Stop ship ends the ship.
  const chat = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste', 'replydelay 2', 'reply Aye, rebasing now'] });
  setState(admiral, chat, 'needs_you');
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, chat).tap();
  await expect(messageBox(page)).toBeEnabled();
  // A draft survives a visit to another tab.
  await messageBox(page).fill('A draft to keep');
  await viewTab(page, 'Terminal').tap();
  await viewTab(page, 'Conversation').tap();
  await expect(messageBox(page)).toHaveValue('A draft to keep');
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));
  await page.route('**/api/v1/ships/*/messages', async (route) => {
    await gate;
    await route.continue();
  });
  await messageBox(page).fill('Please rebase onto main');
  await sendButton(page).tap();
  await expect(sentBubble(page, 'Please rebase onto main')).toBeVisible();
  // A second message while the first is in flight gets its own bubble.
  await messageBox(page).fill('And mind the lockfile');
  await messageBox(page).press('Enter');
  await expect(sentBubble(page, 'And mind the lockfile')).toBeVisible();
  const accepted = page.waitForResponse((r) => r.url().includes('/messages') && r.status() === 200);
  release();
  await accepted;
  // The harness records the message two seconds after the server accepted it: the bubble waits for the record, not the 200.
  await expect(sentBubble(page, 'Please rebase onto main')).toBeVisible();
  await expect(sentBubble(page, 'Please rebase onto main')).toHaveCount(0);
  await expect(sentBubble(page, 'And mind the lockfile')).toHaveCount(0, { timeout: 15_000 });
  await expect(viewPanel(page).getByText('Please rebase onto main', { exact: true })).toHaveCount(1);
  await expect(viewPanel(page).getByText('And mind the lockfile', { exact: true })).toHaveCount(1);
  await expect(viewPanel(page)).toContainText('Aye, rebasing now', { timeout: 15_000 });
  expect(received(admiral, chat)).toContain('Please rebase onto main');
  await page.unroute('**/api/v1/ships/*/messages');

  // A hardware keyboard sends with Enter.
  await messageBox(page).fill('And run the tests');
  await messageBox(page).press('Enter');
  await expect(viewPanel(page).getByText('And run the tests', { exact: true })).toHaveCount(1);
  await expect(messageBox(page)).toHaveValue('');

  setState(admiral, chat, 'working');
  await expect(interruptButton(page)).toBeVisible();
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

  // Changes: a ship in a checkout lists the branch's commit, the uncommitted edit and the untracked file, each with its diff.
  const bridgesBefore = watch.paths.filter((p) => /^\/ws\/(v1\/)?bridge\//.test(p)).length;
  const repo = gitCheckout(admiral, 'repo');
  const dev = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', dir: repo, cues: ['bracketedpaste'] });
  setState(admiral, dev, 'working');
  await expect.poll(async () => (await changesOf(admiral, dev)).files.map((f) => `${f.path} ${f.status}`).sort()).toEqual(['a.txt modified', 'b.txt added', 'c.txt untracked']);
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, dev).tap();
  await viewTab(page, 'Changes').tap();
  await expect(viewTab(page, 'Changes')).toHaveAttribute('aria-selected', 'true');
  await expect(viewPanel(page).getByRole('list')).toBeVisible();
  await expect(changeFile(page, 'a.txt')).toContainText('modified');
  await expect(changeFile(page, 'a.txt')).toContainText('+2');
  await expect(changeFile(page, 'a.txt')).toContainText(/[−-]1/);
  await expect(changeFile(page, 'b.txt')).toContainText('added');
  await expect(changeFile(page, 'c.txt')).toContainText('untracked');
  await expect(diffOf(page, 'a.txt')).toContainText('TWO');
  await expect(diffOf(page, 'b.txt')).toContainText('new two');
  await expect(diffOf(page, 'c.txt')).toContainText('loose');
  await expect(viewPanel(page)).not.toContainText(/No changes|No git history/);
  expect(watch.paths.filter((p) => /^\/ws\/(v1\/)?bridge\//.test(p))).toHaveLength(bridgesBefore);

  // The flotilla: a chip for the ship and one per boat, by role (Teammate when it has none). A picked boat takes the conversation,
  // the terminal, the message box and Interrupt; Changes and Stop ship stay the ship's.
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

  // Changes stay the ship's, and so does Stop ship.
  await viewTab(page, 'Changes').tap();
  await expect(viewPanel(page)).toContainText('No git history to compare');
  await viewTab(page, 'Conversation').tap();
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

  expect(watch.paths.some((p) => p.startsWith('/api/v1/config'))).toBe(true);
  expect(watch.paths).toContain('/ws/v1');
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

interface Capability {
  id: string;
  label: string;
  status: string;
  detail: string;
}

// What the server says about Westwind's claude dock, read from the API so the page is checked against it.
async function westwindClaude(page: Page, server: string) {
  const res = await page.request.get(server + 'api/v1/harness-capabilities');
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { fleets: { key: string; harnesses: { harness: string; version: string; features: { capabilities: Capability[] }[] }[] }[] };
  const claude = body.fleets.find((f) => f.key === 'zulu')!.harnesses.find((h) => h.harness === 'claude')!;
  const caps = claude.features.flatMap((f) => f.capabilities);
  return { version: claude.version, caps, guard: caps.find((c) => c.id === 'autopilot.guard_hook')! };
}

// The fleet's judge object as the API serves it, so the page's judge line is checked against it.
async function westwindJudge(page: Page, server: string) {
  const res = await page.request.get(server + 'api/v1/harness-capabilities');
  expect(res.status()).toBe(200);
  const body = (await res.json()) as { fleets: { key: string; judge: { harness: string | null; reason?: string } }[] };
  return body.fleets.find((f) => f.key === 'zulu')!.judge;
}
// Judge probes that have a mechanism on claude; usage reports has no probe and stays not checked.
const judgeProbed = (c: Capability) => c.id.startsWith('judge.') && c.id !== 'judge.usage_reports';

const capabilities = (page: Page) => page.getByRole('main').getByRole('region', { name: 'Harness capabilities' });
const harnessOf = (page: Page, harness: string) => capabilities(page).getByRole('group', { name: harness, exact: true });
// Each fleet has its own Harness capabilities page in Preferences' nav.
async function openCapabilities(page: Page, fleet: string) {
  await page.getByRole('navigation', { name: 'Preferences' }).getByRole('group', { name: `${fleet} fleet` }).getByRole('link', { name: 'Harness capabilities', exact: true }).tap();
  await expect(surface(page, 'Harness capabilities')).toBeVisible();
  await expect(page.getByRole('main')).toContainText(`${fleet} fleet`);
}
// A row starts with its label, so another row's reason (the advisor's names no tools) never matches.
const rowOf = (harness: ReturnType<typeof harnessOf>, label: string) => harness.getByRole('listitem').filter({ hasText: new RegExp('^' + label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')) });
const missingLine = (harness: ReturnType<typeof harnessOf>, label: string) => harness.getByRole('note').filter({ hasText: label });

test('Preferences shows what each harness was probed to do, and Check again re-runs a failed probe', async ({ page, admiral }) => {
  const claude = probeableClaude(admiral);
  claude.probe('skip-hook');
  seedFleets(admiral, { claude: claude.dock });
  await admiral.start();
  const watch = watchPage(page, admiral.url);

  await page.goto(admiral.url + 'app/inbox');
  await nav(page, 'Preferences').tap();
  await expect(page).toHaveURL(/\/app\/preferences\//);

  // Each fleet's page lists that fleet's own harnesses, in config order.
  await openCapabilities(page, HOME);
  await expect(page).toHaveURL(/\/app\/preferences\/alpha\/capabilities/);
  await expect(capabilities(page).getByRole('group')).toHaveCount(2);
  await expect(harnessOf(page, 'Codex')).toBeVisible();
  await expect(harnessOf(page, 'Copilot')).toBeVisible();
  await openCapabilities(page, WORK);
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/capabilities/);
  await expect(capabilities(page).getByRole('group')).toHaveCount(2);
  const workClaude = harnessOf(page, 'Claude');
  const workCodex = harnessOf(page, 'Codex');
  await expect(workCodex).toBeVisible();
  await expect(workClaude.locator('img[src="/api/v1/logos/claude"]')).toBeVisible();
  await expect(workClaude.locator('img[src="/api/v1/logos/claude"]')).toBeVisible();

  // Nothing has been probed yet: every capability is not checked, the advisor included.
  const before = await westwindClaude(page, admiral.url);
  expect(before.caps.length).toBeGreaterThan(1);
  for (const cap of before.caps) {
    expect(cap.status).toBe('not_checked');
    await expect(rowOf(workClaude, cap.label)).toContainText(/not checked/i);
  }
  await expect(workClaude.getByRole('note')).toHaveCount(0);
  // No harness is proven for judge calls yet: the line reads off, with the API's reason.
  const noJudge = await westwindJudge(page, admiral.url);
  expect(noJudge.harness).toBeNull();
  await expect(page.getByRole('main')).toContainText('Judge calls: off');
  await expect(page.getByRole('main')).toContainText(noJudge.reason!);

  // Check again runs that harness's probe in that fleet and shows the answer. Here the guard fails, with its reason.
  const post = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/harness-capabilities/check');
  const sent = box(await workClaude.getByRole('button', { name: 'Check again Claude', exact: true }).boundingBox());
  expect(sent.height).toBeGreaterThanOrEqual(44);
  await workClaude.getByRole('button', { name: 'Check again Claude', exact: true }).tap();
  expect((await post).postDataJSON()).toEqual({ fleet: 'zulu', harness: 'claude' });

  await expect(rowOf(workClaude, before.guard.label)).toContainText(/failed/i);
  const failed = (await westwindClaude(page, admiral.url)).guard;
  expect(failed.status).toBe('failed');
  expect(failed.detail).not.toBe('');
  await expect(rowOf(workClaude, failed.label)).toContainText(failed.detail);
  await expect(workClaude).toContainText((await westwindClaude(page, admiral.url)).version);
  // A failed capability says so in one line on its own row, naming the harness. Nothing else gets a line.
  await expect(workClaude.getByRole('note')).toHaveCount(1);
  await expect(rowOf(workClaude, failed.label).getByRole('note')).toContainText('Claude');
  await expect(missingLine(workClaude, failed.label)).toContainText(failed.label);
  // The other harnesses were not touched.
  await expect(rowOf(workCodex, failed.label)).toContainText(/not checked/i);

  // The same check proved the judge probes on the fake claude, so its rows read passed and the line names the harness and models,
  // before any reload: the page took the judge object from the check's answer.
  for (const cap of before.caps.filter(judgeProbed)) {
    await expect(rowOf(workClaude, cap.label)).toContainText(/passed/i);
  }
  await expect(rowOf(workClaude, 'Usage reports')).toContainText(/not checked/i);
  await expect(page.getByRole('main')).toContainText('Judge calls: Claude (haiku / sonnet)');
  expect((await westwindJudge(page, admiral.url)).harness).toBe('claude');

  // The probe passes this time. The one-line missing-capability component re-runs it too, and the line goes away with the fix.
  claude.probe('pass');
  await missingLine(workClaude, failed.label).getByRole('button', { name: `Check again ${failed.label} on Claude`, exact: true }).tap();
  await expect(rowOf(workClaude, failed.label)).toContainText(/passed/i);
  await expect(workClaude.getByRole('note')).toHaveCount(0);
  expect((await westwindClaude(page, admiral.url)).guard.status).toBe('passed');
  // The advisor follows the judge harness the earlier check proved.
  await expect(rowOf(workClaude, 'Advisor')).toContainText(/passed/i);
  for (const cap of before.caps.filter((c) => c.id !== failed.id && c.id !== 'autopilot.advisor' && !judgeProbed(c))) {
    await expect(rowOf(workClaude, cap.label)).toContainText(/not checked/i);
  }
  await expect(rowOf(workCodex, failed.label)).toContainText(/not checked/i);

  // A fresh load shows the same: the answer is the server's, not the page's.
  await page.reload();
  await expect(rowOf(harnessOf(page, 'Claude'), failed.label)).toContainText(/passed/i);
  await expect(page.getByRole('main')).toContainText('Judge calls: Claude (haiku / sonnet)');

  // When the judge probes fail, the line goes off with the reason, and each failed row says what is missing.
  claude.judge('leak');
  await harnessOf(page, 'Claude').getByRole('button', { name: 'Check again Claude', exact: true }).tap();
  for (const cap of before.caps.filter(judgeProbed)) {
    await expect(rowOf(harnessOf(page, 'Claude'), cap.label)).toContainText(/failed/i);
  }
  const off = await westwindJudge(page, admiral.url);
  expect(off.harness).toBeNull();
  await expect(page.getByRole('main')).toContainText('Judge calls: off');
  await expect(page.getByRole('main')).toContainText(off.reason!);
  await expect(harnessOf(page, 'Claude').getByRole('note')).toHaveCount(before.caps.filter(judgeProbed).length + 1); // the failed advisor too

  expect(watch.paths.some((p) => p === '/api/v1/harness-capabilities')).toBe(true);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('Check again shows that it is working, and says so when the harness cannot be probed', async ({ page, admiral }) => {
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { claude: claude.dock });
  await admiral.start();

  await page.goto(admiral.url + 'app/preferences/zulu/capabilities');
  const harness = harnessOf(page, 'Claude');
  const again = harness.getByRole('button', { name: 'Check again Claude', exact: true });
  await expect(again).toBeVisible();

  // The guard passes once, so there is something for a later failure to take away.
  const guard = rowOf(harness, (await westwindClaude(page, admiral.url)).guard.label);
  await again.tap();
  await expect(guard).toContainText(/passed/i);

  // A probe can take tens of seconds: hold the answer to see the page wait for it.
  let release!: () => void;
  const held = new Promise<void>((r) => (release = r));
  await page.route('**/api/v1/harness-capabilities/check', async (route) => {
    await held;
    await route.continue();
  });
  claude.version('FAIL');
  await again.tap();
  await expect(again).toBeDisabled();
  await expect(harness).toContainText(/checking/i);
  release();

  // `--version` fails: the server answers 502, the page says the check could not run and lets the person try again.
  await expect(harness.getByRole('alert')).toBeVisible();
  await expect(again).toBeEnabled();
  await expect(harness).not.toContainText(/checking/i);

  // The server cleared the version with that failure, so the page shows the guard as not proven, never the old pass.
  await expect(guard).toContainText(/not checked/i);
  await expect(guard).not.toContainText(/passed/i);

  claude.version('2.1.300 (fake)');
  await again.tap();
  await expect(harness.getByRole('alert')).toHaveCount(0);
  await expect(harness.getByRole('listitem').filter({ hasText: /passed/i })).not.toHaveCount(0);
});

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
});

test('a ship\'s Bridge shows its tokens and its own money, and the Usage pages keep each harness\'s money apart, show quota freshness and a quiet mark past 90%', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const cues = ['usage', 'exit 3'];
  const claude = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues });
  const copilot = await launchShip(admiral, { fleet: 'alpha', harness: 'copilot', cues });
  const codex = await launchShip(admiral, { fleet: 'alpha', harness: 'codex', cues });
  const usage = page.getByLabel('Usage');

  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, claude).tap();
  await expect(usage).toContainText('150 tokens', { timeout: 15_000 });
  await expect(usage).toContainText('est. $0.25');
  await expect(usage).toContainText('from records');
  await expect(usage).not.toContainText(/credit|premium/i);
  // The classes open on tap; the cache classes are not in the headline.
  await expect(usage.getByText('Cache read')).toBeHidden();
  await usage.getByText('150 tokens').tap();
  await expect(usage.getByText('Cache read')).toBeVisible();
  await expect(usage).toContainText('300');

  await page.goto(admiral.url + 'app/ships?fleet=alpha');
  await rowLink(page, copilot).tap();
  await expect(usage).toContainText('2.5 credits', { timeout: 15_000 });
  await expect(usage).toContainText('3 premium requests');
  await expect(usage).not.toContainText('$');

  await page.goto(admiral.url + 'app/ships?fleet=alpha');
  await rowLink(page, codex).tap();
  await expect(usage).toContainText('360 tokens', { timeout: 15_000 });
  await expect(usage).not.toContainText(/\$|credit/);

  // A fleet's Usage: each harness on its own line in its own money, split by spender, no total, no worker cap yet.
  const main = page.getByRole('main');
  const period = (name: string) => main.getByRole('region', { name });
  await page.goto(admiral.url + 'app/preferences/zulu/usage');
  await expect(page.getByRole('heading', { name: 'Usage', exact: true })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Preferences' }).getByRole('link', { name: 'Usage' }).first()).toBeVisible();
  await expect(period('Today')).toContainText('150 tokens', { timeout: 15_000 });
  await expect(period('Today')).toContainText('est. $0.25');
  await expect(period('Today')).toContainText('Ships');
  await expect(period('7 days')).toContainText('est. $0.25');
  await expect(period('30 days')).toContainText('est. $0.25');
  await expect(main).not.toContainText(/credit|total|cap\b/i);
  // Neither dock has a reading yet, and Claude's has no bar.
  await expect(main.getByRole('region', { name: /claude/i })).toContainText('no live data yet');
  await expect(main.getByRole('region', { name: /codex/i })).toContainText('no live data yet');
  await expect(main.getByRole('img', { name: /window/ })).toHaveCount(0);

  await page.goto(admiral.url + 'app/preferences/alpha/usage');
  await expect(period('Today')).toContainText('2.5 credits', { timeout: 15_000 });
  await expect(period('Today')).toContainText('3 premium requests');
  await expect(period('Today')).toContainText('360 tokens');
  await expect(main).not.toContainText(/\$|total/i);
  await expect(main.getByRole('region', { name: /copilot/i })).toHaveCount(0);

  // The cross-fleet page has one row for each fleet and nothing that adds them.
  await page.goto(admiral.url + 'app/preferences/admiral/all-usage');
  await expect(page.getByRole('heading', { name: 'Usage', exact: true })).toBeVisible();
  const fleetRow = (name: string) => main.getByRole('region', { name });
  await expect(fleetRow(WORK)).toContainText('est. $0.25', { timeout: 15_000 });
  await expect(fleetRow(WORK)).toContainText('150 tokens');
  await expect(fleetRow(HOME)).toContainText('2.5 credits');
  await expect(fleetRow(HOME)).toContainText('360 tokens');
  await expect(fleetRow(HOME)).not.toContainText('$');
  await expect(main.getByRole('region', { name: new RegExp(`${WORK}|${HOME}`) })).toHaveCount(2);
  await expect(main).not.toContainText(/total|all fleets combined/i);

  // A live reading from a running Claude ship gives a bar; past 90% it only gets a quiet note beside it.
  const reading = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'internal', 'cli', 'testdata', 'claude-statusline.json'), 'utf8'));
  reading.rate_limits.five_hour = { used_percentage: 93, resets_at: Math.floor(Date.now() / 1000) + 2 * 3600 };
  reading.rate_limits.seven_day = { used_percentage: 41.2, resets_at: Math.floor(Date.now() / 1000) + 3 * 86400 };
  await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['statusline ' + JSON.stringify(reading)] });
  // The page reads once on open and then every 30 s, so open it after the server has the reading.
  const quotaOf = async () => ((await (await fetch(admiral.url + 'api/v1/usage?fleet=zulu')).json()) as { quota: { harness: string; has_data: boolean }[] }).quota.find((q) => q.harness === 'claude');
  await expect.poll(async () => (await quotaOf())?.has_data, { timeout: 30_000 }).toBe(true);
  await page.goto(admiral.url + 'app/preferences/zulu/usage');
  const claudeQuota = main.getByRole('region', { name: /claude/i });
  await expect(claudeQuota.getByRole('img', { name: /^5-hour window, 93% used, resets in/ })).toBeVisible({ timeout: 30_000 });
  await expect(claudeQuota.getByRole('img', { name: /^7-day window, 41% used, resets in/ })).toBeVisible();
  await expect(claudeQuota).toContainText('live');
  await expect(claudeQuota).toContainText('near the limit');
  await expect(claudeQuota.getByText('near the limit')).toHaveCount(1);
  await expect(main.getByRole('region', { name: /codex/i })).toContainText('no live data yet');
});

test('the Inbox lists a question item and a needs-you stop from two fleets, each with its fleet, and the stop leaves live once its ship is back underway', async ({ page, admiral }) => {
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
  expect((await entry(login.name).boundingBox())!.height).toBeGreaterThanOrEqual(44);

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

  // The first load installs the worker; the second is the one it controls and caches.
  async function visitTwice(page: Page, url: string) {
    await page.goto(url + 'app/');
    await expect(surface(page, 'Inbox')).toBeVisible();
    await page.evaluate(() => navigator.serviceWorker.ready);
    await page.reload();
    await expect(surface(page, 'Inbox')).toBeVisible();
    await expect.poll(() => controlled(page)).toBe(true);
  }

  test('the shell links a manifest that makes it installable, and a worker controls /app/', { tag: '@critical' }, async ({ page, admiral }) => {
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

    const scope = await page.evaluate(async () => (await navigator.serviceWorker.ready).scope);
    expect(new URL(scope).pathname).toBe('/app/');
    await page.reload();
    await expect.poll(() => controlled(page)).toBe(true);
  });

  test('with the server down, the cached shell says it cannot reach the Mac, and Retry returns to the shell once it answers', { tag: '@critical' }, async ({ page, admiral }) => {
    await admiral.start();
    // One online load only: the worker caches the shell when it installs, not from a second visit.
    await page.goto(admiral.url + 'app/');
    await expect(surface(page, 'Inbox')).toBeVisible();
    await page.evaluate(() => navigator.serviceWorker.ready);
    await expect.poll(() => cachedPaths(page)).toContain('/app/');
    expect((await cachedPaths(page)).some((p) => p.startsWith('/assets/'))).toBe(true);

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

  test('the worker caches the shell but never an API or socket response, and leaves the old UI uncontrolled', async ({ page, admiral }) => {
    seedFleets(admiral, { live: true });
    await admiral.start({ env: withFakeGh(admiral) });
    await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
    await visitTwice(page, admiral.url);
    await nav(page, 'Ships').click();
    await expect(page.getByRole('main')).toContainText(WORK);

    const paths = await cachedPaths(page);
    expect(paths.some((p) => p.startsWith('/app/'))).toBe(true);
    expect(paths.some((p) => p.startsWith('/assets/'))).toBe(true);
    expect(paths.filter((p) => p.startsWith('/api/') || p.startsWith('/ws/'))).toEqual([]);
    expect(paths).not.toContain('/');

    await page.goto(admiral.url + 'classic/');
    await expect(page).toHaveTitle('Admiral');
    await expect(page.locator('#app')).not.toBeEmpty();
    expect(await controlled(page)).toBe(false);
  });
});

test.describe('a shell that does not match the server', () => {
  const serverBuild = async (page: Page, url: string) => (await (await page.request.get(url + 'api/v1/version')).json()) as { version: string; commit: string };
  const shellLoads = (page: Page) => {
    const loads: string[] = [];
    page.on('request', (r) => {
      if (r.isNavigationRequest() && new URL(r.url()).pathname.startsWith('/app/')) loads.push(r.url());
    });
    return loads;
  };

  test('reloads once when /api/v1/version names another build, and not again while it still differs', async ({ page, admiral }) => {
    await admiral.start();
    await page.route('**/api/v1/version', (route) => route.fulfill({ json: { version: 'other-build', commit: 'zzz9999', api_versions: [1], capabilities: [] } }));
    const loads = shellLoads(page);
    await page.goto(admiral.url + 'app/');
    await expect.poll(() => loads.length).toBe(2);
    await expect(surface(page, 'Inbox')).toBeVisible();
    await holds(() => loads.length === 2, 'the shell reloaded again');
  });

  test('does not reload when /api/v1/version names the shell\'s own build', async ({ page, admiral }) => {
    await admiral.start();
    const build = await serverBuild(page, admiral.url);
    let probed = 0;
    await page.route('**/api/v1/version', (route) => {
      probed++;
      return route.fulfill({ json: { ...build, api_versions: [1], capabilities: [] } });
    });
    const loads = shellLoads(page);
    await page.goto(admiral.url + 'app/');
    await expect(surface(page, 'Inbox')).toBeVisible();
    await expect.poll(() => probed).toBeGreaterThan(0);
    await holds(() => loads.length === 1, 'the shell reloaded');
  });

});

// The fleets: harbor (Harborlight) and lagoon (Lagoonside) have their own AdmiralBot; alpha (Homeport) has no Claude Code dock.
test("the Chat surface shows the fleet's AdmiralBot history beside the Inbox rail, starts a new chat that keeps the history, answers a question in place, and shows only the shown fleet's chat", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  chatSays(admiral, 'harbor', [line.user('Where are the ships'), line.assistant('Two are **afloat**.'), line.assistant(HOSTILE)]);
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
  await main.getByRole('link', { name: /AdmiralBot/ }).tap();
  await expect(page).toHaveURL(/\/app\/preferences\/alpha\/admiralbot/);

  // A rail row opens the item in Peek beside the chat, without leaving it.
  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  await main.getByRole('listitem').filter({ hasText: login.name }).getByRole('link').tap();
  await expect(page).toHaveURL(new RegExp(`/app/chat/harbor/item/${encodeURIComponent(login.session)}/q-port`));
  await expect(page.getByRole('region', { name: 'Peek' })).toContainText('Which port should the login use?');
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Harborlight fleet' })).toBeVisible();

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

test('a fleet whose chat is off has no Open terminal', async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true });
  await admiral.start();
  const main = page.getByRole('main');
  await page.goto(admiral.url + 'app/chat?fleet=alpha');
  await expect(main).toContainText(/no Claude Code dock/i);
  await expect(main.getByRole('button', { name: 'Open terminal' })).toHaveCount(0);
});
