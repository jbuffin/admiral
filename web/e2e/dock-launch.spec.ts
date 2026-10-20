import type { Locator } from '@playwright/test';
import { expect, test } from './fixtures';
import { ships, underwayIn } from './sea';
import { World, dialog, dock, sidebar } from './world';

// Launching a ship from a dock without an order (#102). The names below are
// the contract the dialog is built to, on top of launch.spec.ts's:
//
//   with no order selected, a tap on a dock opens the dialog "Launch orders"
//     (the text "<Fleet> · <Harness> dock"), with no radios, no "Text before the
//     order" box, a textbox "What should it do?", the combobox "Directory", and the
//     buttons "Stay in port" and "Hoist sails"
//   its directory starts empty unless the dock has a last one, and "Hoist sails"
//     is disabled until one is picked
//   the ship's bridge shows the first line of the text as the ship's title

const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

test.describe('Launch orders without an order', () => {
  test('a dock tap with nothing selected takes text and a directory and sails a ship titled by it; with an order selected the skill picker stays', async ({ page, admiral }, info) => {
    const touch = !!info.project.use.hasTouch;
    const w = new World(admiral);
    w.writeConfig();
    const alpha = w.checkout('alpha');
    w.checkout('beta');
    await admiral.start();
    await w.guardData();
    await w.seedOrder('Fix the flaky test\nIt fails on CI only.');
    await page.goto(admiral.url);
    await expect(sidebar(page).getByText('Fix the flaky test', { exact: true })).toBeVisible();

    // Nothing selected: the dock tap opens launch orders with no skill choices.
    await press(dock(page, 'Work', 'Claude'), touch);
    let d = dialog(page);
    await expect(d).toBeVisible();
    await expect(d.getByText('Work · Claude dock')).toBeVisible();
    await expect(d.getByRole('radio')).toHaveCount(0);
    await expect(d.getByRole('textbox', { name: 'Text before the order' })).toHaveCount(0);
    const text = d.getByRole('textbox', { name: 'What should it do?' });
    await expect(text).toBeVisible();
    const directory = d.getByRole('combobox', { name: 'Directory' });
    const hoist = d.getByRole('button', { name: 'Hoist sails' });
    // The text box comes first, then the directory, then the buttons.
    const above = async (a: typeof text, b: typeof text) => expect((await a.boundingBox())!.y).toBeLessThan((await b.boundingBox())!.y);
    await above(text, directory);
    await above(directory, hoist);
    await expect(d.getByRole('button', { name: 'Stay in port' })).toBeVisible();

    // A dock with no last directory selects nothing, and Hoist sails waits for a pick.
    await expect(directory).toHaveValue('');
    await expect(hoist).toBeDisabled();
    await directory.fill('alp');
    await press(page.getByRole('option', { name: /^alpha/ }), touch);
    await expect(directory).toHaveValue('alpha');
    await expect(hoist).toBeEnabled();

    // The text is the prompt, trimmed; its first line is the ship's title on the bridge.
    await text.fill('  fix the flaky test\n');
    await press(hoist, touch);
    await expect(d).toHaveCount(0);
    await expect.poll(() => w.records().length).toBe(1);
    expect(w.records()[0].cwd).toBe(alpha);
    expect(World.prompt(w.records()[0])).toBe('fix the flaky test');
    await expect(sidebar(page).getByText('Fix the flaky test', { exact: true })).toBeVisible(); // nothing leaves the sidebar
    await press(ships(page, underwayIn('Work')), touch);
    await expect(page.getByRole('region', { name: 'Attached terminal' }).getByText('fix the flaky test', { exact: true })).toBeVisible();
    w.endShips();

    // The launch made alpha the dock's last directory.
    await page.goto(admiral.url);
    await press(dock(page, 'Work', 'Claude'), touch);
    d = dialog(page);
    await expect(d).toBeVisible();
    await expect(d.getByRole('combobox', { name: 'Directory' })).toHaveValue('alpha');
    await press(d.getByRole('button', { name: 'Stay in port' }), touch);
    await expect(d).toHaveCount(0);

    // A tap on the ship moored at the dock opens launch orders too.
    await press(dock(page, 'Work', 'Claude').locator('.moored'), touch);
    d = dialog(page);
    await expect(d).toBeVisible();
    await expect(d.getByRole('textbox', { name: 'What should it do?' })).toBeVisible();
    await press(d.getByRole('button', { name: 'Stay in port' }), touch);
    await expect(d).toHaveCount(0);

    // With an order selected, the same tap opens launch orders for that order, skill picker and all.
    await press(sidebar(page).getByText('Fix the flaky test', { exact: true }), touch);
    await press(dock(page, 'Work', 'Claude'), touch);
    d = dialog(page);
    await expect(d).toBeVisible();
    await expect(d.getByRole('radio', { name: 'Just launch' })).toBeVisible();
    await expect(d.getByRole('textbox', { name: 'What should it do?' })).toHaveCount(0);
  });
});
