import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, probeableClaude, seedFleets, watchPage } from './shell';

const surface = (page: Page, title: string) => page.getByRole('heading', { name: title, exact: true });
const nav = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });
const switcher = (page: Page, fleet: string) => page.getByRole('navigation').getByRole('button', { name: fleet, exact: true });

test('the shell navigates its surfaces and scopes them to the chosen fleet', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral);
  await admiral.start();
  const watch = watchPage(page, admiral.url);

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

  await switcher(page, WORK).click();
  const menu = page.getByRole('menu');
  await expect(menu.getByRole('menuitemradio', { name: WORK })).toHaveAttribute('aria-checked', 'true');
  await menu.getByRole('menuitemradio', { name: HOME }).click();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('main')).toContainText(HOME);
  await expect(page.getByRole('main')).not.toContainText(WORK);

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
  seedFleets(admiral, claude.dock);
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

  // The probe passes this time. The one-line missing-capability component re-runs it too, and the line goes away with the fix.
  claude.probe('pass');
  await missingLine(workClaude, failed.label).getByRole('button', { name: `Check again ${failed.label} on Claude`, exact: true }).tap();
  await expect(rowOf(workClaude, failed.label)).toContainText(/passed/i);
  await expect(workClaude.getByRole('note')).toHaveCount(0);
  expect((await westwindClaude(page, admiral.url)).guard.status).toBe('passed');
  for (const cap of before.caps.filter((c) => c.id !== failed.id)) {
    await expect(rowOf(workClaude, cap.label)).toContainText(/not checked/i);
  }
  await expect(rowOf(workCodex, failed.label)).toContainText(/not checked/i);

  // A fresh load shows the same: the answer is the server's, not the page's.
  await page.reload();
  await expect(rowOf(harnessOf(page, 'Claude'), failed.label)).toContainText(/passed/i);

  expect(watch.paths.some((p) => p === '/api/v1/harness-capabilities')).toBe(true);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('Check again shows that it is working, and says so when the harness cannot be probed', async ({ page, admiral }) => {
  const claude = probeableClaude(admiral);
  seedFleets(admiral, claude.dock);
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
