import { join } from 'node:path';
import { expect, test } from './fixtures';
import { T, campaignIdOf, followByPaste, githubReady, issueUrl, seedCampaignFleet } from './campaign';
import { watchPage, withFakeGh } from './shell';

test('on the phone, a person follows a campaign from the Campaigns surface, reads its page and launches one child', async ({ page, admiral }) => {
  seedCampaignFleet(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  await githubReady(admiral);
  const watch = watchPage(page, admiral.url);
  const press = (l: { tap(): Promise<void> }) => l.tap();

  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await page.getByRole('button', { name: 'More', exact: true }).tap();
  await page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Campaigns', exact: true }).tap();
  const main = page.getByRole('main');
  await expect(main.getByRole('button', { name: /^Ended/ })).toBeVisible();

  await followByPaste(page, press, issueUrl(20));
  await expect(page).toHaveURL(/\/app\/campaigns\/[0-9a-f]+/);
  const id = campaignIdOf(page);
  await expect(main.getByRole('heading', { name: T.map })).toBeVisible();
  const group = (title: string) => main.getByRole('region', { name: new RegExp(`^${title}`) });
  await expect(group('Ready, with you')).toContainText(T.grill);
  await expect(group('Ready, without you')).toContainText(T.survey);
  await expect(group('Blocked')).toContainText(T.airlock);
  await expect(group('Claimed, no ship')).toContainText(T.habitat);
  await expect(group('Done')).toContainText(T.site);
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  await main.getByRole('listitem').filter({ hasText: T.survey }).getByRole('button').first().tap();
  await page.getByRole('dialog', { name: new RegExp(T.survey) }).getByRole('button', { name: /^Launch/ }).tap();
  const form = page.getByRole('dialog', { name: 'Launch', exact: true });
  await expect(form.getByLabel('Checkout')).toHaveValue(join(admiral.home, 'widgets'));
  const sent = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/v1/campaigns/${id}/launch`);
  await form.getByRole('button', { name: 'Launch', exact: true }).tap();
  expect((await sent).status()).toBe(201);
  await expect(form).toBeHidden();
  await expect(group('Ready, without you').getByRole('listitem').filter({ hasText: T.survey })).toContainText(/Underway|\b[A-Z][a-z]+ [A-Z][a-z]+\b/);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
