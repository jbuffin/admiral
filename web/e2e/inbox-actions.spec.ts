import type { Locator, Page, TestInfo } from '@playwright/test';
import { existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { type Admiral, expect } from './fixtures';
import { FakeGh } from './fakegh';
import { type Sea, type SeaFleet, badge, badgeName, isTouch, phone, test } from './sea';

// Review and findings items in the web inbox (#116 u4). Items are files written after the ship has launched;
// GitHub is the fake gh (e2e/testdata/fakegh), put first on Admiral's PATH. The names below are the contract,
// from the prototype (designs/canvas/Main.dc.html) and the brief:
//
//   review item (an article): its title, the summary, the PR link "<owner>/<repo>#<n>" with the PR's host
//     (github.com) beside it, link "Edit" (new tab at <pr_url>/files), buttons "Submit" and "Discard"
//   Submit: group "Submit the review as" with buttons COMMENT, APPROVE, REQUEST_CHANGES (names start with the
//     event), a "Cancel"; then the text "Submit this review as APPROVE?" with buttons "Submit APPROVE" and "Change"
//   Discard: the text "Discard this pending review?" with buttons "Discard review" and "Keep it"
//   after an action: role status "Review submitted: APPROVE" / "Review discarded" / "Review no longer pending on
//     GitHub" / "Review removed from the inbox; nothing changed on GitHub" / "Removed from the inbox"
//   a failure: role alert holding gh's message as text, the item still open, its buttons usable again
//     and, for a 502/503 on a review, the button "Remove from inbox"
//   findings item (an article): per finding card a group "Decision for card <n>" with buttons Approve, Edit, Reject
//     (aria-pressed on the chosen one); Edit opens textbox "Edit the proposal for card <n>" with buttons Save
//     and Cancel; "<d> of <n> decided"; button "Send" (disabled until every card is decided)
//   folded findings: role status "Sent 1 approved, 1 edited, 1 rejected" (no zero buckets)

const BLUE = '#3B82F6';
const PR = 'https://github.com/o/r/pull/12';
const REVIEW = 'repos/o/r/pulls/12/reviews/123';

const wide = (info: TestInfo) => {
  const v = info.project.use.viewport;
  return !!v && v.width >= 1024 && v.width > v.height;
};
const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());
const panel = (page: Page, info: TestInfo) =>
  phone(info) ? page.getByRole('region', { name: 'Needs you inbox' }) : page.getByRole(wide(info) ? 'complementary' : 'dialog', { name: 'Needs you inbox' });

const minutesAgo = (n: number) => new Date(Date.now() - n * 60_000).toISOString();

/** Writes an item the way Commodore does: a dot-named temp file, then a rename. */
function seed(a: Admiral, session: string, id: string, item: Record<string, unknown>) {
  const dir = join(a.home, 'inbox', session);
  mkdirSync(dir, { recursive: true });
  const tmp = join(dir, `.${id}.json.tmp`);
  writeFileSync(tmp, JSON.stringify({ version: 1, id, ...item }));
  renameSync(tmp, join(dir, `${id}.json`));
}

const review = (min = 5, extra: Record<string, unknown> = {}) => ({
  kind: 'review',
  created: minutesAgo(min),
  title: 'Review PR 12',
  review: { pr_url: PR, review_id: 123, summary: 'looks fine, two nits', ...extra },
});

const findingCard = (id: string, extra: Record<string, unknown> = {}) => ({
  id,
  source: `reviewer-${id}`,
  quote: `quote of ${id}`,
  verdict: 'agree',
  reason: `reason for ${id}`,
  proposal: { kind: 'diff', content: `- old ${id}\n+ new ${id}` },
  evidence: [`main.go:${id.length}`],
  ...extra,
});

const findings = (cards: unknown[], min = 5) => ({
  kind: 'findings',
  created: minutesAgo(min),
  title: 'Feedback on PR 12',
  findings: { pr_url: PR, cards },
});

