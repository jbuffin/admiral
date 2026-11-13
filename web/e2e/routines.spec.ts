import { join } from 'node:path';
import { expect, test } from './fixtures';
import { proveJudge } from './crew';
import { HOME, WORK, probeableClaude, seedFleets, withFakeGh } from './shell';

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

  // Save shows what the server stored, and a second Save does not revert it.
  const editor = page.getByRole('main').getByRole('form', { name: 'Routine' });
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: 'Form sweep' });
  await expect(row).not.toContainText('Never');
  await editor.getByLabel('When').fill('15 9 * * 1-5');
  await editor.getByRole('button', { name: 'Save', exact: true }).tap();
  await expect(row).toContainText('09:15');
  await expect(editor.getByLabel('When')).toHaveValue('15 9 * * 1-5');
  await editor.getByRole('button', { name: 'Save', exact: true }).tap();
  await expect(editor.getByLabel('When')).toHaveValue('15 9 * * 1-5');
  await expect(row).toContainText('09:15');
  await expect(editor.getByRole('radio', { name: 'Act', exact: true })).toBeChecked();

  // The row's own switch turns the routine off and the editor follows.
  await row.getByRole('switch').tap();
  await expect(row.getByRole('switch')).not.toBeChecked();
  await expect(editor.getByRole('switch', { name: 'On', exact: true })).not.toBeChecked();
});

test('Automate… on an order view opens an unsaved scheduled routine draft', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await page.getByRole('main').getByRole('button', { name: /^Automate/ }).first().tap();
  await expect(page).toHaveURL(/\/app\/routines\/new/);
  const form = page.getByRole('main').getByRole('form', { name: 'Routine' });
  await expect(form.getByLabel('Name')).toHaveValue(/^Automate /);
  await expect(form.getByLabel('When')).toHaveValue('30 8 * * 1-5');
  await expect(form.getByRole('radio', { name: 'Ask first' })).toBeChecked();
  const list = (await (await fetch(`${admiral.url}api/fleets/zulu/routines`)).json()) as { routines: unknown[] };
  expect(list.routines).toEqual([]);
});

type Sentences = { sentences: { field: string; sentence: string }[]; checks: string[]; guidance: string; preset: string };
const orders = async (a: { url: string }, fleet: string) => (await (await fetch(`${a.url}api/v1/fleets/${fleet}/standing-orders`)).json()) as Sentences;
const sentenceFor = (o: Sentences, field: string) => o.sentences.find((s) => s.field === field)!.sentence;

test('the Standing orders tab shows the Work Limits as sentences, keeps a Check and Guidance, and each sentence leads to its field on Preferences', async ({ page, admiral }) => {
  seedFleets(admiral, { claude: probeableClaude(admiral).dock });
  await admiral.start();
  await proveJudge(page, admiral.url);
  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  await page.getByRole('main').getByRole('tab', { name: 'Standing orders' }).click();
  await expect(page).toHaveURL(/\/app\/routines\/standing-orders/);
  const main = page.getByRole('main');
  await expect(main.getByText(`${WORK} fleet only`)).toBeVisible();
  await expect(main.getByText(/Judge calls: claude/)).toBeVisible();

  // Every Limit is a sentence the server wrote; the fixed ones are plain text.
  const work = await orders(admiral, 'zulu');
  expect(work.preset).toBe('work');
  const limits = main.getByRole('region', { name: 'Limits' });
  for (const s of work.sentences) {
    if (s.field) await expect(limits.getByRole('link', { name: s.sentence, exact: true })).toBeVisible();
    else {
      await expect(limits.getByText(s.sentence)).toBeVisible();
      await expect(limits.getByRole('link', { name: s.sentence })).toHaveCount(0);
    }
  }

  // A Check and Guidance are kept.
  const checks = main.getByRole('region', { name: 'Checks' });
  await checks.getByRole('textbox', { name: 'New Check' }).fill('Run make check before any PR.');
  await checks.getByRole('button', { name: 'Add', exact: true }).click();
  await expect.poll(async () => (await orders(admiral, 'zulu')).checks).toEqual(['Run make check before any PR.']);
  const guidance = main.getByRole('region', { name: 'Guidance' });
  await guidance.getByRole('textbox', { name: 'Guidance' }).fill('Prefer small PRs.');
  await guidance.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await orders(admiral, 'zulu')).guidance).toBe('Prefer small PRs.');
  await page.reload();
  await expect(checks.getByRole('textbox', { name: 'Check 1' })).toHaveValue('Run make check before any PR.');
  await expect(guidance.getByRole('textbox', { name: 'Guidance' })).toHaveValue('Prefer small PRs.');

  // A sentence leads to its field on the Limits page, where changing it changes the sentence.
  const before = sentenceFor(work, 'rates.concurrent');
  await limits.getByRole('link', { name: before, exact: true }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/limits\?.*field=rates\.concurrent/);
  const field = page.locator('[id="rates.concurrent"]');
  await expect(field).toBeFocused();
  await field.fill('5');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => sentenceFor(await orders(admiral, 'zulu'), 'rates.concurrent')).not.toBe(before);
  await expect(page.getByRole('main').getByText(sentenceFor(await orders(admiral, 'zulu'), 'rates.concurrent'), { exact: true })).toBeVisible();

  // Home is one switch away, after an in-place confirm; the Limits become Home's.
  const preset = page.getByRole('radiogroup', { name: 'Preset' });
  await preset.getByRole('radio', { name: 'Home' }).click();
  await page.getByRole('group', { name: /Switch to Home/ }).getByRole('button', { name: /^Switch/ }).click();
  await expect.poll(async () => (await orders(admiral, 'zulu')).preset).toBe('home');
  await expect(preset.getByRole('radio', { name: 'Home' })).toBeChecked();
  await expect(page.getByRole('main').getByText(sentenceFor(await orders(admiral, 'zulu'), 'ceilings.t4'), { exact: true })).toBeVisible();
});

