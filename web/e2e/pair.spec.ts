import type { Page } from '@playwright/test';
import { expect, test, type Admiral } from './fixtures';

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
});
