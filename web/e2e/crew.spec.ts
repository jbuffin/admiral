import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';
import { seedCrew } from './crew';
import { watchPage } from './shell';

test('on the iPad, a person reads the crew by layer, customizes a built-in, edits a member and its notes, renames with and without its notes, and deletes an unclaimed note and a member', async ({ page, admiral }) => {
  seedCrew(admiral);
  await admiral.start();
  const watch = watchPage(page, admiral.url);
  const dialogs: string[] = [];
  page.on('dialog', (d) => {
    dialogs.push(d.message());
    void d.dismiss();
  });
  const press = (l: { click(): Promise<void> }) => l.click();
  const crewFile = (name: string) => join(admiral.home, 'fleets', 'zulu', 'crew', `${name}.md`);

  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  const main = page.getByRole('main');
  const group = (title: RegExp) => main.getByRole('region', { name: title });
  const fleet = group(/^Fleet\b/);
  const builtIn = group(/^Built-in\b/);
  const unclaimed = group(/^Unclaimed notes\b/);
  // A member's row is told apart by its pick-me-when line; its notes are list items too.
  const row = (g: ReturnType<typeof group>, pick: string) => g.getByRole('listitem').filter({ hasText: pick }).last();
  const open = (g: ReturnType<typeof group>, pick: string, name: string) => press(row(g, pick).getByRole('link', { name: new RegExp(`\\b${name}\\b`) }).or(row(g, pick).getByRole('button', { name: new RegExp(`\\b${name}\\b`) })));
  const noteRow = (text: string) => main.getByRole('listitem').filter({ hasText: text }).filter({ has: page.getByRole('button', { name: 'Delete note' }) }).last();
  const scope = (title: RegExp) => main.getByRole('region', { name: title });
  const form = main.getByRole('form', { name: /edit/i }).or(page.getByRole('dialog', { name: /edit/i }));

  // Members by layer: Fleet first, then Built-in; the customized one is marked and shown once, under Fleet; a harness lock shows its flag.
  await expect(main.getByRole('heading', { name: 'Crew', exact: true })).toBeVisible();
  await expect(fleet.getByRole('listitem').filter({ hasText: 'Pick for parser work.' })).toBeVisible();
  await expect(fleet.getByRole('listitem').filter({ hasText: 'Pick for rigging.' })).toBeVisible();
  await expect(builtIn.getByRole('listitem').filter({ hasText: 'Pick for the log.' })).toBeVisible();
  expect(await fleet.boundingBox().then((b) => b!.y)).toBeLessThan(await builtIn.boundingBox().then((b) => b!.y));
  await expect(row(fleet, 'Pick for a sharper review.')).toContainText('customized');
  await expect(row(fleet, 'Pick for parser work.')).not.toContainText('customized');
  await expect(builtIn.getByText('Pick for a review.')).toHaveCount(0);
  await expect(row(fleet, 'Pick for parser work.').locator('img[src$="/api/v1/logos/codex"]')).toHaveCount(1);
  await expect(row(builtIn, 'Pick for the log.').locator('img')).toHaveCount(0);
  await expect(unclaimed).toHaveCount(0);

  // Customize the built-in scout: it moves under Fleet, marked, and its notes are still there.
  await open(builtIn, 'Pick for the log.', 'scout');
  await expect(page).toHaveURL(/\/app\/crew\/scout\?(.*&)?fleet=zulu/);
  await expect(main).toContainText('scout keeps the log short');
  await press(main.getByRole('button', { name: 'Customize for this fleet' }));
  await expect(row(fleet, 'Pick for the log.')).toContainText('customized');
  await expect(builtIn.getByText('Pick for the log.')).toHaveCount(0);
  await expect(main).toContainText('scout keeps the log short');
  await expect(main.getByRole('button', { name: 'Customize for this fleet' })).toHaveCount(0);

  // Edit ada's pick-me-when; it is still there after a reload.
  await open(fleet, 'Pick for parser work.', 'ada');
  await expect(page).toHaveURL(/\/app\/crew\/ada\?(.*&)?fleet=zulu/);
  await expect(main).toContainText('Work as ada.');
  await press(main.getByRole('button', { name: 'Edit', exact: true }));
  await expect(form).toBeVisible();
  await form.getByLabel(/pick.me.when/i).fill('Pick for grammar work.');
  await press(form.getByRole('button', { name: 'Save' }));
  await expect(form).toBeHidden();
  await expect(main.getByText('Pick for grammar work.').first()).toBeVisible();
  await page.reload();
  await expect(main.getByText('Pick for grammar work.').first()).toBeVisible();
  await expect(main.getByText('Pick for parser work.')).toHaveCount(0);

  // A refused save shows the server's message beside the input it names, keeps what was typed and changes nothing; a good one is saved with one skill a line.
  await press(main.getByRole('button', { name: 'Edit', exact: true }));
  const skills = form.getByLabel('Skills');
  await skills.fill('not a skill');
  const refused = page.waitForResponse((r) => r.request().method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/fleets/zulu/crew/members/ada');
  await press(form.getByRole('button', { name: 'Save' }));
  const answer = await refused;
  expect(answer.status()).toBe(400);
  const message = ((await answer.json()) as { error: string; field: string });
  expect(message.field).toBe('skills');
  await expect(skills).toHaveAccessibleDescription(new RegExp(message.error.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  await expect(skills).toHaveValue('not a skill');
  await expect(form).toBeVisible();
  await skills.fill('review\nwrite-docs');
  await press(form.getByRole('button', { name: 'Save' }));
  await expect(form).toBeHidden();
  await expect(main.getByText('review', { exact: true })).toBeVisible();
  await expect(main.getByText('write-docs', { exact: true })).toBeVisible();

  // Notes under ada: This fleet and one heading per repo. Edit a note inline.
  await expect(scope(/^This fleet\b/)).toContainText('ada reads the grammar first');
  await expect(scope(/^acme\/api\b/)).toContainText('ada runs make check');
  await press(noteRow('ada keeps handlers thin').getByRole('button', { name: 'Edit note' }));
  await main.getByRole('textbox', { name: 'Note text' }).fill('ada keeps handlers thin and typed');
  await press(main.getByRole('button', { name: 'Save', exact: true }));
  await expect(scope(/^acme\/api\b/)).toContainText('ada keeps handlers thin and typed');
  await expect(scope(/^acme\/api\b/)).not.toContainText(/handlers thin(?! and typed)/);

  // Move the fleet note to the repo, then delete one of the repo's notes.
  await press(noteRow('ada reads the grammar first').getByRole('button', { name: 'Move note' }));
  await main.getByRole('combobox', { name: 'Move to' }).selectOption({ label: 'acme/api' });
  await press(main.getByRole('button', { name: 'Move', exact: true }));
  await expect(scope(/^acme\/api\b/)).toContainText('ada reads the grammar first');
  await expect(scope(/^This fleet\b/)).toHaveCount(0);
  await press(noteRow('ada runs make check').getByRole('button', { name: 'Delete note' }));
  await expect(main).not.toContainText('ada runs make check');
  await expect(scope(/^acme\/api\b/)).toContainText('ada reads the grammar first');

  // Rename dory to a member that exists: the server's refusal shows in the sheet, which stays open.
  const rename = page.getByRole('dialog', { name: /rename/i });
  const renameSheet = async (pick: string, name: string, to: string, carry: boolean) => {
    await open(fleet, pick, name);
    await press(main.getByRole('button', { name: 'Rename', exact: true }));
    await expect(rename).toBeVisible();
    await rename.getByLabel(/new name/i).fill(to);
    const carryBox = rename.getByRole('checkbox', { name: 'Carry notes' });
    await expect(carryBox).not.toBeChecked();
    if (carry) await carryBox.check();
  };
  await renameSheet('Pick for small jobs.', 'dory', 'ada', false);
  await expect(rename).toContainText(/unchecked leaves the notes under Unclaimed/i);
  const clash = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/v1/fleets/zulu/crew/members/dory/rename');
  await press(rename.getByRole('button', { name: 'Rename', exact: true }));
  const clashed = (await clash);
  expect(clashed.status()).toBe(409);
  await expect(rename.getByRole('alert')).toContainText(((await clashed.json()) as { error: string }).error);
  await press(rename.getByRole('button', { name: 'Cancel', exact: true }));
  await expect(rename).toBeHidden();

  // Rename bosun without Carry notes: its notes wait under Unclaimed.
  await renameSheet('Pick for rigging.', 'bosun', 'boatswain', false);
  await press(rename.getByRole('button', { name: 'Rename', exact: true }));
  await expect(rename).toBeHidden();
  await expect(fleet.getByRole('listitem').filter({ hasText: 'Pick for rigging.' })).toBeVisible();
  await expect(fleet).toContainText('boatswain');
  await expect(unclaimed).toContainText('bosun');
  await expect(unclaimed).toContainText('bosun ties a bowline');
  // The page can't show the file: its name: follows the new name.
  expect(existsSync(crewFile('bosun'))).toBe(false);
  expect(readFileSync(crewFile('boatswain'), 'utf8')).toMatch(/^name: boatswain$/m);

  // Rename cook with Carry notes: the notes follow and nothing is left unclaimed for cook.
  await renameSheet('Pick for the galley.', 'cook', 'steward', true);
  await press(rename.getByRole('button', { name: 'Rename', exact: true }));
  await expect(rename).toBeHidden();
  await expect(fleet).toContainText('steward');
  await open(fleet, 'Pick for the galley.', 'steward');
  await expect(main).toContainText('cook salts early');
  await expect(unclaimed).not.toContainText('cook salts early');
  await expect(unclaimed).toContainText('bosun ties a bowline');

  // Delete the unclaimed note: the section goes with its last one.
  await press(unclaimed.getByRole('listitem').filter({ hasText: 'bosun ties a bowline' }).last().getByRole('button', { name: 'Delete note' }));
  await expect(unclaimed).toHaveCount(0);

  // Delete dory: the page asks first, in the page.
  await open(fleet, 'Pick for small jobs.', 'dory');
  await press(main.getByRole('button', { name: 'Delete', exact: true }));
  await expect(fleet.getByRole('listitem').filter({ hasText: 'Pick for small jobs.' })).toBeVisible();
  await press(page.getByRole('button', { name: 'Confirm delete' }));
  await expect(fleet.getByRole('listitem').filter({ hasText: 'Pick for small jobs.' })).toHaveCount(0);
  expect(dialogs).toEqual([]);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
