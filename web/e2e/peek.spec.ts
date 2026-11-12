import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOSTILE, appendLines, launchShip, line, received, rowLink, seedFleets, seedInboxItem, setReason, setState, viewTab, withFakeGh } from './shell';

const peekOf = (page: Page) => page.getByRole('region', { name: 'Peek' });
const peekUrl = (session: string) => new RegExp(`/app/inbox/${encodeURIComponent(session)}(\\?|$)`);

test('Peek opens a stop on its last turn with the answer card and the message box, and answering folds the row and closes Peek', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['bracketedpaste'] });
  const filler = Array.from({ length: 30 }, (_, i) => line.assistant(`Checking part ${i + 1} of the plan`));
  await appendLines(admiral, ship, [
    line.user('An earlier request'),
    line.assistant('An earlier reply'),
    line.turnEnd(5_000),
    line.user('Pick a colour for the banner'),
    ...filler,
    line.ask('toolu_q1', [{ question: 'Which colour?', options: ['Red', 'Green', 'Blue'] }]),
  ]);
  setReason(admiral, ship, 'question');
  setState(admiral, ship, 'needs_you');
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');

  await rowLink(page, ship).tap();
  await expect(page).toHaveURL(peekUrl(ship.session));
  const peek = peekOf(page);
  await expect(peek).toBeVisible();
  await expect(rowLink(page, ship)).toHaveAttribute('aria-current', 'true');

  // Only the last turn, scrolled to the bottom, with the card and the message box.
  await expect(peek).toContainText('Checking part 30 of the plan');
  await expect(peek).not.toContainText('An earlier request');
  await expect(peek).not.toContainText('An earlier reply');
  await expect(peek.getByRole('button', { name: /load earlier/i })).toHaveCount(0);
  await expect(peek.getByRole('button', { name: 'Green', exact: true })).toBeInViewport();
  await expect(peek.getByText('Checking part 30 of the plan')).toBeInViewport();
  await expect(peek.getByRole('textbox', { name: 'Message' })).toBeVisible();
  await expect(peek.getByRole('link', { name: 'Open bridge' })).toBeVisible();

  await peek.getByRole('button', { name: 'Green', exact: true }).tap();
  await expect.poll(() => received(admiral, ship)).toBe('2');
  await expect(page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name })).toContainText('Back underway', { timeout: 15_000 });
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('main').getByRole('listitem').filter({ hasText: ship.name })).toHaveCount(0, { timeout: 15_000 });
});

