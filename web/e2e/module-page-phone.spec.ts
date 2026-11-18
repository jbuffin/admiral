import { expect, test } from './fixtures';
import { entry, expectRowsDrawn, seedModule } from './modulePage';

test('on the phone a module sits under More after Crew, and a row opens as its own screen with a way back', async ({ page, admiral }) => {
  seedModule(admiral);
  await admiral.start();
  await page.goto(admiral.url + 'app/ships?fleet=zulu');

  await page.getByRole('button', { name: 'More', exact: true }).tap();
  const more = page.getByRole('dialog', { name: 'More' });
  await expect(more.getByRole('link')).toHaveText([/^Campaigns/, /^Routines/, /^Crew/, /^Deploys\s*2$/, /^Preferences/]);
  await entry(more).tap();
  await expect(more).toBeHidden();
  await expect(page).toHaveURL(/\/app\/deploys(\?|$)/);
  await expectRowsDrawn(page);

  // The filter is on the phone too, above the groups.
  await expect(page.getByRole('searchbox', { name: /filter/i })).toBeVisible();

  await page.getByRole('button', { name: 'Open Fix login' }).tap();
  await expect(page).toHaveURL(/\/app\/deploys\/t1(\?|$)/);
  await expect(page.getByText('Code review')).toBeVisible();
  await expect(page.getByRole('complementary')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Open Rotate keys' })).toBeHidden();

  await page.getByRole('button', { name: 'Back', exact: true }).tap();
  await expect(page).toHaveURL(/\/app\/deploys(\?|$)/);
  await expect(page.getByRole('button', { name: 'Open Fix login' })).toBeVisible();
});

test('on the phone a row with three built-in actions keeps its title and wraps the actions inside the screen', async ({ page, admiral }) => {
  seedModule(admiral, { chat: true });
  await admiral.start();
  await page.goto(admiral.url + 'app/deploys');
  await expectRowsDrawn(page);

  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Fix login' });
  const title = await row.locator('.orow-title').boundingBox();
  expect(title?.width ?? 0).toBeGreaterThan(60);
  const width = page.viewportSize()!.width;
  for (const button of await row.locator('.mact').all()) {
    const box = await button.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
});
