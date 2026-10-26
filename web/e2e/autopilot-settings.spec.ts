import type { Locator, Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Admiral } from './fixtures';
import { expect, isTouch, test } from './sea';
import { World, sidebar } from './world';

// The autopilot settings form (#181, u2). The names below are the contract the UI is built to. The server
// side (u1) is covered by e2e/autopilot_settings_test.go; these flows are what the form adds on top.
//
//   every panel header has a button "Autopilot settings for <panel title>" (a gear), after the badge and the
//     pause button and before Refresh, at least 44 px square; it opens a dialog named "Autopilot settings"
//   the dialog has: a close button "Close autopilot settings"; a switch "Autopilot"; comboboxes "Fleet" and
//     "Skill"; for each extra argument a textbox "Argument N" (N from 1) and a button "+ Add argument";
//     number fields "Ships at once" and "Retro every"; buttons "Cancel" and "Save"; and, only when the panel
//     has an entry, a button "Remove autopilot"
//   a new entry (the panel has none) opens with the switch on, the first fleet with a claude dock and Commodore
//   a refused save shows an alert that reads "Not saved." and the server's message; the dialog stays open
//   args that fail the allowlist show read-only, with the text "Set by hand, and a save from here would be refused:"
//   Remove autopilot asks first, in an alert inside the dialog ("Remove autopilot from <panel title>?") with the
//     buttons "Keep it" and "Remove"; Escape closes that alert, not the dialog
//   with config.json invalid the dialog shows an alert "Fix config.json first", every field is disabled, and so
//     are Save and Remove autopilot; nothing is sent

const remote = 'https://github.com/acme/alpha';
const settingsPath = '/api/sources/a/panels/main/autopilot/settings';

type Json = { [key: string]: any };

const entry = (extra: Json = {}): Json => ({ enabled: true, fleet: 'work', harness: 'claude', skill: { kind: 'none' }, ...extra });

interface Options {
  /** The panel's stored entry, as a hand edit wrote it; none for a panel without autopilot. */
  stored?: Json;
  /** An order for the panel, from a checkout with a git remote (the engine skips an order no checkout matches). */
  order?: boolean;
  /** Only fleet "work" is configured, so the first fleet with a claude dock is not in doubt. */
  onlyWork?: boolean;
  resources?: Json;
}

/** One test-plugin source "a" with panel "main" titled "Main", on the World's fake claude docks. */
function setup(admiral: Admiral, w: World, o: Options = {}) {
  w.writeConfig();
  if (o.order) {
    const dir = w.checkout('alpha');
    execFileSync('git', ['remote', 'add', 'origin', remote], { cwd: dir });
    w.skill('work', 'claude', 'commodore');
  }
  admiral.installTestPlugin();
  const flags = admiral.flags('a');
  flags.panels([{ id: 'main', title: 'Main' }]);
  flags.orders({ main: o.order ? [{ id: 'o1', title: 'Order o1', repo: remote, text: `Review ${remote}` }] : [] });
  const config = JSON.parse(readFileSync(join(admiral.home, 'config.json'), 'utf8'));
  if (o.onlyWork) delete config.fleets.home;
  config.sources = { a: { plugin: 'testplug', name: 'Test', settings: { flagdir: flags.dir }, ...(o.stored ? { autopilot: { main: o.stored } } : {}) } };
  if (o.resources) config.resources = o.resources;
  admiral.writeConfig(config);
}

const configText = (admiral: Admiral) => readFileSync(join(admiral.home, 'config.json'), 'utf8');
const storedEntry = (admiral: Admiral) => JSON.parse(configText(admiral)).sources?.a?.autopilot?.main;

async function getSettings(url: string): Promise<Json> {
  return (await (await fetch(`${url}${settingsPath.slice(1)}`)).json()) as Json;
}

async function panelAutopilot(url: string) {
  const body = (await (await fetch(`${url}api/sources`)).json()) as Json;
  const list = (Array.isArray(body) ? body : body.sources) as Json[];
  return list.find((s) => s.key === 'a')?.panels[0]?.autopilot ?? null;
}

const shipCount = async (url: string) => {
  const body = (await (await fetch(`${url}api/ships`)).json()) as Json;
  return ((Array.isArray(body) ? body : body.ships) as Json[]).length;
};

const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());
const panel = (page: Page) => sidebar(page).getByRole('region', { name: 'Main', exact: true });
const gearOf = (page: Page) => panel(page).getByRole('button', { name: 'Autopilot settings for Main', exact: true });
const badge = (page: Page) => panel(page).getByRole('img', { name: /^Autopilot (on|paused)$/ });
const form = (page: Page) => page.getByRole('dialog', { name: 'Autopilot settings' });
const save = (d: Locator) => d.getByRole('button', { name: 'Save', exact: true });
const removeButton = (d: Locator) => d.getByRole('button', { name: 'Remove autopilot', exact: true });

