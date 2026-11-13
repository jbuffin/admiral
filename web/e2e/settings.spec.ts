import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { homedir, networkInterfaces } from 'node:os';
import { join } from 'node:path';
import type { Page } from '@playwright/test';
import { expect, test, type Admiral } from './fixtures';
import { HOME, TS_LOGIN, TS_NAME, WORK, fakeTailscale, seedFleets, setRemote, stubClipboard, watchPage } from './shell';

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
  await expect(form.getByLabel('Token', { exact: true })).toHaveValue('');
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
  await moved.getByRole('button', { name: 'Restart', exact: true }).click();
  await expect.poll(() => admiral.flags('orders').starts()).toBeGreaterThan(starts);
  await expect(moved.getByRole('button', { name: 'Restart', exact: true })).toBeEnabled();
  await expect(moved).toContainText(/plugin · ok/);

  // Add source offers the test plugin and not GitHub, and adds to this page's fleet.
  await page.getByRole('button', { name: 'Add source', exact: true }).click();
  const add = page.getByRole('group', { name: 'Add source' });
  await expect(add.getByLabel('Plugin').locator('option')).toHaveText(['testplug']);
  await add.getByRole('button', { name: `Add to ${HOME}` }).click();
  await expect.poll(async () => (await apiSources(admiral)).filter((s) => s.fleet === 'alpha').length).toBe(2);
  await expect(page.getByRole('region')).toHaveCount(2);

  // Remove asks inside the page, not with a browser dialog, and the source goes away.
  await moved.getByRole('button', { name: 'Remove', exact: true }).click();
  await moved.getByRole('group', { name: 'Remove Orders' }).getByRole('button', { name: 'Remove source' }).click();
  await expect(page.getByRole('region')).toHaveCount(1);
  await expect.poll(async () => (await apiSources(admiral)).some((s) => s.key === 'orders')).toBe(false);

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
  // iOS Safari zooms the page when a focused field is under 16px.
  expect(await custom1.getByLabel('Title').evaluate((el) => parseFloat(getComputedStyle(el).fontSize))).toBeGreaterThanOrEqual(16);

  // Add a custom view whose title replaces a default, and say so.
  await form.getByRole('button', { name: 'Add view' }).click();
  const custom2 = form.getByRole('group', { name: 'Custom view 2' });
  await custom2.getByLabel('Title').fill('My PRs');
  await custom2.getByLabel('Query').fill('is:open is:pr author:@me label:ready');
  await expect(custom2.getByRole('status')).toContainText(/replaces.*My PRs/i);
  await custom2.getByLabel('Title').fill('Bugs');
  await custom2.getByLabel('Query').fill('is:open label:bug');
  await expect(custom2.getByRole('status')).toHaveCount(0);

  await form.getByLabel('Host').fill('ghe.example.test');
  await form.getByLabel('Account').fill('default');
  await custom1.getByLabel('Title').fill('Newbies');
  await form.getByRole('button', { name: 'Save' }).click();
  await expect
    .poll(() => config(admiral).sources.gz.settings)
    .toMatchObject({
      host: 'ghe.example.test',
      account: 'default',
      views: [
        { title: 'Newbies', query: 'is:open label:newbie' },
        { title: 'Bugs', query: 'is:open label:bug' },
      ],
    });

  await page.reload();
  await expect(source(page, 'GitHub').getByRole('form', { name: 'Settings' }).getByLabel('Account')).toHaveValue('default');
  const again = source(page, 'GitHub').getByRole('form', { name: 'Settings' });
  await expect(again.getByLabel('Host')).toHaveValue('ghe.example.test');
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
  // Removing a GitHub source asks first and says this shell can't add it back.
  await other.getByRole('button', { name: 'Remove', exact: true }).click();
  await expect(other.getByRole('group', { name: 'Remove GitHub' })).toContainText(/can.t add it back/);
  await other.getByRole('button', { name: 'Cancel' }).click();
  await expect(other.getByRole('group', { name: 'Remove GitHub' })).toHaveCount(0);
  // Looking at another fleet's page leaves the shell's active fleet alone.
  expect(new URL(page.url()).search).not.toContain('fleet=alpha');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

