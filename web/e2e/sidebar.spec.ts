import type { Locator, Page } from '@playwright/test';
import { type Flags, expect, test } from './fixtures';
import { isTouch } from './sea';

// The sidebar scenarios (u8 S1-S7). The page is driven by roles, accessible
// names and visible text only. The names below are the contract the sidebar
// is built to; the prototype supplies the ones it has (designs/prototype/index.html:121-186).
//
//   complementary "Orders" / "Orders, collapsed"; buttons "Collapse orders" / "Expand orders"
//   textbox "Filter orders"
//   one named region per panel, titled "<panel title>" or "<source name> · <panel title>";
//     in it: a button with that title (aria-expanded), the count as text labelled "<n> orders",
//     textbox "Add order" + button "Add", button "Refresh", button "Restart", alert
//   per card: its text, a "High" badge, the ID as a link, button "Actions for <title>" (menuitems Edit, Remove)
//   rail buttons named "<panel title>, <n> orders"
//   one group per source named by the source's name: button "Source actions" (menuitems Settings, Remove, Refresh),
//     and, when the source has no panels, its Restart button and alert
//   button "Add source plugin" opening a list with a button per plugin
//   dialog "Settings": textbox "Name", the schema's fields or textbox "Settings JSON", button "Save", which closes the view
//   source removal: menuitem "Remove", then button "Confirm remove"
//   Sign in card: the message, a link, the code, button "Copy code"

const sidebar = (page: Page) => page.getByRole('complementary', { name: 'Orders', exact: true });
const rail = (page: Page) => page.getByRole('complementary', { name: 'Orders, collapsed', exact: true });
const panel = (page: Page, title: string) => page.getByRole('region', { name: title, exact: true });
const source = (page: Page, name: string) => page.getByRole('group', { name: name, exact: true });
const filter = (page: Page) => page.getByRole('textbox', { name: 'Filter orders' });
const toggle = (page: Page, title: string, expanded: boolean) =>
  page.getByRole('button', { name: title, exact: true, expanded });

const countOf = (p: Locator, n: number) => expect(p.getByLabel(new RegExp(`^${n} orders?$`))).toBeVisible();
const cards = (p: Locator | Page, ...titles: string[]) => Promise.all(titles.map((t) => expect(p.getByText(t)).toBeVisible()));
const noCards = (p: Locator | Page, ...titles: string[]) => Promise.all(titles.map((t) => expect(p.getByText(t)).toHaveCount(0)));

// config.json for these tests: no fleets (the sidebar does not need any) and
// the given sources. A `sources` key at all stops the server adding freeform.
const sourcesConfig = (sources: Record<string, unknown>) => ({ version: 1, fleets: {}, sources });
const freeform = { plugin: 'freeform', name: 'Orders' };
const tracker = (f: Flags, extra: Record<string, unknown> = {}, name = 'Tracker') => ({
  plugin: 'testplug',
  name,
  settings: { flagdir: f.dir, ...extra },
});

test.describe('S1: fresh install', () => {
  test('one empty "Orders" panel with an Add order field', async ({ page, admiral }) => {
    await admiral.start();
    await page.goto(admiral.url);

    await expect(sidebar(page).getByRole('heading', { level: 2, name: 'Orders' })).toBeVisible();
    await expect(sidebar(page).getByText('Drag a card onto a dock to launch')).toBeVisible();
    const orders = panel(page, 'Orders');
    await expect(orders).toBeVisible();
    await countOf(orders, 0);
    await expect(orders.getByRole('textbox', { name: 'Add order' })).toBeVisible();
    await expect(orders.getByText('All hands deployed. Queue is clear.')).toBeVisible();
  });
});

