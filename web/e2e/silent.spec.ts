import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';

// u1 c1/c2: a test page's audio ends at a gain-0 node even with sound turned on.
const destinationOf = (page: Page) =>
  page.evaluate(() => {
    const d = new AudioContext().destination;
    return { isGain: d instanceof GainNode, gain: d instanceof GainNode ? d.gain.value : null };
  });

const soundOnThenReload = async (page: Page, url: string) => {
  await page.goto(url + 'app/');
  await page.evaluate(() => localStorage.setItem('admiral.muted', '0'));
  await page.reload();
  await page.locator('body').tap();
};

test.describe('test browsers are silent', () => {
  test('the fixture page ends its audio at a gain-0 node with sound on', async ({ page, admiral }) => {
    await admiral.start();
    await soundOnThenReload(page, admiral.url);
    expect(await destinationOf(page)).toEqual({ isGain: true, gain: 0 });
  });

  test('a page from browser.newContext() ends its audio at a gain-0 node with sound on', async ({ browser, admiral }) => {
    await admiral.start();
    const own = await browser.newContext({ hasTouch: true });
    try {
      const page = await own.newPage();
      await soundOnThenReload(page, admiral.url);
      expect(await destinationOf(page)).toEqual({ isGain: true, gain: 0 });
    } finally {
      await own.close();
    }
  });
});
