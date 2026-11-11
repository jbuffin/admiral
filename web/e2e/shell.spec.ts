import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, boatOf, boatReceived, boatSays, changeFile, changesOf, chip, diffOf, expectSeededTurn, flotilla, gitCheckout, interruptButton, launchCrew, launchFromTheShell, launchShip, line, appendLines, messageBox, outcomeEvents, probeableClaude, received, releaseBoat, rowLink, say, seedBoatTranscript, seedFleets, seedTurn, sendButton, sentBubble, setBoatState, setState, shipViewUrl, stopDialog, stopShipButton, viewPanel, viewTab, watchPage, withFakeGh } from './shell';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

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
