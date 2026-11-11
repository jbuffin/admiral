import { join } from 'node:path';
import { expect, test } from './fixtures';
import { T, campaignIdOf, followByPaste, githubReady, issueUrl, launchBehindThePage, launchSliceBehindThePage, MAP, seedCampaignFleet } from './campaign';
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
  await expect(main).toContainText(/Last read/);

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
  await expect(group('Ready, with you').getByRole('listitem').filter({ hasText: T.grill })).toContainText(/Underway · /);

  // A second launch of a child someone else started meanwhile is refused with the server's message, which names the ship.
  await press(row(T.rover).getByRole('button').first());
  await press(page.getByRole('dialog', { name: new RegExp(T.rover) }).getByRole('button', { name: /^Launch/ }));
  await expect(form).toBeVisible();
  const holder = await launchBehindThePage(admiral, id, 23);
  await press(form.getByRole('button', { name: 'Launch', exact: true }));
  await expect(form.getByRole('alert')).toContainText(holder);
  await expect(form).toBeVisible();
  await press(form.getByRole('button', { name: 'Cancel', exact: true }));

  // A child's mode is changed in its sheet: the row moves groups and is marked overridden; Reset sends it back.
  const survey = () => page.getByRole('dialog', { name: new RegExp(T.survey) });
  await press(row(T.survey).getByRole('button').first());
  await press(survey().getByText('Change mode or skill'));
  await survey().getByLabel('Mode').selectOption('with-you');
  await press(survey().getByRole('button', { name: 'Save' }));
  await expect(group('Ready, with you').getByRole('listitem').filter({ hasText: T.survey })).toBeVisible();
  await expect(group('Ready, without you')).toHaveCount(0);
  await press(row(T.survey).getByRole('button').first());
  await expect(survey()).toContainText('overridden');
  await press(survey().getByText('Change mode or skill'));
  await press(survey().getByRole('button', { name: 'Reset' }));
  await expect(group('Ready, without you').getByRole('listitem').filter({ hasText: T.survey })).toBeVisible();

  // The work repo: a good value shows in the pill, a bad one is refused with the server's message.
  await press(main.getByRole('button', { name: 'Set work repo' }));
  const repo = main.getByRole('textbox', { name: 'Work repo' });
  const refused = page.waitForResponse((r) => r.request().method() === 'PUT' && new URL(r.url()).pathname === `/api/v1/campaigns/${id}/work-repo`);
  await repo.fill('not a repo');
  await repo.press('Enter');
  const message = ((await (await refused).json()) as { error: string }).error;
  await expect(main.getByRole('alert')).toContainText(message);
  await repo.fill('github.com/acme/widgets');
  await repo.press('Enter');
  await expect(main.getByRole('button', { name: 'github.com/acme/widgets' })).toBeVisible();

  // Unfollow: it leaves the sidebar and is listed under Ended, read-only for launching.
  await press(main.getByRole('button', { name: 'Unfollow' }));
  await expect(nav.getByText(T.map)).toHaveCount(0);
  await press(nav.getByRole('button', { name: /^Ended/ }));
  await press(main.getByText(T.map));
  await expect(main).toContainText('Ended');
  // Launching is closed to an ended campaign; its override and work repo are not.
  await press(row(T.survey).getByRole('button').first());
  await expect(survey()).toBeVisible();
  await expect(survey().getByRole('button', { name: /Start anyway|^Launch/ })).toHaveCount(0);
  await expect(survey().getByText('Change mode or skill')).toBeVisible();
  await press(survey().getByRole('button', { name: 'Close' }));
  await expect(main.getByRole('button', { name: 'github.com/acme/widgets' })).toBeVisible();

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

  // A slice: children that can join carry a checkbox named by their title; checking one opens nothing.
  const annexId = campaignIdOf(page);
  const pick = (title: string) => row(title).getByRole('checkbox', { name: title });
  const ready = group('Ready, without you');
  await expect(ready.getByRole('checkbox')).toHaveCount(4);
  await press(pick('Annex doors'));
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(ready.getByRole('button', { name: /^Launch slice/ })).toHaveCount(0);
  await press(pick('Annex lights'));
  await press(ready.getByRole('button', { name: 'Launch slice (2)' }));

  // Someone else launches the same two meanwhile: the form shows the refusal naming that ship.
  const sliceForm = page.getByRole('dialog', { name: 'Launch', exact: true });
  await expect(sliceForm).toContainText('Without you');
  await expect(sliceForm.getByLabel('Skill')).toHaveCount(0);
  await expect(sliceForm.getByRole('checkbox', { name: /autopilot/i })).toHaveCount(0);
  const holder2 = await launchSliceBehindThePage(admiral, annexId, [32, 31]);
  await press(sliceForm.getByRole('button', { name: 'Launch', exact: true }));
  await expect(sliceForm.getByRole('alert')).toContainText(holder2);
  await press(sliceForm.getByRole('button', { name: 'Cancel', exact: true }));
  // Both rows now show that ship and drop out of the selection: no checkbox, no bar.
  await expect(row('Annex doors')).toContainText(holder2);
  await expect(row('Annex lights')).toContainText(holder2);
  await expect(pick('Annex doors')).toHaveCount(0);
  await expect(pick('Annex lights')).toHaveCount(0);
  await expect(ready.getByRole('button', { name: /^Launch slice/ })).toHaveCount(0);

  // A pair of the two left launches from the page: one request with both URLs, the dirs call names the slice's order, one ship.
  await press(pick('Annex vents'));
  await press(pick('Annex pumps'));
  const sliceDirs = page.waitForRequest((r) => new URL(r.url()).pathname === '/api/v1/launch-orders/dirs');
  await press(ready.getByRole('button', { name: 'Launch slice (2)' }));
  const order = ((await sliceDirs).postDataJSON() as { source: string; id: string });
  expect(order).toMatchObject({ source: 'campaign', id: `${annexId}/${issueUrl(33)},34` });
  await expect(sliceForm.getByLabel('Checkout')).toHaveValue(join(admiral.home, 'widgets'));
  const sliceSent = page.waitForRequest((r) => r.method() === 'POST' && new URL(r.url()).pathname === `/api/v1/campaigns/${annexId}/launch`);
  await press(sliceForm.getByRole('button', { name: 'Launch', exact: true }));
  const sliceBody = (await sliceSent).postDataJSON() as Record<string, unknown>;
  expect(sliceBody).toMatchObject({ issues: [issueUrl(33), issueUrl(34)], harness: 'claude' });
  expect(sliceBody).not.toHaveProperty('child_url');
  await expect(sliceForm).toBeHidden();
  const shipsNow = (await (await fetch(`${admiral.url}api/v1/ships`)).json()) as { ships: { name: string; issues?: string[] }[] };
  const mine = shipsNow.ships.filter((s) => s.issues?.includes(issueUrl(33)));
  expect(mine).toHaveLength(1);
  expect(mine[0].issues).toEqual([issueUrl(33), issueUrl(34)]);
  await expect(row('Annex vents')).toContainText(mine[0].name);
  await expect(row('Annex pumps')).toContainText(mine[0].name);
  await expect(ready.getByRole('checkbox')).toHaveCount(0);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
