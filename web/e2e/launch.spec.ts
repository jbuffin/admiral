import type { Locator, Page } from '@playwright/test';
import { type Admiral, expect, test } from './fixtures';
import { World, dialog, dock, sidebar } from './world';

// The launch orders dialog (u9 S4-S6). The names below are the contract the
// dialog is built to; the prototype supplies the ones it has
// (designs/prototype/index.html:433-480, `?scene=launch`), and the directory
// field, which the prototype lacks, is drawn above "Sail with".
//
//   dialog "Launch orders": heading "Launch orders", the text "<Fleet> · <Harness> dock", the order's title
//   combobox "Directory": its value is the selected entry's name (the checkout's or search root's folder name);
//     typing filters, and the entries are options of a listbox, each named by its folder name first
//     a selected entry that already has a ship shows the text "Another ship already runs here"
//   under "Sail with", radios (one group):
//     "Commodore" with the badge text "Task force"; disabled with the text "Commodore not found" when the dock has none
//       (shown on hover, or on tap on iPad)
//     one radio per favorite installed on this dock, named by the skill
//     "Custom skill" (a radio) with textbox "Custom skill"; typing in the textbox chooses it
//     "Just launch"
//   listbox "Installed skills" (opens with the textbox): the text "Installed skills" and a match count, at most six options,
//     each named by the skill first, with a button "Star <skill>" / "Unstar <skill>" that neither picks the row nor closes the list;
//     with no match, the text "No installed skill matches. It will launch with the name exactly as typed."
//   the refusal of a sixth favorite, "Unstar one first", and "Skill names have no spaces" show as text in the dialog
//   a launch refused by the server shows its message in an alert in the dialog; the dialog stays open
//   buttons "Hoist sails" and "Stay in port"
//
// Escape closes an open list first, otherwise the dialog. Enter picks the
// highlighted row when the list is open (ArrowDown moves down from the first
// row, which starts highlighted), otherwise it is Hoist sails.

const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

// Opens launch orders the way an engineer without a mouse would: select the
// card, then the dock. (Dragging has its own scenarios.)
async function openLaunch(page: Page, touch: boolean, title: string, fleet: string, harness: string) {
  await press(sidebar(page).getByText(title, { exact: true }), touch);
  await press(dock(page, fleet, harness), touch);
  const d = dialog(page);
  await expect(d).toBeVisible();
  return d;
}

const directory = (d: Locator) => d.getByRole('combobox', { name: 'Directory' });
const radio = (d: Locator, name: string | RegExp) => d.getByRole('radio', { name });
const hoist = (d: Locator) => d.getByRole('button', { name: 'Hoist sails' });

async function chooseDirectory(page: Page, d: Locator, touch: boolean, typed: string, name: string) {
  await directory(d).fill(typed);
  await press(page.getByRole('option', { name: new RegExp(`^${name}`) }), touch);
  await expect(directory(d)).toHaveValue(name);
}

async function world(admiral: Admiral, opts: ConstructorParameters<typeof World>[1] = {}) {
  const w = new World(admiral, opts);
  w.writeConfig();
  return w;
}

test.describe('S4: launch with Commodore', () => {
  test('the last-used directory is selected, typing filters, skills follow the directory, Hoist sails runs /commodore with the whole text there', async ({ page, admiral }, info) => {
    const touch = !!info.project.use.hasTouch;
    const w = await world(admiral, { favorites: ['betaskill'] });
    const alpha = w.checkout('alpha');
    const beta = w.checkout('beta');
    w.projectSkill(beta, 'betaskill'); // installed only for a ship in beta
    w.skill('work', 'claude', 'commodore');
    await admiral.start();
    await w.guardData();

    // A ship launched into alpha earlier makes it the dock's last-used directory.
    await w.launchDirect('work', 'claude', alpha, 'earlier work');
    await expect.poll(() => w.records().length).toBe(1);
    w.endShips();
    await expect.poll(async () => (await w.dirs('work', 'claude')).body.selected).toBe(alpha);
    await expect.poll(async () => (await w.dirs('work', 'claude')).body.entries.find((e: { path: string }) => e.path === alpha).ships_here).toBe(0);

    const text = 'Fix the flaky test\nIt fails on CI only.\nStart with the retry helper.';
    await w.seedOrder(text);
    await page.goto(admiral.url);
    const d = await openLaunch(page, touch, 'Fix the flaky test', 'Work', 'Claude');
    await expect(d.getByText('Work · Claude dock')).toBeVisible();

    // Directory: pre-selected, checkouts before the search root, and typing filters.
    await expect(directory(d)).toHaveValue('alpha');
    await expect(radio(d, /^Commodore/)).toBeChecked();
    await expect(d.getByText('Task force', { exact: true })).toBeVisible();
    await expect(radio(d, 'betaskill')).toHaveCount(0); // the favorite is not installed for alpha
    await directory(d).click();
    await directory(d).fill('');
    const names = await page.getByRole('option').allInnerTexts();
    expect(names.map((n) => n.trim().split(/\s/)[0])).toEqual(['alpha', 'beta', 'work']);
    await directory(d).fill('bet');
    await expect(page.getByRole('option', { name: /^alpha/ })).toHaveCount(0);
    await press(page.getByRole('option', { name: /^beta/ }), touch);
    await expect(directory(d)).toHaveValue('beta');

    // The skills were read again for beta: its project skill's favorite is offered now.
    await expect(radio(d, 'betaskill')).toBeVisible();
    await expect(radio(d, /^Commodore/)).toBeChecked();

    await press(hoist(d), touch);
    await expect(d).toHaveCount(0);
    await expect.poll(() => w.records().length).toBe(2);
    const sailed = w.records()[1];
    expect(sailed.cwd).toBe(beta);
    expect(World.prompt(sailed)).toBe(`/commodore ${text}`);
    await expect(sidebar(page).getByText('Fix the flaky test')).toHaveCount(0);
    w.endShips();
  });
});

