import { expect, test } from './fixtures';
import { HOME, WORK, seedFleets, watchRequests } from './shell';

// The phone project only (its testMatch picks up files ending in phone.spec.ts).
test.describe('the new shell on the phone', () => {
  test('the header switcher opens a Fleets sheet that switches fleets; the tab bar is Inbox, Chat, Ships, More', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();
    const requests = watchRequests(page);

    await page.goto(admiral.url + 'app/');
    await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);

    // The tab bar: three surfaces and More, in order.
    const tabs = page.getByRole('link', { name: /^(Inbox|Chat|Ships)$/ });
    await expect(tabs).toHaveText(['Inbox', 'Chat', 'Ships']);
    await expect(page.getByRole('button', { name: 'More', exact: true })).toBeVisible();

    // Touch floor: every tab is at least 44px tall.
    for (const el of await tabs.all()) expect((await el.boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);
    expect((await page.getByRole('button', { name: 'More', exact: true }).boundingBox())?.height ?? 0).toBeGreaterThanOrEqual(44);

    await page.getByRole('link', { name: 'Ships', exact: true }).tap();
    await expect(page.getByRole('main')).toContainText(WORK);

    // Tapping the fleet name opens the sheet.
    const trigger = page.getByRole('banner').locator('[aria-haspopup="dialog"]');
    await expect(trigger).toContainText(WORK);
    await trigger.tap();
    const sheet = page.getByRole('dialog', { name: 'Fleets' });
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole('menuitemradio', { name: WORK })).toHaveAttribute('aria-checked', 'true');
    await expect(sheet.getByText('The inbox and shared resources span fleets.')).toBeVisible();
    await sheet.getByRole('menuitemradio', { name: HOME }).tap();

    await expect(sheet).toBeHidden();
    await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);
    await expect(page.getByRole('main')).toContainText(HOME);
    await expect(page.getByRole('main')).not.toContainText(WORK);
    expect(requests.unversioned()).toEqual([]);
  });

  test('More reaches Crew, Campaigns, Routines and Preferences', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();
    await page.goto(admiral.url + 'app/');

    await page.getByRole('button', { name: 'More', exact: true }).tap();
    for (const s of ['Campaigns', 'Routines', 'Crew', 'Preferences']) {
      await expect(page.getByRole('link', { name: s, exact: true })).toBeVisible();
    }
    await page.getByRole('link', { name: 'Crew', exact: true }).tap();

    await expect(page).toHaveURL(/\/app\/crew/);
    await expect(page.getByRole('heading', { name: 'Crew', exact: true })).toBeVisible();
    await expect(page.getByRole('main')).toContainText(WORK);
  });
});

test.describe('the new shell on the phone: sheet backdrops', () => {
  // A tap on the dimmed area closes the sheet and must not reach what sits under it.
  test('tapping a sheet backdrop over the fleet trigger only closes the sheet', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();
    await page.goto(admiral.url + 'app/crew');
    const trigger = page.getByRole('banner').locator('[aria-haspopup="dialog"]');
    const box = (await trigger.boundingBox())!;
    const at = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    const fleets = page.getByRole('dialog', { name: 'Fleets' });
    const more = page.getByRole('dialog', { name: 'More' });

    await page.getByRole('button', { name: 'More', exact: true }).tap();
    await expect(more).toBeVisible();
    await page.touchscreen.tap(at.x, at.y);
    await expect(more).toBeHidden();
    await expect(fleets).toBeHidden();
    await expect(page).toHaveURL(/\/app\/crew/);

    await trigger.tap();
    await expect(fleets).toBeVisible();
    await page.touchscreen.tap(at.x, at.y);
    await expect(fleets).toBeHidden();
    await expect(page).toHaveURL(/\/app\/crew/);
  });
});
