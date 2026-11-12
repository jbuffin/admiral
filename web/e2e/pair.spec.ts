import type { Page } from '@playwright/test';
import { networkInterfaces } from 'node:os';
import { expect, test, type Admiral } from './fixtures';

const hasLan = Object.values(networkInterfaces()).some((l) => l?.some((i) => i.family === 'IPv4' && !i.internal));
/** The desktop and iPad sidebar's Preferences, then Install app in the all-fleets group. */
async function openInstallApp(page: Page) {
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Preferences' }).click();
  await page.getByRole('navigation', { name: 'Preferences' }).getByRole('group', { name: 'Admiral · all fleets' }).getByRole('link', { name: 'Install app', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Install app', exact: true })).toBeVisible();
}

const ipadSafari = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';

// Minting voids the earlier code, so each pairing mints its own.
const mint = async (admiral: Admiral) => ((await (await fetch(`${admiral.url}api/v1/pairing-codes`, { method: 'POST' })).json()) as { code: string }).code;

const pairByTyping = async (page: Page, admiral: Admiral) => {
  await page.getByRole('textbox', { name: /code/i }).fill(await mint(admiral));
  await page.getByRole('button', { name: 'Pair', exact: true }).click();
  // The shell's Inbox heading is already in the page under the Pair screen: wait for the reload to remove it.
  await expect(page.getByRole('textbox', { name: /code/i })).toBeHidden();
};

// The fixture's own device is named e2e; the other one is the page's.
const pagesDevice = async (admiral: Admiral) => {
  const { devices } = (await (await fetch(`${admiral.url}api/v1/devices`)).json()) as { devices: { id: string; name: string }[] };
  return devices.find((d) => d.name !== 'e2e');
};

test.describe('pairing an unpaired device', () => {
  test.skip(!hasLan, 'needs a LAN address');

  test('an unpaired app opens on Pair this app, refuses a wrong code, pairs by typed code, and says signed out once revoked', { tag: '@critical' }, async ({ page, admiral }) => {
    await admiral.start({ lan: true });
    await page.goto(admiral.lanUrl + 'app/');
    await expect(page.getByRole('heading', { name: 'Pair this app', exact: true })).toBeVisible();
    const field = page.getByRole('textbox', { name: /code/i });
    await expect(field).toHaveAttribute('autocapitalize', 'characters');

    await field.fill('ZZZZ-ZZZZ');
    await page.getByRole('button', { name: 'Pair', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('wrong, used or expired');
    await expect(field).toHaveValue('ZZZZ-ZZZZ');

    await pairByTyping(page, admiral);
    await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Pair this app' })).toBeHidden();
    // The device cookie stays out of reach of the page's script.
    expect(await page.evaluate(() => document.cookie)).toBe('');

    const device = await pagesDevice(admiral);
    expect(device).toBeDefined();
    const revoked = await fetch(`${admiral.url}api/v1/devices/${device!.id}`, { method: 'DELETE' });
    expect(revoked.ok).toBe(true);

    await page.reload();
    await expect(page.getByRole('heading', { name: 'This device was signed out', exact: true })).toBeVisible();
    await expect(page.getByRole('textbox', { name: /code/i })).toBeVisible();

    await pairByTyping(page, admiral);
    await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();
  });

  test('a paired device whose server stops sees Can\'t reach your Mac, never Pair this app', async ({ page, admiral }) => {
    await admiral.start({ lan: true });
    await page.goto(admiral.lanUrl + 'app/');
    await pairByTyping(page, admiral);
    await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();

    await admiral.stopServer();
    await expect(page.getByRole('heading', { name: "Can't reach your Mac" })).toBeVisible();
    await expect(page.getByRole('heading', { name: /Pair this app|signed out/ })).toBeHidden();
  });

  test('iPad Safari gets the Add to Home Screen sheet once after pairing and keeps its steps in Install app; the installed app gets neither', async ({ browser, admiral }) => {
    await admiral.start({ lan: true });
    const open = async (standalone: boolean) => {
      const context = await browser.newContext({ userAgent: ipadSafari, hasTouch: true, viewport: { width: 1194, height: 834 } });
      await context.addInitScript((s) => {
        Object.defineProperty(navigator, 'maxTouchPoints', { value: 5 });
        if (s) Object.defineProperty(navigator, 'standalone', { value: true });
      }, standalone);
      return { context, page: await context.newPage() };
    };
    const sheet = (page: Page) => page.getByRole('dialog', { name: /home screen/i });

    const safari = await open(false);
    try {
      await safari.page.goto(admiral.lanUrl + 'app/');
      await pairByTyping(safari.page, admiral);
      await expect(sheet(safari.page)).toBeVisible();
      await safari.page.keyboard.press('Escape');
      await expect(sheet(safari.page)).toBeHidden();
      await safari.page.reload();
      await expect(safari.page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();
      await expect(sheet(safari.page)).toBeHidden();
      await openInstallApp(safari.page);
      await expect(safari.page.getByRole('main').getByText('Tap Share, then Add to Home Screen.')).toBeVisible();
    } finally {
      await safari.context.close();
    }

    const installed = await open(true);
    try {
      await installed.page.goto(admiral.lanUrl + 'app/');
      await pairByTyping(installed.page, admiral);
      await expect(installed.page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();
      await expect(sheet(installed.page)).toBeHidden();
      await openInstallApp(installed.page);
      await expect(installed.page.getByText('Admiral is installed on this device.')).toBeVisible();
    } finally {
      await installed.context.close();
    }
  });
});

test('Preferences → Install app installs where the browser offers it, and nothing floats over the shell', async ({ page, admiral }) => {
  await admiral.start();
  await page.goto(admiral.url + 'app/');
  await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();

  await openInstallApp(page);
  await expect(page.getByText("This browser isn't offering to install Admiral.")).toBeVisible();

  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Inbox' }).click();
  await page.evaluate(() => {
    const e = new Event('beforeinstallprompt', { cancelable: true });
    Object.assign(e, { prompt: async () => ((window as unknown as { prompted: boolean }).prompted = true), userChoice: Promise.resolve({ outcome: 'accepted' }) });
    window.dispatchEvent(e);
  });
  await expect(page.getByRole('heading', { name: 'Inbox', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Install', exact: true })).toBeHidden();

  await openInstallApp(page);
  await page.getByRole('main').getByRole('button', { name: 'Install', exact: true }).click();
  expect(await page.evaluate(() => (window as unknown as { prompted?: boolean }).prompted)).toBe(true);
  await expect(page.getByText('Admiral is installed on this device.')).toBeVisible();
});