test.describe('S5: the dialog in one flow', () => {
  test('no Commodore, Just launch as written, the type-ahead, stars, refusals, Escape and a typed name', async ({ page, admiral }, info) => {
    const touch = !!info.project.use.hasTouch;
    const w = await world(admiral);
    const alpha = w.checkout('alpha');
    for (const name of ['sk1', 'sk2', 'sk3', 'sk4', 'sk5', 'sk6', 'bar-tool', 'baz-tool']) w.skill('work', 'claude', name);
    await admiral.start();
    await w.guardData();
    expect((await w.skills('work', 'claude', alpha)).body.commodore.found).toBe(false);

    const justLaunch = 'Run the audit\n\nkeep the blank line above';
    await w.seedOrder(justLaunch);
    await w.seedOrder('Order B\nsecond line');
    await w.seedOrder('Order C\nthird line');
    await page.goto(admiral.url);

    // Commodore is absent on this dock: disabled, told why, and the default is Just launch.
    let d = await openLaunch(page, touch, 'Run the audit', 'Work', 'Claude');
    await expect(radio(d, /^Commodore/)).toBeDisabled();
    await (touch ? d.getByText('Commodore', { exact: true }).tap() : d.getByText('Commodore', { exact: true }).hover());
    await expect(d.getByText('Commodore not found')).toBeVisible();
    await expect(radio(d, 'Just launch')).toBeChecked();
    await press(hoist(d), touch);
    await expect(d).toHaveCount(0);
    await expect.poll(() => w.records().length).toBe(1);
    expect(World.prompt(w.records()[0])).toBe(justLaunch);
    w.endShips();

    // Order B: the custom skill field.
    d = await openLaunch(page, touch, 'Order B', 'Work', 'Claude');
    const custom = d.getByRole('textbox', { name: 'Custom skill' });
    const list = d.getByRole('listbox', { name: 'Installed skills' });

    // A name with whitespace is refused where it is typed; nothing launches.
    for (const bad of ['foo bar', 'foo\tbar']) {
      await custom.fill(bad);
      await press(hoist(d), touch);
      await expect(d.getByText('Skill names have no spaces')).toBeVisible();
      await expect(d).toBeVisible();
      expect(w.records().length).toBe(1);
    }

    // No match: the empty state says what will happen.
    await custom.fill('zzz');
    await expect(d.getByText('No installed skill matches. It will launch with the name exactly as typed.')).toBeVisible();

    // At most six rows, whatever matches; stars, five allowed and a sixth refused.
    await custom.fill('sk');
    await expect(list).toContainText('Installed skills');
    await expect(list.getByRole('option')).toHaveCount(6);
    for (const n of ['sk1', 'sk2', 'sk3', 'sk4', 'sk5']) {
      await press(list.getByRole('button', { name: `Star ${n}` }), touch);
      await expect(list.getByRole('button', { name: `Unstar ${n}` })).toBeVisible();
    }
    await press(list.getByRole('button', { name: 'Star sk6' }), touch);
    await expect(d.getByText('Unstar one first')).toBeVisible();
    await expect(list.getByRole('button', { name: 'Star sk6' })).toBeVisible();
    for (const n of ['sk1', 'sk2', 'sk3', 'sk4', 'sk5']) await expect(radio(d, n)).toBeVisible();
    await expect(radio(d, 'sk6')).toHaveCount(0);

    // Escape closes the open list first, then the dialog.
    await page.keyboard.press('Escape');
    await expect(list).toHaveCount(0);
    await expect(d).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(d).toHaveCount(0);

    // Reopened: the default moved from the missing Commodore to the first favorite.
    d = await openLaunch(page, touch, 'Order B', 'Work', 'Claude');
    await expect(radio(d, 'sk1')).toBeChecked();

    // Arrow keys and Enter pick a row; Enter again, with the list closed, hoists sails.
    await d.getByRole('textbox', { name: 'Custom skill' }).fill('ba');
    const rows = d.getByRole('listbox', { name: 'Installed skills' }).getByRole('option');
    const shown = (await rows.allInnerTexts()).map((t) => t.trim().split(/\s/)[0]);
    expect(shown.slice().sort()).toEqual(['bar-tool', 'baz-tool']);
    await page.keyboard.press('ArrowDown');
    await page.keyboard.press('Enter');
    await expect(d.getByRole('textbox', { name: 'Custom skill' })).toHaveValue(shown[1]);
    await expect(d).toBeVisible();
    await page.keyboard.press('Enter');
    await expect(d).toHaveCount(0);
    await expect.poll(() => w.records().length).toBe(2);
    expect(World.prompt(w.records()[1])).toBe(`/${shown[1]} Order B\nsecond line`);
    w.endShips();

    // Order C: a name the index cannot see launches as typed, minus one leading slash.
    d = await openLaunch(page, touch, 'Order C', 'Work', 'Claude');
    await d.getByRole('textbox', { name: 'Custom skill' }).fill('/foo');
    await press(d.getByRole('heading', { name: 'Launch orders' }), touch); // leave the field, so the list is closed
    await press(hoist(d), touch);
    await expect(d).toHaveCount(0);
    await expect.poll(() => w.records().length).toBe(3);
    expect(World.prompt(w.records()[2])).toBe('/foo Order C\nthird line');
    w.endShips();
  });
});

