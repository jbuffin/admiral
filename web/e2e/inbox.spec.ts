import type { Locator, Page, TestInfo } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { type Admiral, expect } from './fixtures';
import { animationsOn, boarded, header, isTouch, spyOnPage, test } from './sea';

// The Needs You inbox in the web UI (#116 u3). Items are files the test writes into the test Admiral's
// $ADMIRAL_HOME/inbox/<session>/ after the ship has launched; a needs-you stop is a fake harness that
// fires a PermissionRequest hook. The page is driven by roles, accessible names and visible text. The
// names below are the contract, taken from the prototype (designs/canvas/Main.dc.html):
//
//   header: button "Needs you, <n> item(s)" / "Needs you, nothing waiting", aria-expanded
//   wide (>= 1024 wide, landscape): complementary "Needs you inbox" (the drawer), button "Close the inbox"
//   otherwise: dialog "Needs you inbox", aria-modal="true" (the sheet), buttons "Back" and "Next", text "n of N"
//   item: its title, the ship's name, "Recommended" on the recommended option,
//     radio / checkbox per option (name starts with the label), radio "Other" revealing textbox "Your answer",
//     textbox "Note" (one per item), button "Send answers" (disabled until every question is answered)
//   folded line: role status, "Answered" / "Back underway"
//   needs-you stop: "Wants to run Bash" with the code "permission:Bash", button "Attach to <name> …"
//   review item (u4 builds its card): in u3 only the title and the ship, so it still counts
//   empty: "Nothing needs you"
//   an item's order_url: link "Open the order", only for https: and http: addresses, rel "noopener noreferrer"

const BLUE = '#3B82F6';
const PERMISSION = 'event PermissionRequest {"tool_name":"Bash"}';

const wide = (info: TestInfo) => {
  const v = info.project.use.viewport;
  return !!v && v.width >= 1024 && v.width > v.height;
};
const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

const inboxButton = (page: Page) => header(page).getByRole('button', { name: /^Needs you, / });
const panel = (page: Page, info: TestInfo) => page.getByRole(wide(info) ? 'complementary' : 'dialog', { name: 'Needs you inbox' });
const position = (p: Locator) => p.getByText(/^\d+ of \d+$/);

const minutesAgo = (n: number) => new Date(Date.now() - n * 60_000).toISOString();

/** Writes an item the way Commodore does: a dot-named temp file, then a rename. */
function seed(a: Admiral, session: string, id: string, item: Record<string, unknown>) {
  const dir = join(a.home, 'inbox', session);
  mkdirSync(dir, { recursive: true });
  const tmp = join(dir, `.${id}.json.tmp`);
  writeFileSync(tmp, JSON.stringify({ version: 1, id, ...item }));
  renameSync(tmp, join(dir, `${id}.json`));
}

const question = (min: number, extra: Record<string, unknown> = {}) => ({
  kind: 'question',
  created: minutesAgo(min),
  title: 'Which way?',
  question: {
    questions: [
      {
        id: '1',
        text: 'Pick one',
        recommended: 'Alpha',
        multi: false,
        options: [
          { label: 'Alpha', description: 'the first way' },
          { label: 'Beta', description: 'the second way' },
        ],
      },
      {
        id: '2',
        text: 'Pick some',
        multi: true,
        options: [
          { label: 'X', description: 'ex' },
          { label: 'Y', description: 'why' },
        ],
      },
    ],
  },
  ...extra,
});

const review = (min: number) => ({
  kind: 'review',
  created: minutesAgo(min),
  title: 'Review PR 12',
  review: { pr_url: 'https://example.com/pull/12', review_id: 123, summary: 'looks fine' },
});

