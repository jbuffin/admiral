import { appendFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';
import { proveJudge } from './crew';
import { probeableClaude, addGitHubSource, appendLines, launchShip, line, received, rowLink, seedFleets, seedInboxItem, seedReviewTarget, setReason, setState, withFakeGh, seedReviewRequest, chatAnswerFile, chatAsks, chatSays } from './shell';

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

const itemRow = (page: Page, title: string) => page.getByRole('main').getByRole('listitem').filter({ hasText: title });

async function visitorWithReview(admiral: Parameters<typeof seedReviewTarget>[0]) {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const visitor = await launchShip(admiral, { fleet: 'alpha', harness: 'codex' });
  setState(admiral, visitor, 'working');
  seedInboxItem(admiral, visitor.session, { id: 'r-diff', kind: 'review', title: 'Review the visitor diff', review: { ...seedReviewTarget(admiral), summary: 'Two small fixes' } });
  return visitor;
}

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

test('a ship with an open Inbox item says so on its view, and Go to Inbox lands on that item in Peek', async ({ page, admiral }) => {
  const visitor = await visitorWithReview(admiral);
  await page.goto(admiral.url + `app/ships/${encodeURIComponent(visitor.session)}?fleet=alpha`);
  await expect(page.getByText(/Waiting on the Inbox/)).toBeVisible();
  await page.getByRole('link', { name: /Go to Inbox/ }).tap();
  await expect(page).toHaveURL(new RegExp(`/app/inbox/${encodeURIComponent(visitor.session)}/item/r-diff`));
  await expect(peekOf(page)).toContainText('Two small fixes');
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
  chatSays(admiral, 'harbor', [line.user('Hello harbor')]);
  await admiral.start({ env: withFakeGh(admiral) });
  // Starting a chat withdraws the fleet's questions, so the chat is running before the question is asked.
  const chat = await page.context().newPage();
  await chat.goto(admiral.url + 'app/chat?fleet=harbor');
  await expect(chat.getByRole('main')).toContainText('Hello harbor');
  await expect(chat.getByRole('main').getByRole('button', { name: 'Open terminal', exact: true })).toHaveCount(1);
  chatAsks(admiral, 'harbor', 'q1', 'Which panel?', ['Freeform', 'Work queue']);
  await expect(chat.getByRole('main')).toContainText('Which panel?');
  await page.goto(admiral.url + 'app/inbox?fleet=harbor');
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Which panel?' });
  await expect(row).toContainText('AdmiralBot', { timeout: 15_000 });
  await row.getByRole('link').tap();
  const peek = peekOf(page);
  await peek.getByRole('radio', { name: 'Freeform' }).tap();

  // New chat withdraws the question; the Inbox push tells the open Peek.
  await chat.getByRole('main').getByRole('button', { name: 'New chat', exact: true }).tap();
  await expect(chat.getByRole('main').getByText(/^New chat · /)).toHaveCount(1);
  const send = peek.getByRole('button', { name: 'Send answers' });
  await expect(peek.getByRole('alert')).toHaveText('AdmiralBot withdrew this question.', { timeout: 15_000 });
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
  // The reviewer's Check and reason are model text over outside text: they render as plain text, never as markup.
  const hostile = '<img src=x onerror="window.__pwned=1"> lantern-sentinel';
  const blocked = { ...card, routine_name: 'Release watch', verdict: 'block', check_id: '0bbdf84d', check: `Never launch on release workflow edits ${hostile}`, reviewer_reason: `the diff edits release.yml ${hostile}` };
  appendFileSync(join(chatDir, 'conversation.jsonl'), JSON.stringify({ id: 'trig-block', kind: 'event_card', text: 'Mallory asks for a release edit.', at, card: blocked, logged: at }) + '\n');
  const askedId = 'event-trig-ask';
  seedInboxItem(admiral, '_admiral', {
    id: askedId, kind: 'event_proposal', title: 'Run Ask watch?',
    event_proposal: { fleet: 'zulu', routine: 'r-ask', routine_name: 'Ask watch', trigger: 'trig-ask', tier: 'T3', expires_at: new Date(Date.now() + 3_600_000).toISOString() },
  });
  mkdirSync(join(admiral.home, 'inbox-events'), { recursive: true });
  writeFileSync(join(admiral.home, 'inbox-events', `${askedId}.json`), JSON.stringify({
    summary: 'Mallory asks for a release edit.', title: 'Release edit', sender: 'mallory-the-stranger', link: 'https://github.com/acme/api/pull/8', metadata: false, mute_by: ['sender'],
    verdict: 'ask', check_id: '0bbdf84d', check: 'Never launch on release workflow edits', reviewer_reason: 'touches the release path',
  }));
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

  // A blocked event's A0 card names the Check, its id and the reviewer's reason, as text.
  const blockCard = page.getByRole('main').locator('.chat-card').filter({ hasText: 'Release watch' });
  await expect(blockCard).toContainText('Blocked by Check 0bbdf84d');
  await expect(blockCard).toContainText('Never launch on release workflow edits');
  await expect(blockCard).toContainText('the diff edits release.yml');
  await expect(blockCard).toContainText('lantern-sentinel');
  await expect(blockCard.locator('img')).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __pwned?: number }).__pwned)).toBeUndefined();

  // An asked event's proposal shows why it wasn't launched.
  await page.goto(admiral.url + 'app/inbox?fleet=zulu');
  await page.getByRole('main').getByRole('listitem').filter({ hasText: 'Ask watch' }).getByRole('link').tap();
  const asked = peekOf(page);
  await expect(asked).toContainText('Asked about Check 0bbdf84d');
  await expect(asked).toContainText('Never launch on release workflow edits');
  await expect(asked).toContainText('touches the release path');

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
