import { expect, type Locator, type Page } from '@playwright/test';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Admiral } from './fixtures';
import { addGitHubSource, launchShip, orderPane, seedFleets, stackAnswer } from './shell';

const H = 3600_000;
const D = 24 * H;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

interface Item {
  number: number;
  title: string;
  repo?: string;
  author?: string;
  labels?: string[];
  pr?: { review: string; checks: string; mergeable: string; reviewed?: boolean };
  requested?: boolean;
  body?: string;
  parent?: { number: number; title: string; repo?: string };
  stack?: { number: number; position: number; size: number };
}

// What the fake gh answers for each default view. The docs sweep and the rate limiter are in two views: For you shows each once.
const SIGNING_BODY = 'Rotate every key older than ninety days and revoke the old ones.';
// The parent of the two assigned issues: in no view, so its row is built from their `parent`.
const EPIC = { number: 30, title: 'Roll out key rotation' };
const ASSIGNED: Item[] = [
  { number: 31, title: 'Rotate the signing keys', author: 'bob', labels: ['priority: high', 'security'], body: SIGNING_BODY, parent: EPIC },
  { number: 32, title: 'Document the API', author: 'bob', labels: ['docs'], parent: EPIC },
  { number: 33, title: 'Port the table', repo: 'acme/web', author: 'bob', labels: ['ux'] },
  { number: 21, title: 'Raise the rate limiter', author: 'fake-viewer', pr: { review: 'CHANGES_REQUESTED', checks: 'SUCCESS', mergeable: 'MERGEABLE' } },
];
// A GitHub stack of three review-requested PRs by another author, answered top first as the search does (most recently updated first).
const STACK = { number: 7, url: 'https://github.com/acme/api/stack/7' };
const STACK_LAYERS = ['Add the store', 'Wire the store to the API', 'Show the store in the page'];
const REVIEW: Item[] = [
  { number: 11, title: 'Review the cache layer', author: 'alice', pr: { review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE' }, requested: true },
  { number: 12, title: 'Review the docs sweep', author: 'fake-viewer', pr: { review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE', reviewed: true }, requested: true },
  ...[3, 2, 1].map((position): Item => ({ number: 40 + position, title: STACK_LAYERS[position - 1], author: 'alice', pr: { review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE' }, requested: true, stack: { number: STACK.number, position, size: 3 } })),
];
const MINE: Item[] = [
  { number: 21, title: 'Raise the rate limiter', author: 'fake-viewer', pr: { review: 'CHANGES_REQUESTED', checks: 'SUCCESS', mergeable: 'MERGEABLE' } },
  { number: 22, title: 'Cache the token lookups', author: 'fake-viewer', pr: { review: 'REVIEW_REQUIRED', checks: 'FAILURE', mergeable: 'MERGEABLE' } },
  { number: 23, title: 'Fix the flaky retry', author: 'fake-viewer', pr: { review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' } },
  { number: 12, title: 'Review the docs sweep', author: 'fake-viewer', pr: { review: 'REVIEW_REQUIRED', checks: 'SUCCESS', mergeable: 'MERGEABLE', reviewed: true } },
];

/** For you: Needs you holds the cache review, the rate limiter, the token lookups, the signing keys and the three layers of the review-requested stack; the other four wait. */
export const FOR_YOU = { needsYou: 7, waiting: 4, total: 11 };

function node(i: Item) {
  const repo = i.repo ?? 'acme/api';
  const n: Record<string, unknown> = {
    number: i.number,
    title: i.title,
    url: `https://github.com/${repo}/${i.pr ? 'pull' : 'issues'}/${i.number}`,
    createdAt: ago(3 * D),
    bodyText: i.body ?? '',
    author: { login: i.author ?? 'bob' },
    repository: { nameWithOwner: repo, url: `https://github.com/${repo}` },
    labels: { nodes: (i.labels ?? []).map((name) => ({ name })) },
  };
  if (i.pr) {
    const oid = `head-${i.number}`;
    Object.assign(n, {
      state: 'OPEN',
      reviewDecision: i.pr.review,
      mergeable: i.pr.mergeable,
      mergeStateStatus: i.pr.review === 'APPROVED' && i.pr.checks === 'SUCCESS' && i.pr.mergeable === 'MERGEABLE' ? 'CLEAN' : 'BLOCKED',
      statusCheckRollup: { state: i.pr.checks },
      headRefOid: oid,
      viewerLatestReview: i.pr.reviewed ? { state: 'APPROVED', commit: { oid } } : null,
    });
  }
  if (i.parent) {
    const repo = i.parent.repo ?? 'acme/api';
    n.parent = { number: i.parent.number, title: i.parent.title, url: `https://github.com/${repo}/issues/${i.parent.number}`, repository: { nameWithOwner: repo, url: `https://github.com/${repo}` } };
  }
  if (i.stack) {
    n.stack = { number: i.stack.number, size: i.stack.size };
    n.stackEntry = { position: i.stack.position };
  }
  if (i.requested) {
    n.timelineItems = { nodes: [{ __typename: 'ReviewRequestedEvent', createdAt: ago(2 * H), requestedReviewer: { login: 'fake-viewer' } }] };
  }
  return n;
}

/**
 * Orders for the zulu fleet: the built-in GitHub source answers its three default views from the fake gh, and the test plugin's
 * source (from seedFleets' live fleet) lists the shell's usual orders under its own view. Call before `start`.
 */
export function seedOrders(a: Admiral) {
  seedFleets(a, { live: true });
  addGitHubSource(a);
  const path = join(a.home, 'config.json');
  const config = JSON.parse(readFileSync(path, 'utf8'));
  config.fleets.zulu.owners = ['acme'];
  a.writeConfig(config);
  const dir = join(a.fakeGh().dir, 'search');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(a.fakeGh().dir, 'viewer'), 'fake-viewer\n');
  // The first key, in name order, whose needle is in the query answers; the catch-all sorts last.
  const canned = (key: string, needle: string, items: Item[]) => {
    writeFileSync(join(dir, `${key}.match`), needle);
    writeFileSync(join(dir, `${key}.json`), JSON.stringify({ data: { search: { nodes: items.map(node) } } }));
  };
  canned('a-assigned', 'assignee:@me', ASSIGNED);
  canned('b-review', 'review-requested:@me', REVIEW);
  canned('c-mine', 'author:@me', MINE);
  canned('z-rest', 'is:open', []);
}

const link = (page: Page, name: RegExp) => page.getByRole('navigation').getByRole('link', { name });
const tabNamed = (page: Page, name: RegExp) => page.getByRole('tab', { name });
const group = (page: Page, title: string) => page.getByRole('main').getByRole('region', { name: new RegExp(`^${title}`) });
const rowOf = (page: Page, title: string) => page.getByRole('main').getByRole('listitem').filter({ hasText: title });

/** The oldest refreshed_at, in ms, across the GitHub source's default views. */
async function oldestAnswer(a: Admiral): Promise<number> {
  const sources = (await (await fetch(`${a.url}api/v1/sources`)).json()) as { plugin: string; views: { id: string; refreshed_at?: string }[] }[];
  const times = sources
    .filter((s) => s.plugin === 'github')
    .flatMap((s) => s.views)
    .filter((v) => ['review-requested', 'my-prs', 'assigned-to-me'].includes(v.id))
    .map((v) => Date.parse(v.refreshed_at ?? ''));
  return times.length === 3 && times.every((t) => !Number.isNaN(t)) ? Math.min(...times) : 0;
}

async function expectTouchTarget(l: Locator, min: number) {
  const box = (await l.boundingBox())!;
  expect(box.width).toBeGreaterThanOrEqual(44);
  expect(box.height).toBeGreaterThanOrEqual(min);
}

/**
 * The Orders journey on one device: open Orders from the nav, read For you and its Needs you count, switch to a view tab,
 * filter (wide only), press Refresh now, open a row's detail, launch from its arrow, and find Ships holding only a link back. `press` is the click or tap of the device.
 */
export async function ordersJourney(page: Page, a: Admiral, press: (l: Locator) => Promise<void>, opts: { phone: boolean }) {
  const { phone } = opts;
  const main = page.getByRole('main');
  const rowMin = phone ? 60 : 52;

  await page.goto(a.url + 'app/ships?fleet=zulu');
  // The sidebar and the tab bar carry For you's Needs you count.
  const nav = link(page, /^Orders\s*7$/);
  await expect(nav).toBeVisible();
  await expect(page.getByRole('navigation').getByRole('link').filter({ hasText: /^(Inbox|Chat|Ships|Orders)/ })).toHaveText(
    phone ? [/^Inbox/, /^Chat/, /^Ships/, /^Orders\s*7$/] : [/^Inbox/, /^Ships/, /^Orders\s*7$/, /^Chat/],
  );

  await press(nav);
  await expect(page).toHaveURL(/\/app\/orders(\?|$)/);
  await expect(page.getByRole('heading', { name: 'Orders', exact: true })).toBeVisible();

  // For you is first and selected; each view has a tab with its count of distinct orders.
  await expect(page.getByRole('tab')).toHaveText([/^For you\s*11$/, /^Assigned to me\s*4$/, /^Review requested\s*5$/, /^My PRs\s*4$/, /^Orders: Orders\s*7$/]);
  await expect(tabNamed(page, /^For you/)).toHaveAttribute('aria-selected', 'true');

  // The groups, each order once, though two sit in two views.
  await expect(group(page, 'Needs you')).toContainText(String(FOR_YOU.needsYou));
  await expect(group(page, 'Needs you').getByRole('listitem')).toHaveCount(FOR_YOU.needsYou);
  await expect(group(page, 'Waiting on others').getByRole('listitem')).toHaveCount(FOR_YOU.waiting);
  for (const t of ['Review the cache layer', 'Raise the rate limiter', 'Cache the token lookups', 'Rotate the signing keys']) await expect(group(page, 'Needs you')).toContainText(t);
  for (const t of ['Review the docs sweep', 'Fix the flaky retry', 'Document the API', 'Port the table']) await expect(group(page, 'Waiting on others')).toContainText(t);
  await expect(rowOf(page, 'Raise the rate limiter')).toHaveCount(1);
  await expect(rowOf(page, 'Review the docs sweep')).toHaveCount(1);

  // Rows: the shape of each device.
  const review = rowOf(page, 'Review the cache layer');
  const mine = rowOf(page, 'Cache the token lookups');
  const hot = rowOf(page, 'Rotate the signing keys');
  const ready = rowOf(page, 'Fix the flaky retry');
  for (const r of [review, mine, hot, ready]) expect((await r.boundingBox())!.height).toBeGreaterThanOrEqual(rowMin);
  await expect(review).toContainText('acme/api #11');
  await expect(review).toContainText('Review requested');
  await expect(mine).toContainText('Checks failing');
  await expect(hot).toContainText('High');
  await expect(ready).toContainText('Ready to merge');
  if (phone) {
    // The phone row is the title and `repo #number · state word`: no reason line, no labels.
    await expect(review).toContainText('acme/api #11 · Review requested');
    await expect(review).not.toContainText('review requested 2h ago');
    await expect(hot).toContainText('acme/api #31 · in #30 · High');
    await expect(hot).not.toContainText('priority: high');
  } else {
    await expect(review).toContainText('review requested 2h ago · @alice');
    await expect(mine).toContainText('your PR · checks failing');
    await expect(rowOf(page, 'Raise the rate limiter')).toContainText('your PR · changes requested');
    await expect(hot).toContainText('assigned');
    await expect(hot).toContainText('in #30');
    await expect(hot).toContainText('priority: high');
  }
  // Every row has the 44px arrow, named for its title, PRs included.
  for (const t of ['Review the cache layer', 'Rotate the signing keys', 'Fix the flaky retry']) await expectTouchTarget(page.getByRole('button', { name: `Launch ${t}`, exact: true }), 44);

  // The header: the source and when it answered, and Refresh now.
  const refresh = page.getByRole('button', { name: 'Refresh now', exact: true });
  await expect(refresh).toBeVisible();
  await expectTouchTarget(refresh, 44);
  await expect(main).toContainText(/refreshed (just now|\d+m ago)/);
  if (!phone) await expect(main).toContainText('GitHub');
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  // Tapping a row's body opens its detail, not the Launch dialog: a column on the iPad, a sheet on the phone.
  const form = page.getByRole('dialog', { name: 'Launch', exact: true });
  const pane = orderPane(page, phone);
  const body = (t: string) => page.getByRole('button', { name: `Open ${t}`, exact: true });
  await expect(pane).toBeHidden();
  await press(body('Rotate the signing keys'));
  await expect(form).toBeHidden();
  await expect(pane).toBeVisible();
  await expect(body('Rotate the signing keys')).toHaveAttribute('aria-pressed', 'true');
  await expect(body('Review the cache layer')).not.toHaveAttribute('aria-pressed', 'true');
  await expect(page).toHaveURL(/[?&]order=gh%2Facme%2Fapi%2331(&|$)/);
  await expect(pane.getByRole('heading', { name: 'Rotate the signing keys' })).toBeVisible();
  await expect(pane).toContainText('acme/api #31');
  await expect(pane).toContainText('priority: high');
  await expect(pane).toContainText('security');
  await expect(pane.locator('dt', { hasText: "Why it's here" })).toBeVisible();
  await expect(pane.locator('dd', { hasText: 'Assigned to you' })).toContainText('Assigned to me');
  await expect(pane.locator('dd', { hasText: 'by @bob' })).toBeVisible();
  await expect(pane).toContainText('Rotate every key older than ninety days and revoke the old ones.');
  const github = pane.getByRole('link', { name: 'Open on GitHub', exact: true });
  await expect(github).toHaveAttribute('href', 'https://github.com/acme/api/issues/31');
  // An issue with no sub-issues offers no campaign action.
  await expect(pane.getByText(/Follow as campaign|Open campaign/)).toHaveCount(0);
  await expectTouchTarget(pane.getByRole('button', { name: 'Launch ship', exact: true }), 44);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

  // A reload keeps the selection; Escape closes the pane and clears it.
  await page.reload();
  await expect(pane).toBeVisible();
  await expect(pane.getByRole('heading', { name: 'Rotate the signing keys' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(pane).toBeHidden();
  await expect(page).not.toHaveURL(/order=/);
  await expect(body('Rotate the signing keys')).not.toHaveAttribute('aria-pressed', 'true');

  // The pane's Launch ship opens the Launch dialog with the order filled in.
  await press(body('Rotate the signing keys'));
  await press(pane.getByRole('button', { name: 'Launch ship', exact: true }));
  await expect(form).toBeVisible();
  await expect(form).toContainText('Rotate the signing keys');
  await expect(form.getByLabel('Text')).toHaveValue('https://github.com/acme/api/issues/31');
  // Escape closes only the form; the pane stays until the next Escape.
  await page.keyboard.press('Escape');
  await expect(form).toBeHidden();
  await expect(pane).toBeVisible();
  if (!phone) {
    // Closing the pane hands focus back to the row it came from.
    await page.keyboard.press('Escape');
    await expect(pane).toBeHidden();
    await expect(body('Rotate the signing keys')).toBeFocused();
  } else {
    await page.keyboard.press('Escape');
    await expect(pane).toBeHidden();
  }

  // The arrow opens the Launch dialog straight away, with no pane.
  await press(page.getByRole('button', { name: 'Launch Rotate the signing keys', exact: true }));
  await expect(form).toBeVisible();
  await expect(pane).toBeHidden();
  await expect(form).toContainText('Rotate the signing keys');
  await expect(form.getByLabel('Text')).toHaveValue('https://github.com/acme/api/issues/31');
  await press(form.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(form).toBeHidden();

  // A view tab: its own distinct count and rows, kept by a reload. The two sub-issues sit in a collapsed parent group whose parent is in no view.
  await press(tabNamed(page, /^Assigned to me/));
  await expect(tabNamed(page, /^Assigned to me/)).toHaveAttribute('aria-selected', 'true');
  await expect(main.getByRole('listitem')).toHaveCount(3);
  await expect(tabNamed(page, /^Assigned to me/)).toHaveText(/^Assigned to me\s*4$/);
  const epic = main.getByRole('listitem').filter({ hasText: EPIC.title });
  const epicToggle = (state: 'Expand' | 'Collapse') => page.getByRole('button', { name: `${state} ${EPIC.title}`, exact: true });
  const child = main.getByText('Rotate the signing keys', { exact: true });
  await expect(epic).toHaveCount(1);
  await expect(epic).toContainText('not in this view');
  await expect(epic).toContainText(/2 sub-issues here/);
  await expect(epic).toContainText('High');
  await expect(epicToggle('Expand')).toHaveAttribute('aria-expanded', 'false');
  await expect(child).toBeHidden();
  await expect(page.getByRole('button', { name: `Launch ${EPIC.title}`, exact: true })).toHaveCount(0);
  await expectTouchTarget(epicToggle('Expand'), 44);

  // Pressing the toggle shows the children, indented, and opens nothing.
  await press(epicToggle('Expand'));
  await expect(epicToggle('Collapse')).toHaveAttribute('aria-expanded', 'true');
  await expect(pane).toBeHidden();
  await expect(child).toBeVisible();
  await expect(main.getByText('Document the API', { exact: true })).toBeVisible();
  const parentX = (await main.getByText(EPIC.title, { exact: true }).boundingBox())!.x;
  expect((await child.boundingBox())!.x).toBeGreaterThan(parentX + 8);
  const childRow = main.getByRole('listitem').filter({ hasText: 'Rotate the signing keys' }).last();
  await expect(childRow).toContainText(phone ? 'acme/api #31 · in #30' : 'in #30 · opened 3d ago · @bob');
  await expect(tabNamed(page, /^Assigned to me/)).toHaveText(/^Assigned to me\s*4$/);

  // A reload keeps it expanded; pressing again collapses it.
  await page.reload();
  await expect(tabNamed(page, /^Assigned to me/)).toHaveAttribute('aria-selected', 'true');
  await expect(epicToggle('Collapse')).toHaveAttribute('aria-expanded', 'true');
  await expect(child).toBeVisible();
  await press(epicToggle('Collapse'));
  await expect(epicToggle('Expand')).toHaveAttribute('aria-expanded', 'false');
  await expect(child).toBeHidden();
  await expect(main.getByRole('listitem')).toHaveCount(3);

  // A parent in no view opens its pane from its row, with a link to GitHub and no Launch ship.
  await press(body(EPIC.title));
  // Such a parent has no Why it's here line, so the phone sheet is found by its name.
  const epicPane = phone ? page.getByRole('dialog', { name: EPIC.title }) : pane;
  await expect(epicPane.getByRole('heading', { name: EPIC.title })).toBeVisible();
  await expect(epicPane.getByRole('link', { name: 'Open on GitHub', exact: true })).toHaveAttribute('href', 'https://github.com/acme/api/issues/30');
  await expect(epicPane.getByRole('button', { name: 'Launch ship', exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(epicPane).toBeHidden();

  if (phone) {
    // No filter and no repo filter on the phone.
    await expect(page.getByPlaceholder('Filter by title, repo, label')).toHaveCount(0);
    await expect(page.getByRole('combobox')).toHaveCount(0);
  } else {
    const filter = page.getByPlaceholder('Filter by title, repo, label');
    await expectTouchTarget(page.locator('label.orders-filter'), 44);
    await expectTouchTarget(page.getByRole('combobox', { name: /repo/i }), 44);
    // A shown child brings its parent, which stays collapsed.
    await filter.fill('DOCS');
    await expect(main.getByRole('listitem')).toHaveCount(1);
    await expect(epic).toBeVisible();
    await expect(epic).toContainText(/1 sub-issues? here/);
    await filter.fill('ACME/WEB');
    await expect(main.getByRole('listitem')).toHaveCount(1);
    await expect(rowOf(page, 'Port the table')).toBeVisible();
    await filter.fill('');
    await expect(main.getByRole('listitem')).toHaveCount(3);
    await page.getByRole('combobox', { name: /repo/i }).selectOption('acme/web');
    await expect(main.getByRole('listitem')).toHaveCount(1);
    await page.getByRole('combobox', { name: /repo/i }).selectOption({ label: 'All repos' });
    await expect(main.getByRole('listitem')).toHaveCount(3);
  }

  // A GitHub stack in a PR tab: a collapsed group of its three layers, built like a parent group and acting on none of it.
  await press(tabNamed(page, /^Review requested/));
  await expect(tabNamed(page, /^Review requested/)).toHaveText(/^Review requested\s*5$/);
  const stackTitle = `Stack #${STACK.number}`;
  const stackRow = main.getByRole('listitem').filter({ hasText: stackTitle });
  const stackToggle = (state: 'Expand' | 'Collapse') => page.getByRole('button', { name: `${state} ${stackTitle}`, exact: true });
  const [bottom, middle, top] = STACK_LAYERS;
  await expect(main.getByRole('listitem')).toHaveCount(3);
  await expect(stackRow).toHaveCount(1);
  await expect(stackRow).toContainText('3 PRs here');
  await expect(stackRow).not.toContainText('not in this view');
  await expect(stackRow).not.toContainText('acme/api #');
  await expect(stackRow.locator('.orow-toggle')).toHaveCount(1);
  if (!phone) await expect(stackRow.locator('svg.orow-mark.stack')).toHaveCount(1);
  await expect(stackToggle('Expand')).toHaveAttribute('aria-expanded', 'false');
  await expectTouchTarget(stackToggle('Expand'), 44);
  await expect(main.getByText(bottom, { exact: true })).toBeHidden();
  // The stack row has a launch arrow for the GitHub stack and no ship pill while no ship is on it.
  await expect(stackRow.getByRole('button', { name: `Launch ${stackTitle}`, exact: true })).toBeVisible();
  await expect(stackRow.getByRole('button', { name: /Open ship$/ })).toHaveCount(0);

  // Expanding shows the layers indented under it, bottom first, each with its own arrow; the tab count stays 5.
  await press(stackToggle('Expand'));
  await expect(stackToggle('Collapse')).toHaveAttribute('aria-expanded', 'true');
  await expect(pane).toBeHidden();
  await expect(main.getByRole('listitem')).toHaveCount(6);
  await expect(main.locator('.orow-title').filter({ hasText: /^(Add the store|Wire the store to the API|Show the store in the page)$/ })).toHaveText([bottom, middle, top]);
  const stackX = (await main.getByText(stackTitle, { exact: true }).boundingBox())!.x;
  expect((await main.getByText(bottom, { exact: true }).boundingBox())!.x).toBeGreaterThan(stackX + 8);
  await expect(rowOf(page, bottom)).toContainText(phone ? 'in stack #7' : 'in stack #7 · opened 3d ago · @alice');
  await expect(tabNamed(page, /^Review requested/)).toHaveText(/^Review requested\s*5$/);

  // A reload keeps it expanded; a layer's arrow opens the Launch dialog with that PR alone.
  await page.reload();
  await expect(tabNamed(page, /^Review requested/)).toHaveAttribute('aria-selected', 'true');
  await expect(stackToggle('Collapse')).toHaveAttribute('aria-expanded', 'true');
  await press(page.getByRole('button', { name: `Launch ${middle}`, exact: true }));
  await expect(form).toBeVisible();
  await expect(form).toContainText(middle);
  await expect(form.getByLabel('Text')).toHaveValue('https://github.com/acme/api/pull/42');
  await press(form.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(form).toBeHidden();

  // Collapsing again is remembered too, and tapping the row opens a pane, not the Launch dialog.
  await press(stackToggle('Collapse'));
  await page.reload();
  await expect(stackToggle('Expand')).toHaveAttribute('aria-expanded', 'false');
  await expect(main.getByRole('listitem')).toHaveCount(3);
  await press(body(stackTitle));
  await expect(form).toBeHidden();
  await expect(phone ? page.getByRole('dialog', { name: stackTitle }) : pane).toBeVisible();
  await page.keyboard.press('Escape');

  // Refresh now asks the source to search again: the sources' answer time moves.
  await press(tabNamed(page, /^For you/));
  const before = await oldestAnswer(a);
  expect(before).toBeGreaterThan(0);
  const asked = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/sources/gh/refresh');
  await press(refresh);
  await asked;
  await expect.poll(() => oldestAnswer(a)).toBeGreaterThan(before);
  await expect(main).toContainText(/refreshed (just now|\d+m ago)/);

  // A refusal shows its text.
  await page.route('**/api/v1/sources/gh/refresh', (r) => r.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ error: 'The source cannot refresh orders' }) }));
  await press(refresh);
  await expect(main).toContainText('The source cannot refresh orders');
  await page.unroute('**/api/v1/sources/gh/refresh');

  // A plugin tab lists its own orders with their reference and view name; its source lists no `refresh`, so none is offered.
  await press(tabNamed(page, /^Orders: Orders/));
  await expect(main.getByRole('listitem')).toHaveCount(7);
  // The id shows once: the reference slot carries it, and the reason line is the view name.
  if (!phone) {
    const login = rowOf(page, 'Fix the login redirect');
    await expect(login).toContainText('ord-login');
    await expect(login).toContainText('Orders');
    expect(await login.innerText()).not.toMatch(/ord-login[\s\S]*ord-login/);
  }
  await expect(page.getByRole('button', { name: 'Launch Fix the login redirect', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Refresh now', exact: true })).toHaveCount(0);

  // An order with no url shows its id and view on top, and offers no GitHub link.
  await press(page.getByRole('button', { name: 'Open Fix the login redirect', exact: true }));
  await expect(pane.locator('.odetail-top')).toHaveText(/^ord-login · Orders/);
  await expect(pane.getByRole('link', { name: 'Open on GitHub' })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await expect(pane).toBeHidden();

  // The arrow of a plugin row fills the dialog with the order's own text.
  await press(page.getByRole('button', { name: 'Launch Tighten the api limits', exact: true }));
  await expect(form.getByLabel('Text')).toHaveValue('Tighten them.');
  await press(form.getByRole('button', { name: 'Cancel', exact: true }));

  // A ship on an order: its pill replaces the arrow and opens the ship; once it anchors the arrow is back; Launch another ship starts a second.
  await page.goto(a.url + 'app/orders?fleet=zulu');
  const docs = rowOf(page, 'Document the API');
  await expect(docs.getByRole('button', { name: 'Launch Document the API', exact: true })).toBeVisible();
  const ship = await launchShip(a, { fleet: 'zulu', harness: 'claude', source: 'gh', order: 'acme/api#32', cue: 'wait go\nexit 3\n' });
  const pill = docs.getByRole('button', { name: new RegExp(`^${ship.name}, .*Open ship$`) });
  await expect(pill).toBeVisible();
  await expect(pill).toContainText(ship.name);
  await expectTouchTarget(pill, 44);
  await expect(docs.getByRole('button', { name: 'Launch Document the API', exact: true })).toHaveCount(0);
  await expect(group(page, 'Waiting on others').getByRole('listitem')).toHaveCount(FOR_YOU.waiting);
  await press(pill);
  await expect(page).toHaveURL(new RegExp(`/app/ships/${ship.session}`));
  await page.goto(a.url + 'app/orders?fleet=zulu');
  writeFileSync(join(a.home, 'rec', ship.session, 'go'), '');
  await expect(docs.getByRole('button', { name: 'Launch Document the API', exact: true })).toBeVisible();
  await expect(docs.getByRole('button', { name: /Open ship$/ })).toHaveCount(0);
  // Launch another ship, from a second ship on the same order.
  const second = await launchShip(a, { fleet: 'zulu', harness: 'claude', source: 'gh', order: 'acme/api#32', anyway: true, cue: 'wait go\nexit 3\n' });
  expect(second.session).not.toBe(ship.session);
  await expect(docs.getByRole('button', { name: new RegExp(`^${second.name}, .*Open ship$`) })).toBeVisible();
  await press(body('Document the API'));
  await expect(pane.getByRole('link', { name: second.name })).toBeVisible();
  await expect(pane.getByRole('button', { name: 'Launch ship', exact: true })).toHaveCount(0);
  const another = pane.getByRole('button', { name: 'Launch another ship', exact: true });
  await expectTouchTarget(another, 44);
  await press(another);
  await expect(form).toBeVisible();
  const sent = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
  await press(form.getByRole('button', { name: 'Launch', exact: true }));
  expect((await sent).status()).toBe(201);
  await page.goto(a.url + 'app/orders?fleet=zulu&order=gh%2Facme%2Fapi%2332');
  await expect(pane.locator('.odetail-ships li')).toHaveCount(2);
  await expect(page.getByRole('main').getByRole('listitem').filter({ hasText: 'Document the API' })).toHaveCount(1);

  // Ships lists no orders, only a quiet link to the ones that need you; it opens Orders.
  await page.goto(a.url + 'app/ships?fleet=zulu');
  const forYouLink = main.getByRole('link', { name: /^7 for you/ });
  await expect(forYouLink).toBeVisible();
  await expect(forYouLink).toHaveText('7 for you →');
  await expect(page.getByRole('region', { name: 'Orders' })).toHaveCount(0);
  await expect(main.getByText('Tighten the api limits')).toHaveCount(0);
  await expect(main.getByRole('button', { name: /^Launch / })).toHaveCount(0);
  await press(forYouLink);
  await expect(page).toHaveURL(/\/app\/orders(\?|$)/);
  await expect(page).toHaveURL(/fleet=zulu/);
  await expect(tabNamed(page, /^For you/)).toHaveAttribute('aria-selected', 'true');

  // A review ship on the GitHub stack, launched from the stack row. Last, so the counts above are untouched.
  stackAnswer(a, STACK.number, [41, 42, 43].map((number) => ({ number, state: 'open' as const })));
  await page.goto(a.url + 'app/orders?fleet=zulu');
  await press(tabNamed(page, /^Review requested/));
  const launchStack = stackRow.getByRole('button', { name: `Launch Stack #${STACK.number}`, exact: true });

  // The stack pane before any ship: layers bottom to top with their state word, a link to GitHub, no Merge, and Launch ship on stack.
  await press(body(`Stack #${STACK.number}`));
  const stackPane = phone ? page.getByRole('dialog', { name: `Stack #${STACK.number}` }) : pane;
  await expect(stackPane).toBeVisible();
  const text = await stackPane.innerText();
  const at = STACK_LAYERS.map((t) => text.indexOf(t));
  expect(at.every((i) => i >= 0) && at[0] < at[1] && at[1] < at[2]).toBe(true);
  for (const t of STACK_LAYERS) await expect(stackPane.getByRole('listitem').filter({ hasText: t })).toContainText('Review requested');
  await expect(stackPane.getByRole('link', { name: 'Open on GitHub', exact: true })).toHaveAttribute('href', STACK.url);
  await expect(stackPane.getByRole('button', { name: /^Merge/ })).toHaveCount(0);
  await expect(stackPane.getByRole('button', { name: 'Launch another ship', exact: true })).toHaveCount(0);
  await press(stackPane.getByRole('button', { name: 'Launch ship on stack', exact: true }));
  await expect(form).toBeVisible();
  await expect(form).toContainText(`Reviewing stack #${STACK.number} · 3 PRs`);
  await press(form.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(form).toBeHidden();
  await page.keyboard.press('Escape');
  await expect(stackPane).toBeHidden();

  // The row's arrow opens the same dialog; the launch sends the bottom layer's order id and stack: true.
  writeFileSync(join(a.home, 'cue'), 'wait go\n');
  await expect(launchStack).toBeVisible();
  await expectTouchTarget(launchStack, 44);
  await press(launchStack);
  await expect(form).toBeVisible();
  await expect(form).toContainText(`Reviewing stack #${STACK.number} · 3 PRs`);
  await expect(form).not.toContainText('already has ship');
  await expect(form.getByLabel('Text')).toHaveValue(STACK.url);
  const sentStack = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
  await press(form.getByRole('button', { name: 'Launch', exact: true }));
  const launched = await sentStack;
  const body1 = launched.request().postDataJSON() as Record<string, unknown>;
  expect(body1).toMatchObject({ source: 'gh', order_id: 'acme/api#41', stack: true });
  expect(body1).not.toHaveProperty('anyway');
  expect(launched.status()).toBe(201);
  const stackShip = ((await launched.json()) as { ship?: { name: string; url?: string }; name: string; url?: string });
  const started = stackShip.ship ?? stackShip;
  expect(started.url).toBe(STACK.url);

  // The launch opens the ship; back on Orders, the stack row shows the ship in place of its arrow, and each layer shows the same ship.
  await page.goto(a.url + 'app/orders?fleet=zulu');
  await press(tabNamed(page, /^Review requested/));
  const stackPill = (row: Locator) => row.getByRole('button', { name: new RegExp(`^${started.name}, .*Open ship$`) });
  await expect(stackPill(stackRow)).toBeVisible();
  await expect(launchStack).toHaveCount(0);
  await press(stackToggle('Expand'));
  for (const title of STACK_LAYERS) await expect(stackPill(rowOf(page, title))).toBeVisible();
  await press(stackToggle('Collapse'));

  // The pane lists the ship and offers Launch another ship in place of Launch ship on stack.
  await press(body(`Stack #${STACK.number}`));
  await expect(stackPane.getByRole('link', { name: started.name })).toBeVisible();
  await expect(stackPane.getByRole('button', { name: 'Launch ship on stack', exact: true })).toHaveCount(0);
  await expect(stackPane.getByRole('button', { name: /^Merge/ })).toHaveCount(0);

  // Launch another ship warns per layer that already has a ship, and still launches with anyway.
  const anotherOnStack = stackPane.getByRole('button', { name: 'Launch another ship', exact: true });
  await expectTouchTarget(anotherOnStack, 44);
  await press(anotherOnStack);
  await expect(form).toBeVisible();
  for (const n of [41, 42, 43]) await expect(form).toContainText(`PR #${n} already has ship ${started.name}`);
  const sentAgain = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/ships');
  await press(form.getByRole('button', { name: 'Launch', exact: true }));
  expect((await sentAgain).postDataJSON()).toMatchObject({ stack: true, anyway: true });
  // The launch opened the ship; the callers carry on from Orders with For you selected.
  await page.goto(a.url + 'app/orders?fleet=zulu');
  await expect(tabNamed(page, /^For you/)).toHaveAttribute('aria-selected', 'true');
}

/** On the phone, More lists the surfaces the bar has no tab for, and not Orders. */
export async function expectMoreWithoutOrders(page: Page, press: (l: Locator) => Promise<void>) {
  await press(page.getByRole('navigation').getByRole('button', { name: 'More', exact: true }));
  const more = page.getByRole('dialog', { name: 'More' });
  await expect(more.getByRole('link')).toHaveText(['Campaigns', 'Routines', 'Crew', 'Preferences']);
}
