import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { homedir, networkInterfaces } from 'node:os';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test, type Admiral } from './fixtures';
import { cannedSearch, seedOrders } from './orders';
import { HOME, TS_LOGIN, TS_NAME, WORK, fakeTailscale, gitCheckout, seedFleets, setRemote, stubClipboard, watchPage, withFakeGh } from './shell';

// The fleets are `zulu` (Westwind) and `alpha` (Homeport); zulu is the active fleet until one is chosen.
const sourcesPage = (a: Admiral, fleet: string) => `${a.url}app/preferences/${fleet}/sources`;
const apiSources = async (a: Admiral) => (await (await fetch(`${a.url}api/v1/sources`)).json()) as { key: string; state: string; fleet: string; warnings: string[] }[];
const config = (a: Admiral) => JSON.parse(readFileSync(join(a.home, 'config.json'), 'utf8')) as { sources: Record<string, { fleet: string; settings: Record<string, unknown> }> };
const source = (page: Page, name: string) => page.getByRole('region', { name, exact: true });
const prefsNav = (page: Page) => page.getByRole('navigation', { name: 'Preferences' });

/** seedFleets plus these sources. */
function seedWith(a: Admiral, sources: Record<string, unknown>, roots?: { zulu: string; alpha: string }) {
  seedFleets(a);
  const cfg = JSON.parse(readFileSync(join(a.home, 'config.json'), 'utf8'));
  cfg.sources = sources;
  if (roots) {
    cfg.fleets.zulu.search_roots = [roots.zulu];
    cfg.fleets.alpha.search_roots = [roots.alpha];
  }
  a.writeConfig(cfg);
}

const SECRETS = { token: 'fake-token-work-2', tokens: ['fake-token-work-2', 'decoy-secret-0a1b2c'], keys: [{ name: 'main', key: 'decoy-secret-3d4e5f' }] };

