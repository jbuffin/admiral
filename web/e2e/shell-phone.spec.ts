import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, seedFleets, watchPage } from './shell';

const tab = (page: Page, name: string) => page.getByRole('navigation').getByRole('link', { name, exact: true });

test('on the phone, the tab bar and More reach every surface and the Fleets sheet switches fleets', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral);
  await admiral.start();
  const watch = watchPage(page, admiral.url);

  await page.goto(admiral.url + 'app/');
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('navigation').getByRole('link')).toHaveText(['Inbox', 'Chat', 'Ships']);

  await tab(page, 'Ships').tap();
  await expect(page.getByRole('heading', { name: 'Ships', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toContainText(WORK);

  await page.getByRole('banner').getByRole('button', { name: WORK, exact: true }).tap();
  const sheet = page.getByRole('dialog', { name: 'Fleets' });
  await expect(sheet.getByRole('menuitemradio', { name: WORK })).toHaveAttribute('aria-checked', 'true');
  await sheet.getByRole('menuitemradio', { name: HOME }).tap();
  await expect(sheet).toBeHidden();
  await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('main')).toContainText(HOME);
  await expect(page.getByRole('main')).not.toContainText(WORK);

  await page.getByRole('button', { name: 'More', exact: true }).tap();
  const more = page.getByRole('dialog', { name: 'More' });
  await expect(more.getByRole('link')).toHaveText(['Campaigns', 'Routines', 'Crew', 'Preferences']);
  await more.getByRole('link', { name: 'Crew', exact: true }).tap();
  await expect(more).toBeHidden();
  await expect(page).toHaveURL(/\/app\/crew\?(.*&)?fleet=alpha/);
  await expect(page.getByRole('heading', { name: 'Crew', exact: true })).toBeVisible();

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