const zulu = (a: Admiral) => JSON.parse(readFileSync(join(a.home, 'config.json'), 'utf8')).fleets.zulu as Record<string, unknown>;
const viewOf = async (a: Admiral, key: string) =>
  ((await (await fetch(`${a.url}api/v1/config`)).json()) as { fleets: { key: string; search_roots: string[] }[] }).fleets.find((f) => f.key === key);

test('a fleet page edits search roots, owners, clone root and colour, keeping what the person did not touch, and shows docks read-only', async ({ page, admiral }) => {
  // The file stores zulu's search root as ~/…, the view shows it expanded.
  const stored = '~/admiral-u3-roots';
  const shownRoot = join(homedir(), 'admiral-u3-roots');
  seedWith(admiral, {}, { zulu: stored, alpha: admiral.home });
  await admiral.start();
  const watch = watchPage(page, admiral.url);
  await expect.poll(async () => (await viewOf(admiral, 'zulu'))?.search_roots).toEqual([shownRoot]);

  // The switcher's "<fleet> fleet settings" item opens the page; the nav's Fleet link, reload and Back return to it.
  await page.goto(admiral.url + 'app/ships');
  await page.getByRole('navigation').getByRole('button', { name: WORK, exact: true }).click();
  await page.getByRole('menu').getByRole('menuitem', { name: `${WORK} fleet settings` }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/fleet/);
  await expect(page.getByRole('heading', { name: 'Fleet', exact: true })).toBeVisible();
  const group = prefsNav(page).getByRole('group', { name: `${WORK} fleet` });
  await expect(group.getByRole('link')).toHaveText(['Fleet', 'AdmiralBot', 'Order sources', 'Harness capabilities', 'Usage']);
  await expect(group.getByRole('link', { name: 'Fleet', exact: true })).toHaveAttribute('aria-current', 'page');
  await group.getByRole('link', { name: 'Order sources' }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/sources/);
  await page.goBack();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/fleet/);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Fleet', exact: true })).toBeVisible();

  const roots = page.getByRole('group', { name: 'Search roots' });
  const owners = page.getByRole('group', { name: 'Owners' });
  const cloneRoot = page.getByRole('textbox', { name: 'Clone root' });
  const save = page.getByRole('button', { name: 'Save', exact: true });
  const put = () => page.waitForRequest((r) => r.method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/fleets/zulu');
  await expect(roots.getByRole('textbox', { name: 'Search root 1' })).toHaveValue(shownRoot);

  // Add an owner and an absolute clone root: only those two fields are sent, and the stored ~/ root stays.
  const clones = join(admiral.home, 'clones');
  await owners.getByRole('button', { name: 'Add owner' }).click();
  await owners.getByRole('textbox', { name: 'Owner 1' }).fill('acme');
  await cloneRoot.fill(clones);
  let sent = put();
  await save.click();
  expect((await sent).postDataJSON()).toEqual({ owners: ['acme'], clone_root: clones });
  await expect.poll(() => zulu(admiral).owners).toEqual(['acme']);
  expect(zulu(admiral)).toMatchObject({ clone_root: clones, search_roots: [stored] });
  await page.reload();
  await expect(owners.getByRole('textbox', { name: 'Owner 1' })).toHaveValue('acme');
  await expect(cloneRoot).toHaveValue(clones);

  // Reviewers who can wake a ship: a list saved alone, and still there after a reload.
  const reviewers = page.getByRole('group', { name: 'Reviewers who can wake a ship' });
  await reviewers.getByRole('button', { name: 'Add reviewer' }).click();
  await reviewers.getByRole('textbox', { name: 'Reviewer 1' }).fill('Alice-Dev');
  sent = put();
  await save.click();
  expect((await sent).postDataJSON()).toEqual({ wake_reviewers: ['Alice-Dev'] });
  await expect.poll(() => zulu(admiral).wake_reviewers).toEqual(['Alice-Dev']);
  await page.reload();
  await expect(reviewers.getByRole('textbox', { name: 'Reviewer 1' })).toHaveValue('Alice-Dev');

  // Notify is on until the person turns it off; the switch saves at once, sends only itself and survives a reload.
  const notify = page.getByRole('switch', { name: 'Notify' });
  await expect(notify).toBeChecked();
  expect(zulu(admiral)).not.toHaveProperty('notify');
  sent = put();
  await notify.click();
  expect((await sent).postDataJSON()).toEqual({ notify: false });
  await expect.poll(() => zulu(admiral).notify).toBe(false);
  await page.reload();
  await expect(page.getByRole('switch', { name: 'Notify' })).not.toBeChecked();
  expect((await page.getByRole('switch', { name: 'Notify' }).boundingBox())?.height).toBeGreaterThanOrEqual(44);

  // Changing a list sends it whole, as shown.
  const second = join(admiral.home, 'second');
  await roots.getByRole('button', { name: 'Add search root' }).click();
  await roots.getByRole('textbox', { name: 'Search root 2' }).fill(second);
  sent = put();
  await save.click();
  expect((await sent).postDataJSON()).toEqual({ search_roots: [shownRoot, second] });
  await expect.poll(() => zulu(admiral).search_roots).toEqual([shownRoot, second]);

  // Typing the ~/ form back is a list change too; after it, a colour-only save leaves that stored form alone.
  await roots.getByRole('textbox', { name: 'Search root 1' }).fill(stored);
  await roots.getByRole('button', { name: 'Remove search root 2' }).click();
  await save.click();
  await expect.poll(() => zulu(admiral).search_roots).toEqual([stored]);
  await page.reload();
  sent = put();
  await page.getByLabel('Color', { exact: true }).fill('#1a2b3c');
  await save.click();
  expect((await sent).postDataJSON()).toEqual({ color: '#1a2b3c' });
  await expect.poll(() => zulu(admiral).color).toBe('#1a2b3c');
  await expect(page.getByRole('img', { name: 'Fleet colour' })).toHaveCSS('background-color', 'rgb(26, 43, 60)');
  expect(zulu(admiral).search_roots).toEqual([stored]);

  // "Use default" sends an empty colour and the file drops the key.
  await page.reload();
  sent = put();
  await page.getByRole('button', { name: 'Use default' }).click();
  await expect(page.getByRole('img', { name: 'Default colour' })).toBeVisible();
  await save.click();
  expect((await sent).postDataJSON()).toEqual({ color: '' });
  await expect.poll(() => 'color' in zulu(admiral)).toBe(false);

  // A clone root the server refuses shows its message at the field, and the file keeps the old value.
  await cloneRoot.fill('relative/x');
  await save.click();
  await expect(cloneRoot).toHaveAccessibleDescription(/must be an absolute path or start with ~\//);
  expect(zulu(admiral).clone_root).toBe(clones);

  // Taking away every search root says the fleet then can't launch, before anything is saved.
  await page.reload();
  await roots.getByRole('button', { name: 'Remove search root 1' }).click();
  await expect(page.getByRole('status').filter({ hasText: /launch/i })).toBeVisible();
  expect(zulu(admiral).search_roots).toEqual([stored]);

  // Docks: one row per harness, no field to edit, no env, and the way to change them.
  const docks = page.getByRole('list', { name: 'Docks' });
  await expect(docks.getByRole('listitem')).toHaveCount(2);
  await expect(docks).toContainText('claude');
  await expect(docks).toContainText('codex');
  await expect(docks.getByRole('textbox')).toHaveCount(0);
  await expect(page.getByRole('main')).toContainText('admiral setup');
  await expect(page.getByRole('main')).not.toContainText('CLAUDE_CONFIG_DIR');
  await expect(page.getByRole('main')).not.toContainText('identity/');

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
  await page.getByRole('group', { name: 'Set up Tailscale mode' }).getByRole('button', { name: 'Turn on Tailscale mode' }).click();
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

test("a fleet's AdmiralBot page shows its harness, model, effort and refresh threshold, saves a change that survives a reload, shows a refusal and, for a fleet with no Claude Code dock, why its chat is off", async ({ page, admiral }) => {
  seedFleets(admiral, { chat: true, versioned: true });
  // Notes AdmiralBot saved earlier, written before the start so the chat is live for them.
  const ledgerAt = (n: number) => `{"v":1,"ts":"2026-11-1${n}T09:00:00Z","event":"added","id":"n${n}","kind":"note","text":"%s","via":"admiralbot"}\n`;
  mkdirSync(join(admiral.home, 'chat', 'harbor'), { recursive: true });
  writeFileSync(
    join(admiral.home, 'chat', 'harbor', 'ledger.jsonl'),
    [
      ledgerAt(1).replace('%s', 'Deploys go out on Tuesdays.'),
      ledgerAt(2).replace('%s', 'The staging box is flaky.'),
      ledgerAt(3).replace('%s', `Stale note to delete. ${'x'.repeat(300)}`),
      // Commitments that stay open through the runner's first tick: a time far ahead, and an old no-trigger one already asked about.
      '{"v":1,"ts":"2026-10-01T09:00:00Z","event":"added","id":"c1","kind":"commitment","text":"Ask me about the dock review.","via":"admiralbot","trigger":{"kind":"none"},"action":"remind"}\n',
      '{"v":1,"ts":"2026-11-05T09:00:00Z","event":"added","id":"c2","kind":"commitment","text":"Ring the bell at the new year.","via":"admiralbot","trigger":{"kind":"time","subject":"2099-06-15T12:00:00Z"},"action":"remind"}\n',
      '{"v":1,"ts":"2026-11-06T09:00:00Z","event":"shown","id":"c1"}\n',
    ].join(''),
  );
  await admiral.start();
  await expect.poll(async () => ((await (await fetch(`${admiral.url}api/v1/chat/harbor/notes`)).json()) as { notes: unknown[] }).notes.length).toBe(3);
  const watch = watchPage(page, admiral.url);
  const harbor = () => JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8')).fleets.harbor as { chat?: Record<string, unknown> };
  const pick = (g: string, name: string) => page.getByRole('radiogroup', { name: g }).getByRole('radio', { name, exact: true });

  await page.goto(admiral.url + 'app/preferences/zulu/sources');
  const group = prefsNav(page).getByRole('group', { name: 'Harborlight fleet' });
  await expect(group.getByRole('link')).toHaveText(['Fleet', 'AdmiralBot', 'Order sources', 'Harness capabilities', 'Usage']);
  await group.getByRole('link', { name: 'AdmiralBot', exact: true }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/harbor\/admiralbot/);
  await expect(page.getByRole('heading', { name: 'AdmiralBot', exact: true })).toBeVisible();
  const main = page.getByRole('main');
  await expect(main).toContainText("Your chief of staff for the Harborlight fleet. It only sees this fleet's ships, orders, campaigns and logins.");
  await expect(pick('Harness', 'Claude')).toBeChecked();
  await expect(page.getByRole('radiogroup', { name: 'Harness' }).getByRole('radio')).toHaveText(['Claude']);
  await expect(page.getByRole('radiogroup', { name: 'Model' }).getByRole('radio')).toHaveText(['Sonnet', 'Opus', 'Haiku']);
  await expect(pick('Model', 'Sonnet')).toBeChecked();
  await expect(page.getByRole('radiogroup', { name: 'Effort' }).getByRole('radio')).toHaveText(['Default', 'Low', 'Medium', 'High']);
  await expect(pick('Effort', 'Default')).toBeChecked();
  await expect(page.getByRole('radiogroup', { name: 'Refresh context at' }).getByRole('radio')).toHaveText(['100K', '150K', '300K', 'Never']);
  await expect(pick('Refresh context at', '150K')).toBeChecked();
  // What this ticket does not build is not drawn.
  for (const absent of ['Worker model', 'Also refresh after an idle hour', 'Acting for you', 'Guardrails']) await expect(main).not.toContainText(absent);
  await expect(main).not.toContainText('ledger');
  await expect(page.getByRole('heading', { name: 'What AdmiralBot knows', level: 2 })).toBeVisible();

  // A change saves through the fleet, and a reload shows it. Only the chat object is sent.
  await pick('Model', 'Opus').click();
  await pick('Effort', 'High').click();
  await pick('Refresh context at', '300K').click();
  const put = page.waitForRequest((r) => r.method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/fleets/harbor');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  expect((await put).postDataJSON()).toEqual({ chat: { model: 'opus', effort: 'high', context_threshold: 300000 } });
  await expect.poll(() => harbor().chat).toMatchObject({ model: 'opus', effort: 'high', context_threshold: 300000 });
  await page.reload();
  await expect(pick('Model', 'Opus')).toBeChecked();
  await expect(pick('Effort', 'High')).toBeChecked();
  await expect(pick('Refresh context at', '300K')).toBeChecked();
  // Back to the harness default effort removes the key.
  await pick('Effort', 'Default').click();
  await pick('Refresh context at', 'Never').click();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(() => harbor().chat).toMatchObject({ model: 'opus', context_threshold: 'never' });
  expect(harbor().chat).not.toHaveProperty('effort');

  // A refusal shows the server's error and keeps what the person picked.
  await page.route('**/api/v1/fleets/harbor', (route) =>
    route.request().method() === 'PUT' ? route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'fleets.harbor.chat.model: "haiku" is refused for this test' }) }) : route.fallback(),
  );
  await pick('Model', 'Haiku').click();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('"haiku" is refused for this test');
  await expect(pick('Model', 'Haiku')).toBeChecked();
  await page.unroute('**/api/v1/fleets/harbor');

  // What AdmiralBot knows: the instructions are the engineer's, and the notes AdmiralBot saved can be moved into them or deleted.
  const instructions = page.getByRole('textbox', { name: 'Instructions' });
  const saveInstructions = page.getByRole('button', { name: 'Save instructions', exact: true });
  const note = (text: string) => main.getByRole('listitem').filter({ hasText: text });
  const instructionsFile = () => (existsSync(join(admiral.home, 'chat', 'harbor', 'instructions.md')) ? readFileSync(join(admiral.home, 'chat', 'harbor', 'instructions.md'), 'utf8') : null);
  await expect(main).toContainText('Plain words AdmiralBot follows every time. Only you can change them.');
  await expect(instructions).toHaveValue('');
  await expect(main).toContainText('0 / 2000');
  await expect(saveInstructions).toBeDisabled();
  await expect(main.getByRole('listitem').filter({ hasText: /Deploys go out|staging box|Stale note/ })).toHaveText([/Deploys go out on Tuesdays\./, /The staging box is flaky\./, /Stale note to delete\./]);
  // A long unbroken note keeps its text column readable beside its actions, narrow or not.
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
  await expect(main.getByRole('listitem').filter({ hasText: /Deploys go out|staging box|Stale note/ })).toHaveCount(3);
  // Move to instructions: the text lands in the textarea, the note leaves the list and the file holds it.
  await note('Deploys go out').getByRole('button', { name: 'Move to instructions' }).click();
  await expect(instructions).toHaveValue('Deploys go out on Tuesdays.');
  await expect(note('Deploys go out').getByRole('button', { name: 'Move to instructions' })).toHaveCount(0);
  await expect.poll(instructionsFile).toBe('Deploys go out on Tuesdays.');
  // An edit counts live, over the cap disables Save, and a save writes the file.
  await instructions.fill('😀'.repeat(2001));
  await expect(main).toContainText('2001 / 2000');
  await expect(saveInstructions).toBeDisabled();
  await instructions.fill('Deploys go out on Tuesdays.\n\nBe terse.');
  await expect(saveInstructions).toBeEnabled();
  const saved = page.waitForRequest((r) => r.method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/chat/harbor/instructions');
  await saveInstructions.click();
  expect((await saved).postDataJSON()).toEqual({ text: 'Deploys go out on Tuesdays.\n\nBe terse.' });
  await expect.poll(instructionsFile).toBe('Deploys go out on Tuesdays.\n\nBe terse.');
  await expect(saveInstructions).toBeDisabled();
  // An unsaved draft survives Delete on a note, and Move to instructions waits for a save.
  await instructions.fill('Deploys go out on Tuesdays.\n\nBe terse.\n\nA draft.');
  await expect(note('Stale note').getByRole('button', { name: 'Move to instructions' })).toBeDisabled();
  await note('Stale note').getByRole('button', { name: 'Delete' }).click();
  await expect(note('Stale note')).toHaveCount(0);
  await expect(instructions).toHaveValue('Deploys go out on Tuesdays.\n\nBe terse.\n\nA draft.');
  await instructions.fill('Deploys go out on Tuesdays.\n\nBe terse.');
  await expect(note('staging box').getByRole('button', { name: 'Move to instructions' })).toBeEnabled();
  // Delete dropped a note, and the instructions stay.
  await expect(note('staging box')).toHaveCount(1);
  expect(instructionsFile()).toBe('Deploys go out on Tuesdays.\n\nBe terse.');
  await page.reload();
  await expect(instructions).toHaveValue('Deploys go out on Tuesdays.\n\nBe terse.');
  await expect(main.getByRole('listitem').filter({ hasText: /Deploys go out|staging box|Stale note/ })).toHaveText([/The staging box is flaky\./]);

  // Commitments: the open ones, oldest first with their marks; Cancel is the engineer's alone.
  const list = main.getByRole('list', { name: 'Commitments' });
  const commitment = (text: string) => list.getByRole('listitem').filter({ hasText: text });
  const openIds = async () => ((await (await fetch(`${admiral.url}api/v1/chat/harbor/commitments`)).json()) as { commitments: { id: string }[] }).commitments.map((c) => c.id);
  const ledgerEvents = () =>
    readFileSync(join(admiral.home, 'chat', 'harbor', 'ledger.jsonl'), 'utf8')
      .split('\n')
      .filter(Boolean)
      .map((l) => JSON.parse(l) as Record<string, unknown>);
  // A launch can't stay waiting in this home, so the first read gets one more row.
  let waitingAdded = false;
  await page.route('**/api/v1/chat/harbor/commitments', async (route) => {
    if (waitingAdded || route.request().method() !== 'GET') return route.fallback();
    waitingAdded = true;
    const res = await route.fetch();
    const body = (await res.json()) as { commitments: unknown[] };
    body.commitments.push({
      id: 'w1',
      text: 'Launch the quartz ferry.',
      trigger: { kind: 'ship_sailed', subject: 'gone-session' },
      action: 'launch',
      launch: { prompt: 'SECRET LAUNCH PROMPT' },
      ts: '2026-11-07T09:00:00Z',
      via: 'admiralbot',
      waiting: true,
    });
    await route.fulfill({ response: res, json: body });
  });
  await page.reload();
  await expect(list.getByRole('listitem')).toHaveText([/Ask me about the dock review\./, /Ring the bell at the new year\./, /Launch the quartz ferry\./]);
  await expect(commitment('dock review')).toContainText(/no trigger/i);
  await expect(commitment('dock review')).toContainText(/remind/i);
  await expect(commitment('dock review')).toContainText('Still want this?');
  await expect(commitment('new year')).toContainText('2099');
  await expect(commitment('new year')).toContainText(/remind/i);
  await expect(commitment('new year')).not.toContainText('Still want this?');
  await expect(commitment('quartz ferry')).toContainText('gone-session');
  await expect(commitment('quartz ferry')).toContainText(/waiting/i);
  await expect(commitment('quartz ferry')).toContainText(/launch/i);
  await expect(main).not.toContainText('SECRET LAUNCH PROMPT');
  await expect(main).not.toContainText(/ledger|promise/i);
  await page.unroute('**/api/v1/chat/harbor/commitments');

  // Cancel removes the commitment, and it stays gone after a reload.
  await commitment('new year').getByRole('button', { name: 'Cancel' }).click();
  await expect(commitment('new year')).toHaveCount(0);
  await expect.poll(openIds).toEqual(['c1']);
  const cancelled = ledgerEvents().find((e) => e.event === 'cancelled' && e.id === 'c2');
  expect(cancelled, 'the ledger holds a cancelled event for it').toBeDefined();
  expect(cancelled).not.toHaveProperty('via');
  await page.reload();
  await expect(list.getByRole('listitem')).toHaveText([/Ask me about the dock review\./]);

  // A commitment that closed since the page loaded answers 404: the error shows and the row leaves.
  expect((await fetch(`${admiral.url}api/v1/chat/harbor/commitments/c1`, { method: 'DELETE' })).status).toBe(204);
  await commitment('dock review').getByRole('button', { name: 'Cancel' }).click();
  await expect(page.getByRole('alert')).toContainText(/no open commitment/i);
  await expect(commitment('dock review')).toHaveCount(0);
  await expect(main).toContainText(/no open commitments/i);

  // A fleet with no Claude Code dock: no harness to pick, the reason on top, the settings still editable.
  await prefsNav(page).getByRole('group', { name: `${HOME} fleet` }).getByRole('link', { name: 'AdmiralBot', exact: true }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/alpha\/admiralbot/);
  await expect(main).toContainText(/no Claude Code dock/i);
  await expect(page.getByRole('radiogroup', { name: 'Harness' }).getByRole('radio')).toHaveText(['Codex', 'Copilot']);
  await expect(pick('Harness', 'Copilot')).toBeDisabled();
  await expect(page.getByRole('radiogroup', { name: 'Model' })).toBeVisible();

  // A fleet with a Codex dock lists it greyed out with why, until its probes pass; then it is selectable and brings its own models.
  await page.goto(admiral.url + 'app/preferences/zulu/admiralbot');
  await expect(page.getByRole('radiogroup', { name: 'Harness' }).getByRole('radio')).toHaveText(['Claude', 'Codex']);
  await expect(pick('Harness', 'Codex')).toBeDisabled();
  await expect(main).toContainText(/Codex.*has not been checked/i);
  await expect(main.getByRole('button', { name: /Check again.*Codex/ })).toBeVisible();
  // Codex's probes run for real against the fake codex, so Check again proves it; it reads the fleets again, so the harness turns on without a reload.
  await main.getByRole('button', { name: /Check again.*Codex/ }).click();
  await expect(pick('Harness', 'Codex')).toBeEnabled();
  await expect(main).not.toContainText(/Codex.*has not been checked/i);
  await pick('Harness', 'Codex').click();
  await expect(page.getByRole('radiogroup', { name: 'Model' }).getByRole('radio')).not.toContainText(['Sonnet']);
  await expect(page.getByRole('radiogroup', { name: 'Model' }).getByRole('radio', { checked: true })).toHaveCount(1);
  // Switching the harness sends it with a model of that harness, never the old model alone.
  const putCodex = page.waitForRequest((r) => r.method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/fleets/zulu');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  const sent = (await putCodex).postDataJSON() as { chat: { harness?: string; model?: string } };
  expect(sent.chat.harness).toBe('codex');
  expect(sent.chat.model).toMatch(/^gpt-/);
  const zulu = () => JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8')).fleets.zulu as { chat?: Record<string, unknown> };
  await expect.poll(() => zulu().chat).toMatchObject({ harness: 'codex' });

  // Copilot takes no effort: once proven it offers only Default.
  await page.goto(admiral.url + 'app/preferences/alpha/admiralbot');
  await main.getByRole('button', { name: /Check again.*Copilot/ }).click();
  await expect(pick('Harness', 'Copilot')).toBeEnabled();
  await pick('Harness', 'Copilot').click();
  await expect(page.getByRole('radiogroup', { name: 'Effort' }).getByRole('radio')).toHaveText(['Default']);
  expect(watch.unversioned()).toEqual([]);
});
