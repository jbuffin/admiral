import { expect, test } from './fixtures';
import { seedCrew, seedManyBuiltIns } from './crew';

test('on the iPad, a member far down a long list opens with its detail in view, and stays in view after Customize', async ({ page, admiral }) => {
  seedCrew(admiral);
  seedManyBuiltIns(admiral, 20);
  await admiral.start();
  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  const main = page.getByRole('main');

  const last = main.getByRole('link', { name: /\bmate20\b/ });
  await last.scrollIntoViewIfNeeded();
  await last.click();
  const heading = main.getByRole('heading', { name: 'mate20', level: 2 });
  await expect(heading).toBeInViewport();

  await main.getByRole('button', { name: 'Customize for this fleet' }).click();
  await expect(main.getByRole('region', { name: /^Fleet\b/ }).getByRole('link', { name: /\bmate20\b/ })).toBeVisible();
  await expect(heading).toBeInViewport();
});
