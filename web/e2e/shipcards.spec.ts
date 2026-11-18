import { rmSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';
import { addGitHubSource, launchMooredShip, mooringEnv, prAnswer, seedFleets, shipViewUrl, watchPage } from './shell';

// The ship moors in zulu, whose chat dock shares the ships' cue file, so the cue goes once the ship has moored and before zulu's chat opens; lagoon is the other fleet.
test("a moored ship and then a sailed-off one each post one card in their own fleet's chat, linking the PR and, while afloat, the ship, and no other fleet's chat shows them", { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true, chat: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  prAnswer(admiral, 7, { state: 'OPEN' });
  const ship = await launchMooredShip(admiral, 7, { findings: '# Root cause\n\n- the cache\n- the lock\n\n```\nmutex.Lock()\n```\n' + '\nA long closing paragraph of notes.\n'.repeat(80) });
  rmSync(join(admiral.home, 'cue'), { force: true });

  await page.goto(admiral.url + 'app/chat?fleet=zulu');
  const main = page.getByRole('main');
  await expect(main.getByText('Moored', { exact: true })).toHaveCount(1);
  await expect(main.getByText('Sailed off', { exact: true })).toHaveCount(0);

  // The PR opens outside the app; the journey checks the link, never the network.
  const pr = main.getByRole('link', { name: 'PR #7' });
  await expect(pr).toHaveAttribute('href', 'https://github.com/acme/api/pull/7');
  await expect(pr).toHaveAttribute('target', '_blank');
  await expect(pr).toHaveAttribute('rel', /noopener/);
  await expect(pr).toHaveAttribute('rel', /noreferrer/);

  // While the ship is moored, a tap on its name opens its ship view.
  await main.getByRole('link', { name: `Open ship ${ship.name}` }).tap();
  await expect(page).toHaveURL(shipViewUrl(admiral, ship));
  await page.goBack();
  await expect(main.getByText('Moored', { exact: true })).toHaveCount(1);

  // The kept findings open in a sheet over the chat, rendered as markdown.
  await main.getByRole('button', { name: 'Findings' }).tap();
  const sheet = page.getByRole('dialog', { name: 'Findings' });
  await expect(sheet.getByRole('heading', { name: 'Root cause' })).toBeVisible();
  await expect(sheet.getByRole('listitem')).toHaveText(['the cache', 'the lock']);
  await expect(sheet.locator('pre')).toContainText('mutex.Lock()');
  // A long body scrolls under Close, which stays in view.
  await sheet.locator('.findings-body').evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  await expect(sheet.getByRole('button', { name: 'Close' })).toBeInViewport();
  await sheet.getByRole('button', { name: 'Close' }).tap();
  await expect(sheet).toHaveCount(0);

  // Closing the PR, not merging it, makes the ship sail off.
  prAnswer(admiral, 7, { state: 'CLOSED' });
  await expect
    .poll(async () => {
      await fetch(`${admiral.url}api/v1/moored/refresh`, { method: 'POST' });
      return main.getByText('Sailed off', { exact: true }).count();
    }, { timeout: 30_000 })
    .toBe(1);
  await expect(main.getByText('Moored', { exact: true })).toHaveCount(1);
  await expect(main.getByText('Sailed off', { exact: true })).toHaveCount(1);

  // Another fleet's chat shows neither card.
  await page.goto(admiral.url + 'app/chat?fleet=lagoon');
  await expect(page.getByRole('heading', { name: 'AdmiralBot · Lagoonside fleet' })).toBeVisible();
  // The thread shows Loading… until lagoon's snapshot arrives, so the counts below read a loaded thread.
  await expect(main.getByText('Loading…')).toHaveCount(0);
  await expect(main.getByText('Moored', { exact: true })).toHaveCount(0);
  await expect(main.getByText('Sailed off', { exact: true })).toHaveCount(0);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
