import { expect, test } from './fixtures';
import { proveJudge, seedCrew } from './crew';
import { probeableClaude, watchPage } from './shell';

test('on the phone, the crew list, a member and the add-crew draft fit the screen, and Draft and Save are reachable and tall enough to tap', async ({ page, admiral }) => {
  seedCrew(admiral, { claude: probeableClaude(admiral).dock });
  await admiral.start();
  await proveJudge(page, admiral.url);
  const watch = watchPage(page, admiral.url);
  const main = page.getByRole('main');
  const fits = () => page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth));

  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  const ada = main.getByRole('listitem').filter({ hasText: 'Pick for parser work.' }).last();
  await expect(ada).toBeVisible();
  expect(await fits()).toBe(true);
  await ada.getByRole('link', { name: /\bada\b/ }).tap();
  await expect(main).toContainText('Work as ada.');
  expect(await fits()).toBe(true);
  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  await main.getByRole('button', { name: 'Add crew' }).tap();
  const panel = main.getByRole('region', { name: 'Add crew' });
  // The panel replaces the list, as a member's detail does.
  await expect(main.getByText('Pick for rigging.')).toHaveCount(0);
  await panel.getByRole('textbox', { name: 'Describe the member' }).fill('A quartermaster who keeps the ship stocked.');
  await expect(panel.getByRole('button', { name: 'Draft' })).toBeInViewport();
  expect(await fits()).toBe(true);
  await panel.getByRole('button', { name: 'Draft' }).tap();

  const adding = main.getByRole('form', { name: 'New crew member' });
  await expect(adding.getByLabel('Name')).toHaveValue('quartermaster');
  expect(await fits()).toBe(true);
  const save = adding.getByRole('button', { name: 'Save' });
  await save.scrollIntoViewIfNeeded();
  await expect(save).toBeInViewport();
  expect((await save.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await save.tap();
  await expect(page).toHaveURL(/\/app\/crew\/quartermaster\?(.*&)?fleet=zulu/);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