test.describe('S2: the freeform list', () => {
  test('add a three-line order, edit it, remove it; a refused text shows the server message and adds nothing', async ({
    page,
    admiral,
  }, info) => {
    const touch = isTouch(info);
    await admiral.start();
    await page.goto(admiral.url);
    const orders = panel(page, 'Orders');
    const add = orders.getByRole('textbox', { name: 'Add order' });
    const addButton = orders.getByRole('button', { name: 'Add', exact: true });

    // Enter adds, Shift+Enter is a new line. A touch keyboard has no Shift+Return, so there
    // Return is a new line and the Add button adds.
    await add.click();
    await page.keyboard.type('Fix the flaky test');
    await page.keyboard.press(touch ? 'Enter' : 'Shift+Enter');
    await page.keyboard.type('It fails on CI only.');
    await page.keyboard.press(touch ? 'Enter' : 'Shift+Enter');
    await page.keyboard.type('Start with the retry helper.');
    if (touch) await addButton.tap();
    else await page.keyboard.press('Enter');

    await cards(orders, 'Fix the flaky test');
    await noCards(orders, 'It fails on CI only.');
    await countOf(orders, 1);
    await expect(add).toHaveValue('');

    await orders.getByRole('button', { name: 'Actions for Fix the flaky test' }).click();
    await page.getByRole('menuitem', { name: 'Edit' }).click();
    const editor = orders.getByRole('textbox', { name: 'Edit order' });
    await expect(editor).toHaveValue('Fix the flaky test\nIt fails on CI only.\nStart with the retry helper.');
    await editor.fill('Fix the flaky retry helper\nIt fails on CI only.');
    await orders.getByRole('button', { name: 'Save' }).click();

    await cards(orders, 'Fix the flaky retry helper');
    await noCards(orders, 'Fix the flaky test');
    await countOf(orders, 1);

    // Over-long: the server refuses, the page shows why and adds nothing.
    await add.fill('x'.repeat(12_001));
    if (touch) await addButton.tap();
    else await add.press('Enter');
    await expect(orders.getByRole('alert')).toBeVisible();
    await countOf(orders, 1);

    await orders.getByRole('button', { name: 'Actions for Fix the flaky retry helper' }).click();
    await page.getByRole('menuitem', { name: 'Remove' }).click();
    await noCards(orders, 'Fix the flaky retry helper');
    await countOf(orders, 0);
  });
});

test.describe('S3: a source with refresh only', () => {
  test('no Add field and no edit or remove, a refresh icon, the ID as a link and the High badge', async ({
    page,
    admiral,
  }) => {
    admiral.installTestPlugin();
    const f = admiral.flags('tracker');
    f.panels([
      { id: 'todo', title: 'Todo' },
      { id: 'done', title: 'Done' },
    ]);
    f.orders({
      todo: [
        { id: 'ABC-1', title: 'Rotate the keys', url: 'https://example.com/ABC-1', hot: true },
        { id: 'ABC-2', title: 'Tidy the docs' },
      ],
      done: [{ id: 'ABC-3', title: 'Cut the release' }],
    });
    admiral.writeConfig(sourcesConfig({ tracker: tracker(f) }));
    await admiral.start();
    await page.goto(admiral.url);

    const todo = panel(page, 'Tracker · Todo');
    const done = panel(page, 'Tracker · Done');
    await countOf(todo, 2);
    await countOf(done, 1);

    for (const p of [todo, done]) {
      await expect(p.getByRole('textbox', { name: 'Add order' })).toHaveCount(0);
      await expect(p.getByRole('button', { name: /^Actions for / })).toHaveCount(0);
      await expect(p.getByRole('button', { name: 'Refresh' })).toBeVisible();
    }

    const link = todo.getByRole('link', { name: 'ABC-1' });
    await expect(link).toHaveAttribute('href', 'https://example.com/ABC-1');
    await expect(link).toHaveAttribute('target', '_blank');
    await expect(link).toHaveAttribute('rel', /noopener/);
    await expect(link).toHaveAttribute('rel', /noreferrer/);
    // An order with no url shows no ID.
    await expect(todo.getByText('ABC-2')).toHaveCount(0);
    await expect(todo.getByText('High')).toHaveCount(1);
    await expect(done.getByText('High')).toHaveCount(0);
  });
});

