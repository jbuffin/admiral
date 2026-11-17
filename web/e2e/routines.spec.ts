import { expect, test } from './fixtures';
import { proveJudge } from './crew';
import { WORK, probeableClaude, seedFleets } from './shell';

type Sentences = { sentences: { field: string; sentence: string }[]; checks: string[]; guidance: string; preset: string };
const orders = async (a: { url: string }, fleet: string) => (await (await fetch(`${a.url}api/v1/fleets/${fleet}/standing-orders`)).json()) as Sentences;
const sentenceFor = (o: Sentences, field: string) => o.sentences.find((s) => s.field === field)!.sentence;

test('a Standing orders sentence leads to its field on the Limits page, and a Check, a decimal and the Home preset reach the server', async ({ page, admiral }) => {
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { claude: claude.dock });
  await admiral.start();
  await proveJudge(page, admiral.url);
  await page.goto(admiral.url + 'app/routines?fleet=zulu');
  await page.getByRole('main').getByRole('tab', { name: 'Standing orders' }).click();
  await expect(page).toHaveURL(/\/app\/routines\/standing-orders/);
  const main = page.getByRole('main');
  await expect(main.getByText(`${WORK} fleet only`)).toBeVisible();

  const work = await orders(admiral, 'zulu');
  expect(work.preset).toBe('work');
  const limits = main.getByRole('region', { name: 'Limits' });

  // On a touch project each sentence link is a target at least 44px tall.
  if (test.info().project.name.startsWith('ipad')) {
    for (const link of await limits.getByRole('link').all()) expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  }

  // A Check kept through the page is stored and survives a reload.
  const checks = main.getByRole('region', { name: 'Checks' });
  await checks.getByRole('textbox', { name: 'New Check' }).fill('Run make check before any PR.');
  await checks.getByRole('button', { name: 'Add', exact: true }).click();
  await expect.poll(async () => (await orders(admiral, 'zulu')).checks).toEqual(['Run make check before any PR.']);
  await page.reload();
  await expect(checks.getByRole('textbox', { name: 'Check 1' })).toHaveValue('Run make check before any PR.');

  // A sentence leads to its field on the Limits page, where changing it changes the sentence.
  const before = sentenceFor(work, 'rates.concurrent');
  await limits.getByRole('link', { name: before, exact: true }).click();
  await expect(page).toHaveURL(/\/app\/preferences\/zulu\/limits\?.*field=rates\.concurrent/);
  const field = page.locator('[id="rates.concurrent"]');
  await expect(field).toBeFocused();
  await field.fill('5');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => sentenceFor(await orders(admiral, 'zulu'), 'rates.concurrent')).not.toBe(before);

  // A decimal typed key by key keeps its point.
  const spendBefore = sentenceFor(await orders(admiral, 'zulu'), 'spend.lookout_call_usd');
  const spend = page.locator('[id="spend.lookout_call_usd"]');
  await spend.click();
  await spend.press('ControlOrMeta+a');
  await spend.pressSequentially('0.3');
  await expect(spend).toHaveValue('0.3');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).not.toHaveAttribute('aria-busy', 'true');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect.poll(async () => sentenceFor(await orders(admiral, 'zulu'), 'spend.lookout_call_usd')).not.toBe(spendBefore);
  expect(sentenceFor(await orders(admiral, 'zulu'), 'spend.lookout_call_usd')).toContain('0.3');

  // Home is one switch away, after an in-place confirm; the Limits become Home's.
  const preset = page.getByRole('radiogroup', { name: 'Preset' });
  await preset.getByRole('radio', { name: 'Home' }).click();
  await page.getByRole('group', { name: /Switch to Home/ }).getByRole('button', { name: /^Switch/ }).click();
  await expect.poll(async () => (await orders(admiral, 'zulu')).preset).toBe('home');
  await expect(preset.getByRole('radio', { name: 'Home' })).toBeChecked();
});
