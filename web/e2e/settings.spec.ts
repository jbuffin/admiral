import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test, type Admiral } from './fixtures';
import { HOME, WORK, seedFleets, watchPage } from './shell';

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
    ...SECRETS,
    settingsSchema: {
      type: 'object',
      properties: {
        flagdir: { type: 'string' },
        label: { type: 'string', title: 'Label' },
        token: { type: 'string', title: 'Token', format: 'password' },
        tokens: { type: 'array', title: 'Tokens', items: { type: 'string', format: 'password' } },
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
  await expect(page.getByRole('heading', { name: 'Order sources', exact: true })).toBeVisible();
  await expect(prefsNav(page).getByRole('group', { name: `${WORK} fleet` }).getByRole('link', { name: 'Order sources' })).toBeVisible();
  await expect(prefsNav(page).getByRole('group', { name: `${HOME} fleet` })).toBeVisible();

  const orders = source(page, 'Orders');
  await expect(orders).toBeVisible();
  await expect(orders).toContainText('plugin');
  const form = orders.getByRole('form', { name: 'Settings' });
  // The kept secret is never drawn: its field is empty and says a value is stored.
  await expect(form.getByLabel('Token')).toHaveValue('');
  await expect(form).toContainText(/stored/i);
  await expect(form.getByLabel('Label')).toHaveValue('before');

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
  await expect(orders.getByRole('form', { name: 'Settings' }).getByLabel('Token')).toHaveValue('');
  expect(config(admiral).sources.orders.settings).toMatchObject({ label: 'after', ...SECRETS });

  // A secret typed over replaces the stored one; the others stay.
  await form.getByLabel('Token').fill('decoy-secret-9f8e7d');
  await form.getByRole('button', { name: 'Save' }).click();
  await expect.poll(() => config(admiral).sources.orders.settings.token).toBe('decoy-secret-9f8e7d');
  expect(config(admiral).sources.orders.settings).toMatchObject({ tokens: SECRETS.tokens, keys: SECRETS.keys });

  // Move it to the other fleet: it leaves this page, lands on that fleet's, and keeps its secrets.
  await orders.getByLabel('Move to fleet').selectOption({ label: HOME });
  await orders.getByRole('button', { name: 'Move' }).click();
  await expect(source(page, 'Orders')).toHaveCount(0);
  await expect.poll(() => config(admiral).sources.orders.fleet).toBe('alpha');
  expect(config(admiral).sources.orders.settings).toMatchObject({ tokens: SECRETS.tokens, keys: SECRETS.keys });
  await page.goto(sourcesPage(admiral, 'alpha'));
  await expect(source(page, 'Orders')).toBeVisible();
  // Looking at another fleet's page leaves the shell's active fleet alone.
  expect(new URL(page.url()).search).not.toContain('fleet=alpha');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

function checkout(root: string, dir: string, repo: string) {
  const d = join(root, dir);
  mkdirSync(d, { recursive: true });
  for (const args of [['init', '-q'], ['remote', 'add', 'origin', `https://github.com/${repo}.git`]]) {
    expect(spawnSync('git', args, { cwd: d }).status).toBe(0);
  }
}

test('a GitHub source keeps its host, account and custom views, names its fleet and shows routing warnings', async ({ page, admiral }) => {
  const gh = admiral.fakeGh();
  gh.token('', 'fake-token-default-1');
  gh.token('work', 'fake-token-work-2');
  // Both fleets have acme/widgets checked out, so each source's fleet gets a routing warning.
  const roots = { zulu: join(admiral.home, 'rz'), alpha: join(admiral.home, 'ra') };
  checkout(roots.zulu, 'widgets', 'acme/widgets');
  checkout(roots.alpha, 'widgets-fork', 'acme/widgets');
  writeFileSync(join(gh.dir, 'search', 'any.match'), 'is:open');
  writeFileSync(join(gh.dir, 'search', 'any.json'), JSON.stringify({ data: { search: { issueCount: 0, nodes: [] } } }));
  seedWith(
    admiral,
    {
      gz: { plugin: 'github', fleet: 'zulu', settings: { account: 'work', views: [{ title: 'Newbie', query: 'is:open label:newbie' }] } },
      ga: { plugin: 'github', fleet: 'alpha', settings: { views: [] } },
    },
    roots,
  );
  await admiral.start({ env: gh.env });
  const watch = watchPage(page, admiral.url);
  page.on('dialog', (d) => {
    watch.errors.push(`a browser dialog opened: ${d.message()}`);
    void d.dismiss();
  });
  await expect
    .poll(async () => {
      const list = await apiSources(admiral);
      return list.map((s) => `${s.key}:${s.state}:${s.warnings.some((w) => w.includes('acme/widgets'))}`).sort();
    })
    .toEqual(['ga:ok:true', 'gz:ok:true']);

  await page.goto(sourcesPage(admiral, 'zulu'));
  const github = source(page, 'GitHub');
  await expect(github).toBeVisible();
  await expect(github).toContainText('built in');
  // Told apart by fleet: through the accessible name, so a colour dot with a label counts.
  await expect(github.getByLabel(`${WORK} fleet`).first()).toBeVisible();
  const warning = github.getByRole('status').filter({ hasText: 'acme/widgets' });
  await expect(warning).toBeVisible();
  await expect(github.getByRole('alert')).toHaveCount(0);

  const defaults = github.getByRole('list', { name: 'Default views' }).getByRole('listitem');
  await expect(defaults).toHaveText([/Assigned to me/, /Review requested/, /My PRs/]);
  const form = github.getByRole('form', { name: 'Settings' });
  await expect(form.getByLabel('Host')).toHaveValue('github.com');
  await expect(form.getByLabel('Account')).toHaveValue('work');
  const custom1 = form.getByRole('group', { name: 'Custom view 1' });
  await expect(custom1.getByLabel('Title')).toHaveValue('Newbie');
  await expect(custom1.getByLabel('Query')).toHaveValue('is:open label:newbie');

  // Add a custom view whose title replaces a default, and say so.
  await form.getByRole('button', { name: 'Add view' }).click();
  const custom2 = form.getByRole('group', { name: 'Custom view 2' });
  await custom2.getByLabel('Title').fill('My PRs');
  await custom2.getByLabel('Query').fill('is:open is:pr author:@me label:ready');
  await expect(custom2.getByRole('status')).toContainText(/replaces.*My PRs/i);
  await custom2.getByLabel('Title').fill('Bugs');
  await custom2.getByLabel('Query').fill('is:open label:bug');
  await expect(custom2.getByRole('status')).toHaveCount(0);

  await form.getByLabel('Host').fill('github.com');
  await form.getByLabel('Account').fill('default');
  await custom1.getByLabel('Title').fill('Newbies');
  await form.getByRole('button', { name: 'Save' }).click();
  await expect
    .poll(() => config(admiral).sources.gz.settings)
    .toMatchObject({
      account: 'default',
      views: [
        { title: 'Newbies', query: 'is:open label:newbie' },
        { title: 'Bugs', query: 'is:open label:bug' },
      ],
    });

  await page.reload();
  await expect(source(page, 'GitHub').getByRole('form', { name: 'Settings' }).getByLabel('Account')).toHaveValue('default');
  const again = source(page, 'GitHub').getByRole('form', { name: 'Settings' });
  await expect(again.getByRole('group', { name: 'Custom view 2' }).getByLabel('Title')).toHaveValue('Bugs');

  // Delete a custom view.
  await again.getByRole('group', { name: 'Custom view 1' }).getByRole('button', { name: 'Remove view' }).click();
  await again.getByRole('button', { name: 'Save' }).click();
  await expect.poll(() => (config(admiral).sources.gz.settings.views as unknown[]).length).toBe(1);
  expect(config(admiral).sources.gz.settings.views).toEqual([{ title: 'Bugs', query: 'is:open label:bug' }]);
  expect(Object.keys(config(admiral).sources)).toContain('gz');

  // The other fleet's GitHub source names that fleet, and a GitHub source has no move control.
  await page.goto(sourcesPage(admiral, 'alpha'));
  const other = source(page, 'GitHub');
  await expect(other.getByLabel(`${HOME} fleet`).first()).toBeVisible();
  await expect(other.getByRole('status').filter({ hasText: 'acme/widgets' })).toBeVisible();
  await expect(other.getByLabel('Move to fleet')).toHaveCount(0);
  // Looking at another fleet's page leaves the shell's active fleet alone.
  expect(new URL(page.url()).search).not.toContain('fleet=alpha');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