test.describe('S6: a busy directory and refusals', () => {
  test('an underway ship warns but does not block; a deleted checkout, a fleet with no search roots and a missing executable show the refusal', async ({ page, admiral }, info) => {
    const touch = !!info.project.use.hasTouch;
    const w = await world(admiral, { refusals: true });
    const alpha = w.checkout('alpha');
    const beta = w.checkout('beta');
    await admiral.start();
    await w.guardData();

    // An underway ship in alpha (the fakes report `working` first).
    await w.launchDirect('work', 'claude', alpha, 'already at sea');
    await expect.poll(() => w.records().length).toBe(1);
    await expect
      .poll(async () => (await w.dirs('work', 'claude')).body.entries.find((e: { path: string }) => e.path === alpha).ships_here)
      .toBe(1);

    await w.seedOrder('Sail twice\nsame place');
    await w.seedOrder('Order two\nbeta bound');
    await page.goto(admiral.url);

    // The warning shows, and the launch is still allowed.
    let d = await openLaunch(page, touch, 'Sail twice', 'Work', 'Claude');
    await expect(directory(d)).toHaveValue('alpha');
    await expect(d.getByText('Another ship already runs here')).toBeVisible();
    await press(hoist(d), touch);
    await expect(d).toHaveCount(0);
    await expect.poll(() => w.records().length).toBe(2);
    expect(w.records()[1].cwd).toBe(alpha);
    w.endShips();

    // A checkout deleted after the dialog opened: the server's refusal shows, nothing sails.
    d = await openLaunch(page, touch, 'Order two', 'Work', 'Claude');
    await chooseDirectory(page, d, touch, 'bet', 'beta');
    w.removeCheckout(beta);
    const refused = await w.launchRefusal('work', 'claude', beta);
    expect(refused.status).toBeGreaterThanOrEqual(400);
    await press(hoist(d), touch);
    await expect(d.getByRole('alert')).toContainText(refused.error);
    await expect(d).toBeVisible();
    expect(w.records().length).toBe(2);
    await expect(sidebar(page).getByText('Order two', { exact: true })).toBeVisible();
    await press(d.getByRole('button', { name: 'Stay in port' }), touch);
    await expect(d).toHaveCount(0);

    // A fleet with no search roots: the server's message, and no Hoist sails.
    d = await openLaunch(page, touch, 'Order two', 'Bare', 'Claude');
    await expect(d.getByText('This fleet has no search roots')).toBeVisible();
    await expect(hoist(d)).toHaveCount(0);
    await page.keyboard.press('Escape');
    await expect(d).toHaveCount(0);

    // A dock whose executable is missing still takes the order; the launch is refused in the dialog.
    await press(sidebar(page).getByText('Order two', { exact: true }), touch);
    await expect(dock(page, 'Gone', 'Claude').getByText('Drop here')).toBeVisible();
    await press(dock(page, 'Gone', 'Claude'), touch);
    d = dialog(page);
    await expect(d).toBeVisible();
    await chooseDirectory(page, d, touch, 'alp', 'alpha');
    const noExe = await w.launchRefusal('gone', 'claude', alpha);
    expect(noExe.status).toBeGreaterThanOrEqual(400);
    await press(hoist(d), touch);
    await expect(d.getByRole('alert')).toContainText(noExe.error);
    expect(w.records().length).toBe(2);
    await expect(sidebar(page).getByText('Order two', { exact: true })).toBeVisible();
  });
});
