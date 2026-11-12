import { expect, test } from './fixtures';
import { newSubscriber, pushStub, startFakePush } from './push';
import { seedFleets } from './shell';

test.use({ serviceWorkers: 'allow' });

test('on the phone, the Notifications page fits the screen and the switch turns push on for this device', async ({ page, context, admiral }) => {
  seedFleets(admiral, { live: true });
  const fake = await startFakePush(admiral.home);
  try {
    const sub = newSubscriber();
    await admiral.start({ env: { ADMIRAL_PUSH_TEST_ROOTS: fake.rootsFile } });
    await context.addInitScript(pushStub, { endpoint: `${fake.url}/sub/mac`, p256dh: sub.p256dh, auth: sub.authB64 });
    await page.goto(admiral.url + 'app/preferences/admiral/notifications');
    const toggle = page.getByRole('switch', { name: 'Notifications on this device' });
    await expect(toggle).not.toBeChecked();
    await toggle.tap();
    await expect(toggle).toBeChecked();
    await expect(page.getByRole('list', { name: 'Devices' }).getByRole('listitem').filter({ hasText: 'This Mac' })).toContainText('push on');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  } finally {
    await fake.close();
  }
});
