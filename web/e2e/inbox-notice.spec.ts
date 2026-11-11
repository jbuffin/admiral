import { existsSync, mkdirSync, renameSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';

// #259 u2: a notice is a read-only Inbox item the server writes under
// $ADMIRAL_HOME/inbox/_admiral/. It has a title and a plain-text body and one
// action, Dismiss, which removes it without asking. It needs no ship.

const BODY = ['Config migrated. Backup: config.json.pre-dev', '', 'Order from Notes:', '  <b>bold?</b> <img src=x onerror="document.title=1"> & more'].join('\n');

test('a notice shows its body as text with its line breaks, and Dismiss removes it', async ({ page, admiral }) => {
  await admiral.start();
  const dir = join(admiral.home, 'inbox', '_admiral');
  mkdirSync(dir, { recursive: true });
  const tmp = join(dir, '.n-1.json.tmp');
  writeFileSync(tmp, JSON.stringify({ version: 1, id: 'n-1', kind: 'notice', created: new Date().toISOString(), title: 'Admiral updated your config', notice: { body: BODY } }));
  renameSync(tmp, join(dir, 'n-1.json'));

  let dialogs = 0;
  page.on('dialog', (d) => {
    dialogs++;
    void d.dismiss();
  });
  await page.goto(admiral.url);
  await page.getByRole('button', { name: /^Needs you, 1 item/ }).click();
  const inbox = page.getByLabel('Needs you inbox');
  await expect(inbox.getByText('Admiral updated your config')).toBeVisible();

  // The body is text: line breaks kept, markup shown literally, nothing injected.
  const body = inbox.getByText('Order from Notes:');
  await expect(body).toBeVisible();
  expect(await body.evaluate((el) => getComputedStyle(el).whiteSpace)).toMatch(/^pre/);
  expect(await body.evaluate((el) => el.textContent)).toBe(BODY);
  await expect(inbox.getByText('<b>bold?</b>', { exact: false })).toBeVisible();
  await expect(inbox.locator('b, img')).toHaveCount(0);
  expect(await page.title()).not.toBe('1');

  // Dismiss removes it at once, with no confirm step.
  await inbox.getByRole('button', { name: 'Dismiss' }).click();
  await expect(inbox.getByText('Admiral updated your config')).toHaveCount(0);
  await expect(inbox.getByRole('button', { name: /confirm/i })).toHaveCount(0);
  expect(dialogs).toBe(0);
  await expect(page.getByRole('button', { name: 'Needs you, nothing waiting' })).toBeVisible();
  await expect.poll(() => existsSync(join(dir, 'n-1.json'))).toBe(false);
});