test.describe('S4: collapse is remembered, the filter is not', () => {
  test('collapse a panel and the sidebar, reload; type a filter, reload', async ({ page, admiral }) => {
    admiral.installTestPlugin();
    const f = admiral.flags('tracker');
    f.panels([
      { id: 'todo', title: 'Todo' },
      { id: 'done', title: 'Done' },
    ]);
    f.orders({ todo: [{ id: 'ABC-1', title: 'Rotate the keys' }], done: [{ id: 'ABC-2', title: 'Tidy the docs' }] });
    admiral.writeConfig(sourcesConfig({ tracker: tracker(f) }));
    await admiral.start();
    await page.goto(admiral.url);

    await cards(panel(page, 'Tracker · Todo'), 'Rotate the keys');
    await toggle(page, 'Tracker · Todo', true).click();
    await expect(toggle(page, 'Tracker · Todo', false)).toBeVisible();
    await expect(page.getByText('Rotate the keys')).toHaveCount(0);
    await countOf(panel(page, 'Tracker · Todo'), 1); // collapsed, the count still shows

    await filter(page).fill('tidy');
    await page.reload();
    await expect(toggle(page, 'Tracker · Todo', false)).toBeVisible();
    await expect(filter(page)).toHaveValue('');
    await cards(panel(page, 'Tracker · Done'), 'Tidy the docs');

    await sidebar(page).getByRole('button', { name: 'Collapse orders' }).click();
    await expect(rail(page)).toBeVisible();
    await expect(rail(page).getByRole('button', { name: 'Expand orders' })).toBeVisible();
    await expect(rail(page).getByRole('button', { name: /^Tracker · Done, 1 orders?$/ })).toBeVisible();
    await page.reload();
    await expect(rail(page)).toBeVisible();
    await expect(sidebar(page)).toHaveCount(0);

    // A rail button opens the sidebar again; the panel collapse is still remembered.
    await rail(page).getByRole('button', { name: /^Tracker · Done, 1 orders?$/ }).click();
    await expect(sidebar(page)).toBeVisible();
    await expect(toggle(page, 'Tracker · Todo', false)).toBeVisible();
    await expect(filter(page)).toHaveValue('');
  });
});

test.describe('S5: filter', () => {
  test('by title and by ID across two panels, counts follow, an empty panel stays', async ({ page, admiral }) => {
    admiral.installTestPlugin();
    const f = admiral.flags('tracker');
    f.panels([
      { id: 'todo', title: 'Todo' },
      { id: 'done', title: 'Done' },
    ]);
    f.orders({
      todo: [
        { id: 'ALPHA-1', title: 'Fix login' },
        { id: 'ALPHA-2', title: 'Write docs' },
      ],
      done: [
        { id: 'BETA-1', title: 'Fix tests' },
        { id: 'BETA-2', title: 'Ship release' },
      ],
    });
    admiral.writeConfig(sourcesConfig({ tracker: tracker(f) }));
    await admiral.start();
    await page.goto(admiral.url);
    const todo = panel(page, 'Tracker · Todo');
    const done = panel(page, 'Tracker · Done');
    await countOf(todo, 2);
    await countOf(done, 2);

    // By title, trimmed and case-insensitive.
    await filter(page).fill('  FIX ');
    await countOf(todo, 1);
    await countOf(done, 1);
    await cards(todo, 'Fix login');
    await cards(done, 'Fix tests');
    await noCards(page, 'Write docs', 'Ship release');

    // By ID: one panel has no match and stays, with its count at 0.
    await filter(page).fill('beta-2');
    await countOf(todo, 0);
    await countOf(done, 1);
    await expect(todo.getByText('No orders match that filter.')).toBeVisible();
    await cards(done, 'Ship release');

    await filter(page).fill('');
    await countOf(todo, 2);
    await countOf(done, 2);
  });
});

