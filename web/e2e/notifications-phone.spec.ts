import { expect, test } from './fixtures';
import { seedFleets } from './shell';

test('on the phone, the Notifications page shows its switch and fits the screen', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start();
  await page.goto(admiral.url + 'app/preferences/admiral/notifications');
  await expect(page.getByRole('switch', { name: 'Notifications on this device' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
