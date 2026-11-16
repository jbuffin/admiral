import type { Page } from '@playwright/test';
import { crewNote, seedAdHocMember, seedCrew } from './crew';
import { expect, test } from './fixtures';
import { appendLines, launchShip, line, received, rowLink, seedFleets, setReason, setState, withFakeGh } from './shell';

const peekOf = (page: Page) => page.getByRole('region', { name: 'Peek' });
const listRow = (page: Page, text: string) => page.getByRole('main').getByRole('listitem').filter({ hasText: text });

test('on the phone, a stop opens in Peek, is answered there, and the list comes back', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  await appendLines(admiral, ship, [line.user('Pick a colour for the banner'), line.ask('toolu_q1', [{ question: 'Which colour?', options: ['Red', 'Green', 'Blue'] }])]);
  setReason(admiral, ship, 'question');
  setState(admiral, ship, 'needs_you');
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');

  await rowLink(page, ship).tap();
  const peek = peekOf(page);
  await expect(peek).toBeVisible();
  await expect(peek.getByRole('textbox', { name: 'Message' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  const green = peek.getByRole('button', { name: 'Green', exact: true });
  await expect(green).toBeInViewport();
  expect((await green.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await green.tap();
  await expect.poll(() => received(admiral, ship)).toBe('2');
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('navigation').getByRole('link', { name: 'Inbox', exact: true })).toBeVisible();
  await expect(listRow(page, ship.name)).toHaveCount(0, { timeout: 15_000 });
});

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
