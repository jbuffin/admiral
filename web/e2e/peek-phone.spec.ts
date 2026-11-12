import type { Page } from '@playwright/test';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';
import { addGitHubSource, appendLines, launchShip, line, mergeFailsInPeek, mergeFromPeek, mooringEnv, received, rowLink, seedFleets, seedInboxItem, setReason, setState, readyStackMergesFromPeek, withFakeGh } from './shell';

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

test('on the phone, a question item opens in Peek and its answers are sent', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  seedInboxItem(admiral, visitor.session, {
    id: 'q-port',
    kind: 'question',
    title: 'Which port should the visitor use?',
    question: { questions: [{ id: 'p', text: 'Which port?', options: [{ label: '80' }, { label: '8080' }] }] },
  });
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  const row = listRow(page, 'Which port should the visitor use?');
  await expect(row).toContainText(visitor.name, { timeout: 15_000 });
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('Which port?');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  const choice = peek.getByRole('radio', { name: '8080' });
  expect((await choice.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await choice.tap();

  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/inbox/${visitor.session}/q-port/answer`);
  await peek.getByRole('button', { name: 'Send answers' }).tap();
  expect((await posted).postDataJSON()).toEqual({ answers: { p: { choice: '8080' } } });
  await expect.poll(() => existsSync(join(admiral.home, 'inbox', visitor.session, 'q-port.answer.json'))).toBe(true);
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(listRow(page, visitor.name)).toHaveCount(0, { timeout: 15_000 });
});

test('on the phone, Merge in Peek merges a ready PR with one gh call, only after the tap, and the ship sails off', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await mergeFromPeek(page, admiral, { phone: true });
});

test('on the phone, a merge that gh fails shows its message in Peek and the ship stays moored', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await mergeFailsInPeek(page, admiral);
});

test('on the phone, a ready stack shows Ready to merge, and Merge in Peek merges the whole stack with one gh stack merge after the tap', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await readyStackMergesFromPeek(page, admiral);
});
