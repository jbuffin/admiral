import { join } from 'node:path';
import { expect, test } from './fixtures';
import { proveJudge } from './crew';
import { probeableClaude, seedFleets, withFakeGh } from './shell';

test('a routine made through the form fires on its minute and its ship shows in Recent runs', async ({ page, admiral }) => {
  test.setTimeout(150_000);
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { live: true, claude: claude.dock });
  await admiral.start({ env: withFakeGh(admiral) });
  await proveJudge(page, admiral.url);
  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  await page.getByRole('main').getByRole('link', { name: 'New routine' }).tap();

  const due = new Date(Math.ceil((Date.now() + 5_000) / 60_000) * 60_000);
  const form = page.getByRole('main').getByRole('form', { name: 'Routine' });
  await form.getByLabel('Name').fill('Form sweep');
  await form.getByLabel('When').fill(`${due.getMinutes()} ${due.getHours()} * * *`);
  await expect(form.getByText(/Daily/)).toBeVisible();
  await form.getByRole('radio', { name: 'Claude' }).tap();
  await form.getByLabel('Checkout').selectOption(join(admiral.home, 'proj'));
  await form.getByLabel('Text').fill('go');
  await form.getByRole('radio', { name: 'Act', exact: true }).tap();
  await form.getByRole('button', { name: 'Save', exact: true }).tap();
  await expect(page).toHaveURL(/\/app\/routines\/[^/?]+/);

  const runs = page.getByRole('region', { name: 'Recent runs' });
  await expect(runs.getByRole('link')).toHaveCount(1, { timeout: 120_000 });
  const ships = (await (await fetch(`${admiral.url}api/v1/ships`)).json()) as { ships: { name: string }[] };
  expect(ships.ships).toHaveLength(1);
  await expect(runs).toContainText(ships.ships[0]!.name);
});

test('Automate… on an order view opens an unsaved scheduled routine draft', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  await page.goto(admiral.url + 'app/orders?fleet=zulu');
  await page.getByRole('main').getByRole('button', { name: /^Automate/ }).first().tap();
  await expect(page).toHaveURL(/\/app\/routines\/new/);
  const form = page.getByRole('main').getByRole('form', { name: 'Routine' });
  await expect(form.getByLabel('Name')).toHaveValue(/^Automate /);
  await expect(form.getByLabel('When')).toHaveValue('30 8 * * 1-5');
  await expect(form.getByRole('radio', { name: 'Ask first' })).toBeChecked();
  const list = (await (await fetch(`${admiral.url}api/fleets/zulu/routines`)).json()) as { routines: unknown[] };
  expect(list.routines).toEqual([]);
});
