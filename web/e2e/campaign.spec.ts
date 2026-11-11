import { join } from 'node:path';
import { expect, test } from './fixtures';
import { T, campaignIdOf, followByPaste, githubReady, issueUrl, launchBehindThePage, MAP, seedCampaignFleet } from './campaign';
import { watchPage, withFakeGh } from './shell';

test('on the iPad, a person follows a campaign by pasting it, sees each child\'s status and mode, launches a ready child, is refused a second launch, unfollows it and finds it under Ended; Follow on an order opens its campaign', async ({ page, admiral }) => {
  seedCampaignFleet(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  await githubReady(admiral);
  const watch = watchPage(page, admiral.url);
  const press = (l: { click(): Promise<void> }) => l.click();

  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  const nav = page.getByRole('navigation');
  const main = page.getByRole('main');
  const group = (title: string) => main.getByRole('region', { name: new RegExp(`^${title}`) });
  const row = (title: string) => main.getByRole('listitem').filter({ hasText: title });

  // A paste the server would refuse for its shape says so before it is sent.
  await press(nav.getByRole('button', { name: 'Add campaign' }));
  const field = page.getByRole('textbox', { name: /campaign/i });
  await field.fill(issueUrl(MAP) + '/');
  await expect(page.getByText(/trailing slash/i)).toBeVisible();
  await field.fill(issueUrl(MAP) + '#issuecomment-123456');
  await expect(page.getByText(/comment/i)).toBeVisible();

  await followByPaste(page, press, 'acme/widgets#20');
  await expect(page).toHaveURL(/\/app\/campaigns\/[0-9a-f]+/);
  const id = campaignIdOf(page);
  await expect(main.getByRole('heading', { name: T.map })).toBeVisible();
  await expect(nav).toContainText(T.map);
  // Following launched nothing.
  expect(((await (await fetch(`${admiral.url}api/v1/ships`)).json()) as { ships: unknown[] }).ships).toHaveLength(0);

  // Ready with you and without you, blocked with its blocker, claimed with no ship, and done; each in sub-issue order.
  await expect(group('Ready, with you').getByRole('listitem')).toHaveText([new RegExp(T.grill), new RegExp(T.rover)]);
  await expect(group('Ready, without you').getByRole('listitem')).toHaveText([new RegExp(T.survey)]);
  await expect(group('Blocked')).toContainText(T.airlock);
  await expect(group('Blocked')).toContainText('Issue 25');
  await expect(group('Claimed, no ship')).toContainText(T.habitat);
  await expect(group('Claimed, no ship').getByRole('button', { name: /Start anyway/ })).toBeVisible();
  await expect(group('Done')).toContainText(T.site);
  // The page says when it last read the tree.
  await expect(main).toContainText(/read/i);

  // Launch a ready child through the launch form: its checkout is preselected, and the server's choices are not the form's.
  await press(row(T.grill).getByRole('button').first());
  const sheet = page.getByRole('dialog', { name: new RegExp(T.grill) });
  await expect(sheet).toContainText('With you');
  const dirs = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/v1/launch-orders/dirs');
  await press(sheet.getByRole('button', { name: /^Launch/ }));
  expect(JSON.stringify((await dirs).postDataJSON())).toContain(`"source":"campaign"`);
  expect(JSON.stringify((await dirs).postDataJSON())).toContain(`${id}/${issueUrl(21)}`);
  const form = page.getByRole('dialog', { name: 'Launch', exact: true });
  await expect(form.getByLabel('Checkout')).toHaveValue(join(admiral.home, 'widgets'));
  await expect(form.getByLabel('Skill')).toHaveCount(0);
  await expect(form.getByRole('checkbox', { name: /autopilot/i })).toHaveCount(0);
  const sent = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/campaigns/${id}/launch`);
  await press(form.getByRole('button', { name: 'Launch', exact: true }));
  const body = (await sent).postDataJSON() as Record<string, unknown>;
  expect(body).toMatchObject({ child_url: issueUrl(21), harness: 'claude', dir: join(admiral.home, 'widgets') });
  for (const f of ['skill', 'text', 'autopilot', 'resources']) expect(body).not.toHaveProperty(f);
  await expect(form).toBeHidden();
  // The child is underway in its Ready group, with its ship's name.
  await expect(group('Ready, with you').getByRole('listitem').filter({ hasText: T.grill })).toContainText(/Underway|\b[A-Z][a-z]+ [A-Z][a-z]+\b/);

  // A second launch of a child someone else started meanwhile is refused with the server's message, which names the ship.
  await press(row(T.rover).getByRole('button').first());
  await press(page.getByRole('dialog', { name: new RegExp(T.rover) }).getByRole('button', { name: /^Launch/ }));
  await expect(form).toBeVisible();
  const holder = await launchBehindThePage(admiral, id, 23);
  await press(form.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form.getByRole('alert')).toContainText(holder);
  await expect(form).toBeVisible();
  await press(form.getByRole('button', { name: 'Cancel', exact: true }));

  // Unfollow: it leaves the sidebar and is listed under Ended, read-only for launching.
  await press(main.getByRole('button', { name: 'Unfollow' }));
  await expect(nav.getByText(T.map)).toHaveCount(0);
  await press(nav.getByRole('button', { name: /^Ended/ }));
  await press(main.getByText(T.map));
  await expect(main).toContainText('Ended');
  await expect(main.getByRole('button', { name: /Start anyway|^Launch/ })).toHaveCount(0);

  // Follow on an order that has sub-issues; one without them offers none. Following launches nothing.
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  const ships = async () => ((await (await fetch(`${admiral.url}api/v1/ships`)).json()) as { ships: unknown[] }).ships.length;
  const before = await ships();
  await expect(row(T.hangar)).toBeVisible();
  await expect(row(T.hangar).getByRole('button', { name: /^Follow/ })).toHaveCount(0);
  await press(row(T.annex).getByRole('button', { name: /^Follow/ }));
  await expect(page).toHaveURL(/\/app\/campaigns\/[0-9a-f]+/);
  await expect(main.getByRole('heading', { name: T.annex })).toBeVisible();
  expect(await ships()).toBe(before);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
