import { appendFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { crewNote, proveJudge, seedAdHocMember, seedCrew } from './crew';
import { WORK, openPreferences, probeableClaude, addGitHubSource, appendLines, HOSTILE, launchShip, line, mergeFailsInPeek, mergeFromPeek, mooringEnv, received, restCalls, rowLink, seedFleets, seedInboxItem, seedReviewTarget, shipCheckout, setReason, setState, staleMergeTokenInPeek, readyStackMergesFromPeek, viewTab, withFakeGh, seedReviewRequest, chatAnswerFile, chatAsks } from './shell';

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

test('a stop left unanswered through Back leaves the Inbox, and its next stop comes back', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude' });
  await appendLines(admiral, ship, [line.user('Tidy the README'), line.assistant('Done, the README is tidy'), line.turnEnd(2_000)]);
  setReason(admiral, ship, 'stop');
  setState(admiral, ship, 'needs_you');
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');

  await rowLink(page, ship).tap();
  await expect(peekOf(page)).toContainText('Done, the README is tidy');
  await peekOf(page).getByRole('link', { name: 'Back' }).tap();
  await expect(peekOf(page)).toHaveCount(0);
  await expect(rowLink(page, ship)).toHaveCount(0, { timeout: 15_000 });
  const status = async () => {
    const res = await page.request.get(admiral.url + 'api/ships');
    return ((await res.json()) as { ships: { session: string; status: string }[] }).ships.find((s) => s.session === ship.session)?.status;
  };
  expect(await status()).toBe('needs_you');

  setState(admiral, ship, 'working');
  await expect.poll(status).toBe('underway');
  setState(admiral, ship, 'needs_you');
  await expect(rowLink(page, ship)).toBeVisible({ timeout: 15_000 });
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

test('on a portrait iPad Peek replaces the list instead of squeezing beside it, and the PR link has its own space', async ({ page, admiral }) => {
  await page.setViewportSize({ width: 834, height: 1194 });
  await visitorWithReview(admiral);
  await page.goto(admiral.url + 'app/inbox?fleet=alpha');
  await itemRow(page, 'Review the visitor diff').getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toBeVisible();
  await expect(page.getByRole('heading', { name: 'To review' })).toHaveCount(0);
  expect((await peek.boundingBox())!.width).toBeGreaterThan(400);

  const link = await peek.getByRole('link', { name: /acme\/widgets#12/ }).boundingBox();
  const pending = await peek.getByText(/Pending review #123/).boundingBox();
  expect(link!.y + link!.height <= pending!.y || link!.x + link!.width < pending!.x).toBe(true);
  await peek.getByRole('link', { name: 'Back' }).tap();
  await expect(page.getByRole('heading', { name: 'To review' })).toBeVisible();
});

test('Merge in Peek merges a ready PR with one gh call, only after the tap, and the ship sails off', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await mergeFromPeek(page, admiral, { phone: false });
});

test('a merge that gh fails shows its message in Peek and the ship stays moored', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await mergeFailsInPeek(page, admiral);
});

test('a ready stack shows Ready to merge, and Merge in Peek merges the whole stack with one gh stack merge after the tap', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await readyStackMergesFromPeek(page, admiral);
});

test('a Merge the server refuses as 403 says Admiral restarted, offers a reload, and merges nothing', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });

  await staleMergeTokenInPeek(page, admiral);
});