test.describe('S6: add and configure source plugins', () => {
  test('the same plugin twice, settings saved, one restarts, one removed', async ({ page, admiral }) => {
    admiral.installTestPlugin();
    const a = admiral.flags('alpha');
    const b = admiral.flags('beta');
    a.orders({ main: [{ id: 'A-1', title: 'Alpha order' }] });
    b.orders({ main: [{ id: 'B-1', title: 'Beta order' }] });
    admiral.writeConfig(sourcesConfig({}));
    await admiral.start();
    await page.goto(admiral.url);

    const addPlugin = async (name: string, f: Flags) => {
      await sidebar(page).getByRole('button', { name: 'Add source plugin' }).click();
      await page.getByRole('button', { name: /testplug/ }).click();
      // A new source starts with empty settings, which this plugin cannot run on:
      // it has no schema yet, so its settings come as one JSON field.
      const dialog = page.getByRole('dialog', { name: 'Settings' });
      await dialog.getByRole('textbox', { name: 'Name' }).fill(name);
      await dialog.getByRole('textbox', { name: 'Settings JSON' }).fill(JSON.stringify({ flagdir: f.dir }));
      await dialog.getByRole('button', { name: 'Save' }).click();
      await expect(dialog).toBeHidden();
    };

    await sidebar(page).getByRole('button', { name: 'Add source plugin' }).click();
    await expect(page.getByRole('button', { name: /testplug/ })).toBeEnabled();
    await expect(page.getByText('Test plugin testplug')).toBeVisible();
    await page.keyboard.press('Escape');

    await addPlugin('Alpha', a);
    await addPlugin('Beta', b);

    // Both run the same plugin and list a panel titled "Test", so the panel
    // headers carry the source names.
    await expect(source(page, 'Alpha')).toBeVisible();
    await expect(source(page, 'Beta')).toBeVisible();
    await cards(panel(page, 'Alpha · Test'), 'Alpha order');
    await cards(panel(page, 'Beta · Test'), 'Beta order');

    // Saving one source's settings restarts that source and no other.
    await expect.poll(() => a.starts()).toBeGreaterThanOrEqual(1);
    const beforeA = a.starts();
    const beforeB = b.starts();
    await source(page, 'Alpha').getByRole('button', { name: 'Source actions' }).click();
    await page.getByRole('menuitem', { name: 'Settings' }).click();
    const dialog = page.getByRole('dialog', { name: 'Settings' });
    // Running now, the plugin has declared its schema: a field per property.
    await expect(dialog.getByRole('textbox', { name: 'flagdir' })).toHaveValue(a.dir);
    await dialog.getByRole('button', { name: 'Save' }).click();
    await expect.poll(() => a.starts()).toBeGreaterThan(beforeA);
    expect(b.starts()).toBe(beforeB);
    await expect(dialog).toBeHidden();
    await cards(panel(page, 'Alpha · Test'), 'Alpha order');

    // Remove one: its row and panels go, the other stays.
    await source(page, 'Alpha').getByRole('button', { name: 'Source actions' }).click();
    await page.getByRole('menuitem', { name: 'Remove' }).click();
    await page.getByRole('button', { name: 'Confirm remove' }).click();
    await expect(source(page, 'Alpha')).toHaveCount(0);
    await expect(page.getByRole('region', { name: /^Alpha/ })).toHaveCount(0);
    await expect(source(page, 'Beta')).toBeVisible();
    await expect(panel(page, 'Test')).toBeVisible(); // alone now, so no source name is needed
  });
});

