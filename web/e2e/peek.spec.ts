import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { HOSTILE, appendLines, launchShip, line, received, restCalls, rowLink, seedFleets, seedInboxItem, seedReviewTarget, setReason, setState, viewTab, withFakeGh } from './shell';

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
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which port should the visitor use?' });
  await expect(row).toContainText(visitor.name, { timeout: 15_000 });

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
  const file = JSON.parse(readFileSync(join(admiral.home, 'inbox', visitor.session, 'q-port.answer.json'), 'utf8'));
  expect(file.removed).toBe(true);
  expect(file.answers).toBeUndefined();
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

const itemRow = (page: Page, title: string) => page.getByRole('main').getByRole('listitem').filter({ hasText: title });

async function visitorWithReview(admiral: Parameters<typeof seedReviewTarget>[0]) {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  seedInboxItem(admiral, visitor.session, { id: 'r-diff', kind: 'review', title: 'Review the visitor diff', review: { ...seedReviewTarget(admiral), summary: 'Two small fixes' } });
  return visitor;
}

test('Peek submits a review item as COMMENT through the confirm: submit goes to the route, GitHub gets the event, the row folds and leaves', async ({ page, admiral }) => {
  const visitor = await visitorWithReview(admiral);
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  const row = itemRow(page, 'Review the visitor diff');
  await expect(row).toContainText(visitor.name, { timeout: 15_000 });
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek.getByRole('link', { name: /acme\/widgets#12/ })).toBeVisible();
  await expect(peek).toContainText('123');
  await expect(peek).not.toContainText('Answered from the bridge');

  await peek.getByRole('button', { name: 'Submit', exact: true }).tap();
  await peek.getByRole('group', { name: 'Submit the review as' }).getByRole('button', { name: /COMMENT/ }).tap();
  await expect(peek).toContainText('posted to the PR as you');
  expect(restCalls(admiral)).toEqual([]);

  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/inbox/${visitor.session}/r-diff/submit`);
  await peek.getByRole('button', { name: 'Submit COMMENT' }).tap();
  expect((await posted).postDataJSON()).toEqual({ event: 'COMMENT', edited: false });
  await expect.poll(() => restCalls(admiral)).toEqual(['GET repos/acme/widgets/pulls/12/reviews/123', 'POST repos/acme/widgets/pulls/12/reviews/123/events']);

  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(itemRow(page, 'Review submitted: COMMENT')).toContainText(visitor.name);
  await expect(itemRow(page, 'Review submitted: COMMENT')).toHaveCount(0, { timeout: 15_000 });
});

test('Peek discards a review item after a confirm that says the draft comments are deleted from GitHub', async ({ page, admiral }) => {
  const visitor = await visitorWithReview(admiral);
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  await itemRow(page, 'Review the visitor diff').getByRole('link').tap();
  const peek = peekOf(page);
  await peek.getByRole('button', { name: 'Discard', exact: true }).tap();
  await expect(peek).toContainText('deleted from GitHub');
  expect(restCalls(admiral)).toEqual([]);

  await peek.getByRole('button', { name: 'Discard review' }).tap();
  await expect.poll(() => restCalls(admiral)).toEqual(['DELETE repos/acme/widgets/pulls/12/reviews/123']);
  await expect(peekOf(page)).toHaveCount(0);
  await expect(itemRow(page, 'Review discarded')).toContainText(visitor.name);
  await expect(itemRow(page, 'Review discarded')).toHaveCount(0, { timeout: 15_000 });
});

test('a review action GitHub refuses shows the error and keeps the actions; one already answered locks them', async ({ page, admiral }) => {
  const visitor = await visitorWithReview(admiral);
  await page.goto(admiral.url + `app/inbox/${encodeURIComponent(visitor.session)}/item/r-diff?fleet=alpha`);
  const peek = peekOf(page);
  const submit = peek.getByRole('button', { name: 'Submit', exact: true });
  await expect(submit).toBeVisible();

  let refusal = { status: 502, error: 'github said no' };
  await page.route('**/api/v1/inbox/*/r-diff/submit', (route) => route.fulfill({ status: refusal.status, contentType: 'application/json', body: JSON.stringify({ error: refusal.error }) }));
  const confirmed = async () => {
    await submit.tap();
    await peek.getByRole('group', { name: 'Submit the review as' }).getByRole('button', { name: /APPROVE/ }).tap();
    await peek.getByRole('button', { name: 'Submit APPROVE' }).tap();
  };
  await confirmed();
  await expect(peek.getByRole('alert')).toContainText('github said no');
  await expect(peek.getByRole('button', { name: 'Submit APPROVE' })).toBeEnabled();

  refusal = { status: 409, error: 'another action on this item is still running' };
  await peek.getByRole('button', { name: 'Submit APPROVE' }).tap();
  await expect(peek.getByRole('alert')).toContainText('still running');
  await expect(peek.getByRole('button', { name: 'Submit APPROVE' })).toBeEnabled();

  refusal = { status: 409, error: 'inbox item is already answered' };
  await peek.getByRole('button', { name: 'Submit APPROVE' }).tap();
  await expect(peek.getByRole('alert')).toContainText('already answered');
  await expect(peek.getByRole('button', { name: 'Submit APPROVE' })).toBeDisabled();
  await expect(peek.getByRole('button', { name: 'Discard', exact: true })).toBeDisabled();
});

test('Peek answers a findings item: decide every card, edit one, Send posts the decisions to the findings route and GitHub is never called', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  const card = (id: string, extra: Record<string, unknown> = {}) => ({ id, source: `reviewer on ${id}`, quote: `Quote for ${id}`, verdict: 'agree', reason: `Reason for ${id}`, proposal: { kind: 'reply', content: `Proposed reply for ${id}` }, evidence: [], ...extra });
  seedInboxItem(admiral, visitor.session, {
    id: 'f-cards',
    kind: 'findings',
    title: 'Review comments to act on',
    findings: { pr_url: 'https://github.com/acme/widgets/pull/12', cards: [card('c1', { quote: HOSTILE }), card('c2'), card('c3')] },
  });
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  await itemRow(page, 'Review comments to act on').getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('Quote for c2');
  await expect(peek).toContainText(HOSTILE);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  const card1 = peek.getByRole('region', { name: 'Finding card 1' });
  const card2 = peek.getByRole('region', { name: 'Finding card 2' });
  const card3 = peek.getByRole('region', { name: 'Finding card 3' });
  const send = peek.getByRole('button', { name: 'Send', exact: true });
  await expect(peek).toContainText('0 of 3 decided');
  await expect(send).toBeDisabled();

  await card1.getByRole('button', { name: 'Approve' }).tap();
  await card3.getByRole('button', { name: 'Reject' }).tap();
  await card2.getByRole('button', { name: 'Edit' }).tap();
  await expect(peek).toContainText('Save or Cancel the open edit first.');
  await expect(send).toBeDisabled();
  await peek.getByRole('textbox', { name: 'Edit the proposal for card 2' }).fill('My own reply');
  await peek.getByRole('button', { name: 'Save' }).tap();
  await expect(peek).toContainText('3 of 3 decided');
  await expect(send).toBeEnabled();

  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/inbox/${visitor.session}/f-cards/findings`);
  await send.tap();
  expect((await posted).postDataJSON()).toEqual({
    cards: [{ id: 'c1', decision: 'approved' }, { id: 'c2', decision: 'edited', proposal: { kind: 'reply', content: 'My own reply' } }, { id: 'c3', decision: 'rejected' }],
  });
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(itemRow(page, 'approved')).toContainText(visitor.name);
  await expect(itemRow(page, visitor.name)).toHaveCount(0, { timeout: 15_000 });
  expect(restCalls(admiral)).toEqual([]);
});

test('Remove from inbox asks first, then drops a question item without answering it', async ({ page, admiral }) => {
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
  await itemRow(page, 'Which port should the visitor use?').getByRole('link').tap();
  const peek = peekOf(page);
  await peek.getByRole('button', { name: 'Remove from inbox' }).tap();
  await expect(peek).toContainText('The ship never gets an answer from here');

  await peek.getByRole('button', { name: 'Keep it' }).tap();
  await expect(peek.getByRole('button', { name: 'Remove from inbox' })).toBeVisible();
  await peek.getByRole('button', { name: 'Remove from inbox' }).tap();
  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/inbox/${visitor.session}/q-port/remove`);
  await peek.getByRole('button', { name: 'Remove', exact: true }).tap();
  await posted;
  await expect(peekOf(page)).toHaveCount(0);
  await expect(itemRow(page, 'Removed from the inbox')).toContainText(visitor.name);
  await expect(itemRow(page, visitor.name)).toHaveCount(0, { timeout: 15_000 });
  const file = JSON.parse(readFileSync(join(admiral.home, 'inbox', visitor.session, 'q-port.answer.json'), 'utf8'));
  expect(file.removed).toBe(true);
  expect(file.answers).toBeUndefined();
});

test('a review on a ship that is gone shows its own actions and says the ship is gone, never a dead bridge line', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  seedInboxItem(admiral, 'gone-session', { id: 'r-old', kind: 'review', title: 'Review left by a ship that sailed', name: 'Ghost', review: { ...seedReviewTarget(admiral), summary: 'Left behind' } });
  await page.goto(admiral.url + 'app/inbox/gone-session/item/r-old?fleet=alpha');
  const peek = peekOf(page);
  await expect(peek).toContainText('This ship is gone.', { timeout: 15_000 });
  await expect(peek.getByRole('button', { name: 'Submit', exact: true })).toBeVisible();
  await expect(peek.getByRole('button', { name: 'Discard', exact: true })).toBeVisible();
  await expect(peek.getByRole('button', { name: 'Remove from inbox' })).toBeVisible();
  await expect(peek).not.toContainText('Answered from the bridge');
});

test('a notice expands in place and its Dismiss removes it from the list', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  seedInboxItem(admiral, '_admiral', { id: 'n-heads-up', kind: 'notice', title: 'Heads up about the fleet', notice: { body: 'Plain words here' } });
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  const row = itemRow(page, 'Heads up about the fleet');
  await row.getByRole('button').first().tap();
  await expect(row).toContainText('Plain words here');
  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/inbox/_admiral/n-heads-up/remove');
  await row.getByRole('button', { name: 'Dismiss' }).tap();
  await posted;
  await expect(itemRow(page, 'Heads up about the fleet')).toHaveCount(0, { timeout: 15_000 });
});

test('J and K move the selection and open that row’s Peek, Enter opens the bridge, and keys typed in the message box do nothing', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  for (const id of ['q-one', 'q-two']) {
    seedInboxItem(admiral, visitor.session, { id, kind: 'question', title: `Question ${id}`, question: { questions: [{ id: 'p', text: `Text of ${id}`, options: [{ label: 'Yes' }, { label: 'No' }] }] } });
  }
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  await expect(itemRow(page, 'Question q-one')).toBeVisible({ timeout: 15_000 });
  await expect(itemRow(page, 'Question q-two')).toBeVisible();

  await page.keyboard.press('j');
  await expect(peekOf(page)).toBeVisible();
  const first = (await peekOf(page).getByRole('heading', { level: 2 }).locator('xpath=..').innerText()).includes('q-one') ? 'q-one' : 'q-two';
  const second = first === 'q-one' ? 'q-two' : 'q-one';
  await expect(page).toHaveURL(new RegExp(`/item/${first}`));
  await page.keyboard.press('j');
  await expect(page).toHaveURL(new RegExp(`/item/${second}`));
  await page.keyboard.press('k');
  await expect(page).toHaveURL(new RegExp(`/item/${first}`));

  await page.keyboard.press('1');
  await expect(peekOf(page).getByRole('radio', { name: 'Yes' })).toBeChecked();

  await peekOf(page).getByRole('textbox', { name: 'Note (optional)' }).focus();
  await page.keyboard.type('jek');
  await expect(peekOf(page).getByRole('textbox', { name: 'Note (optional)' })).toHaveValue('jek');
  await expect(page).toHaveURL(new RegExp(`/item/${first}`));
  await expect(peekOf(page).getByRole('button', { name: 'Remove', exact: true })).toHaveCount(0);

  await page.locator('body').click({ position: { x: 2, y: 2 } });
  await page.keyboard.press('e');
  await expect(peekOf(page).getByRole('button', { name: 'Remove', exact: true })).toBeVisible();
  await peekOf(page).getByRole('button', { name: 'Keep it' }).tap();

  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(new RegExp(`/app/ships/${encodeURIComponent(visitor.session)}`));
});

test('1 to 3 on a review Peek pick the event and stop at the confirm: nothing is posted until Submit', async ({ page, admiral }) => {
  const visitor = await visitorWithReview(admiral);
  await page.goto(admiral.url + `app/inbox/${encodeURIComponent(visitor.session)}/item/r-diff?fleet=alpha`);
  const peek = peekOf(page);
  await expect(peek.getByRole('button', { name: 'Submit', exact: true })).toBeVisible();
  await page.keyboard.press('3');
  await expect(peek).toContainText('Submit this review as');
  await expect(peek.getByRole('button', { name: 'Submit REQUEST_CHANGES' })).toBeVisible();
  expect(restCalls(admiral)).toEqual([]);
});
