import { expect, test } from './fixtures';
import { T, campaignIdOf, githubReady, seedCampaignFleet } from './campaign';
import { watchPage, withFakeGh } from './shell';

test('on the phone, the Campaigns page and a child sheet fit the screen and their controls are tall enough to tap', async ({ page, admiral }) => {
  seedCampaignFleet(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  await githubReady(admiral);
  const watch = watchPage(page, admiral.url);

  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Campaigns', exact: true }).tap();
  const main = page.getByRole('main');
  await expect(main.getByRole('button', { name: /^Ended/ })).toBeVisible();

  // The Add campaign sheet fits the screen, and a row takes a tap.
  await main.getByRole('button', { name: 'Add campaign' }).tap();
  const add = page.getByRole('dialog', { name: 'Add campaign' });
  const pick = add.getByRole('button', { name: new RegExp(T.map) });
  await expect(pick).toBeVisible();
  const room = page.viewportSize()!;
  const frame = (await add.boundingBox())!;
  expect(frame.x).toBeGreaterThanOrEqual(0);
  expect(frame.x + frame.width).toBeLessThanOrEqual(room.width);
  expect(frame.y + frame.height).toBeLessThanOrEqual(room.height);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  expect((await pick.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await pick.tap();
  await expect(page).toHaveURL(/\/app\/campaigns\/[0-9a-f]+/);
  const id = campaignIdOf(page);
  await expect(main.getByRole('heading', { name: T.map })).toBeVisible();
  const group = (title: string) => main.getByRole('region', { name: new RegExp(`^${title}`) });
  await expect(group('Ready, with you')).toContainText(T.grill);
  await expect(group('Ready, without you')).toContainText(T.survey);
  await expect(group('Blocked')).toContainText(T.airlock);
  await expect(group('Claimed, no ship')).toContainText(T.habitat);
  await group('Done').getByRole('button').tap();
  await expect(group('Done')).toContainText(T.site);
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  // The campaign's title is a tap target, and its sheet fits the screen with a Launch tall enough to tap.
  const title = main.getByRole('heading', { name: T.map }).getByRole('button');
  await expect(title).toBeVisible();
  expect((await title.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await title.tap();
  const rootSheet = page.getByRole('dialog', { name: T.map });
  const view = page.viewportSize()!;
  const fit = (await rootSheet.boundingBox())!;
  expect(fit.x).toBeGreaterThanOrEqual(0);
  expect(fit.x + fit.width).toBeLessThanOrEqual(view.width);
  expect(fit.y + fit.height).toBeLessThanOrEqual(view.height);
  expect((await rootSheet.getByRole('button', { name: /^Launch/ }).boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await rootSheet.getByRole('button', { name: 'Close' }).tap();
  await expect(rootSheet).toBeHidden();

  await main.getByRole('listitem').filter({ hasText: T.survey }).getByRole('button').first().tap();
  const sheet = page.getByRole('dialog', { name: new RegExp(T.survey) });
  for (const chip of await sheet.getByRole('radiogroup', { name: 'Mode' }).getByRole('radio').all()) expect((await chip.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  await expect(sheet.getByRole('radio')).toHaveCount(2);
  // A mode that differs from the labels shows Use labels: a 44px touch target that stays clear of the chips and keeps the note's line short.
  const mode = sheet.getByRole('radiogroup', { name: 'Mode' });
  await mode.getByRole('radio', { name: 'With you', exact: true }).tap();
  await expect(sheet).toContainText('Labels say Without you.');
  const useLabels = sheet.getByRole('button', { name: 'Use labels' });
  const chips = await mode.getByRole('radio').all();
  const lastChip = (await chips[chips.length - 1].boundingBox())!;
  const useBox = (await useLabels.boundingBox())!;
  expect(useBox.height).toBeGreaterThanOrEqual(44);
  expect(useBox.y).toBeGreaterThanOrEqual(lastChip.y + lastChip.height);
  expect((await sheet.getByText('Labels say').boundingBox())!.height).toBeLessThan(44);
  await useLabels.tap();
  await expect(sheet.getByText('Labels say')).toHaveCount(0);
  await expect(mode.getByRole('radio', { name: 'Without you', exact: true })).toHaveAttribute('aria-checked', 'true');

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