test.describe('S7: sign-in and crashed sources', () => {
  test('auth.required shows a Sign in card whose copy button avoids the Clipboard API; auth.ok clears it', async ({
    page,
    admiral,
  }) => {
    admiral.installTestPlugin();
    const f = admiral.flags('tracker');
    f.orders({ main: [{ id: 'T-1', title: 'Rotate the keys' }] });
    admiral.writeConfig(sourcesConfig({ tracker: tracker(f) }));
    await admiral.start();
    // Admiral is plain HTTP on the LAN, where the iPad has no navigator.clipboard.
    // 127.0.0.1 is a secure context, so take the API away and watch the fallback.
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: undefined });
      const w = window as unknown as { copies: { cmd: string; selected: string }[] };
      w.copies = [];
      document.execCommand = (cmd: string) => {
        // The selection lives in the page or, in the usual fallback, in a hidden field.
        const field = document.activeElement as HTMLInputElement | null;
        const inField = field && typeof field.selectionStart === 'number' ? field.value.slice(field.selectionStart, field.selectionEnd ?? 0) : '';
        w.copies.push({ cmd, selected: String(getSelection()) || inField });
        return true;
      };
    });
    await page.goto(admiral.url);
    const tracked = panel(page, 'Test');
    await cards(tracked, 'Rotate the keys');

    f.put(
      'do-auth-required',
      JSON.stringify({ message: 'Sign in to Tracker', url: 'https://example.com/device', code: 'ABCD-1234' }),
    );
    await expect(tracked.getByText('Sign in to Tracker')).toBeVisible();
    await expect(tracked.getByRole('link', { name: 'https://example.com/device' })).toHaveAttribute(
      'href',
      'https://example.com/device',
    );
    await expect(tracked.getByText('ABCD-1234')).toBeVisible();

    await tracked.getByRole('button', { name: 'Copy code' }).click();
    expect(await page.evaluate(() => (window as unknown as { copies: unknown[] }).copies)).toEqual([
      { cmd: 'copy', selected: 'ABCD-1234' },
    ]);

    f.put('do-auth-ok');
    await expect(page.getByText('Sign in to Tracker')).toHaveCount(0);
    await expect(page.getByText('ABCD-1234')).toHaveCount(0);
    await cards(tracked, 'Rotate the keys');
  });

  test('a crash shows the error and Restart, controls disabled and orders kept; Restart brings it back', async ({
    page,
    admiral,
  }) => {
    admiral.installTestPlugin();
    const f = admiral.flags('tracker');
    f.orders({ main: [{ id: 'T-1', title: 'Rotate the keys' }] });
    // A second source that crashes on its first panels.list: no panels, so its
    // error and Restart sit under its row.
    const early = admiral.flags('early');
    early.put('crash-on-panels');
    admiral.writeConfig(
      sourcesConfig({
        tracker: tracker(f, { capabilities: ['add', 'edit', 'remove', 'refresh'] }),
        early: tracker(early, {}, 'Early'),
      }),
    );
    await admiral.start();
    await page.goto(admiral.url);
    const tracked = panel(page, 'Test');
    await cards(tracked, 'Rotate the keys');
    await expect(tracked.getByRole('textbox', { name: 'Add order' })).toBeEnabled();

    // Crash-loop (so the automatic restart does not hide the state) until the flag is removed.
    f.put('crash-on-initialize');
    f.put('do-crash');
    await expect(tracked.getByRole('alert')).toBeVisible();
    await expect(tracked.getByRole('button', { name: 'Restart' })).toBeVisible();
    await expect(tracked.getByRole('textbox', { name: 'Add order' })).toBeDisabled();
    await expect(tracked.getByRole('button', { name: 'Refresh' })).toBeDisabled();
    await expect(tracked.getByRole('button', { name: 'Actions for Rotate the keys' })).toBeDisabled();
    await source(page, 'Tracker').getByRole('button', { name: 'Source actions' }).click();
    await expect(page.getByRole('menuitem', { name: 'Refresh' })).toBeDisabled();
    await page.keyboard.press('Escape');
    await cards(tracked, 'Rotate the keys'); // the last orders stay

    f.remove('crash-on-initialize');
    await tracked.getByRole('button', { name: 'Restart' }).click();
    await expect(tracked.getByRole('button', { name: 'Restart' })).toHaveCount(0);
    await expect(tracked.getByRole('textbox', { name: 'Add order' })).toBeEnabled();
    await cards(tracked, 'Rotate the keys');

    const row = source(page, 'Early');
    await expect(row.getByRole('alert')).toBeVisible();
    await expect(row.getByRole('button', { name: 'Restart' })).toBeVisible();
    await expect(page.getByRole('region', { name: /Early/ })).toHaveCount(0);
  });
});
