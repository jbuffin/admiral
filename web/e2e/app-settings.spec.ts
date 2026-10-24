import type { Page } from '@playwright/test';
import { expect, test } from './fixtures';

// The app Settings modal (issue #119, u1), not the source settings of settings.spec.ts. Names it is built to:
//
//   header: button "Settings" (the gear); the Sound pill keeps "Mute chime" / "Turn chime on"
//   dialog "Settings" with sections (regions) "General" and "Chat", and buttons "Cancel" and "Save"
//   General: a button "Mute attention sounds" while sound is on, "Turn attention sounds on" while muted
//   Chat: comboboxes "Fleet", "Model" and "Effort" (options sonnet|opus|haiku and Harness default|low|medium|high),
//         and a preview line "Writes to config.json" with text like "chat": {"fleet": "work", "model": "sonnet"}

const config = (claude: string) => ({
  version: 1,
  fleets: { work: { name: 'Work Fleet', claude: { executable: claude } } },
});

const chatOf = async (url: string) => {
  const res = await fetch(`${url}api/config`);
  return ((await res.json()) as { chat?: { fleet: string; model: string; effort?: string } }).chat;
};

const openSettings = async (page: Page) => {
  await page.getByRole('button', { name: 'Settings', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Settings' });
  await expect(dialog).toBeVisible();
  return dialog;
};

const storedMute = (page: Page) => page.evaluate(() => localStorage.getItem('admiral.muted'));

test('chat settings: the defaults, then model and effort are saved to config.json and reset', async ({ page, admiral }) => {
  admiral.writeConfig(config(admiral.fakeHarness('claude')));
  await admiral.start();
  await page.goto(admiral.url);

  let dialog = await openSettings(page);
  await expect(dialog.getByRole('region', { name: 'General' })).toBeVisible();
  const chat = dialog.getByRole('region', { name: 'Chat' });
  await expect(chat.getByRole('combobox', { name: 'Fleet' })).toHaveValue('work');
  await expect(chat.getByRole('combobox', { name: 'Model' })).toHaveValue('sonnet');
  await expect(chat.getByRole('combobox', { name: 'Effort' })).toHaveValue('');
  await expect(chat).toContainText('"chat": {"fleet": "work", "model": "sonnet"}');

  await chat.getByRole('combobox', { name: 'Model' }).selectOption('opus');
  await chat.getByRole('combobox', { name: 'Effort' }).selectOption('high');
  await expect(chat).toContainText('"chat": {"fleet": "work", "model": "opus", "effort": "high"}');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(() => chatOf(admiral.url)).toEqual({ fleet: 'work', model: 'opus', effort: 'high' });

  // Back to Harness default: the effort field goes.
  dialog = await openSettings(page);
  await expect(dialog.getByRole('combobox', { name: 'Effort' })).toHaveValue('high');
  await dialog.getByRole('combobox', { name: 'Effort' }).selectOption({ label: 'Harness default' });
  await expect(dialog.getByRole('region', { name: 'Chat' })).toContainText('"chat": {"fleet": "work", "model": "opus"}');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(async () => (await chatOf(admiral.url))?.effort ?? '').toBe('');
  await expect.poll(() => chatOf(admiral.url)).toMatchObject({ fleet: 'work', model: 'opus' });
});

test('general settings: Sound follows Save and Cancel, and the header pill stays', async ({ page, admiral }) => {
  admiral.writeConfig(config(admiral.fakeHarness('claude')));
  await admiral.start();
  await page.goto(admiral.url);
  const pill = (name: string) => page.getByRole('button', { name, exact: true });
  await expect(pill('Mute chime')).toBeVisible();

  // Cancel discards the toggle; the header pill is untouched while the draft changes.
  let dialog = await openSettings(page);
  const general = dialog.getByRole('region', { name: 'General' });
  await general.getByRole('button', { name: 'Mute attention sounds' }).click();
  await expect(general.getByRole('button', { name: 'Turn attention sounds on' })).toBeVisible();
  await expect(pill('Mute chime')).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toBeHidden();
  await expect(pill('Mute chime')).toBeVisible();
  expect(await storedMute(page)).not.toBe('1');

  // Save applies it: the same admiral.muted state as the header pill.
  dialog = await openSettings(page);
  await expect(dialog.getByRole('button', { name: 'Mute attention sounds' })).toBeVisible();
  await dialog.getByRole('button', { name: 'Mute attention sounds' }).click();
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect(pill('Turn chime on')).toBeVisible();
  expect(await storedMute(page)).toBe('1');

  // And it unmutes the same way.
  dialog = await openSettings(page);
  await dialog.getByRole('button', { name: 'Turn attention sounds on' }).click();
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect(pill('Mute chime')).toBeVisible();
  expect(await storedMute(page)).toBe('0');
});
