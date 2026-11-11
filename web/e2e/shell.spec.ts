import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, seedFleets, watchPage } from './shell';

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
