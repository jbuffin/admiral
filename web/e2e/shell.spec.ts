import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, launchCrew, launchFromTheShell, launchShip, probeableClaude, say, seedFleets, setState, watchPage, withFakeGh } from './shell';

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
  launchCrew(admiral, billing, 'mate');

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
const rowOf = (harness: ReturnType<typeof harnessOf>, label: string) => harness.getByRole('listitem').filter({ hasText: label });
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

  // Nothing has been probed yet: every capability is not checked, and no row claims a failure.
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
  for (const cap of before.caps.filter((c) => c.id !== failed.id && !judgeProbed(c))) {
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
  await expect(harnessOf(page, 'Claude').getByRole('note')).toHaveCount(before.caps.filter(judgeProbed).length);

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
