import { expect, test } from './fixtures';
import { seedCrew } from './crew';
import { watchPage } from './shell';

test('on the phone, a person opens a crew member from the list, reads it in place of the list, goes back, and lands on a not-found line for an unknown name', async ({ page, admiral }) => {
  seedCrew(admiral);
  await admiral.start();
  const watch = watchPage(page, admiral.url);
  const main = page.getByRole('main');
  const fits = () => page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth));

  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Crew', exact: true }).tap();
  const ada = main.getByRole('listitem').filter({ hasText: 'Pick for parser work.' }).last();
  await expect(ada).toBeVisible();
  expect(await fits()).toBe(true);

  await ada.getByRole('link', { name: /\bada\b/ }).or(ada.getByRole('button', { name: /\bada\b/ })).tap();
  await expect(page).toHaveURL(/\/app\/crew\/ada\?(.*&)?fleet=zulu/);
  await expect(main).toContainText('Work as ada.');
  // The detail replaces the list.
  await expect(main.getByText('Pick for rigging.')).toHaveCount(0);
  expect(await fits()).toBe(true);

  await main.getByRole('button', { name: /back/i }).or(main.getByRole('link', { name: /back/i })).tap();
  await expect(page).toHaveURL(/\/app\/crew\?(.*&)?fleet=zulu/);
  await expect(main.getByText('Pick for rigging.')).toBeVisible();

  // A deep link opens the detail, and an unknown name says so with a way back.
  await page.goto(admiral.url + 'app/crew/ada?fleet=zulu');
  await expect(main).toContainText('Work as ada.');
  await page.goto(admiral.url + 'app/crew/nobody?fleet=zulu');
  await expect(main).toContainText(/not found|no member/i);
  await main.getByRole('link', { name: /crew|list|back/i }).tap();
  await expect(page).toHaveURL(/\/app\/crew\?(.*&)?fleet=zulu/);
  await expect(main.getByText('Pick for rigging.')).toBeVisible();

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
