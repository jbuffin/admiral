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
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { claude: claude.dock });
  await admiral.start();
  await proveJudge(page, admiral.url);
  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  await page.getByRole('main').getByRole('tab', { name: 'Standing orders' }).click();
  await expect(page).toHaveURL(/\/app\/routines\/standing-orders/);
  const main = page.getByRole('main');
  await expect(main.getByText(`${WORK} fleet only`)).toBeVisible();
  await expect(main.getByText(/Judge calls: Claude/)).toBeVisible();

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

  // On a touch project each sentence link is a target at least 44px tall.
  if (test.info().project.name.startsWith('ipad')) {
    for (const link of await limits.getByRole('link').all()) expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(44);
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

  // A decimal typed key by key keeps its point.
  const spendBefore = sentenceFor(await orders(admiral, 'zulu'), 'spend.lookout_call_usd');
  const spend = page.locator('[id="spend.lookout_call_usd"]');
  await spend.click();
  await spend.press('ControlOrMeta+a');
  await spend.pressSequentially('0.3');
  await expect(spend).toHaveValue('0.3');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => sentenceFor(await orders(admiral, 'zulu'), 'spend.lookout_call_usd')).not.toBe(spendBefore);
  expect(sentenceFor(await orders(admiral, 'zulu'), 'spend.lookout_call_usd')).toContain('0.3');

  // Filing prose drafts a loosened Limit, a Check and Guidance; the second tap confirms the loosening, and the edited Check is what is kept.
  claude.judge('orders-mixed');
  await page.goto(admiral.url + 'app/routines/standing-orders?fleet=zulu');
  const filing = page.getByRole('main');
  await filing.getByRole('textbox', { name: 'New standing order' }).fill('Stop ships after a day. Ask before changing CI. Keep commits small and focused.');
  await filing.getByRole('button', { name: 'Draft', exact: true }).click();
  await expect(filing.getByText(/1,?440 minutes/).first()).toBeVisible();
  await expect(filing.getByText(/loosen/i).first()).toBeVisible();
  await expect(filing.getByRole('textbox', { name: 'Drafted Check 1' })).toHaveValue('Ask before changing CI.');
  await expect(filing.getByRole('textbox', { name: 'Drafted Guidance' })).toHaveValue('Keep commits small and focused.');
  await filing.getByRole('textbox', { name: 'Drafted Check 1' }).fill('Ask before changing CI or releases.');
  await filing.getByRole('button', { name: 'Confirm', exact: true }).click();
  await filing.getByRole('button', { name: /^Confirm, loosening 1 Limit/ }).waitFor();
  expect((await orders(admiral, 'zulu')).checks).toEqual(['Run make check before any PR.']);
  // The orders save fails once after the Limits are stored: the message says so, and the retry sends only the orders.
  let ordersPuts = 0;
  let fleetPuts = 0;
  await page.route('**/api/v1/fleets/zulu*', async (route) => {
    if (route.request().method() === 'PUT' && new URL(route.request().url()).pathname === '/api/v1/fleets/zulu') fleetPuts++;
    await route.fallback();
  });
  await page.route('**/api/v1/fleets/zulu/standing-orders', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback();
    ordersPuts++;
    if (ordersPuts === 1) return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"disk full"}' });
    return route.fallback();
  });
  const firstOrders = page.waitForResponse((r) => r.request().method() === 'PUT' && new URL(r.url()).pathname === '/api/v1/fleets/zulu/standing-orders');
  // A tap within 400ms of arming is a double tap and only arms.
  await page.waitForTimeout(500);
  await filing.getByRole('button', { name: /^Confirm, loosening 1 Limit/ }).click();
  await firstOrders;
  await expect(filing.getByText(/The Limits were saved, but the Checks and Guidance weren't/)).toBeVisible();
  expect(sentenceFor(await orders(admiral, 'zulu'), 'wall_clock_minutes')).toMatch(/1,?440 minutes/);
  expect((await orders(admiral, 'zulu')).checks).toEqual(['Run make check before any PR.']);
  await filing.getByRole('button', { name: 'Confirm', exact: true }).click();
  await expect.poll(async () => (await orders(admiral, 'zulu')).checks).toEqual(['Run make check before any PR.', 'Ask before changing CI or releases.']);
  expect(fleetPuts).toBe(1);
  expect((await orders(admiral, 'zulu')).guidance).toBe('Prefer small PRs.\n\nKeep commits small and focused.');
  expect(sentenceFor(await orders(admiral, 'zulu'), 'wall_clock_minutes')).toMatch(/1,?440 minutes/);
  await expect(filing.getByRole('region', { name: 'Limits' }).getByText(sentenceFor(await orders(admiral, 'zulu'), 'wall_clock_minutes'), { exact: true })).toBeVisible();
  await expect(filing.getByRole('textbox', { name: 'Check 2', exact: true })).toHaveValue('Ask before changing CI or releases.');
  await expect(filing.getByRole('textbox', { name: 'Guidance', exact: true })).toHaveValue('Prefer small PRs.\n\nKeep commits small and focused.');
  await expect(filing.getByRole('textbox', { name: 'Drafted Check 1' })).toHaveCount(0);

  // Describing a routine opens the editor filled from the draft; nothing is stored until it is saved.
  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  await filing.getByRole('textbox', { name: 'Describe a routine' }).fill('every night at 3, check for outdated dependencies');
  await filing.getByRole('button', { name: 'Draft', exact: true }).click();
  const editor = filing.getByRole('form', { name: 'Routine' });
  await expect(editor.getByLabel('Name')).toHaveValue('Nightly dependency check');
  await expect(editor.getByLabel('When')).toHaveValue('0 3 * * *');
  expect(((await (await fetch(`${admiral.url}api/fleets/zulu/routines`)).json()) as { routines: unknown[] }).routines).toEqual([]);
  await editor.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(filing.getByRole('listitem').filter({ hasText: 'Nightly dependency check' })).toBeVisible();
  await page.goto(admiral.url + 'app/preferences/zulu/limits');

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
  await expect.poll(async () => (await orders(admiral, 'alpha')).preset).toBe('home');
  const alpha = await orders(admiral, 'alpha');
  const notice = main.getByRole('status');
  await expect(notice).toContainText(/Copied/);
  await expect(notice).toContainText(HOME);
  // Home's T3 ceiling is higher than Work's, so the notice names it with alpha's own sentence.
  await expect(notice).toContainText(sentenceFor(alpha, 'ceilings.t3'));
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

  // The input is a plain form: no Draft, the reason shows, and a Check saves as typed.
  await expect(main.getByRole('button', { name: 'Draft', exact: true })).toHaveCount(0);
  const plain = main.getByRole('form', { name: 'New standing order' });
  await plain.getByRole('textbox', { name: 'New standing order' }).fill('Never touch the payments-api migrations folder.');
  await plain.getByRole('radio', { name: 'Check', exact: true }).check();
  await plain.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await orders(admiral, 'alpha')).checks).toEqual(['Never touch the payments-api migrations folder.']);
  await expect(main.getByRole('textbox', { name: 'Check 1', exact: true })).toHaveValue('Never touch the payments-api migrations folder.');

  // An unsaved edit survives a filing through the form, and the later Save keeps both Checks.
  await main.getByRole('textbox', { name: 'Check 1', exact: true }).fill('Never touch the payments-api folder.');
  await plain.getByRole('textbox', { name: 'New standing order' }).fill('Ask before changing CI.');
  await plain.getByRole('radio', { name: 'Check', exact: true }).check();
  await plain.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => (await orders(admiral, 'alpha')).checks).toEqual(['Never touch the payments-api migrations folder.', 'Ask before changing CI.']);
  await expect(main.getByRole('textbox', { name: 'Check 1', exact: true })).toHaveValue('Never touch the payments-api folder.');
  await expect(main.getByRole('textbox', { name: 'Check 2', exact: true })).toHaveValue('Ask before changing CI.');
  await main.getByRole('button', { name: 'Save Checks' }).click();
  await expect.poll(async () => (await orders(admiral, 'alpha')).checks).toEqual(['Never touch the payments-api folder.', 'Ask before changing CI.']);

  // The Routines tab hides Describe a routine, keeps New routine and says why.
  await page.goto(admiral.url + 'app/routines?fleet=alpha');
  await expect(main.getByRole('textbox', { name: 'Describe a routine' })).toHaveCount(0);
  await expect(main.getByRole('link', { name: 'New routine' })).toBeVisible();
  await expect(main.getByText(/Judge calls: off/)).toBeVisible();
});
