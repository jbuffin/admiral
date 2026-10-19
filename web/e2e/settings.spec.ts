import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';

// The settings row editor (issue #65, "List settings in the form"). Names the form is built to:
//
//   per array field (titled "Panels", "Labels"): button "Add <title>"; per row a button "Remove <title>"
//   an array of strings: one textbox per row, named "<title>…"
//   an array of flat objects: per row a control per field, named by the field's title (textbox "Title", spinbutton "Limit")

const schema = {
  type: 'object',
  properties: {
    flagdir: { type: 'string' },
    panels: {
      type: 'array',
      title: 'Panels',
      items: {
        type: 'object',
        properties: {
          title: { type: 'string', title: 'Title' },
          query: { type: 'string', title: 'Query' },
          limit: { type: 'integer', title: 'Limit' },
        },
      },
    },
    labels: { type: 'array', title: 'Labels', items: { type: 'string' } },
  },
};

const settingsOf = async (url: string) => {
  const res = await fetch(`${url}api/sources/tracker/settings`);
  return ((await res.json()) as { settings: Record<string, unknown> }).settings;
};

const openSettings = async (page: Page) => {
  await page.getByRole('complementary', { name: 'Orders', exact: true }).getByRole('group', { name: 'Tracker' }).getByRole('button', { name: 'Source actions' }).click();
  await page.getByRole('menuitem', { name: 'Settings' }).click();
  return page.getByRole('dialog', { name: 'Settings' });
};

test('list settings: add rows, save, the new panel appears; remove the last row and the key goes', async ({ page, admiral }) => {
  admiral.installTestPlugin();
  const f = admiral.flags('tracker');
  admiral.writeConfig({
    version: 1,
    fleets: {},
    sources: { tracker: { plugin: 'testplug', name: 'Tracker', settings: { flagdir: f.dir, settingsSchema: schema } } },
  });
  await admiral.start();
  await page.goto(admiral.url);
  await expect(page.getByRole('region', { name: 'Test', exact: true })).toBeVisible();

  let dialog = await openSettings(page);
  await expect(dialog.getByRole('textbox', { name: 'flagdir' })).toHaveValue(f.dir);
  await dialog.getByRole('button', { name: /^Add.*Panels/ }).click();
  await dialog.getByRole('textbox', { name: 'Title' }).fill('Triage');
  await dialog.getByRole('textbox', { name: 'Query' }).fill('is:open');
  await dialog.getByRole('spinbutton', { name: 'Limit' }).fill('5');
  await dialog.getByRole('button', { name: /^Add.*Labels/ }).click();
  await dialog.getByRole('textbox', { name: /^Labels/ }).fill('bug');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();

  // The plugin lists its panels from the saved setting.
  await expect(page.getByRole('region', { name: 'Triage', exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Test', exact: true })).toHaveCount(0);
  // Strings stay strings, the integer is a number.
  await expect.poll(() => settingsOf(admiral.url)).toMatchObject({
    panels: [{ title: 'Triage', query: 'is:open', limit: 5 }],
    labels: ['bug'],
  });

  // The saved rows come back in the form; removing the only row deletes the key.
  dialog = await openSettings(page);
  await expect(dialog.getByRole('textbox', { name: /^Labels/ })).toHaveValue('bug');
  await dialog.getByRole('button', { name: /^Remove.*Labels/ }).click();
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(async () => 'labels' in (await settingsOf(admiral.url))).toBe(false);
  expect((await settingsOf(admiral.url)).panels).toEqual([{ title: 'Triage', query: 'is:open', limit: 5 }]);
});
