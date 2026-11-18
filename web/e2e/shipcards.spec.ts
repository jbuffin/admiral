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
  const ship = await launchMooredShip(admiral, 7);
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
  await expect(main.getByText('Moored', { exact: true })).toHaveCount(0);
  await expect(main.getByText('Sailed off', { exact: true })).toHaveCount(0);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