test('Peek answers a question item: choices and a note go to the answer route, the answer file is written, the row leaves', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  seedInboxItem(admiral, visitor.session, {
    id: 'q-port',
    kind: 'question',
    title: 'Which port should the visitor use?',
    question: {
      questions: [
        { id: 'p', text: `Which port? ${HOSTILE}`, options: [{ label: '80' }, { label: '8080', description: 'unprivileged' }], recommended: '8080' },
        { id: 'f', text: 'Which flags?', multi: true, options: [{ label: '--tls' }, { label: '--gzip' }] },
      ],
    },
  });
  seedInboxItem(admiral, visitor.session, { id: 'r-diff', kind: 'review', title: 'Review the visitor diff', review: {} });
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which port should the visitor use?' });
  await expect(row).toContainText(visitor.name, { timeout: 15_000 });

  // A review item has no form yet: its Peek says it is answered from the bridge.
  const reviewRow = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Review the visitor diff' });
  await expect(reviewRow).toBeVisible({ timeout: 15_000 });
  await reviewRow.getByRole('link').tap();
  await expect(peekOf(page)).toContainText('Answered from the bridge for now');
  await expect(peekOf(page).getByRole('button', { name: 'Send answers' })).toHaveCount(0);
  await peekOf(page).getByRole('link', { name: 'Back' }).tap();
  await expect(peekOf(page)).toHaveCount(0);

  await row.getByRole('link').tap();
  await expect(page).toHaveURL(new RegExp(`/app/inbox/${encodeURIComponent(visitor.session)}/item/q-port`));

  const peek = peekOf(page);
  await expect(peek).toContainText('Which port?');
  await expect(peek).toContainText(HOSTILE);
  await expect(peek).toContainText('Recommended');
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  // One answer form at a time: this ship's own card is not shown above a file item.
  await expect(peek.getByRole('button', { name: 'Allow once' })).toHaveCount(0);

  const send = peek.getByRole('button', { name: 'Send answers' });
  await expect(peek).toContainText('0 of 2 answered');
  await expect(send).toBeDisabled();
  await peek.getByRole('radio', { name: /8080/ }).tap();
  await peek.getByRole('checkbox', { name: '--tls' }).tap();
  await peek.getByRole('checkbox', { name: '--gzip' }).tap();
  await peek.getByRole('textbox', { name: 'Note (optional)' }).fill('thanks');
  await expect(peek).toContainText('2 of 2 answered');

  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/inbox/${visitor.session}/q-port/answer`);
  await send.tap();
  expect((await posted).postDataJSON()).toEqual({ answers: { p: { choice: '8080' }, f: { choice: ['--tls', '--gzip'] } }, note: 'thanks' });

  const answerFile = join(admiral.home, 'inbox', visitor.session, 'q-port.answer.json');
  await expect.poll(() => existsSync(answerFile)).toBe(true);
  expect(readFileSync(answerFile, 'utf8')).toContain('8080');
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  // The fold keeps only the ship and the confirmation, so the row is found by the confirmation.
  const folded = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Answered' });
  await expect(folded).toContainText(visitor.name);
  await expect(folded).toHaveCount(0, { timeout: 15_000 });
});

test('a refused question item answer shows the error: 409 locks Send, 400 keeps the form editable', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  seedInboxItem(admiral, visitor.session, {
    id: 'q-port',
    kind: 'question',
    title: 'Which port should the visitor use?',
    question: { questions: [{ id: 'p', text: 'Which port should the visitor use?', options: [{ label: '80' }, { label: '8080' }] }] },
  });
  await page.goto(admiral.url + `app/inbox/${encodeURIComponent(visitor.session)}/item/q-port?fleet=alpha`);
  const peek = peekOf(page);
  const send = peek.getByRole('button', { name: 'Send answers' });
  await expect(peek).toContainText('Which port should the visitor use?');
  await peek.getByRole('radio', { name: '80', exact: true }).tap();

  let status = 400;
  await page.route('**/api/v1/inbox/*/q-port/answer', (route) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ error: status === 400 ? 'that answer is not valid' : 'already answered' }) }));
  await send.tap();
  await expect(peek.getByRole('alert')).toContainText('that answer is not valid');
  await expect(send).toBeEnabled();

  status = 409;
  await send.tap();
  await expect(peek.getByRole('alert')).toContainText('already answered');
  await expect(send).toBeDisabled();
  expect(existsSync(join(admiral.home, 'inbox', visitor.session, 'q-port.answer.json'))).toBe(false);
});

test('Back leaves Peek for the list, and Open bridge opens the ship on its Conversation tab in the ship’s own fleet', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  seedInboxItem(admiral, visitor.session, {
    id: 'q-port',
    kind: 'question',
    title: 'Which port should the visitor use?',
    question: { questions: [{ id: 'p', text: 'Which port should the visitor use?', options: [{ label: '80' }, { label: '8080' }] }] },
  });
  // The active fleet is zulu; the ship's own is alpha.
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: visitor.name });
  await expect(row).toContainText('Which port should the visitor use?', { timeout: 15_000 });
  await row.getByRole('link').tap();
  await expect(peekOf(page)).toBeVisible();

  await page.goBack();
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(peekOf(page)).toHaveCount(0);

  await row.getByRole('link').tap();
  await peekOf(page).getByRole('link', { name: 'Open bridge' }).tap();
  await expect(page).toHaveURL(new RegExp(`/app/ships/${encodeURIComponent(visitor.session)}\\?(.*&)?fleet=alpha`));
  await expect(viewTab(page, 'Conversation')).toHaveAttribute('aria-selected', 'true');
});
