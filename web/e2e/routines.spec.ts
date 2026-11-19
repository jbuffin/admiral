import { expect, test } from './fixtures';
import { proveJudge } from './crew';
import { WORK, probeableClaude, seedFleets } from './shell';

type Sentences = { sentences: { field: string; sentence: string }[]; checks: string[]; guidance: string; preset: string };
const orders = async (a: { url: string }, fleet: string) => (await (await fetch(`${a.url}api/v1/fleets/${fleet}/standing-orders`)).json()) as Sentences;
const sentenceFor = (o: Sentences, field: string) => o.sentences.find((s) => s.field === field)!.sentence;

test('a person adds a Post a fleet digest routine with a schedule and extra words, and it shows in the list and on the server', async ({ page, admiral }) => {
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { claude: claude.dock });
  await admiral.start();
  await page.goto(admiral.url + 'app/routines/new?fleet=zulu');
  const form = page.getByRole('form', { name: 'Routine' });
  await form.getByLabel('Name').fill('Morning brief');
  await form.getByRole('radio', { name: 'Post a fleet digest' }).click();
  // a digest has no ship to launch and no event to answer
  await expect(form.getByLabel('Checkout')).toHaveCount(0);
  await expect(form.getByRole('radio', { name: 'An event' })).toHaveCount(0);
  await form.getByLabel('When').fill('0 8 * * *');
  await form.getByLabel(/extra words/i).fill('lead with PRs');
  await form.getByRole('radio', { name: 'Act' }).click();
  await form.getByRole('button', { name: 'Save' }).click();

  const row = page.getByRole('listitem').filter({ hasText: 'Morning brief' });
  await expect(row).toBeVisible();
  await expect(row).toContainText(/digest/i);
  const stored = (await (await page.request.get(admiral.url + 'api/v1/fleets/zulu/routines')).json()) as { routines: { name: string; when: string; before_acting: string; do: unknown }[] };
  expect(stored.routines).toHaveLength(1);
  expect(stored.routines[0]).toMatchObject({ name: 'Morning brief', when: '0 8 * * *', before_acting: 'act', do: { digest: { extra: 'lead with PRs' } } });
});

test('the link of a kept routine draft opens the filled-in form, and a Standing orders sentence leads to its field on the Limits page, and a Check, a decimal and the Home preset reach the server', async ({ page, admiral }) => {
  const claude = probeableClaude(admiral);
  seedFleets(admiral, { claude: claude.dock });
  await admiral.start();
  await proveJudge(page, admiral.url);

  // The link draft_routine hands AdmiralBot opens the kept draft in the form, unsaved; an unknown draft opens an empty form with a note.
  const made = await page.request.post(admiral.url + 'api/v1/fleets/zulu/routines/draft', { data: { prose: 'every night at 3, check for outdated dependencies' } });
  expect(made.ok()).toBe(true);
  const kept = (await made.json()) as { id?: string; draft: { name: string; when: string } };
  expect(kept.id).toBeTruthy();
  await page.goto(admiral.url + `app/routines/new?fleet=zulu&draft=${encodeURIComponent(kept.id!)}`);
  const routine = page.getByRole('form', { name: 'Routine' });
  await expect(routine.getByLabel('Name')).toHaveValue(kept.draft.name);
  await expect(routine.getByLabel('When')).toHaveValue(kept.draft.when);
  await page.goto(admiral.url + 'app/routines/new?fleet=zulu&draft=unknown');
  await expect(page.getByText(/expired or can.t be found/)).toBeVisible();
  await expect(page.getByRole('form', { name: 'Routine' }).getByLabel('Name')).toHaveValue('');

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
