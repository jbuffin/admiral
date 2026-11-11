import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, LIVE_ENV, WORK, launchShip, say, seedFleets, setState, watchPage } from './shell';

const tab = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });

test('on the phone, the tab bar and More reach every surface, the Ships tab lists the fleet\'s ships and the Fleets sheet switches fleets', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: LIVE_ENV });
  const watch = watchPage(page, admiral.url);

  const login = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-login' });
  setState(admiral, login, 'needs_you');
  await say(admiral, login, 'Which branch should I rebase onto?');
  const billing = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', order: 'ord-billing' });
  setState(admiral, billing, 'working');
  const moored = await launchShip(admiral, { fleet: 'zulu', harness: 'codex', exit: 3 });

  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Inbox', 'Chat', 'Ships']);

  await tab(page, 'Ships').tap();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toContainText(WORK);

  // The same groups and rows as the wide shell, inside the screen's width.
  const main = page.getByRole('main');
  await expect(main.getByRole('heading', { level: 2 })).toHaveText([/^Needs you\s*1$/, /^Underway\s*1$/, /^Anchored\s*1$/]);
  const row = (ship: { name: string }) => main.getByRole('listitem').filter({ hasText: ship.name });
  await expect(row(login)).toContainText('Fix the login redirect');
  await expect(row(login)).toContainText('Which branch should I rebase onto?');
  await expect(row(login).locator('img[src="/api/v1/logos/claude"]')).toBeVisible();
  await expect(row(moored).locator('img[src="/api/v1/logos/codex"]')).toBeVisible();
  for (const ship of [login, billing, moored]) expect((await row(ship).boundingBox())!.height).toBeGreaterThanOrEqual(44);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

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

  expect(watch.paths).toContain('/ws/v1');
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
