import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOME, WORK, seedFleets, watchRequests } from './shell';

const switcher = (page: Page) => page.locator('[aria-haspopup="menu"]');
const surface = (page: Page, title: string) => page.getByRole('heading', { name: title, exact: true });
const nav = (page: Page, name: string) => page.getByRole('link', { name, exact: true });
const surfaces = ['Inbox', 'Ships', 'Chat', 'Campaigns', 'Routines', 'Crew', 'Preferences'];

async function chooseFleet(page: Page, name: string) {
  await switcher(page).click();
  await page.getByRole('menu').getByRole('menuitemradio', { name }).click();
}

test.describe('the new shell at /app/', () => {
  test('nav, fleet switching and Ships scope to the chosen fleet, over /api/v1 only', { tag: '@critical' }, async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();
    const requests = watchRequests(page);

    await page.goto(admiral.url + 'app/');

    // /app/ lands on the inbox, and the nav lists every surface in order.
    await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
    await expect(surface(page, 'Inbox')).toBeVisible();
    for (const s of surfaces) await expect(nav(page, s)).toBeVisible();
    await expect(page.getByRole('navigation').getByRole('link')).toHaveText(surfaces);

    // The first fleet is active; the menu lists both, with the active one checked.
    await expect(switcher(page)).toContainText(WORK);
    await switcher(page).click();
    const menu = page.getByRole('menu');
    await expect(menu).toBeVisible();
    await expect(menu.getByRole('menuitemradio', { name: WORK })).toHaveAttribute('aria-checked', 'true');
    await expect(menu.getByRole('menuitemradio', { name: HOME })).toHaveAttribute('aria-checked', 'false');
    await expect(menu.getByText('The inbox and shared resources span fleets.')).toBeVisible();
    await page.keyboard.press('Escape');

    // Ships names the active fleet and its docks.
    await nav(page, 'Ships').click();
    await expect(surface(page, 'Ships')).toBeVisible();
    await expect(page).toHaveURL(/\/app\/ships/);
    const main = page.getByRole('main');
    await expect(main).toContainText(WORK);
    const letters = main.locator('.flag-letters');
    await expect(letters.filter({ hasText: 'Cx' })).toBeVisible(); // codex has no logo installed: placeholder letters
    await expect(letters.filter({ hasText: 'Co' })).toHaveCount(0);

    // Switching fleets rescopes Ships without leaving it, and puts the key in the URL.
    await chooseFleet(page, HOME);
    await expect(page).toHaveURL(/\/app\/ships\?(.*&)?fleet=alpha/);
    await expect(main).toContainText(HOME);
    await expect(main).not.toContainText(WORK);
    await expect(letters.filter({ hasText: 'Co' })).toBeVisible(); // copilot's placeholder
    await expect(switcher(page)).toContainText(HOME);

    // ?fleet= survives navigation.
    await nav(page, 'Crew').click();
    await expect(surface(page, 'Crew')).toBeVisible();
    await expect(page).toHaveURL(/\/app\/crew\?(.*&)?fleet=alpha/);

    expect(requests.paths.some((p) => p.startsWith('/api/v1/config'))).toBe(true);
    expect(requests.unversioned()).toEqual([]);
  });

  test('the claude flag loads its logo as an image; a missing logo shows letters', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();
    const logo = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/logos/claude');

    await page.goto(admiral.url + 'app/ships');

    expect((await logo).status()).toBe(200);
    await expect(page.getByRole('main').locator('img[src="/api/v1/logos/claude"]')).toBeVisible();
    // Never inlined: the logo's markup is not in the DOM.
    await expect(page.getByRole('main').locator('svg rect[fill="#D97757"]')).toHaveCount(0);
    await expect(page.getByRole('main').locator('.flag-letters').filter({ hasText: 'Cx' })).toBeVisible();
  });

  test('links are client-side, Back and Forward work, and deep links load the surface', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();

    await page.goto(admiral.url + 'app/crew?fleet=alpha');
    await expect(surface(page, 'Crew')).toBeVisible();
    await expect(page.getByRole('main')).toContainText(HOME);
    await expect(switcher(page)).toContainText(HOME);

    await page.evaluate(() => ((window as unknown as { noReload: boolean }).noReload = true));
    await nav(page, 'Inbox').click();
    await expect(surface(page, 'Inbox')).toBeVisible();
    // The inbox spans every fleet, so it names none.
    await expect(page.getByRole('main')).not.toContainText(HOME);
    await nav(page, 'Chat').click();
    await expect(surface(page, 'Chat')).toBeVisible();

    await page.goBack();
    await expect(surface(page, 'Inbox')).toBeVisible();
    await page.goBack();
    await expect(surface(page, 'Crew')).toBeVisible();
    await page.goForward();
    await expect(surface(page, 'Inbox')).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as { noReload?: boolean }).noReload)).toBe(true);

    // The bare /app redirect keeps ?fleet= and doesn't loop Back.
    await page.goto(admiral.url + 'app?fleet=zulu');
    await expect(page).toHaveURL(/\/app\/inbox\?(.*&)?fleet=zulu/);
    await page.goBack();
    await expect(page).not.toHaveURL(/fleet=zulu/);
  });

  test('an unknown fleet key falls back to the first fleet; the choice is remembered', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();

    await page.goto(admiral.url + 'app/ships?fleet=nope');
    await expect(switcher(page)).toContainText(WORK);

    await chooseFleet(page, HOME);
    await page.goto(admiral.url + 'app/ships');
    await expect(switcher(page)).toContainText(HOME);
  });

  test('the menu opens with the keyboard and Escape returns focus to the switcher', async ({ page, admiral }) => {
    seedFleets(admiral);
    await admiral.start();
    await page.goto(admiral.url + 'app/');

    await switcher(page).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('menu')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('menu')).toBeHidden();
    await expect(switcher(page)).toBeFocused();
  });

  test('nav items meet the 44px touch floor on iPad', async ({ page, admiral }, info) => {
    test.skip(info.project.name !== 'ipad-landscape', 'the touch floor is checked on the iPad landscape project');
    seedFleets(admiral);
    await admiral.start();
    await page.goto(admiral.url + 'app/');

    for (const s of surfaces) {
      const box = await nav(page, s).boundingBox();
      expect(box?.height ?? 0, `${s} nav item height`).toBeGreaterThanOrEqual(44);
    }
  });
});