test.describe('S2: the badge, the inbox and a question', () => {
  test('the badge counts the items; answering a question folds it, drops the badge, then slides it out', { tag: '@critical' }, async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha');
    seed(admiral, ship.session, 'q-1', question(10));
    seed(admiral, ship.session, 'r-1', review(5));
    await page.goto(admiral.url);

    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 2 items');
    await expect(inboxButton(page)).toHaveAttribute('aria-expanded', 'false');
    await press(inboxButton(page), touch);
    const inbox = panel(page, info);
    await expect(inbox).toBeVisible();
    await expect(inboxButton(page)).toHaveAttribute('aria-expanded', 'true');

    // The oldest first, with its ship's name.
    await expect(inbox.getByText('Which way?')).toBeVisible();
    await expect(inbox.getByText(ship.name).first()).toBeVisible();
    await expect(inbox.getByText('Recommended')).toBeVisible();
    const send = inbox.getByRole('button', { name: 'Send answers' });
    await expect(send).toBeDisabled();

    // One question answered is not enough.
    await press(inbox.getByRole('radio', { name: /^Alpha/ }), touch);
    await expect(send).toBeDisabled();
    await press(inbox.getByRole('checkbox', { name: /^Y/ }), touch);
    await expect(send).toBeEnabled();

    // "Other" reveals a text field, and picking it replaces the option (the field answers instead).
    await press(inbox.getByRole('radio', { name: /^Other/ }), touch);
    await expect(inbox.getByPlaceholder('Your answer')).toBeVisible();
    await expect(send).toBeDisabled();
    await press(inbox.getByRole('radio', { name: /^Alpha/ }), touch);
    await expect(inbox.getByPlaceholder('Your answer')).toBeHidden();
    await expect(send).toBeEnabled();

    await inbox.getByRole('textbox', { name: /note/i }).fill('go with alpha');
    await press(send, touch);

    // At once: the folded line, and the badge without it. The review item still counts.
    await expect(inbox.getByRole('status').filter({ hasText: 'Answered' })).toBeVisible();
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 1 item');

    // The answer is a file beside the item, with the choices keyed by question id.
    const file = join(admiral.home, 'inbox', ship.session, 'q-1.answer.json');
    await expect.poll(() => existsSync(file)).toBe(true);
    const answer = JSON.parse(readFileSync(file, 'utf8'));
    expect(answer).toMatchObject({ kind: 'question', id: 'q-1', note: 'go with alpha', answers: { '1': { choice: 'Alpha' }, '2': { choice: ['Y'] } } });

    // After the hold it slides out; the review item is next, as a placeholder with its title.
    await expect(inbox.getByRole('status').filter({ hasText: 'Answered' })).toBeHidden({ timeout: 10_000 });
    await expect(inbox.getByText('Review PR 12')).toBeVisible();
    await expect(inbox.getByText('Which way?')).toHaveCount(0);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 1 item');
  });
});

test.describe('S5: nothing waiting', () => {
  test('the button says nothing is waiting and the inbox says "Nothing needs you"', async ({ page, admiral }, info) => {
    await admiral.start();
    await page.goto(admiral.url);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, nothing waiting');
    await press(inboxButton(page), isTouch(info));
    await expect(panel(page, info).getByText('Nothing needs you')).toBeVisible();
  });
});

test.describe('S3: the drawer beside the bridge', () => {
  test.beforeEach(({}, info) => test.skip(!wide(info), 'the drawer is the wide layout'));

  test('opening collapses Orders and closing restores it; expanding Orders closes the drawer', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha');
    seed(admiral, ship.session, 'q-1', question(10));
    await page.goto(admiral.url);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 1 item');

    const orders = page.getByRole('complementary', { name: 'Orders', exact: true });
    const rail = page.getByRole('complementary', { name: 'Orders, collapsed', exact: true });
    await expect(orders).toBeVisible();

    await press(inboxButton(page), touch);
    await expect(panel(page, info)).toBeVisible();
    await expect(rail).toBeVisible();
    await expect(orders).toHaveCount(0);

    await press(panel(page, info).getByRole('button', { name: 'Close the inbox' }), touch);
    await expect(panel(page, info)).toHaveCount(0);
    await expect(orders).toBeVisible();

    // Collapsed by the engineer first: closing the drawer leaves it collapsed.
    await press(page.getByRole('button', { name: 'Collapse orders' }), touch);
    await expect(rail).toBeVisible();
    await press(inboxButton(page), touch);
    await press(panel(page, info).getByRole('button', { name: 'Close the inbox' }), touch);
    await expect(rail).toBeVisible();

    // Expanding Orders while the drawer is open closes the drawer.
    await press(inboxButton(page), touch);
    await expect(panel(page, info)).toBeVisible();
    await press(page.getByRole('button', { name: 'Expand orders' }), touch);
    await expect(panel(page, info)).toHaveCount(0);
    await expect(orders).toBeVisible();
  });

  test('a needs-you stop: Attach opens the bridge beside the drawer, nothing is covered, and the button pulses', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE, cue: [PERMISSION] } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha');
    seed(admiral, ship.session, 'q-1', question(10));
    await spyOnPage(page);
    await page.goto(admiral.url);

    // The stop and the item are two entries.
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 2 items');
    expect(await animationsOn(inboxButton(page))).toBeGreaterThan(0);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    expect(await animationsOn(inboxButton(page))).toBe(0);
    await page.emulateMedia({ reducedMotion: 'no-preference' });

    await press(inboxButton(page), touch);
    const drawer = panel(page, info);
    await expect(drawer.getByText('Wants to run Bash')).toBeVisible();
    await expect(drawer.getByText('permission:Bash')).toBeVisible();
    await expect(drawer.getByText(/^Waiting /)).toBeVisible();

    await press(drawer.getByRole('button', { name: new RegExp(`^Attach to ${ship.name}`) }), touch);
    expect(await boarded(page)).toEqual([ship.session]);
    const terminal = page.getByRole('region', { name: 'Attached terminal' });
    await expect(terminal).toBeVisible();
    await expect(drawer).toBeVisible();

    // The terminal ends where the drawer begins; the inbox is still open and still counts.
    const [t, d] = [await terminal.boundingBox(), await drawer.boundingBox()];
    expect(t!.x + t!.width).toBeLessThanOrEqual(d!.x + 1);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 2 items');

    // At 1133x744 the key row and the drawer's bottom edge are on the screen (#116: it was 40px below the fold).
    await page.setViewportSize({ width: 1133, height: 744 });
    await expect(drawer).toBeVisible();
    const bottom = async (l: Locator) => {
      const b = await l.boundingBox();
      return b!.y + b!.height;
    };
    expect(await bottom(drawer)).toBeLessThanOrEqual(744);
    if (touch) expect(await bottom(terminal.getByRole('button', { name: 'Send Enter' }))).toBeLessThanOrEqual(744);
    expect(await page.evaluate(() => document.scrollingElement!.scrollHeight <= innerHeight)).toBe(true);
  });
});