test('Copy to another fleet confirms in place, replaces the target and names each Limit it loosens', async ({ page, admiral }) => {
  seedFleets(admiral);
  await admiral.start();
  // Zulu on Home is looser than alpha on Work, and carries a Check.
  const put = (fleet: string, path: string, data: unknown) => page.request.put(`${admiral.url}api/v1/fleets/${fleet}${path}`, { data });
  expect((await put('zulu', '', { preset: 'home' })).ok()).toBe(true);
  expect((await put('zulu', '/standing-orders', { checks: ['Ask before touching CI.'] })).ok()).toBe(true);
  await expect.poll(async () => (await orders(admiral, 'zulu')).preset).toBe('home');

  await page.goto(admiral.url + 'app/routines/standing-orders?fleet=zulu');
  const main = page.getByRole('main');
  await main.getByRole('button', { name: 'Copy to another fleet' }).click();
  await main.getByRole('combobox', { name: 'Copy to' }).selectOption({ label: HOME });
  await main.getByRole('button', { name: /^Copy$/ }).click();
  const confirm = main.getByRole('group', { name: /Copy/ });
  await expect(confirm).toContainText(/replaces/);
  await expect(confirm).toContainText(/Checks/);
  await confirm.getByRole('button', { name: /^Replace|^Confirm|^Copy/ }).click();

  await expect.poll(async () => (await orders(admiral, 'alpha')).checks).toEqual(['Ask before touching CI.']);
  const alpha = await orders(admiral, 'alpha');
  expect(alpha.preset).toBe('home');
  await expect(main.getByText(/Copied/)).toContainText(HOME);
  // Home's T3 ceiling is higher than Work's, so the line names it with alpha's own sentence.
  await expect(main.getByText(sentenceFor(alpha, 'ceilings.t3'), { exact: true })).toBeVisible();
});

test('with no proven judge the tab says so, greys the features that need one, and Check again re-runs the probe', async ({ page, admiral }) => {
  seedFleets(admiral);
  await admiral.start();
  await page.goto(admiral.url + 'app/routines/standing-orders?fleet=alpha');
  const main = page.getByRole('main');
  await expect(main.getByText(/Judge calls: off/)).toBeVisible();
  const off = main.getByRole('region', { name: 'Needs the judge' });
  for (const feature of [/Lookout/, /reviewer/, /drafter|Describe a routine/, /advisor/]) await expect(off.getByText(feature).first()).toBeVisible();
  const probe = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === '/api/v1/harness-capabilities/check');
  await main.getByRole('button', { name: /Check again/ }).first().click();
  expect((await probe).postDataJSON()).toMatchObject({ fleet: 'alpha' });
  await expect(main.getByText(/Judge calls: off/)).toBeVisible();
});
