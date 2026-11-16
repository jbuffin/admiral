import type { Page } from '@playwright/test';
import { crewNote, seedAdHocMember, seedCrew } from './crew';
import { expect, test } from './fixtures';

const peekOf = (page: Page) => page.getByRole('region', { name: 'Peek' });
const listRow = (page: Page, text: string) => page.getByRole('main').getByRole('listitem').filter({ hasText: text });

test('on the phone, a crew member item shows its whole definition and Keep and Drop stay reachable', async ({ page, admiral }) => {
  seedCrew(admiral);
  seedAdHocMember(admiral, 'deckhand', { id: 'crew-m1', run: 'run-c', notes: [crewNote('n1', 'deckhand', { note: 'Deckhand swabs before it paints' })], lines: 60 });
  await admiral.start();
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await listRow(page, 'Keep ad hoc member deckhand?').getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('Work as deckhand; swab first.');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  const definition = peek.locator('pre').filter({ hasText: 'Work as deckhand; swab first.' });
  expect(await definition.evaluate((el) => el.scrollHeight <= el.clientHeight || ['auto', 'scroll'].includes(getComputedStyle(el).overflowY))).toBe(true);
  const last = peek.getByText('Rule 60 of the deck.');
  await last.scrollIntoViewIfNeeded();
  await expect(last).toBeInViewport();
  for (const name of ['Keep', 'Drop']) {
    const button = peek.getByRole('button', { name, exact: true });
    await button.scrollIntoViewIfNeeded();
    await expect(button).toBeInViewport();
    expect((await button.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  }
  await peek.getByRole('button', { name: 'Drop', exact: true }).tap();
  await expect(peek.getByRole('button', { name: 'Drop deckhand' })).toBeInViewport();
});