test.describe('S4: the sheet', () => {
  test.beforeEach(({}, info) => test.skip(wide(info), 'the sheet is the narrow layout'));

  test('a modal dialog: Back, Next and "n of N" work, Escape closes it and focus returns to the button', async ({ page, admiral, sea }) => {
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha');
    seed(admiral, ship.session, 'q-1', question(10));
    seed(admiral, ship.session, 'r-1', review(5));
    await page.goto(admiral.url);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 2 items');

    await inboxButton(page).tap();
    const sheet = panel(page, test.info());
    await expect(sheet).toBeVisible();
    await expect(sheet).toHaveAttribute('aria-modal', 'true');

    // One item at a time, oldest first.
    const back = sheet.getByRole('button', { name: /Back/ });
    const next = sheet.getByRole('button', { name: /Next/ });
    await expect(position(sheet)).toHaveText('1 of 2');
    await expect(sheet.getByText('Which way?')).toBeVisible();
    await expect(sheet.getByText('Review PR 12')).toHaveCount(0);
    await expect(back).toBeDisabled();
    for (const b of [back, next]) expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(44);

    await next.tap();
    await expect(position(sheet)).toHaveText('2 of 2');
    await expect(sheet.getByText('Review PR 12')).toBeVisible();
    await expect(next).toBeDisabled();
    await back.tap();
    await expect(position(sheet)).toHaveText('1 of 2');

    // Focus stays inside the sheet however far Tab goes.
    for (let i = 0; i < 25; i++) {
      await page.keyboard.press('Tab');
      expect(await sheet.evaluate((el) => el.contains(document.activeElement))).toBe(true);
    }

    await page.keyboard.press('Escape');
    await expect(sheet).toHaveCount(0);
    await expect(inboxButton(page)).toBeFocused();
  });
});

test.describe('items are untrusted text', () => {
  test('markup shows as text, and only http(s) addresses become links', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha');
    seed(admiral, ship.session, 'q-bad', question(10, { title: '<b>bold</b> <img src=x onerror=alert(1)>', order_url: 'javascript:alert(1)' }));
    seed(admiral, ship.session, 'q-ok', question(5, { title: 'A good link', order_url: 'https://example.com/issues/9' }));
    await page.goto(admiral.url);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 2 items');
    await press(inboxButton(page), touch);
    const inbox = panel(page, info);

    await expect(inbox.getByText('<b>bold</b> <img src=x onerror=alert(1)>')).toBeVisible();
    await expect(inbox.locator('b, img')).toHaveCount(0);
    await expect(inbox.getByRole('link', { name: /Open the order/ })).toHaveCount(0);
    expect(await page.locator('a[href^="javascript" i]').count()).toBe(0);

    if (!wide(info)) await press(inbox.getByRole('button', { name: /Next/ }), touch);
    const link = inbox.getByRole('link', { name: /Open the order/ });
    await expect(link).toHaveAttribute('href', 'https://example.com/issues/9');
    await expect(link).toHaveAttribute('rel', /noopener/);
    await expect(link).toHaveAttribute('rel', /noreferrer/);
  });
});