/** One Admiral with the fake gh first on PATH and one launched ship; items are seeded after the launch. */
async function setup(admiral: Admiral, sea: (fleets: Record<string, SeaFleet>) => Sea) {
  const gh = new FakeGh(admiral.home);
  gh.can('GET', REVIEW, { json: { state: 'PENDING', body: 'the draft body' } });
  gh.can('POST', `${REVIEW}/events`, { json: {} });
  gh.can('DELETE', REVIEW, { json: {} });
  const s = sea({ work: { name: 'Work', color: BLUE } });
  await admiral.start({ env: gh.env() });
  const ship = await s.launch('work', 'alpha');
  return { gh, ship };
}

async function open(page: Page, admiral: Admiral, info: TestInfo, count: number) {
  await page.goto(admiral.url);
  await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, `Needs you, ${count} item${count === 1 ? '' : 's'}`));
  await press(badge(page, info), isTouch(info));
  const inbox = panel(page, info);
  await expect(inbox).toBeVisible();
  return inbox;
}

const dialogs = (page: Page) => {
  const seen: string[] = [];
  page.on('dialog', (d) => {
    seen.push(d.message());
    void d.dismiss();
  });
  return seen;
};

test.describe('a review item', () => {
  test('S1: Submit asks for the event, then confirms; gh gets the event, the item folds and the badge drops', { tag: '@critical' }, async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'r-1', review());
    const inbox = await open(page, admiral, info, 1);

    // The summary, the PR link with its host beside it.
    await expect(inbox.getByText('looks fine, two nits')).toBeVisible();
    const pr = inbox.getByRole('link', { name: /o\/r#12/ });
    await expect(pr).toHaveAttribute('href', PR);
    await expect(pr).toHaveAttribute('target', '_blank');
    await expect(pr).toHaveAttribute('rel', /noopener/);
    await expect(pr).toHaveAttribute('rel', /noreferrer/);
    await expect(inbox.getByText('github.com').first()).toBeVisible();

    const submit = inbox.getByRole('button', { name: 'Submit', exact: true });
    for (const b of [submit, inbox.getByRole('link', { name: /^Edit/ }), inbox.getByRole('button', { name: 'Discard', exact: true })]) {
      expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    }

    await press(submit, touch);
    const choice = inbox.getByRole('group', { name: 'Submit the review as' });
    await expect(choice).toBeVisible();
    for (const e of ['COMMENT', 'APPROVE', 'REQUEST_CHANGES']) await expect(choice.getByRole('button', { name: new RegExp(`^${e}`) })).toBeVisible();
    await press(choice.getByRole('button', { name: /^APPROVE/ }), touch);

    // Nothing has gone to GitHub before the confirm.
    await expect(inbox.getByText('Submit this review as APPROVE?')).toBeVisible();
    expect(gh.calls()).toEqual([]);
    await press(inbox.getByRole('button', { name: 'Submit APPROVE' }), touch);

    await expect(inbox.getByRole('status').filter({ hasText: 'Review submitted: APPROVE' })).toBeVisible();
    await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, 'Needs you, nothing waiting'));

    // gh read the review, then posted the event; the event is in the POST's stdin.
    await expect.poll(() => gh.calls().map((c) => `${c.method} ${c.endpoint}`)).toEqual([`GET ${REVIEW}`, `POST ${REVIEW}/events`]);
    expect(JSON.parse(gh.calls()[1].stdin)).toMatchObject({ event: 'APPROVE' });

    // The answer file records it, and after the hold the item slides out.
    const file = join(admiral.home, 'inbox', ship.session, 'r-1.answer.json');
    await expect.poll(() => existsSync(file)).toBe(true);
    expect(JSON.parse(readFileSync(file, 'utf8'))).toMatchObject({ kind: 'review', id: 'r-1', action: 'submitted', event: 'APPROVE' });
    await expect(inbox.getByRole('status').filter({ hasText: 'Review submitted' })).toBeHidden({ timeout: 10_000 });
    await expect(inbox.getByText('Nothing needs you')).toBeVisible();
  });

  test('S2: Edit opens the PR files in a new tab, and a later Submit says it was edited', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'r-1', review());
    // The test has no network: whatever github.com is asked for, a stub answers.
    await page.context().route('https://github.com/**', (r) => r.fulfill({ contentType: 'text/html', body: '<title>stub</title>' }));
    const inbox = await open(page, admiral, info, 1);

    const edit = inbox.getByRole('link', { name: /^Edit/ });
    await expect(edit).toHaveAttribute('href', `${PR}/files`);
    await expect(edit).toHaveAttribute('target', '_blank');
    await expect(edit).toHaveAttribute('rel', /noopener/);
    const [tab] = await Promise.all([page.context().waitForEvent('page'), press(edit, touch)]);
    await expect.poll(() => tab.url()).toBe(`${PR}/files`);
    await tab.close();

    await press(inbox.getByRole('button', { name: 'Submit', exact: true }), touch);
    await press(inbox.getByRole('button', { name: /^COMMENT/ }), touch);
    await press(inbox.getByRole('button', { name: 'Submit COMMENT' }), touch);
    await expect(inbox.getByRole('status').filter({ hasText: 'Review submitted: COMMENT' })).toBeVisible();

    const file = join(admiral.home, 'inbox', ship.session, 'r-1.answer.json');
    await expect.poll(() => existsSync(file)).toBe(true);
    expect(JSON.parse(readFileSync(file, 'utf8')).event).toBe('COMMENT');
    expect(JSON.parse(gh.calls().find((c) => c.method === 'POST')!.stdin)).toMatchObject({ event: 'COMMENT' });
    // `edited` goes to Admiral in the submit request, which records it in the outcome log.
    const logged = () =>
      readdirSync(join(admiral.home, 'outcomes'))
        .map((f) => readFileSync(join(admiral.home, 'outcomes', f), 'utf8'))
        .join('');
    await expect.poll(logged).toMatch(/"review_event":"COMMENT"/);
    expect(logged()).toMatch(/"edited":true/);
  });

  test('S3: Discard asks once inline, with no browser dialog, then folds', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'r-1', review());
    const seen = dialogs(page);
    const inbox = await open(page, admiral, info, 1);

    // "Keep it" goes back, and nothing was sent.
    await press(inbox.getByRole('button', { name: 'Discard', exact: true }), touch);
    await expect(inbox.getByText('Discard this pending review?')).toBeVisible();
    await press(inbox.getByRole('button', { name: 'Keep it' }), touch);
    await expect(inbox.getByText('Discard this pending review?')).toHaveCount(0);
    expect(gh.calls()).toEqual([]);

    await press(inbox.getByRole('button', { name: 'Discard', exact: true }), touch);
    await press(inbox.getByRole('button', { name: 'Discard review' }), touch);
    await expect(inbox.getByRole('status').filter({ hasText: 'Review discarded' })).toBeVisible();
    await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, 'Needs you, nothing waiting'));
    await expect.poll(() => gh.calls().map((c) => `${c.method} ${c.endpoint}`)).toEqual([`DELETE ${REVIEW}`]);
    expect(seen).toEqual([]);
  });

  test('S5: a gh failure shows inline as text, keeps the item and the badge, and a retry works', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    gh.can('POST', `${REVIEW}/events`, { status: 500, stderr: 'gh: <b>boom</b> from GitHub (HTTP 500)' });
    seed(admiral, ship.session, 'r-1', review());
    const inbox = await open(page, admiral, info, 1);

    await press(inbox.getByRole('button', { name: 'Submit', exact: true }), touch);
    await press(inbox.getByRole('button', { name: /^REQUEST_CHANGES/ }), touch);
    await press(inbox.getByRole('button', { name: 'Submit REQUEST_CHANGES' }), touch);

    // gh's own words, as text; the item did not fold and still counts.
    const alert = inbox.getByRole('alert');
    await expect(alert).toContainText('<b>boom</b> from GitHub');
    await expect(alert.locator('b')).toHaveCount(0);
    await expect(inbox.getByRole('status').filter({ hasText: 'Review submitted' })).toHaveCount(0);
    await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, 'Needs you, 1 item'));
    expect(existsSync(join(admiral.home, 'inbox', ship.session, 'r-1.answer.json'))).toBe(false);

    // The engineer's way back is still there: Submit works again, and this time GitHub answers.
    gh.can('POST', `${REVIEW}/events`, { json: {} });
    const submit = inbox.getByRole('button', { name: 'Submit', exact: true });
    await expect(submit.or(inbox.getByRole('button', { name: 'Submit REQUEST_CHANGES' }))).toBeVisible();
    if (await submit.isVisible()) {
      await press(submit, touch);
      await press(inbox.getByRole('button', { name: /^REQUEST_CHANGES/ }), touch);
    }
    await press(inbox.getByRole('button', { name: 'Submit REQUEST_CHANGES' }), touch);
    await expect(inbox.getByRole('status').filter({ hasText: 'Review submitted: REQUEST_CHANGES' })).toBeVisible();
  });

  test('a 404 for a review that is no longer pending folds as gone', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    gh.can('GET', REVIEW, { json: { state: 'COMMENTED', body: '' } });
    seed(admiral, ship.session, 'r-1', review());
    const inbox = await open(page, admiral, info, 1);

    await press(inbox.getByRole('button', { name: 'Submit', exact: true }), touch);
    await press(inbox.getByRole('button', { name: /^COMMENT/ }), touch);
    await press(inbox.getByRole('button', { name: 'Submit COMMENT' }), touch);
    await expect(inbox.getByRole('status').filter({ hasText: 'Review no longer pending on GitHub' })).toBeVisible();
    await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, 'Needs you, nothing waiting'));
  });

  test('a PR address that is not https is not a link, and Edit is not offered', async ({ page, admiral, sea }, info) => {
    const { ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'r-1', review(5, { pr_url: 'javascript:alert(1)' }));
    const inbox = await open(page, admiral, info, 1);
    await expect(inbox.getByText('looks fine, two nits')).toBeVisible();
    expect(await page.locator('a[href^="javascript" i]').count()).toBe(0);
    await expect(inbox.getByRole('link', { name: /^Edit/ })).toHaveCount(0);
  });

  test('Remove from inbox after a gh failure forgets the item', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    gh.can('DELETE', REVIEW, { status: 500, stderr: 'gh: Server Error (HTTP 500)' });
    seed(admiral, ship.session, 'r-1', review());
    const inbox = await open(page, admiral, info, 1);

    await press(inbox.getByRole('button', { name: 'Discard', exact: true }), touch);
    await press(inbox.getByRole('button', { name: 'Discard review' }), touch);
    await expect(inbox.getByRole('alert')).toContainText('Server Error');
    await press(inbox.getByRole('button', { name: /Remove from inbox/ }), touch);
    await expect(inbox.getByRole('status').filter({ hasText: 'Removed from the inbox' })).toBeVisible();
    await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, 'Needs you, nothing waiting'));
  });
});