/** Seeding worked before the UI is blamed: the server answers the stored entry, and the panel is on the page. */
async function openPage(page: Page, admiral: Admiral, expected: Json | null) {
  await expect.poll(async () => (await getSettings(admiral.url)).entry, { message: 'the server reads the seeded entry' }).toEqual(expected);
  await page.goto(admiral.url);
  await expect(panel(page)).toBeVisible();
}

/** Presses the gear and waits for the form to have loaded. */
async function openForm(page: Page, touch: boolean) {
  const gear = gearOf(page);
  await expect(gear).toBeVisible();
  await press(gear, touch);
  const d = form(page);
  await expect(d).toBeVisible();
  await expect(d.getByRole('switch', { name: 'Autopilot', exact: true })).toBeVisible();
  return d;
}

test.describe('autopilot settings form', () => {
  test('on a panel with no entry, turning autopilot on launches a ship', { tag: '@critical' }, async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    const w = new World(admiral);
    setup(admiral, w, { order: true, onlyWork: true });
    await admiral.start();
    await openPage(page, admiral, null);
    await expect(badge(page)).toHaveCount(0);

    const d = await openForm(page, touch);
    // A new entry starts on, on the first claude fleet, with Commodore.
    await expect(d.getByRole('switch', { name: 'Autopilot', exact: true })).toBeChecked();
    await expect(d.getByRole('combobox', { name: 'Fleet' })).toHaveValue('work');
    await expect(removeButton(d)).toHaveCount(0);
    await press(save(d), touch);
    await expect(d).toBeHidden();

    await expect.poll(() => storedEntry(admiral), { message: 'the entry in config.json' }).toEqual({ enabled: true, fleet: 'work', harness: 'claude', skill: { kind: 'commodore' } });
    await expect(badge(page)).toHaveAccessibleName('Autopilot on');
    await expect.poll(() => shipCount(admiral.url), { timeout: 20_000, message: 'a ship launched from the panel' }).toBeGreaterThanOrEqual(1);
    // Closing the form returned focus to the gear.
    await expect(gearOf(page)).toBeFocused();
    w.endShips();
  });

  test('a save with an argument off the allowlist is refused and leaves config.json as it was', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    setup(admiral, new World(admiral), { stored: entry() });
    await admiral.start();
    await openPage(page, admiral, entry());
    const before = configText(admiral);

    const d = await openForm(page, touch);
    await press(d.getByRole('button', { name: '+ Add argument' }), touch);
    await d.getByRole('textbox', { name: 'Argument 1' }).fill('--dangerously-skip-permissions');
    await press(save(d), touch);

    const strip = d.getByRole('alert').filter({ hasText: 'Not saved.' });
    await expect(strip).toContainText('--dangerously-skip-permissions is refused');
    await expect(d).toBeVisible();
    expect(configText(admiral)).toBe(before);
  });

  test('args that already fail the allowlist show read-only, and a save of another field keeps them', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    const args = ['--panel-arg', '--dangerously-skip-permissions'];
    setup(admiral, new World(admiral), { stored: entry({ enabled: false, args, max_ships: 2 }) });
    await admiral.start();
    await openPage(page, admiral, entry({ enabled: false, args, max_ships: 2 }));
    expect((await getSettings(admiral.url)).args_error, 'the server flags the stored args').not.toBe('');

    const d = await openForm(page, touch);
    await expect(d.getByText('Set by hand, and a save from here would be refused:')).toBeVisible();
    await expect(d.getByText('--panel-arg --dangerously-skip-permissions')).toBeVisible();
    await expect(d.getByRole('textbox', { name: /^Argument / })).toHaveCount(0);

    await d.getByRole('spinbutton', { name: 'Ships at once' }).fill('3');
    await press(save(d), touch);
    await expect(d).toBeHidden();
    await expect.poll(() => storedEntry(admiral)?.max_ships, { message: 'the new cap in config.json' }).toBe(3);
    expect(storedEntry(admiral)).toMatchObject({ enabled: false, args });
  });

  test('opening the form on a hand-edited entry and saving changes nothing; every control is a touch target', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    // No enabled key, resources out of order: a form that rebuilds them from its own state would rewrite the file.
    const stored = { fleet: 'work', harness: 'claude', skill: { kind: 'none' }, resources: ['simulator', 'phone'] };
    setup(admiral, new World(admiral), {
      stored,
      resources: { phone: { capacity: 1, check: ['true'] }, simulator: { capacity: 1, check: ['true'] } },
    });
    await admiral.start();
    await openPage(page, admiral, stored);
    const before = configText(admiral);

    // A panel without a badge still has its gear; it is a touch target inside the header.
    await expect(badge(page)).toHaveCount(0);
    const gear = gearOf(page);
    await expect(gear).toBeVisible();
    const gearBox = (await gear.boundingBox())!;
    const panelBox = (await panel(page).boundingBox())!;
    expect(Math.min(gearBox.width, gearBox.height), 'the gear is a touch target').toBeGreaterThanOrEqual(44);
    expect(gearBox.x + gearBox.width, 'the gear stays inside the panel').toBeLessThanOrEqual(panelBox.x + panelBox.width);

    const d = await openForm(page, touch);
    const targets: Locator[] = [
      d.getByRole('button', { name: 'Close autopilot settings' }),
      d.getByRole('switch', { name: 'Autopilot', exact: true }),
      d.getByRole('button', { name: '+ Add argument' }),
      save(d),
      removeButton(d),
    ];
    for (const t of targets) {
      const box = (await t.boundingBox())!;
      expect(Math.min(box.width, box.height), await t.innerText().catch(() => 'a control')).toBeGreaterThanOrEqual(44);
    }

    await press(save(d), touch);
    await expect(d).toBeHidden();
    expect(configText(admiral)).toBe(before);
  });

  test('a hand edit made after the form opened is refused with the reopen message, and the hand edit stays', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    const stored = entry({ enabled: false, max_ships: 2 });
    setup(admiral, new World(admiral), { stored });
    await admiral.start();
    await openPage(page, admiral, stored);
    const opened = (await getSettings(admiral.url)).version;

    const d = await openForm(page, touch);
    const config = JSON.parse(configText(admiral));
    config.sources.a.autopilot.main.max_ships = 5;
    admiral.writeConfig(config);
    await expect.poll(async () => (await getSettings(admiral.url)).version, { message: 'the server sees the hand edit' }).not.toBe(opened);
    const edited = configText(admiral);

    await d.getByRole('spinbutton', { name: 'Ships at once' }).fill('4');
    await press(save(d), touch);
    await expect(d.getByRole('alert').filter({ hasText: 'Not saved.' })).toContainText('the config changed; reopen');
    await expect(d).toBeVisible();
    expect(configText(admiral)).toBe(edited);
  });

  test('Remove asks first, deletes the entry and clears the pause', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    setup(admiral, new World(admiral), { stored: entry() });
    await admiral.start();
    await openPage(page, admiral, entry());

    await press(panel(page).getByRole('button', { name: 'Pause autopilot' }), touch);
    await expect(badge(page)).toHaveAccessibleName('Autopilot paused');
    await expect.poll(() => panelAutopilot(admiral.url)).toMatchObject({ paused: true });

    const d = await openForm(page, touch);
    const ask = d.getByRole('alert').filter({ hasText: 'Remove autopilot from Main?' });
    await press(removeButton(d), touch);
    await expect(ask).toBeVisible();
    // Escape closes the question first, then the form.
    await page.keyboard.press('Escape');
    await expect(ask).toHaveCount(0);
    await expect(d).toBeVisible();
    await press(removeButton(d), touch);
    await press(ask.getByRole('button', { name: 'Keep it', exact: true }), touch);
    await expect(ask).toHaveCount(0);
    expect(storedEntry(admiral)).toEqual(entry());

    await press(removeButton(d), touch);
    await press(ask.getByRole('button', { name: 'Remove', exact: true }), touch);
    await expect(d).toBeHidden();
    await expect.poll(() => storedEntry(admiral), { message: 'the entry left config.json' }).toBeUndefined();
    await expect(badge(page)).toHaveCount(0);
    await expect(panel(page).getByRole('button', { name: /^(Pause|Resume) autopilot$/ })).toHaveCount(0);
    await expect.poll(() => panelAutopilot(admiral.url)).toBeNull();

    // The pause is gone with the entry: turned on again, the panel runs.
    const again = await openForm(page, touch);
    await press(save(again), touch);
    await expect(again).toBeHidden();
    await expect(badge(page)).toHaveAccessibleName('Autopilot on');
    await expect.poll(() => panelAutopilot(admiral.url)).toMatchObject({ enabled: true, paused: false });
  });

  test('with config.json invalid the form is read-only and sends nothing', async ({ page, admiral }, info) => {
    const touch = isTouch(info);
    setup(admiral, new World(admiral), { stored: entry() });
    await admiral.start();
    const sent: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/autopilot/settings') && ['PUT', 'DELETE'].includes(r.method())) sent.push(`${r.method()} ${r.url()}`);
    });
    await openPage(page, admiral, entry());

    admiral.writeConfigText('{ "version": 1, "fleets": ');
    await expect.poll(async () => (await getSettings(admiral.url)).config_error, { message: 'the server reports the broken file' }).not.toBe('');
    const before = configText(admiral);

    const d = await openForm(page, touch);
    const alert = d.getByRole('alert').filter({ hasText: 'Fix config.json first' });
    await expect(alert).toBeVisible();
    await expect(alert.locator('code')).not.toBeEmpty();
    await expect(d.getByRole('switch', { name: 'Autopilot', exact: true })).toBeDisabled();
    await expect(d.getByRole('spinbutton', { name: 'Ships at once' })).toBeDisabled();
    await expect(save(d)).toBeDisabled();
    await expect(removeButton(d)).toBeDisabled();

    // A disabled button is not proof: press them anyway and see that nothing goes out.
    await save(d).click({ force: true });
    await removeButton(d).click({ force: true });
    await page.waitForTimeout(500);
    expect(sent, 'no PUT or DELETE reached the settings route').toEqual([]);
    expect(configText(admiral)).toBe(before);
  });
});