test('Peek answers a crew notes item: keep one note, edit another to repo scope, Send writes only those, and the item folds', async ({ page, admiral }) => {
  seedCrew(admiral);
  await admiral.start();
  seedInboxItem(admiral, '_admiral', {
    id: 'crew-n1',
    kind: 'crew_notes',
    title: 'Crew notes from Dory',
    crew_notes: {
      fleet: 'zulu',
      ship: 'Dory',
      run_date: '2026-11-12',
      notes: [crewNote('n1', 'scout', { role: 'scribe', note: 'Scout dates every entry', replaces: '2026-11-10 (run-a): scout keeps the log short' }), crewNote('n2', 'ada', { note: 'Ada runs make check first', replaces: null })],
    },
  });
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await itemRow(page, 'Crew notes from Dory').getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('from Dory, 2026-11-12');
  await expect(peek).toContainText('Scout dates every entry');
  await expect(peek).toContainText('Replaces:');
  await expect(peek).toContainText('scout keeps the log short');
  await expect(peek).not.toContainText('This ship is gone.');
  await expect(peek.getByRole('link', { name: 'Open bridge' })).toHaveCount(0);
  const card1 = peek.getByRole('region', { name: 'Note card 1' });
  const card2 = peek.getByRole('region', { name: 'Note card 2' });
  const send = peek.getByRole('button', { name: 'Send', exact: true });
  await expect(peek).toContainText('0 of 2 decided');
  await expect(send).toBeDisabled();
  await page.keyboard.press('1');
  await expect(peek).toContainText('0 of 2 decided');

  await card1.getByRole('button', { name: 'Keep', exact: true }).tap();
  await card2.getByRole('button', { name: 'Keep', exact: true }).tap();
  await expect(send).toBeEnabled();
  await card2.getByRole('button', { name: 'Edit', exact: true }).tap();
  await expect(send).toBeDisabled();
  const text = card2.getByRole('textbox', { name: /^Edit note/ });
  await text.fill('');
  await expect(card2.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await text.fill('Ada runs make check before anything');
  await card2.getByRole('radio', { name: 'Repo', exact: true }).or(card2.getByRole('button', { name: 'Repo', exact: true })).tap();
  const repo = card2.getByRole('textbox', { name: /repo/i });
  await repo.fill('acme');
  await expect(card2.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await repo.fill('acme/api');
  await card2.getByRole('button', { name: 'Save', exact: true }).tap();
  await expect(peek).toContainText('2 of 2 decided');
  await expect(send).toBeEnabled();

  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/inbox/_admiral/crew-n1/crew-notes');
  await send.tap();
  expect((await posted).postDataJSON()).toEqual({
    notes: { n1: { decision: 'keep' }, n2: { decision: 'edit', note: 'Ada runs make check before anything', scope: 'repo', repo: 'acme/api' } },
  });
  await expect(peekOf(page)).toHaveCount(0);
  await expect(itemRow(page, 'Crew notes from Dory')).toHaveCount(0, { timeout: 15_000 });
  const memory = join(admiral.home, 'fleets', 'zulu', 'crew', 'memory');
  await expect.poll(() => readFileSync(join(memory, 'scout', 'fleet.md'), 'utf8')).toContain('- 2026-11-12 (Dory): Scout dates every entry');
  expect(readFileSync(join(memory, 'scout', 'fleet.md'), 'utf8')).not.toContain('scout keeps the log short');
  expect(readFileSync(join(memory, 'ada', 'repos', 'acme', 'api.md'), 'utf8')).toContain('- 2026-11-12 (Dory): Ada runs make check before anything');
  expect(readFileSync(join(memory, 'ada', 'fleet.md'), 'utf8')).not.toContain('make check');
});

test('Peek shows an ad hoc member whole, 1 asks to keep it, the confirm adds it to the fleet crew, and the Crew page lists it', async ({ page, admiral }) => {
  seedCrew(admiral);
  const adHoc = seedAdHocMember(admiral, 'deckhand', { id: 'crew-m1', run: 'run-c', notes: [crewNote('n1', 'deckhand', { note: 'Deckhand swabs before it paints' })] });
  const rigger = seedAdHocMember(admiral, 'rigger', { id: 'crew-m3', run: 'run-e', notes: [] });
  await admiral.start();
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await itemRow(page, 'Keep ad hoc member deckhand?').getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('from Dory, 2026-11-12');
  await expect(peek).toContainText('run-c');
  await expect(peek).toContainText('Work as deckhand; swab first.');
  await expect(peek).toContainText('Deckhand swabs before it paints');
  await expect(peek).not.toContainText('This ship is gone.');
  await expect(peek.getByRole('link', { name: 'Open bridge' })).toHaveCount(0);

  let posts = 0;
  page.on('request', (r) => r.method() === 'POST' && posts++);
  await page.keyboard.press('1');
  await expect(peek).toContainText('every later run');
  expect(posts).toBe(0);
  const posted = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/inbox/_admiral/crew-m1/crew-member');
  await peek.getByRole('button', { name: 'Keep deckhand' }).tap();
  expect((await posted).postDataJSON()).toEqual({ decision: 'keep' });
  await expect(peekOf(page)).toHaveCount(0);
  await expect(itemRow(page, 'Kept deckhand')).toBeVisible();
  await expect(itemRow(page, 'Keep ad hoc member deckhand?')).toHaveCount(0, { timeout: 15_000 });
  expect(existsSync(adHoc)).toBe(false);
  expect(readFileSync(join(admiral.home, 'fleets', 'zulu', 'crew', 'memory', 'deckhand', 'fleet.md'), 'utf8')).toContain('Deckhand swabs before it paints');

  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  await expect(page.getByRole('main').getByRole('region', { name: /^Fleet\b/ }).getByRole('listitem').filter({ hasText: 'Pick for the deck.' })).toBeVisible();

  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await expect(itemRow(page, 'Keep ad hoc member rigger?')).toContainText('from Dory');
  await itemRow(page, 'Keep ad hoc member rigger?').getByRole('link').tap();
  await page.keyboard.press('2');
  await expect(peekOf(page)).toContainText('notes are discarded');
  await peekOf(page).getByRole('button', { name: 'Drop rigger' }).tap();
  await expect(itemRow(page, 'Dropped rigger; the ad hoc file was deleted')).toBeVisible();
  expect(existsSync(rigger)).toBe(false);
});

test('a member Keep the server refuses shows its error and leaves the item', async ({ page, admiral }) => {
  seedCrew(admiral);
  const adHoc = seedAdHocMember(admiral, 'deckhand', { id: 'crew-m2', run: 'run-d', notes: [] });
  await admiral.start();
  appendFileSync(adHoc, 'Edited after the run.\n');
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await itemRow(page, 'Keep ad hoc member deckhand?').getByRole('link').tap();
  const peek = peekOf(page);
  await peek.getByRole('button', { name: 'Keep', exact: true }).tap();
  await peek.getByRole('button', { name: 'Keep deckhand' }).tap();
  await expect(peek.getByRole('alert')).toContainText('nothing was changed');
  await expect(peek.getByRole('button', { name: 'Keep', exact: true }).or(peek.getByRole('button', { name: 'Keep deckhand' }))).toBeVisible();
  expect(existsSync(join(admiral.home, 'fleets', 'zulu', 'crew', 'deckhand.md'))).toBe(false);
  expect(existsSync(adHoc)).toBe(true);
  expect(existsSync(join(admiral.home, 'inbox', '_admiral', 'crew-m2.answer.json'))).toBe(false);
});

test('a crew note the server cannot place shows its error on that note card and keeps the decisions', async ({ page, admiral }) => {
  seedCrew(admiral);
  await admiral.start();
  seedInboxItem(admiral, '_admiral', {
    id: 'crew-n2',
    kind: 'crew_notes',
    title: 'Crew notes from Dory',
    crew_notes: { fleet: 'zulu', ship: 'Dory', run_date: '2026-11-12', notes: [crewNote('n1', 'scout', { role: 'scribe', note: 'Scout dates every entry', replaces: '2026-01-01 (run-z): no such line' }), crewNote('n2', 'ada')] },
  });
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await itemRow(page, 'Crew notes from Dory').getByRole('link').tap();
  const peek = peekOf(page);
  const card1 = peek.getByRole('region', { name: 'Note card 1' });
  await card1.getByRole('button', { name: 'Keep', exact: true }).tap();
  await peek.getByRole('region', { name: 'Note card 2' }).getByRole('button', { name: 'Keep', exact: true }).tap();
  await peek.getByRole('button', { name: 'Send', exact: true }).tap();
  await expect(card1.getByRole('alert')).toContainText('nothing was changed');
  await expect(card1.getByRole('button', { name: 'Keep', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(peek).toContainText('2 of 2 decided');
  expect(existsSync(join(admiral.home, 'inbox', '_admiral', 'crew-n2.answer.json'))).toBe(false);

  await card1.getByRole('button', { name: 'Drop', exact: true }).tap();
  await expect(card1.getByRole('alert')).toHaveCount(0);
  await peek.getByRole('button', { name: 'Remove from inbox' }).tap();
  await expect(peek).toContainText('It counts as Drop');
  await expect(peek.getByRole('button', { name: 'Cancel' })).toBeVisible();
});

test("AdmiralBot's waiting question shows in the Inbox as AdmiralBot's, is answered from Peek, leaves the Inbox, and the fleet's chat shows the answer", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  await admiral.start({ env: withFakeGh(admiral) });
  chatAsks(admiral, 'harbor', 'q1', 'Which panel?', ['Freeform', 'Work queue']);
  chatAsks(admiral, 'lagoon', 'q2', 'Which branch?', ['main', 'next']);
  await page.goto(admiral.url + 'app/inbox?fleet=harbor');

  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which panel?' });
  await expect(row).toContainText('AdmiralBot', { timeout: 15_000 });
  await expect(row).toContainText('Harborlight');
  // Chat items are never ships: the Ships surface does not count or list them.
  await page.getByRole('navigation').getByRole('link', { name: 'Ships', exact: true }).tap();
  await expect(page.getByRole('main').getByText('AdmiralBot')).toHaveCount(0);
  await page.goBack();

  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('Which panel?');
  // Withdrawing one chat question is not specified, so Peek offers no Remove.
  await expect(peek.getByRole('button', { name: 'Remove from inbox' })).toHaveCount(0);
  await peek.getByRole('radio', { name: 'Freeform' }).tap();
  await peek.getByRole('button', { name: 'Send answers' }).tap();

  // The answer reaches the harbor chat's question file, and not lagoon's.
  await expect.poll(() => chatAnswerFile(admiral, 'harbor', 'q1')?.answers['1']?.choice).toBe('Freeform');
  expect(chatAnswerFile(admiral, 'lagoon', 'q2')).toBeUndefined();
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
  await expect(page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which panel?' })).toHaveCount(0, { timeout: 15_000 });

  await page.goto(admiral.url + 'app/chat?fleet=harbor');
  await expect(page.getByRole('main')).toContainText('You answered: Freeform');
});

test("a chat question withdrawn while its Peek is open stays open on a plain notice, with Send answers off and Back working", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  await admiral.start({ env: withFakeGh(admiral) });
  chatAsks(admiral, 'harbor', 'q1', 'Which panel?', ['Freeform', 'Work queue']);
  await page.goto(admiral.url + 'app/inbox?fleet=harbor');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which panel?' });
  await expect(row).toContainText('AdmiralBot', { timeout: 15_000 });
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await peek.getByRole('radio', { name: 'Freeform' }).tap();

  // The question is withdrawn behind Peek's back, as New chat does. Whether the Inbox push or a Send finds it first, Peek ends on this notice.
  rmSync(join(admiral.home, 'chat', 'harbor', 'questions', 'q1.json'));
  const send = peek.getByRole('button', { name: 'Send answers' });
  await expect(async () => {
    if (await send.isEnabled()) await send.tap();
    await expect(peek.getByRole('alert')).toHaveText('AdmiralBot withdrew this question.', { timeout: 1_000 });
  }).toPass({ timeout: 15_000 });
  await expect(send).toBeDisabled();
  await expect(peek).not.toContainText('_chat');
  expect(chatAnswerFile(admiral, 'harbor', 'q1')).toBeUndefined();

  await peek.getByRole('link', { name: 'Back' }).tap();
  await expect(peekOf(page)).toHaveCount(0);
  await expect(page).toHaveURL(/\/app\/inbox(\?|$)/);
});

test("a chat question answered elsewhere while its Peek is open stays open on a plain notice and sends nothing", async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  await admiral.start({ env: withFakeGh(admiral) });
  chatAsks(admiral, 'harbor', 'q1', 'Which panel?', ['Freeform', 'Work queue']);
  await page.goto(admiral.url + 'app/inbox?fleet=harbor');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which panel?' });
  await expect(row).toContainText('AdmiralBot', { timeout: 15_000 });
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await peek.getByRole('radio', { name: 'Freeform' }).tap();

  // The chat's own card answers it first.
  const r = await page.request.post(admiral.url + 'api/chat/harbor/questions/q1/answer', { data: { answers: { '1': { choice: 'Work queue' } } } });
  expect(r.ok()).toBe(true);
  await expect(peek.getByRole('alert')).toHaveText('This question was already answered.', { timeout: 15_000 });
  await expect(peek.getByRole('button', { name: 'Send answers' })).toBeDisabled();
  expect(chatAnswerFile(admiral, 'harbor', 'q1')?.answers['1']?.choice).toBe('Work queue');
});

const proposalSeed = (id: string, name: string) => ({
  id,
  kind: 'routine_proposal' as const,
  title: `Run ${name}?`,
  routine_proposal: { fleet: 'zulu', routine: 'r-dismiss', routine_name: name, trigger: id, action: 'launch claude in the proj checkout: go', expires_at: new Date(Date.now() + 3_600_000).toISOString() },
});

test('with Hold fire on, a due routine waits in the Inbox as a proposal, and Launch in its Peek starts one ship', async ({ page, admiral }) => {
  test.setTimeout(150_000);
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { live: true, claude: claude.dock });
  await admiral.start({ env: withFakeGh(admiral) });
  await proveJudge(page, admiral.url);
  const api = (path: string, init?: RequestInit) => fetch(`${admiral.url}api/${path}`, { headers: { 'Content-Type': 'application/json' }, ...init });
  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  const banner = page.getByRole('status').filter({ hasText: 'Hold fire' });
  await expect(banner).toHaveCount(0);
  const holdFire = page.getByRole('main').getByRole('switch', { name: 'Hold fire', exact: true });
  await holdFire.tap();
  await expect(holdFire).toBeChecked();
  await expect(banner).toContainText('you');
  await expect.poll(async () => ((await (await api('fleets/zulu/hold-fire')).json()) as { on: boolean }).on).toBe(true);
  const due = new Date(Math.ceil((Date.now() + 5_000) / 60_000) * 60_000);
  const created = await api('fleets/zulu/routines', {
    method: 'POST',
    body: JSON.stringify({
      name: 'Held sweep', enabled: true, when: `${due.getMinutes()} ${due.getHours()} * * *`, before_acting: 'act',
      do: { launch: { harness: 'claude', dir: join(admiral.home, 'proj'), text: 'go' } },
    }),
  });
  expect([200, 201]).toContain(created.status);

  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Held sweep' });
  await expect(row).toBeVisible({ timeout: 120_000 });
  const shipsNow = async () => ((await (await api('ships')).json()) as { ships: unknown[] }).ships.length;
  expect(await shipsNow()).toBe(0);

  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('Held sweep');
  await expect(peek.getByRole('button', { name: 'Dismiss', exact: true })).toBeVisible();
  await peek.getByRole('button', { name: 'Launch', exact: true }).tap();
  await expect.poll(shipsNow, { timeout: 30_000 }).toBe(1);
  await expect(peekOf(page)).toHaveCount(0);
  await expect(row).toHaveCount(0, { timeout: 15_000 });

  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  await expect(banner).toBeVisible();
  await page.getByRole('main').getByRole('switch', { name: 'Hold fire', exact: true }).tap();
  await expect(banner).toHaveCount(0);
  await expect.poll(async () => ((await (await api('fleets/zulu/hold-fire')).json()) as { on: boolean }).on).toBe(false);

  const all = page.getByRole('main').getByRole('switch', { name: 'Hold fire, all fleets', exact: true });
  await all.tap();
  await expect(all).toBeChecked();
  await expect(banner).toBeVisible();
  await all.tap();
  await expect(all).not.toBeChecked();
  await expect(banner).toHaveCount(0);
});

test('with a plan window over the quota line, a due routine waits in the Inbox as a proposal that says it was held for quota, Usage says why, and Launch in its Peek starts one ship', async ({ page, admiral }) => {
  test.setTimeout(150_000);
  const claude = probeableClaude(admiral);
  // The status-line ship needs the live fake's cue file and record dir, which the probeable dock leaves out.
  seedFleets(admiral, { live: true, claude: { ...claude.dock, env: { ...claude.dock.env, FAKE_RECORD_DIR: join(admiral.home, 'rec'), FAKE_CUE: join(admiral.home, 'cue') } } });
  await admiral.start({ env: withFakeGh(admiral) });
  await proveJudge(page, admiral.url);
  const api = (path: string, init?: RequestInit) => fetch(`${admiral.url}api/${path}`, { headers: { 'Content-Type': 'application/json' }, ...init });
  const shipsNow = async () => ((await (await api('ships')).json()) as { ships: unknown[] }).ships.length;

  // A running Claude ship reports its five-hour window at 93%, over the Work preset's line of 90.
  const reading = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', '..', 'internal', 'cli', 'testdata', 'claude-statusline.json'), 'utf8'));
  reading.rate_limits.five_hour = { used_percentage: 93, resets_at: Math.floor(Date.now() / 1000) + 2 * 3600 };
  reading.rate_limits.seven_day = { used_percentage: 41.2, resets_at: Math.floor(Date.now() / 1000) + 3 * 86400 };
  await launchShip(admiral, { fleet: 'zulu', harness: 'claude', cues: ['statusline ' + JSON.stringify(reading)] });
  const quotaOf = async () => ((await (await fetch(admiral.url + 'api/v1/usage?fleet=zulu')).json()) as { quota: { harness: string; has_data: boolean }[] }).quota.find((q) => q.harness === 'claude');
  await expect.poll(async () => (await quotaOf())?.has_data, { timeout: 30_000 }).toBe(true);
  expect(await shipsNow()).toBe(1);

  const due = new Date(Math.ceil((Date.now() + 5_000) / 60_000) * 60_000);
  const created = await api('fleets/zulu/routines', {
    method: 'POST',
    body: JSON.stringify({
      name: 'Quota sweep', enabled: true, when: `${due.getMinutes()} ${due.getHours()} * * *`, before_acting: 'act',
      do: { launch: { harness: 'claude', dir: join(admiral.home, 'proj'), text: 'go' } },
    }),
  });
  expect([200, 201]).toContain(created.status);

  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Quota sweep' });
  await expect(row).toBeVisible({ timeout: 120_000 });
  await expect(row).toContainText(/held for quota/i);
  expect(await shipsNow()).toBe(1);

  // The fleet's Usage says unattended launches are held, which window, and until when.
  await page.goto(admiral.url + 'app/preferences/zulu/usage');
  const main = page.getByRole('main');
  await expect(main).toContainText(/unattended launches are held/i, { timeout: 30_000 });
  await expect(main).toContainText(/5-hour window/i);
  await expect(main).toContainText(/\buntil\b/i);

  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await expect(peek).toContainText('Quota sweep');
  await expect(peek).toContainText(/held for quota/i);
  await peek.getByRole('button', { name: 'Launch', exact: true }).tap();
  await expect.poll(shipsNow, { timeout: 30_000 }).toBe(2);
  await expect(row).toHaveCount(0, { timeout: 15_000 });
});

test('Dismiss on a routine proposal removes it from the Inbox and starts no ship', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  seedInboxItem(admiral, '_admiral', proposalSeed('routine-t1', 'Nightly sweep'));
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Nightly sweep' });
  await expect(row).toBeVisible({ timeout: 15_000 });
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  const dismiss = peek.getByRole('button', { name: 'Dismiss', exact: true });
  await expect(dismiss).toBeVisible();
  const box = await dismiss.boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(44);
  await dismiss.tap();
  await expect(peekOf(page)).toHaveCount(0);
  await expect(row).toHaveCount(0, { timeout: 15_000 });
  const res = await fetch(`${admiral.url}api/ships`);
  expect(((await res.json()) as { ships: unknown[] }).ships).toEqual([]);
});

test('Peek opens an event proposal with the untrusted summary, Mute rule by sender adds a skip that shows on the Limits page and can be removed, and an A0 card shows in chat marked untrusted', async ({ page, admiral }) => {
  test.setTimeout(150_000);
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { live: true, claude: claude.dock });
  addGitHubSource(admiral);
  // The work preset stops a stranger's event at a card (A0); this fleet lets it reach a proposal.
  const cfg = JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8'));
  cfg.fleets.zulu.limits = { ceilings: { t3: { level: 'A1' } } };
  admiral.writeConfig(cfg);
  const card = { routine: 'r-seeded', routine_name: 'Review watch', tier: 'T3', sender: 'mallory-the-stranger', link: 'https://github.com/acme/api/pull/7', metadata: false, mute_by: ['sender'] };
  const chatDir = join(admiral.home, 'chat', 'zulu');
  mkdirSync(chatDir, { recursive: true });
  const at = new Date().toISOString();
  writeFileSync(join(chatDir, 'conversation.jsonl'), JSON.stringify({ id: 'trig-card', kind: 'event_card', text: 'Mallory asks for a review of a lantern fix.', at, card, logged: at }) + '\n');
  await admiral.start({ env: withFakeGh(admiral) });
  await proveJudge(page, admiral.url);
  // The routine is made through the When picker, which lists the source's event kinds once it has reported them.
  await page.goto(admiral.url + 'app/routines/new?fleet=zulu');
  const form = page.getByRole('main').getByRole('form', { name: 'Routine' });
  await form.getByLabel('Name').fill('Review watch');
  await form.getByRole('radio', { name: 'An event' }).tap();
  const when = form.getByLabel('When');
  await expect(when.locator('option', { hasText: 'Review requested of you' })).toHaveCount(1, { timeout: 30_000 });
  await when.selectOption({ label: 'Review requested of you' });
  // a slip to a schedule and back keeps the event chosen
  await form.getByRole('radio', { name: 'A schedule' }).tap();
  await form.getByRole('radio', { name: 'An event' }).tap();
  await expect(when.locator('option:checked')).toHaveText('Review requested of you');
  await form.getByRole('radio', { name: 'Claude' }).tap();
  await form.getByLabel('Checkout').selectOption(join(admiral.home, 'proj'));
  await form.getByLabel('Text').fill('sweep the board');
  await form.getByRole('radio', { name: 'Ask first' }).tap();
  await form.getByRole('button', { name: 'Save', exact: true }).tap();
  await expect(page).toHaveURL(/\/app\/routines\/(?!new)[^/?]+/);
  const routine = new URL(page.url()).pathname.split('/').pop()!;
  const saved = async () => ((await (await page.request.get(admiral.url + `api/fleets/zulu/routines`)).json()) as { routines: { id: string; on?: { source: string; kind: string }; when?: string }[] }).routines.find((r) => r.id === routine);
  await expect.poll(saved).toMatchObject({ on: { source: 'gh', kind: 'review_requested' } });
  expect((await saved())!.when ?? '').toBe('');
  await page.reload();
  await expect(page.getByRole('main').getByRole('form', { name: 'Routine' }).getByLabel('When')).toHaveValue(/review_requested|Review requested of you/);
  await expect.poll(saved).toMatchObject({ on: { source: 'gh', kind: 'review_requested' } });

  // A real review request from a stranger (not in sender_allowlist, so T3): the first poll baselines, so ask for another.
  seedReviewRequest(admiral, { sender: 'mallory-the-stranger' });
  claude.judge('lookout');
  await expect.poll(async () => {
    await page.request.post(admiral.url + 'api/sources/gh/refresh', { data: {} });
    const runs = (await (await page.request.get(admiral.url + `api/fleets/zulu/routines/${routine}/runs`)).json()) as { runs: { outcome: string }[] | null };
    return (runs.runs ?? []).map((r) => r.outcome);
  }, { timeout: 60_000, intervals: [500, 1000, 2000] }).toContain('proposed');
  await page.goto(admiral.url + `app/routines/${routine}?fleet=zulu`);
  const recent = page.getByRole('region', { name: 'Recent runs' });
  await expect(recent).toContainText('T3');
  await expect(recent).toContainText('mallory-the-stranger');
  await expect(recent).toContainText(/\$0\.0001/);
  await expect(recent).not.toContainText('lantern');
  await expect(recent).toContainText('Proposed');
  await recent.getByRole('link', { name: 'Open the proposal' }).tap();
  const peek = peekOf(page);
  await expect(peek).toBeVisible();
  await expect(peek).toContainText('Untrusted');
  await expect(peek).toContainText('Mallory asks for a review of a lantern fix.');
  await expect(peek).toContainText('Tier T3');
  await peek.getByRole('button', { name: 'Mute rule' }).tap();
  await peek.getByRole('button', { name: /Mute this sender/ }).tap();
  await expect(peek).toContainText('Muted this sender');

  await page.goto(admiral.url + 'app/preferences/zulu/limits');
  const skip = page.getByRole('main').locator('.row-field').filter({ hasText: 'Review watch' });
  await expect(skip.locator('input')).toHaveValue('mallory-the-stranger');
  await expect(skip).not.toContainText(routine);
  const skipped = async () => {
    const body = (await (await page.request.get(admiral.url + 'api/config')).json()) as { fleets: { key: string; limits?: { skip?: { senders?: { value: string; routine?: string }[] } } }[] };
    return body.fleets.find((f) => f.key === 'zulu')?.limits?.skip?.senders ?? [];
  };
  await expect.poll(skipped).toEqual([{ value: 'mallory-the-stranger', routine }]);
  await skip.getByRole('button', { name: /Remove/ }).tap();
  await expect(skip).toHaveCount(0);
  await page.getByRole('button', { name: 'Save', exact: true }).tap();
  await expect.poll(skipped).toEqual([]);

  await page.goto(admiral.url + 'app/chat?fleet=zulu');
  await expect(page.getByRole('main').getByText('Untrusted').first()).toBeVisible();
  await expect(page.getByRole('main')).toContainText('Tier T3');
  await expect(page.getByRole('main')).toContainText('Mallory asks for a review of a lantern fix.');

  // An event launch posts a launch card: Stop, Undo and Mute rule; Undo asks first, removes the clone, and stays undone after a reload.
  const sail = await launchShip(admiral, { fleet: 'zulu', harness: 'claude' });
  const clone = join(admiral.home, 'checkouts', sail.session);
  mkdirSync(clone, { recursive: true });
  writeFileSync(join(clone, 'work.txt'), 'local work');
  const now = new Date().toISOString();
  const launchRecord = { id: 'trig-launch', at: now, routine, outcome: 'launched', tier: 'T1', ship: sail.session, restricted: 'draft_fix', worker_launched: true };
  appendFileSync(join(admiral.home, 'routines', 'zulu', 'triggers.jsonl'), JSON.stringify(launchRecord) + '\n');
  const launchCard = { routine, routine_name: 'Review watch', tier: 'T1', sender: 'alice', link: 'https://github.com/acme/api/pull/7', metadata: false, mute_by: ['sender'], ship: sail.session, level: 'A3', restricted: 'draft_fix' };
  appendFileSync(join(chatDir, 'conversation.jsonl'), JSON.stringify({ id: 'trig-launch', kind: 'launch_card', text: 'Alice asks for a review.', at: now, card: launchCard, logged: now }) + '\n');
  // Undo and Promote exclude each other, so a second Draft fix card carries the Promote flow. proj has no commit and Promote needs a base, so it gets one first.
  const proj = join(admiral.home, 'proj');
  const git = (cwd: string, ...args: string[]) => {
    const r = spawnSync('git', ['-c', 'core.hooksPath=/dev/null', '-c', 'commit.gpgsign=false', ...args], {
      cwd,
      encoding: 'utf8',
      env: { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1', GIT_AUTHOR_NAME: 'Test', GIT_AUTHOR_EMAIL: 'test@example.com', GIT_COMMITTER_NAME: 'Test', GIT_COMMITTER_EMAIL: 'test@example.com' },
    });
    expect(r.status, r.stderr).toBe(0);
    return r.stdout.trim();
  };
  writeFileSync(join(proj, 'base.txt'), 'base\n');
  git(proj, 'add', 'base.txt');
  git(proj, 'commit', '-q', '-m', 'base');
  const second = await launchShip(admiral, { fleet: 'zulu', harness: 'claude' });
  const secondClone = join(admiral.home, 'checkouts', second.session);
  rmSync(secondClone, { recursive: true, force: true });
  git(admiral.home, 'clone', '-q', '--shared', proj, secondClone);
  git(secondClone, 'checkout', '-q', '-b', `admiral/${second.session}`);
  writeFileSync(join(secondClone, 'fix.txt'), 'the fix\n');
  git(secondClone, 'add', 'fix.txt');
  git(secondClone, 'commit', '-q', '-m', 'fix');
  appendFileSync(join(admiral.home, 'routines', 'zulu', 'triggers.jsonl'), JSON.stringify({ ...launchRecord, id: 'trig-promote', ship: second.session }) + '\n');
  appendFileSync(join(chatDir, 'conversation.jsonl'), JSON.stringify({ id: 'trig-promote', kind: 'launch_card', text: 'Bob asks for a fix.', at: now, card: { ...launchCard, sender: 'bob', ship: second.session }, logged: now }) + '\n');
  // The chat log keeps its tail in memory once read, so a seeded line shows after a restart.
  await admiral.restart();
  await expect
    .poll(async () => ((await (await page.request.get(admiral.url + 'api/chat/zulu/session')).json()) as { messages: { kind: string }[] }).messages.map((m) => m.kind))
    .toContain('launch_card');
  await page.goto(admiral.url + 'app/chat?fleet=zulu');
  const shipCard = page.getByRole('main').locator('.chat-card').filter({ hasText: sail.session });
  await expect(shipCard).toContainText('Restricted · Draft fix');
  await expect(shipCard.getByRole('button', { name: 'Stop', exact: true })).toBeVisible();
  await expect(shipCard.getByRole('button', { name: 'Undo', exact: true })).toBeVisible();
  await expect(shipCard.getByRole('button', { name: 'Mute rule' })).toBeVisible();
  await expect(shipCard.getByRole('button', { name: 'Promote', exact: true })).toHaveCount(1);
  await shipCard.getByRole('button', { name: 'Undo', exact: true }).tap();
  await shipCard.getByRole('button', { name: 'Confirm undo', exact: true }).tap();
  await expect(shipCard).toContainText(/undone/i);
  await expect(shipCard.getByRole('button', { name: 'Stop', exact: true })).toHaveCount(0);
  await expect(shipCard.getByRole('button', { name: 'Undo', exact: true })).toHaveCount(0);
  await expect(shipCard.getByRole('button', { name: 'Promote', exact: true })).toHaveCount(0);
  await page.reload();
  const again = page.getByRole('main').locator('.chat-card').filter({ hasText: sail.session });
  await expect(again).toContainText(/undone/i);
  await expect(again.getByRole('button', { name: 'Undo', exact: true })).toHaveCount(0);

  // Promote asks first, starts a normal ship from the restricted ship's work, and the card keeps saying so after a reload.
  const promoteCard = () => page.getByRole('main').locator('.chat-card').filter({ hasText: second.session });
  const promotedShip = async () => {
    const runs = (await (await page.request.get(admiral.url + `api/fleets/zulu/routines/${routine}/runs`)).json()) as { runs: { id: string; promoted_ship?: string }[] | null };
    return (runs.runs ?? []).find((r) => r.id === 'trig-promote')?.promoted_ship ?? '';
  };
  expect(await promotedShip()).toBe('');
  await expect(promoteCard().getByRole('button', { name: 'Promote', exact: true })).toBeVisible();
  await promoteCard().getByRole('button', { name: 'Promote', exact: true }).tap();
  await expect(promoteCard()).toContainText('That ship can push and open PRs.');
  await promoteCard().getByRole('button', { name: 'Confirm promote', exact: true }).tap();
  await expect.poll(promotedShip, { timeout: 30_000 }).not.toBe('');
  const heir = await promotedShip();
  expect(heir).not.toBe(second.session);
  await expect(promoteCard()).toContainText(`Promoted to ${heir}`);
  await expect(promoteCard().getByRole('link', { name: heir })).toBeVisible();
  await expect(promoteCard().getByRole('button', { name: 'Promote', exact: true })).toHaveCount(0);
  await expect(promoteCard().getByRole('button', { name: 'Undo', exact: true })).toHaveCount(0);
  await page.reload();
  await expect(promoteCard()).toContainText(`Promoted to ${heir}`);
  await expect(promoteCard().getByRole('button', { name: 'Promote', exact: true })).toHaveCount(0);
  await expect(promoteCard().getByRole('button', { name: 'Undo', exact: true })).toHaveCount(0);
  expect(existsSync(join(admiral.home, 'checkouts', heir, 'fix.txt'))).toBe(true);
  expect(existsSync(join(admiral.home, 'checkouts', heir, '.admiral', 'untrusted'))).toBe(false);
});