test('a plugin source keeps its stored secrets through a save and moves to another fleet', async ({ page, admiral }) => {
  const src = admiral.ordersSource('orders', 'Orders', { fleet: 'zulu' });
  const settings = src.settings as Record<string, unknown>;
  Object.assign(settings, {
    label: 'before',
    tags: ['alpha-team'],
    ...SECRETS,
    settingsSchema: {
      type: 'object',
      properties: {
        flagdir: { type: 'string' },
        label: { type: 'string', title: 'Label' },
        token: { type: 'string', title: 'Token', format: 'password' },
        tokens: { type: 'array', title: 'Tokens', items: { type: 'string', format: 'password' } },
        tags: { type: 'array', title: 'Tags', items: { type: 'string' } },
        keys: { type: 'array', title: 'Keys', items: { type: 'object', properties: { name: { type: 'string' }, key: { type: 'string', format: 'password' } } } },
      },
    },
  });
  seedWith(admiral, { orders: src });
  await admiral.start();
  const watch = watchPage(page, admiral.url);
  page.on('dialog', (d) => {
    watch.errors.push(`a browser dialog opened: ${d.message()}`);
    void d.dismiss();
  });
  await expect.poll(async () => (await apiSources(admiral)).find((s) => s.key === 'orders')?.state).toBe('ok');

  // Bare /app/preferences opens the active fleet's sources; the fleet shows in the URL of the page.
  await page.goto(admiral.url + 'app/preferences');
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/sources/);
  await expect(page.getByRole('heading', { name: 'Plugin sources', exact: true })).toBeVisible();
  await expect(prefsNav(page).getByRole('group', { name: `${WORK} fleet` }).getByRole('link', { name: 'Plugin sources' })).toBeVisible();
  await expect(prefsNav(page).getByRole('group', { name: `${HOME} fleet` })).toBeVisible();

  const orders = source(page, 'Orders');
  await expect(orders).toBeVisible();
  await expect(orders).toContainText('plugin');
  const form = orders.getByRole('form', { name: 'Settings' });
  // The kept secret is never drawn: its field is empty and says a value is stored.
  await expect(form.getByLabel('Token', { exact: true })).toHaveValue('');
  await expect(form).toContainText(/stored/i);
  await expect(form.getByLabel('Label')).toHaveValue('before');
  // The PR tab's styles stay out of a settings row.
  const tag = form.getByLabel('Tags 1', { exact: true });
  await expect(tag).toHaveValue('alpha-team');
  expect(await tag.locator('xpath=..').evaluate((e) => getComputedStyle(e).flexDirection)).toBe('row');

  await form.getByLabel('Label').fill('after');
  const put = page.waitForRequest((r) => r.method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/sources/orders');
  await form.getByRole('button', { name: 'Save' }).click();
  const sent = (await put).postDataJSON() as { settings: Record<string, unknown> };
  expect(sent.settings.token).toBe('__admiral_unchanged__');
  expect(sent.settings.tokens).toEqual(['__admiral_unchanged__', '__admiral_unchanged__']);
  expect(sent.settings.label).toBe('after');

  // The page shows what the server kept, and so does the file: only the label changed.
  await expect(form.getByLabel('Label')).toHaveValue('after');
  await expect.poll(() => config(admiral).sources.orders.settings.label).toBe('after');
  expect(config(admiral).sources.orders.settings).toMatchObject(SECRETS);
  await page.reload();
  await expect(orders.getByRole('form', { name: 'Settings' }).getByLabel('Token', { exact: true })).toHaveValue('');
  expect(config(admiral).sources.orders.settings).toMatchObject({ label: 'after', ...SECRETS });

  // A secret typed over replaces the stored one; the others stay.
  await form.getByLabel('Token', { exact: true }).fill('decoy-secret-9f8e7d');
  await form.getByRole('button', { name: 'Save' }).click();
  await expect.poll(() => config(admiral).sources.orders.settings.token).toBe('decoy-secret-9f8e7d');
  expect(config(admiral).sources.orders.settings).toMatchObject({ tokens: SECRETS.tokens, keys: SECRETS.keys });

  // Move it to the other fleet: it leaves this page, lands on that fleet's, and keeps its secrets.
  await orders.getByLabel('Move to fleet').selectOption({ label: HOME });
  await orders.getByRole('button', { name: 'Move', exact: true }).click();
  await expect(source(page, 'Orders')).toHaveCount(0);
  await expect.poll(() => config(admiral).sources.orders.fleet).toBe('alpha');
  expect(config(admiral).sources.orders.settings).toMatchObject({ tokens: SECRETS.tokens, keys: SECRETS.keys });
  await page.goto(sourcesPage(admiral, 'alpha'));
  await expect(source(page, 'Orders')).toBeVisible();
  // Looking at another fleet's page leaves the shell's active fleet alone.
  expect(new URL(page.url()).search).not.toContain('fleet=alpha');

  // Restart re-reads the source until its state settles.
  const moved = source(page, 'Orders');
  const starts = admiral.flags('orders').starts();
  const restart = moved.getByRole('button', { name: 'Restart', exact: true });
  let release!: () => void;
  const held = new Promise<void>((r) => (release = r));
  await page.route('**/api/v1/sources/orders/restart', async (route) => {
    await held;
    await route.continue();
  });
  await restart.click();
  // A held restart spins under its own label instead of swapping the text.
  await expect(restart).toHaveAttribute('aria-busy', 'true');
  await expect(restart).toHaveText('Restart');
  release();
  await expect.poll(() => admiral.flags('orders').starts()).toBeGreaterThan(starts);
  await expect(restart).not.toHaveAttribute('aria-busy', 'true');
  await expect(moved).toContainText(/plugin · ok/);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

function checkout(root: string, dir: string, repo: string) {
  gitCheckout(join(root, dir), `https://github.com/${repo}.git`);
}

const githubPage = (a: Admiral, fleet: string) => `${a.url}app/preferences/${fleet}/github`;
type StoredView = { id?: string; title?: string; query?: string; hidden?: boolean };
// The shared seed adds a custom view; a journey about the built-in four starts without it.
function dropSeededViews(a: Admiral) {
  const seeded = config(a);
  delete seeded.sources.gh.settings.views;
  a.writeConfig(seeded);
}
const storedViews = (a: Admiral, key: string) => (config(a).sources[key].settings.views ?? []) as StoredView[];
const viewRows = (page: Page) => page.getByRole('list', { name: 'Views' }).getByRole('listitem');
const viewRow = (page: Page, title: string) => viewRows(page).filter({ has: page.getByRole('button', { name: title, exact: true }) });
const githubNavLink = (page: Page, fleet: string) => prefsNav(page).getByRole('group', { name: `${fleet} fleet` }).getByRole('link', { name: /^GitHub/ });

test('a signed-out fleet says how to sign in with its views greyed, and turns Connected once gh is signed in', async ({ page, admiral }) => {
  const gh = admiral.fakeGh();
  gh.viewer('jbuffin-work');
  gh.count('a-broken', 'reviews:approved', { fail: 'GitHub does not know the qualifier reviews:approved' });
  gh.count('z-open', 'is:open', 9);
  writeFileSync(join(gh.dir, 'search', 'any.match'), 'is:open');
  writeFileSync(join(gh.dir, 'search', 'any.json'), JSON.stringify({ data: { search: { issueCount: 0, nodes: [] } } }));
  const roots = { zulu: join(admiral.home, 'rz'), alpha: join(admiral.home, 'ra') };
  checkout(roots.zulu, 'widgets', 'acme/widgets');
  seedWith(admiral, { gz: { plugin: 'github', fleet: 'zulu', settings: { views: [{ title: 'Broken', query: 'is:open reviews:approved' }] } } }, roots);
  const cfg = JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8'));
  cfg.fleets.zulu.owners = ['acme'];
  admiral.writeConfig(cfg);
  const copied = await stubClipboard(page);
  await admiral.start({ env: gh.env });
  const watch = watchPage(page, admiral.url);
  // Signed out, the source rests at auth_required; ok is only the moment before its first token check.
  await expect.poll(async () => (await apiSources(admiral)).find((s) => s.key === 'gz')?.state).toBe('auth_required');

  await page.goto(githubPage(admiral, 'zulu'));
  await expect(page.getByRole('heading', { name: 'GitHub', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toContainText(`${WORK}'s issues and pull requests. They show in Orders, and routines can watch them.`);
  await expect(githubNavLink(page, WORK)).toHaveAttribute('aria-current', 'page');

  // Signed out: what to run, with Copy, and the views greyed under the board's line.
  const connection = page.getByRole('region', { name: 'Connection', exact: true });
  await expect(connection).toContainText('Not signed in to github.com');
  await expect(connection).toContainText('Run this in a terminal on the Mac, then check again');
  await expect(connection).toContainText('brew install gh');
  const login = connection.getByRole('group', { name: 'gh auth login -h github.com' });
  await expect(login).toBeVisible();
  await login.getByRole('button', { name: 'Copy', exact: true }).click();
  await expect.poll(copied).toBe('gh auth login -h github.com');
  const dot = githubNavLink(page, WORK).getByRole('img', { name: 'not signed in' });
  await expect(dot).toBeVisible();
  await expect(prefsNav(page).getByRole('group', { name: `${WORK} fleet` }).getByRole('link')).toHaveText(['Fleet', 'AdmiralBot', 'GitHub', 'Plugin sources', 'Harness capabilities', 'Usage', 'Limits and quiet hours', 'Allow rules']);

  const views = page.getByRole('region', { name: 'Views', exact: true });
  await expect(views).toContainText('They run once GitHub is connected.');
  await expect(viewRows(page)).toHaveText([/For you.*—/s, /Assigned to me.*—/s, /Review requested.*—/s, /My PRs.*—/s, /Broken.*—/s]);
  for (const sw of await viewRows(page).getByRole('switch').all()) await expect(sw).toBeDisabled();
  for (const grip of await viewRows(page).getByRole('button', { name: /^Move / }).all()) await expect(grip).toBeDisabled();
  await expect(viewRow(page, 'Broken').getByRole('button', { name: 'Broken', exact: true })).toBeDisabled();
  await expect(viewRow(page, 'Broken').getByRole('button', { name: 'Remove view Broken', exact: true })).toBeDisabled();
  await expect(viewRow(page, 'For you')).toContainText('Built in');
  await expect(viewRow(page, 'Broken')).not.toContainText('Built in');
  await expect(connection).toContainText(`Orders is empty for ${WORK}.`);
  await expect(views).not.toContainText(/\d+ open/);

  // Signing in on the Mac and pressing Check again turns the page Connected, and the dot goes.
  gh.token('', 'fake-token-work-3');
  await connection.getByRole('button', { name: 'Check again', exact: true }).click();
  await expect(connection).toContainText('Signed in as');
  await expect(connection).toContainText('@jbuffin-work');
  await expect(connection).toContainText('Connected');
  await expect(connection).toContainText('github.com · through the gh CLI · token checked');
  await expect(connection).not.toContainText('Not signed in');
  await expect(dot).toHaveCount(0);
  await expect(githubNavLink(page, WORK)).toHaveText('GitHub');
  await connection.getByRole('button', { name: 'Switch account', exact: true }).click();
  await expect(connection.getByRole('group', { name: 'gh auth switch -h github.com' })).toBeVisible();

  // The views come alive: plain words, a count each, and the failing search's error with Edit search.
  await expect(viewRow(page, 'For you')).toContainText('Everything below that needs you now');
  await expect(viewRow(page, 'For you')).not.toContainText(/\d+ open/);
  await expect(viewRow(page, 'Review requested')).toContainText('Open PRs where you are a requested reviewer');
  await expect(viewRow(page, 'Review requested')).toContainText('9 open');
  await expect(viewRow(page, 'My PRs')).toContainText('Open PRs you wrote');
  await expect(viewRow(page, 'My PRs')).toContainText('9 open');
  await expect(viewRow(page, 'Broken')).toContainText('is:open reviews:approved');
  await expect(viewRow(page, 'Broken')).toContainText('Search failed: GitHub does not know the qualifier reviews:approved');
  await expect(viewRow(page, 'Broken').getByRole('button', { name: 'Edit search', exact: true })).toBeVisible();
  await expect(viewRow(page, 'My PRs').getByRole('button', { name: 'Edit search', exact: true })).toHaveCount(0);
  await expect(viewRows(page).getByRole('switch').first()).toBeEnabled();

  // Repos: what routes to this fleet, read-only, with the way to edit it.
  const repos = page.getByRole('region', { name: 'Repos', exact: true });
  await expect(repos).toContainText('org: acme');
  await expect(repos).toContainText('acme/widgets');
  await expect(repos).toContainText(/no other fleet claims/i);
  await expect(repos.getByRole('link', { name: 'Edit in Fleet' })).toHaveAttribute('href', /\/app\/preferences\/zulu\/fleet$/);

  // AC5: GitHub is always present: nothing here removes or restarts it, and a custom view's remove has its own name.
  await expect(page.getByRole('button', { name: 'Remove', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Restart', exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Move to fleet')).toHaveCount(0);
  await expect(viewRow(page, 'Broken').getByRole('button', { name: 'Remove view Broken', exact: true })).toBeVisible();
  await expect(viewRow(page, 'My PRs').getByRole('button', { name: /^Remove view/ })).toHaveCount(0);

  // Plugin sources no longer holds a GitHub card.
  await page.goto(sourcesPage(admiral, 'zulu'));
  await expect(page.getByRole('heading', { name: 'Plugin sources', exact: true })).toBeVisible();
  await expect(source(page, 'GitHub')).toHaveCount(0);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('hiding, renaming and moving views changes the Orders tabs and survives a restart', async ({ page, admiral }) => {
  seedOrders(admiral);
  dropSeededViews(admiral);
  admiral.fakeGh().count('z-open', 'is:open', 3);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);
  await expect.poll(() => storedViews(admiral, 'gh').map((v) => v.id)).toEqual(['for-you', 'assigned-to-me', 'review-requested', 'my-prs']);

  await page.goto(githubPage(admiral, 'zulu'));
  await expect(page.getByRole('region', { name: 'Connection', exact: true })).toContainText('Connected');
  await expect(viewRows(page)).toHaveText([/For you/, /Assigned to me/, /Review requested/, /My PRs/]);

  // Each change saves at once; wait for the file before the next.
  const hideReview = viewRow(page, 'Review requested').getByRole('switch');
  await expect(hideReview).toHaveAttribute('aria-checked', 'true');
  await hideReview.click();
  await expect(hideReview).toHaveAttribute('aria-checked', 'false');
  await expect.poll(() => storedViews(admiral, 'gh').find((v) => v.id === 'review-requested')?.hidden).toBe(true);

  await viewRow(page, 'My PRs').getByRole('button', { name: 'My PRs', exact: true }).click();
  const name = page.getByRole('textbox', { name: 'Name of My PRs' });
  await name.fill('Mine');
  await name.press('Enter');
  await expect.poll(() => storedViews(admiral, 'gh').find((v) => v.id === 'my-prs')?.title).toBe('Mine');

  const grip = viewRow(page, 'For you').getByRole('button', { name: 'Move For you', exact: true });
  await grip.focus();
  await grip.press('ArrowDown');
  await expect.poll(() => storedViews(admiral, 'gh').map((v) => v.id)).toEqual(['assigned-to-me', 'for-you', 'review-requested', 'my-prs']);
  // The second press moves the same row on, though the list has changed under it.
  await grip.press('ArrowDown');
  await expect.poll(() => storedViews(admiral, 'gh').map((v) => v.id)).toEqual(['assigned-to-me', 'review-requested', 'for-you', 'my-prs']);

  const orderTabs = async () => {
    await page.getByRole('navigation').getByRole('link', { name: /^Orders/ }).click();
    await expect(page.getByRole('tab').first()).toBeVisible();
  };
  await orderTabs();
  await expect(page.getByRole('tab')).toHaveText([/^Assigned to me/, /^For you/, /^Mine/, /^Orders: Orders/]);

  // Leave the app first so the restart's dropped socket isn't logged by the open page.
  await page.goto('about:blank');
  await admiral.restart();
  await page.goto(githubPage(admiral, 'zulu'));
  await expect(viewRows(page)).toHaveText([/Assigned to me/, /Review requested/, /For you/, /Mine/]);
  await expect(viewRow(page, 'Review requested').getByRole('switch')).toHaveAttribute('aria-checked', 'false');
  await expect(viewRow(page, 'Mine').getByRole('switch')).toHaveAttribute('aria-checked', 'true');
  await orderTabs();
  await expect(page.getByRole('tab')).toHaveText([/^Assigned to me/, /^For you/, /^Mine/, /^Orders: Orders/]);

  // With For you hidden, Orders opens on the first tab that is left.
  await page.goto(githubPage(admiral, 'zulu'));
  await viewRow(page, 'For you').getByRole('switch').click();
  await expect.poll(() => storedViews(admiral, 'gh').find((v) => v.id === 'for-you')?.hidden).toBe(true);
  await orderTabs();
  await expect(page.getByRole('tab')).toHaveText([/^Assigned to me/, /^Mine/, /^Orders: Orders/]);
  await expect(page.getByRole('tab').first()).toHaveAttribute('aria-selected', 'true');

  // Host and account save like any other change; a host that is not a hostname is refused by its field and leaves the file as it was.
  await page.goto(githubPage(admiral, 'zulu'));
  const connection = page.getByRole('region', { name: 'Connection', exact: true });
  await connection.getByRole('button', { name: 'Edit host and account', exact: true }).click();
  await connection.getByRole('textbox', { name: 'Host', exact: true }).fill('not a host');
  const saveFields = connection.getByRole('button', { name: 'Save host and account', exact: true });
  await saveFields.click();
  await expect(connection.getByRole('alert')).toContainText('hostname');
  await expect(saveFields).not.toHaveAttribute('aria-busy', 'true');
  await connection.getByRole('textbox', { name: 'Host', exact: true }).fill('github.com');
  await connection.getByRole('textbox', { name: 'Account', exact: true }).fill('jbuffin-work');
  await saveFields.click();
  await expect.poll(() => config(admiral).sources.gh.settings.account).toBe('jbuffin-work');
  expect(config(admiral).sources.gh.settings.host).toBe('github.com');
  await expect(connection.getByRole('textbox', { name: 'Host', exact: true })).toHaveCount(0);
  await page.goto('about:blank');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

const newViewCard = (page: Page) => page.getByRole('region', { name: 'New view', exact: true });
const shownSearch = (page: Page) => newViewCard(page).getByRole('group', { name: 'Search', exact: true });

test('a new view from the builder saves the search it shows, and its count matches what Orders lists', async ({ page, admiral }) => {
  seedOrders(admiral);
  dropSeededViews(admiral);
  const gh = admiral.fakeGh();
  gh.count('z-open', 'is:open', 3);
  // Both in this fleet's repos, none held by a ship; the keys sort before seedOrders' own.
  const item = (number: number, title: string) => ({ number, title, author: 'fake-viewer', pr: { review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' } });
  gh.count('0-new', 'author:@me review:approved', 2);
  cannedSearch(admiral, '0-new', 'author:@me review:approved', [item(41, 'Ship the approved cache change'), item(42, 'Ship the approved retry fix')]);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);
  await expect.poll(() => storedViews(admiral, 'gh').map((v) => v.id)).toEqual(['for-you', 'assigned-to-me', 'review-requested', 'my-prs']);

  await page.goto(githubPage(admiral, 'zulu'));
  await expect(page.getByRole('region', { name: 'Connection', exact: true })).toContainText('Connected');
  const views = page.getByRole('region', { name: 'Views', exact: true });
  await expect(views).toContainText('The tabs across the top of Orders, in this order. Drag to reorder, switch off to hide.');
  const open = views.getByRole('button', { name: '+ New view', exact: true });
  await open.click();

  const card = newViewCard(page);
  await expect(card).toContainText('Start from a preset or build one. No search syntax needed.');
  await expect(card.getByRole('button', { name: 'Needs my review', exact: true })).toBeFocused();
  await expect(card.getByRole('button', { name: 'Needs my review', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await expect(shownSearch(page)).toContainText('is:open is:pr review-requested:@me draft:false -review:approved');
  await expect(card.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Needs my review');

  // A preset sets the Name and the search; one control then leaves no preset pressed and keeps the Name.
  await card.getByRole('button', { name: 'Approved and ready to merge', exact: true }).click();
  await expect(card.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Approved and ready to merge');
  await expect(shownSearch(page)).toContainText('is:open is:pr author:@me draft:false review:approved');
  await card.getByRole('button', { name: 'Drafts', exact: true }).click();
  await expect(shownSearch(page)).toContainText('is:open is:pr author:@me review:approved');
  await expect(shownSearch(page)).not.toContainText('draft:false');
  for (const p of await card.getByRole('button', { name: /^(Needs my review|My PRs awaiting review|Approved and ready to merge|Assigned issues|Has label…|Mentions me)$/ }).all()) {
    await expect(p).toHaveAttribute('aria-pressed', 'false');
  }
  await expect(card.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('Approved and ready to merge');
  await expect(shownSearch(page)).toContainText('2 matches');

  await card.getByRole('button', { name: 'Add view', exact: true }).click();
  await expect(card).toHaveCount(0);
  await expect.poll(() => storedViews(admiral, 'gh').at(-1)).toEqual({ title: 'Approved and ready to merge', query: 'is:open is:pr author:@me review:approved' });
  await expect(viewRows(page).last()).toContainText('Approved and ready to merge');
  await expect(viewRows(page).last()).toContainText('Open PRs you wrote');

  // The new view is the last tab, and lists as many rows as the count said.
  await page.getByRole('navigation').getByRole('link', { name: /^Orders/ }).click();
  const tab = page.getByRole('tab', { name: /^Approved and ready to merge/ });
  await expect(tab).toBeVisible();
  await expect(page.getByRole('tab').nth(-2)).toHaveText(/^Approved and ready to merge/);
  await tab.click();
  const main = page.getByRole('main');
  await expect(main.getByRole('listitem').filter({ hasText: /Ship the approved/ })).toHaveCount(2);

  // A change to the new row made as soon as it appears, with the server's id still to come, sticks on the page and on the server.
  await page.goto(githubPage(admiral, 'zulu'));
  await expect(viewRows(page).last()).toContainText('Approved and ready to merge');
  await page.getByRole('region', { name: 'Views', exact: true }).getByRole('button', { name: '+ New view', exact: true }).click();
  await newViewCard(page).getByRole('button', { name: 'Mentions me', exact: true }).click();
  await newViewCard(page).getByRole('button', { name: 'Add view', exact: true }).click();
  const hideNew = viewRows(page).last().getByRole('switch');
  await hideNew.click();
  await expect.poll(() => storedViews(admiral, 'gh').at(-1)).toEqual({ title: 'Mentions me', query: 'is:open mentions:@me', hidden: true });
  await expect(hideNew).toHaveAttribute('aria-checked', 'false');
  await page.reload();
  await expect(viewRows(page)).toHaveCount(6);
  await expect(viewRows(page).last().getByRole('switch')).toHaveAttribute('aria-checked', 'false');
  await expect(viewRows(page).nth(4).getByRole('switch')).toHaveAttribute('aria-checked', 'true');
  const grip = viewRows(page).nth(5).getByRole('button', { name: /^Move / });
  await grip.focus();
  await grip.press('ArrowUp');
  await expect.poll(() => storedViews(admiral, 'gh').slice(3).map((v) => v.query ?? v.id)).toEqual(['my-prs', 'is:open mentions:@me', 'is:open is:pr author:@me review:approved']);
  await page.goto('about:blank');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('the New view card shows a failed search count and still saves it, and a hand edit changes what is saved', async ({ page, admiral }) => {
  seedOrders(admiral);
  dropSeededViews(admiral);
  const gh = admiral.fakeGh();
  gh.count('z-open', 'is:open', 3);
  gh.count('0-broken', 'review-requested:@me draft:false', { fail: 'GitHub does not know the qualifier review-requested' });
  gh.count('0-hand', 'label:"by hand"', 1);
  await admiral.start({ env: withFakeGh(admiral) });
  await expect.poll(() => storedViews(admiral, 'gh').length).toBe(4);
  await page.goto(githubPage(admiral, 'zulu'));
  await expect(page.getByRole('region', { name: 'Connection', exact: true })).toContainText('Connected');

  const views = page.getByRole('region', { name: 'Views', exact: true });
  await views.getByRole('button', { name: '+ New view', exact: true }).click();
  const card = newViewCard(page);
  await expect(shownSearch(page)).toContainText('Search failed: GitHub does not know the qualifier review-requested');

  // Cancel closes the card and returns to the button.
  await card.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(card).toHaveCount(0);
  await expect(views.getByRole('button', { name: '+ New view', exact: true })).toBeFocused();
  await views.getByRole('button', { name: '+ New view', exact: true }).click();

  // Blank name: Add view waits. Enter in the Name doesn't submit.
  const name = card.getByRole('textbox', { name: 'Name', exact: true });
  const add = card.getByRole('button', { name: 'Add view', exact: true });
  await name.fill('');
  await expect(add).toBeDisabled();
  await name.fill('Broken but mine');
  await expect(add).toBeEnabled();

  // A hand edit is the search, with the line that says a control rebuilds it, and the rebuild drops it.
  await card.getByText('Edit as a GitHub search', { exact: true }).click();
  const field = card.getByRole('textbox', { name: 'GitHub search', exact: true });
  await field.fill('is:open is:pr label:"by hand"');
  await expect(card).toContainText('Changing an option above rebuilds the search.');
  await expect(shownSearch(page)).toContainText('1 match');
  await card.getByRole('button', { name: 'Author', exact: true }).click();
  await expect(field).toHaveValue('is:open is:pr author:@me draft:false -review:approved');
  await expect(card).not.toContainText('Changing an option above rebuilds the search.');
  await field.fill('is:open is:pr label:"by hand"');
  await add.click();
  await expect(card).toHaveCount(0);
  await expect.poll(() => storedViews(admiral, 'gh').at(-1)).toEqual({ title: 'Broken but mine', query: 'is:open is:pr label:"by hand"' });

  // A search that fails to count still saves.
  await views.getByRole('button', { name: '+ New view', exact: true }).click();
  await expect(shownSearch(page)).toContainText('Search failed');
  await card.getByRole('textbox', { name: 'Name', exact: true }).fill('Failing');
  await card.getByRole('button', { name: 'Add view', exact: true }).click();
  await expect.poll(() => storedViews(admiral, 'gh').at(-1)?.title).toBe('Failing');
  await expect(viewRow(page, 'Failing')).toContainText('Search failed: GitHub does not know the qualifier review-requested');

  // A label: Enter commits it, and Pull requests' parts are off with Issues.
  await views.getByRole('button', { name: '+ New view', exact: true }).click();
  await card.getByRole('button', { name: 'Has label…', exact: true }).click();
  await expect(add).toBeDisabled();
  const label = card.getByRole('textbox', { name: 'Label', exact: true });
  await label.fill('needs design');
  await label.press('Enter');
  await expect(shownSearch(page)).toContainText('is:open involves:@me label:"needs design"');
  await expect(card.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue('needs design');
  await expect(add).toBeEnabled();
  await card.getByRole('button', { name: 'Needs my review', exact: true }).click();
  await card.getByRole('button', { name: 'Issues', exact: true }).click();
  await expect(card.getByRole('button', { name: 'Reviewer', exact: true })).toBeDisabled();
  await expect(card.getByRole('button', { name: 'Drafts', exact: true })).toBeDisabled();
  await page.goto('about:blank');
});

test('the priority labels and the check interval save at once', async ({ page, admiral }) => {
  seedOrders(admiral);
  admiral.fakeGh().count('z-open', 'is:open', 3);
  await admiral.start({ env: withFakeGh(admiral) });
  await page.goto(githubPage(admiral, 'zulu'));
  const prefs = page.getByRole('region', { name: 'Priority and refresh', exact: true });

  // The counts landing moves this section, and a click aimed before that can miss its button.
  await expect(viewRow(page, 'My PRs')).toContainText('3 open');
  await expect(viewRows(page).getByText('—', { exact: true })).toHaveCount(0);

  // The first change writes the whole list, defaults included.
  await prefs.getByRole('button', { name: 'Add label', exact: true }).click();
  await prefs.getByRole('textbox', { name: 'Label', exact: true }).fill('sev1');
  await prefs.getByRole('textbox', { name: 'Label', exact: true }).press('Enter');
  await expect.poll(() => config(admiral).sources.gh.settings.hotLabels).toEqual(['priority: high', 'P0', 'P1', 'urgent', 'sev1']);
  await prefs.getByRole('button', { name: 'Remove label P0', exact: true }).click();
  await expect.poll(() => config(admiral).sources.gh.settings.hotLabels).toEqual(['priority: high', 'P1', 'urgent', 'sev1']);

  await prefs.getByRole('radio', { name: '15 min', exact: true }).click();
  await expect.poll(() => config(admiral).sources.gh.settings.refreshMinutes).toBe(15);
  expect(config(admiral).sources.gh.settings.hotLabels).toEqual(['priority: high', 'P1', 'urgent', 'sev1']);
});

test('a repo checked out in two fleets shows its routing warning under Repos on both fleets\' pages', async ({ page, admiral }) => {
  const roots = { zulu: join(admiral.home, 'rz'), alpha: join(admiral.home, 'ra') };
  checkout(roots.zulu, 'widgets', 'acme/widgets');
  checkout(roots.alpha, 'widgets-fork', 'acme/widgets');
  seedWith(admiral, { gz: { plugin: 'github', fleet: 'zulu' }, ga: { plugin: 'github', fleet: 'alpha' } }, roots);
  await admiral.start({ env: admiral.fakeGh().env });
  await expect.poll(async () => (await apiSources(admiral)).map((s) => `${s.key}:${s.warnings.some((w) => w.includes('acme/widgets'))}`).sort()).toEqual(['ga:true', 'gz:true']);

  // zulu is first in the config, so it is the default fleet; the default line does not depend on the warning.
  for (const [fleet, isDefault] of [['zulu', true], ['alpha', false]] as const) {
    await page.goto(githubPage(admiral, fleet));
    const repos = page.getByRole('region', { name: 'Repos', exact: true });
    await expect(repos).toContainText(/acme\/widgets is checked out in fleets (zulu and alpha|alpha and zulu); \w+ takes its items/);
    if (isDefault) await expect(repos).toContainText(/no other fleet claims/i);
    else await expect(repos).not.toContainText(/no other fleet claims/i);
  }
});

test('a fleet with no GitHub source says so and offers nothing to add', async ({ page, admiral }) => {
  seedWith(admiral, { gz: { plugin: 'github', fleet: 'zulu', settings: { views: [] } } });
  await admiral.start({ env: admiral.fakeGh().env });
  await page.goto(githubPage(admiral, 'alpha'));
  await expect(page.getByRole('heading', { name: 'GitHub', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toContainText(`${HOME} has no GitHub source.`);
  await expect(page.getByRole('main').getByRole('button', { name: /^Add/ })).toHaveCount(0);
  await expect(githubNavLink(page, HOME)).toBeVisible();
});

const zulu = (a: Admiral) => JSON.parse(readFileSync(join(a.home, 'config.json'), 'utf8')).fleets.zulu as Record<string, unknown>;
const viewOf = async (a: Admiral, key: string) =>
  ((await (await fetch(`${a.url}api/v1/config`)).json()) as { fleets: { key: string; search_roots: string[] }[] }).fleets.find((f) => f.key === key);

test('a fleet page opens from the switcher, writes its owners and clone root to the file keeping the ~/ root, and New fleet… starts setup', async ({ page, admiral }) => {
  // The file stores zulu's search root as ~/…, the view shows it expanded.
  const stored = '~/admiral-u3-roots';
  const shownRoot = join(homedir(), 'admiral-u3-roots');
  seedWith(admiral, {}, { zulu: stored, alpha: admiral.home });
  await admiral.start();
  const watch = watchPage(page, admiral.url);
  await expect.poll(async () => (await viewOf(admiral, 'zulu'))?.search_roots).toEqual([shownRoot]);

  await page.goto(admiral.url + 'app/ships');
  await page.getByRole('navigation').getByRole('button', { name: WORK, exact: true }).click();
  await page.getByRole('menu').getByRole('menuitem', { name: `${WORK} fleet settings` }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/fleet/);
  const group = prefsNav(page).getByRole('group', { name: `${WORK} fleet` });
  await expect(group.getByRole('link')).toHaveText(['Fleet', 'AdmiralBot', 'GitHub', 'Plugin sources', 'Harness capabilities', 'Usage', 'Limits and quiet hours', 'Allow rules']);
  await group.getByRole('link', { name: 'Plugin sources' }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/sources/);
  await page.goBack();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/fleet/);
  await page.reload();
  await expect(page.getByRole('textbox', { name: 'Search root 1' })).toHaveValue(shownRoot);

  const clones = join(admiral.home, 'clones');
  const owners = page.getByRole('group', { name: 'Owners' });
  await owners.getByRole('button', { name: 'Add owner' }).click();
  await owners.getByRole('textbox', { name: 'Owner 1' }).fill('acme');
  await page.getByRole('textbox', { name: 'Clone root' }).fill(clones);
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => zulu(admiral).owners).toEqual(['acme']);
  expect(zulu(admiral)).toMatchObject({ clone_root: clones, search_roots: [stored] });
  await page.reload();
  await expect(owners.getByRole('textbox', { name: 'Owner 1' })).toHaveValue('acme');

  // A touch target is at least 44px tall; the component test has no layout.
  expect((await page.getByRole('switch', { name: 'Notify' }).boundingBox())?.height).toBeGreaterThanOrEqual(44);

  // Allow rules: a tap on Remove drops the rule from the store, and the skip stays.
  const allowRules = join(admiral.home, 'allow-rules.json');
  writeFileSync(
    allowRules,
    JSON.stringify({ fleets: { zulu: { rules: [{ harness: 'codex', pattern: ['make', 'check'], added: '2026-11-02T15:00:00Z' }], skipped: [{ harness: 'claude', repo: 'acme/widgets', rule: 'Bash(npm test:*)', added: '2026-11-03T15:00:00Z' }] } } }),
  );
  await group.getByRole('link', { name: 'Allow rules' }).tap();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/allow-rules/);
  const rules = page.getByRole('list', { name: 'Allow rules' });
  await expect(rules.getByRole('listitem')).toHaveText([/make check.*every repo/s]);
  const remove = rules.getByRole('button', { name: 'Remove rule make check' });
  expect((await remove.boundingBox())?.height).toBeGreaterThanOrEqual(44);
  await remove.tap();
  await expect(page.getByText('No allow rules yet.')).toBeVisible();
  const storedRules = () => (JSON.parse(readFileSync(allowRules, 'utf8')) as { fleets: { zulu: { rules: unknown[]; skipped: unknown[] } } }).fleets.zulu;
  expect(storedRules().rules).toEqual([]);
  expect(storedRules().skipped).toHaveLength(1);
  await expect(page.getByRole('list', { name: 'Skipped proposals' }).getByRole('listitem')).toHaveText([/Bash\(npm test:\*\).*acme\/widgets/s]);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);

  // "New fleet…" starts setup and sends the person to the old dashboard; setup is left running.
  await page.goto(admiral.url + 'app/ships');
  await page.getByRole('navigation').getByRole('button', { name: WORK, exact: true }).click();
  await page.getByRole('menu').getByRole('menuitem', { name: 'New fleet…' }).click();
  await expect(page).toHaveURL(admiral.url + 'classic/');
  const setup = (await (await fetch(`${admiral.url}api/v1/setup`)).json()) as { running: boolean };
  expect(setup.running).toBe(true);
});

const hasLan = Object.values(networkInterfaces()).some((l) => l?.some((i) => i.family === 'IPv4' && !i.internal));
const CODE = /\b[A-Z0-9]{4}-[A-Z0-9]{4}\b/;

test('Remote access shows a pairing code, lists the devices that redeem it, renames, revokes one and signs them all out', async ({ page, admiral }) => {
  test.skip(!hasLan, 'needs a LAN address');
  // No fleet is configured: pairing doesn't need one.
  await admiral.start({ lan: true });
  const watch = watchPage(page, admiral.url);
  page.on('dialog', (d) => {
    watch.errors.push(`a browser dialog opened: ${d.message()}`);
    void d.dismiss();
  });

  await page.goto(admiral.url + 'app/preferences');
  await expect(page).toHaveURL(/\/app\/preferences\/admiral\/remote/);
  await expect(page.getByRole('heading', { name: 'Remote access', exact: true })).toBeVisible();
  await expect(page.getByRole('main')).toContainText('All fleets');
  const section = prefsNav(page).getByRole('group', { name: 'Admiral · all fleets' });
  await expect(section.getByRole('link', { name: 'Remote access' })).toHaveAttribute('aria-current', 'page');

  // The code on screen is the one to redeem; minting another would void it.
  const pairing = page.getByRole('group', { name: 'Pairing code' });
  await expect(pairing.getByRole('img', { name: 'Pairing QR code' })).toBeVisible();
  await expect(pairing).toContainText(CODE);
  const codeOf = async () => CODE.exec(await pairing.innerText())![0];
  const first = await codeOf();
  const pair = async (code: string, name: string) => {
    const res = await fetch(`${admiral.lanUrl}api/v1/pair`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code, name }) });
    expect(res.status).toBe(200);
    return ((await res.json()) as { token: string }).token;
  };
  const lanStatus = async (token: string) => (await fetch(`${admiral.lanUrl}api/v1/devices`, { headers: { Authorization: `Bearer ${token}` } })).status;

  const kitchen = await pair(first, 'Kitchen iPad');
  expect(await lanStatus(kitchen)).toBe(200);
  const devices = page.getByRole('list', { name: 'Devices' });
  const row = (name: string) => devices.getByRole('listitem', { name, exact: true });
  await expect(row('Kitchen iPad')).toBeVisible();

  // Rename in place; the server keeps it.
  await row('Kitchen iPad').getByRole('textbox', { name: 'Device name' }).fill('Hall iPad');
  await page.keyboard.press('Enter');
  await expect(row('Hall iPad')).toBeVisible();
  await expect
    .poll(async () => ((await (await fetch(`${admiral.url}api/v1/devices`)).json()) as { devices: { name: string }[] }).devices.map((d) => d.name))
    .toContain('Hall iPad');

  // Revoke asks inside the page; that device's next LAN call is refused.
  await row('Hall iPad').getByRole('button', { name: 'Revoke' }).click();
  await page.getByRole('group', { name: 'Revoke Hall iPad' }).getByRole('button', { name: 'Revoke device' }).click();
  await expect(row('Hall iPad')).toHaveCount(0);
  await expect.poll(() => lanStatus(kitchen)).toBe(401);

  // New code replaces the one on screen.
  await page.getByRole('button', { name: 'New code' }).click();
  await expect.poll(codeOf).not.toBe(first);
  const den = await pair(await codeOf(), 'Den iPad');
  await expect(row('Den iPad')).toBeVisible();

  await page.getByRole('button', { name: 'Sign out all devices', exact: true }).click();
  await page.getByRole('group', { name: 'Sign out all devices' }).getByRole('button', { name: 'Sign out all', exact: true }).click();
  await expect(page.getByRole('status').filter({ hasText: /signed out/i })).toBeVisible();
  await expect(page.getByText('No paired devices yet.')).toBeVisible();
  await expect.poll(() => lanStatus(den)).toBe(401);
  await expect.poll(() => lanStatus(admiral.token)).toBe(401);
  // This browser is local, so it stays signed in.
  await expect(page.getByRole('heading', { name: 'Remote access', exact: true })).toBeVisible();

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a failed Sign out all keeps its error through the next device refresh', async ({ page, admiral }) => {
  await admiral.start();
  await page.route('**/api/v1/devices', async (route) => {
    if (route.request().method() === 'DELETE') await route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'the store is read-only' }) });
    else await route.fallback();
  });
  await page.goto(admiral.url + 'app/preferences/admiral/remote');
  await page.getByRole('button', { name: 'Sign out all devices', exact: true }).click();
  await page.getByRole('group', { name: 'Sign out all devices' }).getByRole('button', { name: 'Sign out all', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'the store is read-only' })).toBeVisible();
  await page.waitForResponse((r) => r.url().endsWith('/api/v1/devices') && r.request().method() === 'GET' && r.ok());
  await page.waitForResponse((r) => r.url().endsWith('/api/v1/devices') && r.request().method() === 'GET' && r.ok());
  await expect(page.getByRole('alert').filter({ hasText: 'the store is read-only' })).toBeVisible();
});

const BOB = 'bob@example.com';

test('Settings shows the Tailscale checks, copies the URL, edits the allowed logins live, signs out a device whose login goes, and sets the mode up', async ({ page, browser, admiral }) => {
  const ts = fakeTailscale(admiral);
  setRemote(admiral, { tailscale: false, allowedLogins: [] });
  await admiral.start({ env: ts.env });
  const port = new URL(admiral.url).port;
  const remote = async () =>
    (await (await fetch(`${admiral.url}api/v1/remote`)).json()) as { tailscale: boolean; allowedLogins: string[]; url: string; checks: { id: string; ok: boolean }[] };
  const watch = watchPage(page, admiral.url);
  const copied = await stubClipboard(page);
  const section = page.getByRole('group', { name: 'Tailscale' });

  await page.goto(admiral.url + 'app/preferences/admiral/remote');
  await expect(section.getByText(/Tailscale mode is off/i)).toBeVisible();

  // A failed check stops Set up at its row and writes nothing, serve included.
  ts.status('NeedsLogin');
  await section.getByRole('button', { name: 'Set up', exact: true }).click();
  const confirm = page.getByRole('group', { name: 'Set up Tailscale mode' });
  await expect(confirm).toContainText('Certificate Transparency');
  expect(ts.serveCalls()).toEqual([]);
  await confirm.getByRole('button', { name: 'Turn on Tailscale mode' }).click();
  await expect(section.getByRole('listitem').filter({ hasText: /logged out/i })).toBeVisible();
  expect((await remote()).tailscale).toBe(false);
  expect(ts.serveCalls()).toEqual([]);

  // With the check fixed, Set up runs serve once on Admiral's own port, turns the mode on and allows the Mac's login.
  ts.status();
  await section.getByRole('button', { name: 'Set up', exact: true }).click();
  const turnOn = page.getByRole('group', { name: 'Set up Tailscale mode' }).getByRole('button', { name: 'Turn on Tailscale mode' });
  await expect(turnOn).not.toHaveAttribute('aria-busy', 'true');
  await turnOn.click();
  await expect.poll(async () => (await remote()).tailscale).toBe(true);
  expect(ts.serveCalls()).toEqual([`serve --bg --https=443 http://127.0.0.1:${port}`]);
  expect((await remote()).allowedLogins).toEqual([TS_LOGIN]);
  await expect(page.getByText(/takes effect the next time it starts/i)).toHaveCount(0);

  // The checks are one row each, and the URL copies.
  const url = `https://${TS_NAME}/`;
  await expect(section.getByRole('listitem')).toHaveCount(4);
  await expect(section.getByText(url)).toBeVisible();
  await section.getByRole('button', { name: 'Copy' }).tap();
  await expect(section.getByRole('status').filter({ hasText: /copied/i })).toBeVisible();
  expect(await copied()).toBe(url);

  // Adding a login shows in the API; so does removing it.
  const logins = page.getByRole('list', { name: 'Allowed logins' });
  await page.getByRole('textbox', { name: 'Allowed login' }).fill(`  ${BOB} `);
  await page.getByRole('button', { name: 'Add login' }).tap();
  await expect(logins.getByRole('listitem').filter({ hasText: BOB })).toBeVisible();
  expect((await remote()).allowedLogins).toEqual([TS_LOGIN, BOB]);

  // A device paired over bob's login, in its own browser context behind the proxy headers, is signed out when bob goes, with no reload.
  const minted = (await (await fetch(`${admiral.url}api/v1/pairing-codes`, { method: 'POST' })).json()) as { code: string };
  const tailnet = await browser.newContext({ extraHTTPHeaders: { 'Tailscale-User-Login': BOB, 'X-Forwarded-For': '100.64.0.9' } });
  try {
    const other = await tailnet.newPage();
    await other.goto(`${admiral.url}pair?code=${minted.code}`);
    await other.goto(admiral.url + 'app/inbox');
    await expect(other.getByRole('navigation').first()).toBeVisible();
    await other.evaluate(() => ((window as unknown as { __kept?: boolean }).__kept = true));

    await logins.getByRole('listitem').filter({ hasText: BOB }).getByRole('button', { name: `Remove ${BOB}` }).tap();
    await expect.poll(async () => (await remote()).allowedLogins).toEqual([TS_LOGIN]);
    await expect(other.getByRole('heading', { name: 'This device was signed out' })).toBeVisible();
    await expect(other.getByLabel('Pairing code')).toBeVisible();
    expect(await other.evaluate(() => (window as unknown as { __kept?: boolean }).__kept)).toBe(true);
  } finally {
    await tailnet.close();
  }

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

// The move message is pushed through a routed copy of the page's own ships socket; the server's real push is pinned in the Go e2e.
test('Turn off Tailscale mode asks first, keeps the logins and serve, names the command that stops serve, and a move message sends the page to its address', async ({ page, admiral }) => {
  const ts = fakeTailscale(admiral);
  setRemote(admiral, { tailscale: true, allowedLogins: [TS_LOGIN] });
  await admiral.start({ env: ts.env });
  ts.serve(new URL(admiral.url).port);
  const remote = async () =>
    (await (await fetch(`${admiral.url}api/v1/remote`)).json()) as { tailscale: boolean; allowedLogins: string[] };
  let pushToPage: ((msg: string) => void) | undefined;
  await page.routeWebSocket(/\/ws\/v1/, (ws) => {
    ws.connectToServer();
    pushToPage = (msg) => ws.send(msg);
  });
  const watch = watchPage(page, admiral.url);
  const section = page.getByRole('group', { name: 'Tailscale' });

  await page.goto(admiral.url + 'app/preferences/admiral/remote');
  await expect(section.getByRole('list', { name: 'Checks' })).toBeVisible();
  await expect(page.getByText(/takes effect the next time it starts/i)).toHaveCount(0);
  await expect(page.getByText(/--listen/)).toBeVisible();

  // Backing out of the confirm changes nothing.
  await section.getByRole('button', { name: 'Turn off', exact: true }).tap();
  const confirm = page.getByRole('group', { name: 'Turn off Tailscale mode' });
  await confirm.getByRole('button', { name: 'Cancel' }).tap();
  expect((await remote()).tailscale).toBe(true);

  await section.getByRole('button', { name: 'Turn off', exact: true }).tap();
  await page.getByRole('group', { name: 'Turn off Tailscale mode' }).getByRole('button', { name: 'Turn off Tailscale mode' }).tap();
  await expect.poll(async () => (await remote()).tailscale).toBe(false);
  expect((await remote()).allowedLogins).toEqual([TS_LOGIN]);
  expect(ts.serveCalls()).toEqual([]);
  await expect(section.getByText(/tailscale mode is off/i)).toBeVisible();
  await expect(page.getByText('tailscale serve --https=443 off')).toBeVisible();
  await expect(page.getByRole('button', { name: /tailscale serve/i })).toHaveCount(0);

  // The move message sends this page to the address it carries.
  await expect.poll(() => pushToPage).toBeDefined();
  pushToPage!(JSON.stringify({ type: 'move', url: `${admiral.url}app/inbox` }));
  await expect(page).toHaveURL(/\/app\/inbox/);

  expect(watch.errors).toEqual([]);

});

test("a fleet's AdmiralBot page writes its chat settings, instructions, notes and commitments to the files and proves Codex through Check again", async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true, versioned: true });
  const ledgerAt = (n: number) => `{"v":1,"ts":"2026-11-1${n}T09:00:00Z","event":"added","id":"n${n}","kind":"note","text":"%s","via":"admiralbot"}\n`;
  mkdirSync(join(admiral.home, 'chat', 'harbor'), { recursive: true });
  writeFileSync(
    join(admiral.home, 'chat', 'harbor', 'ledger.jsonl'),
    [
      ledgerAt(1).replace('%s', 'Deploys go out on Tuesdays.'),
      ledgerAt(3).replace('%s', `Stale note to delete. ${'x'.repeat(300)}`),
      '{"v":1,"ts":"2026-11-05T09:00:00Z","event":"added","id":"c2","kind":"commitment","text":"Ring the bell at midsummer.","via":"admiralbot","trigger":{"kind":"time","subject":"2099-06-15T12:00:00Z"},"action":"remind"}\n',
    ].join(''),
  );
  await admiral.start();
  await expect.poll(async () => ((await (await fetch(`${admiral.url}api/v1/chat/harbor/notes`)).json()) as { notes: unknown[] }).notes.length).toBe(2);
  const watch = watchPage(page, admiral.url);
  const harbor = () => JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8')).fleets.harbor as { chat?: Record<string, unknown> };
  const pick = (g: string, name: string) => page.getByRole('radiogroup', { name: g }).getByRole('radio', { name, exact: true });

  await page.goto(admiral.url + 'app/preferences/zulu/sources');
  await prefsNav(page).getByRole('group', { name: 'Harborlight fleet' }).getByRole('link', { name: 'AdmiralBot', exact: true }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/harbor\/admiralbot/);
  const main = page.getByRole('main');

  // A change saves through the fleet; back to the default effort removes the key.
  const model = page.getByLabel('Model', { exact: true });
  await expect(model.locator('option')).toHaveText(['Sonnet', 'Opus', 'Haiku', 'Fable']);
  await model.selectOption('opus');
  await pick('Effort', 'High').click();
  await pick('Refresh context at', '300K').click();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => harbor().chat).toMatchObject({ model: 'opus', effort: 'high', context_threshold: 300000 });
  await page.reload();
  await expect(model).toHaveValue('opus');
  await pick('Effort', 'Default').click();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => harbor().chat?.effort).toBeUndefined();
  expect(harbor().chat).toMatchObject({ model: 'opus' });

  // Instructions and notes reach the file and the ledger.
  const instructions = page.getByRole('textbox', { name: 'Instructions' });
  const note = (text: string) => main.getByRole('listitem').filter({ hasText: text });
  const instructionsFile = () => (existsSync(join(admiral.home, 'chat', 'harbor', 'instructions.md')) ? readFileSync(join(admiral.home, 'chat', 'harbor', 'instructions.md'), 'utf8') : null);
  // A long unbroken note keeps its text column readable beside touch-sized actions, narrow or not; the component test has no layout.
  const original = page.viewportSize()!;
  for (const [width, height] of [[834, 1194], [402, 800]]) {
    await page.setViewportSize({ width, height });
    const text = note('Stale note').locator('.set-text');
    await expect.poll(() => text.evaluate((e) => e.getBoundingClientRect().width)).toBeGreaterThan(150);
    for (const b of await note('Stale note').getByRole('button').all()) expect(await b.evaluate((e) => e.getBoundingClientRect().height)).toBeGreaterThanOrEqual(44);
    const cancels = await main.getByRole('list', { name: 'Commitments' }).getByRole('button', { name: 'Cancel' }).all();
    expect(cancels.length).toBeGreaterThan(0);
    for (const b of cancels) expect(await b.evaluate((e) => e.getBoundingClientRect().height)).toBeGreaterThanOrEqual(44);
  }
  await page.setViewportSize(original);
  await note('Deploys go out').getByRole('button', { name: 'Move to instructions' }).click();
  await expect.poll(instructionsFile).toBe('Deploys go out on Tuesdays.');
  await instructions.fill('Deploys go out on Tuesdays.\n\nBe terse.');
  await page.getByRole('button', { name: 'Save instructions', exact: true }).click();
  await expect.poll(instructionsFile).toBe('Deploys go out on Tuesdays.\n\nBe terse.');
  await note('Stale note').getByRole('button', { name: 'Delete' }).click();
  await expect(note('Stale note')).toHaveCount(0);
  await page.reload();
  await expect(instructions).toHaveValue('Deploys go out on Tuesdays.\n\nBe terse.');
  await expect(note('Stale note')).toHaveCount(0);

  // Cancel on a commitment writes a cancelled event the engineer authored, and it stays gone after a reload.
  const list = main.getByRole('list', { name: 'Commitments' });
  await list.getByRole('listitem').filter({ hasText: 'midsummer' }).getByRole('button', { name: 'Cancel' }).click();
  await expect(list.getByRole('listitem').filter({ hasText: 'midsummer' })).toHaveCount(0);
  const ledgerEvents = () =>
    readFileSync(join(admiral.home, 'chat', 'harbor', 'ledger.jsonl'), 'utf8')
      .split('\n')
      .filter(Boolean)
      .map((l) => JSON.parse(l) as Record<string, unknown>);
  await expect.poll(() => ledgerEvents().find((e) => e.event === 'cancelled' && e.id === 'c2')).toBeDefined();
  expect(ledgerEvents().find((e) => e.event === 'cancelled' && e.id === 'c2')).not.toHaveProperty('via');

  // Codex's probes run for real against the fake codex, so Check again proves it, and a save sends a model of that harness.
  await page.goto(admiral.url + 'app/preferences/zulu/admiralbot');
  await expect(pick('Harness', 'Codex')).toBeDisabled();
  await main.getByRole('button', { name: /Check again.*Codex/ }).click();
  await expect(pick('Harness', 'Codex')).toBeEnabled();
  await pick('Harness', 'Codex').click();
  const putCodex = page.waitForRequest((r) => r.method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/fleets/zulu');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  const sent = (await putCodex).postDataJSON() as { chat: { harness?: string; model?: string } };
  expect(sent.chat.harness).toBe('codex');
  expect(sent.chat.model).toMatch(/^gpt-/);
  const zulu = () => JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8')).fleets.zulu as { chat?: Record<string, unknown> };
  await expect.poll(() => zulu().chat).toMatchObject({ harness: 'codex' });
  expect(watch.unversioned()).toEqual([]);
});