test.describe('a findings item', () => {
  test('S4: Send waits for every card; Edit then Save sends edited with the new text; the fold counts', { tag: '@critical' }, async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { gh, ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'f-1', findings([
      findingCard('r1'),
      findingCard('r2', { verdict: 'unclear', proposal: { kind: 'reply', content: 'what do you mean?' } }),
      findingCard('r3', { verdict: 'disagree' }),
    ]));
    const dlg = dialogs(page);
    const inbox = await open(page, admiral, info, 1);

    // Each card shows its source, quote, verdict, reason, proposal and evidence, all as text.
    await expect(inbox.getByText('reviewer-r1')).toBeVisible();
    await expect(inbox.getByText('quote of r1')).toBeVisible();
    await expect(inbox.getByText('reason for r1')).toBeVisible();
    await expect(inbox.getByText('+ new r1')).toBeVisible();
    await expect(inbox.getByText('main.go:2').first()).toBeVisible();
    await expect(inbox.getByText('what do you mean?')).toBeVisible();

    const send = inbox.getByRole('button', { name: 'Send', exact: true });
    const group = (n: number) => inbox.getByRole('group', { name: `Decision for card ${n}` });
    const pick = (n: number, name: string) => press(group(n).getByRole('button', { name, exact: true }), touch);
    for (const b of [send, group(1).getByRole('button', { name: 'Approve' })]) expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(44);

    await expect(send).toBeDisabled();
    await expect(inbox.getByText('0 of 3 decided')).toBeVisible();

    await pick(1, 'Approve');
    await expect(group(1).getByRole('button', { name: 'Approve' })).toHaveAttribute('aria-pressed', 'true');
    await pick(3, 'Reject');
    await expect(send).toBeDisabled();
    await expect(inbox.getByText('2 of 3 decided')).toBeVisible();

    // Cancel leaves the card as it was; Save marks it edited; Approve after the Save keeps the edit.
    await pick(2, 'Edit');
    const box = inbox.getByRole('textbox', { name: 'Edit the proposal for card 2' });
    await box.fill('could you say more?');
    await press(inbox.getByRole('button', { name: 'Cancel' }), touch);
    await expect(box).toHaveCount(0);
    await expect(send).toBeDisabled();
    await expect(inbox.getByText('what do you mean?')).toBeVisible();

    await pick(2, 'Edit');
    await box.fill('   ');
    await expect(inbox.getByRole('button', { name: 'Save' })).toBeDisabled();
    await box.fill('could you say more?');
    await press(inbox.getByRole('button', { name: 'Save' }), touch);
    await expect(group(2).getByRole('button', { name: 'Edit' })).toHaveAttribute('aria-pressed', 'true');
    await expect(inbox.getByText('could you say more?')).toBeVisible();
    await pick(2, 'Approve');
    await expect(group(2).getByRole('button', { name: 'Edit' })).toHaveAttribute('aria-pressed', 'true');
    await expect(inbox.getByText('could you say more?')).toBeVisible();

    await expect(inbox.getByText('3 of 3 decided')).toBeVisible();
    await expect(send).toBeEnabled();
    await press(send, touch);

    await expect(inbox.getByRole('status').filter({ hasText: 'Sent 1 approved, 1 edited, 1 rejected' })).toBeVisible();
    await expect(badge(page, info)).toHaveAccessibleName(badgeName(info, 'Needs you, nothing waiting'));

    // The answer file: the wire decisions, and a proposal only on the edited card, with the original kind.
    const file = join(admiral.home, 'inbox', ship.session, 'f-1.answer.json');
    await expect.poll(() => existsSync(file)).toBe(true);
    const answer = JSON.parse(readFileSync(file, 'utf8'));
    expect(answer).toMatchObject({ kind: 'findings', id: 'f-1' });
    expect(answer.cards).toEqual({
      r1: { decision: 'approved' },
      r2: { decision: 'edited', proposal: { kind: 'reply', content: 'could you say more?' } },
      r3: { decision: 'rejected' },
    });
    expect(gh.calls()).toEqual([]);
    expect(dlg).toEqual([]);
  });

  test('Reject after a Save drops the edit; the fold leaves out the buckets with none', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'f-1', findings([findingCard('r1'), findingCard('r2')]));
    const inbox = await open(page, admiral, info, 1);

    const group = (n: number) => inbox.getByRole('group', { name: `Decision for card ${n}` });
    await press(group(1).getByRole('button', { name: 'Edit', exact: true }), touch);
    await inbox.getByRole('textbox', { name: 'Edit the proposal for card 1' }).fill('my version');
    await press(inbox.getByRole('button', { name: 'Save' }), touch);
    await press(group(1).getByRole('button', { name: 'Reject', exact: true }), touch);
    await expect(inbox.getByText('my version')).toHaveCount(0);
    await expect(inbox.getByText('+ new r1')).toBeVisible();
    await press(group(2).getByRole('button', { name: 'Approve', exact: true }), touch);
    await press(inbox.getByRole('button', { name: 'Send', exact: true }), touch);

    await expect(inbox.getByRole('status').filter({ hasText: 'Sent 1 approved, 1 rejected' })).toBeVisible();
    const file = join(admiral.home, 'inbox', ship.session, 'f-1.answer.json');
    await expect.poll(() => existsSync(file)).toBe(true);
    expect(JSON.parse(readFileSync(file, 'utf8')).cards).toEqual({ r1: { decision: 'rejected' }, r2: { decision: 'approved' } });
  });

  test('markup in a card shows as text, and a findings PR link must be https', async ({ page, admiral, sea }, info) => {
    const { ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'f-1', {
      ...findings([findingCard('r1', { quote: '<img src=x onerror=alert(1)>', proposal: { kind: 'diff', content: '<script>alert(2)</script>' } })]),
      findings: { pr_url: 'javascript:alert(3)', cards: [findingCard('r1', { quote: '<img src=x onerror=alert(1)>', proposal: { kind: 'diff', content: '<script>alert(2)</script>' } })] },
    });
    const inbox = await open(page, admiral, info, 1);
    await expect(inbox.getByText('<img src=x onerror=alert(1)>')).toBeVisible();
    await expect(inbox.getByText('<script>alert(2)</script>')).toBeVisible();
    await expect(inbox.locator('img, script')).toHaveCount(0);
    expect(await page.locator('a[href^="javascript" i]').count()).toBe(0);
  });

  test('a findings item with no cards can be sent with an empty list', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'f-1', findings([]));
    const inbox = await open(page, admiral, info, 1);
    const send = inbox.getByRole('button', { name: 'Send', exact: true });
    await expect(send).toBeEnabled();
    await press(send, touch);
    await expect(inbox.getByRole('status')).toHaveText(/Sent, no cards/);
    const file = join(admiral.home, 'inbox', ship.session, 'f-1.answer.json');
    await expect.poll(() => existsSync(file)).toBe(true);
    expect(JSON.parse(readFileSync(file, 'utf8')).cards).toEqual({});
  });

  test('Send waits while an edit is open, and a reused ship name and item id starts fresh', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const { ship } = await setup(admiral, sea);
    seed(admiral, ship.session, 'f-1', findings([findingCard('a'), findingCard('b')]));
    const inbox = await open(page, admiral, info, 1);
    const send = inbox.getByRole('button', { name: 'Send', exact: true });
    const group = (n: number) => inbox.getByRole('group', { name: `Decision for card ${n}` });
    await press(group(1).getByRole('button', { name: 'Approve', exact: true }), touch);
    await press(group(2).getByRole('button', { name: 'Approve', exact: true }), touch);
    await expect(send).toBeEnabled();
    await press(group(2).getByRole('button', { name: 'Edit', exact: true }), touch);
    await expect(send).toBeDisabled();
    await expect(inbox.getByText(/Save or Cancel/)).toBeVisible();
    await press(inbox.getByRole('button', { name: 'Cancel' }), touch);
    await press(send, touch);
    await expect(inbox.getByRole('status').filter({ hasText: 'Sent 2 approved' })).toBeVisible();
    await expect(inbox.getByText('Nothing needs you')).toBeVisible({ timeout: 10_000 });

    // The ship sails off, its name is reused, and the new ship writes an item with the same id.
    rmSync(join(admiral.home, 'inbox', ship.session, 'f-1.answer.json'));
    seed(admiral, ship.session, 'f-1', findings([findingCard('c'), findingCard('d'), findingCard('e')]));
    await expect(inbox.getByText('quote of c')).toBeVisible();
    await expect(inbox.getByText('0 of 3 decided')).toBeVisible();
    await expect(send).toBeDisabled();
  });
});
