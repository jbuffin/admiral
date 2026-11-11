import { expect, test } from './fixtures';
import { changeFile, gitCheckout, launchShip, rowLink, seedFleets, viewPanel, viewTab, withFakeGh } from './shell';

const changesRoute = '**/api/v1/ships/*/changes';

test('Changes says why there is no diff, shows a cut diff as cut, and retries after an error', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  await admiral.start({ env: withFakeGh(admiral) });
  const ship = await launchShip(admiral, { fleet: 'zulu', harness: 'claude', dir: gitCheckout(admiral, 'clean', { clean: true }) });
  await page.goto(admiral.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();

  // A server error reads as an error with Retry, and Retry asks again.
  let failing = true;
  await page.route(changesRoute, (route) =>
    failing ? route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'git timed out' }) }) : route.continue(),
  );
  await viewTab(page, 'Changes').tap();
  await expect(viewPanel(page).getByRole('alert')).toBeVisible();
  await expect(viewPanel(page)).not.toContainText(/No changes|No git history/);
  failing = false;
  await viewPanel(page).getByRole('button', { name: 'Retry' }).tap();
  await expect(changeFile(page, 'b.txt')).toContainText('added');
  await expect(viewPanel(page).getByRole('alert')).toHaveCount(0);

  // A checkout with nothing to compare reads No changes.
  await page.unroute(changesRoute);
  await page.route(changesRoute, (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ state: 'ok', files: [], diff: '', truncated: false }) }));
  await viewTab(page, 'Conversation').tap();
  await viewTab(page, 'Changes').tap();
  await expect(viewPanel(page)).toContainText('No changes');
  await expect(viewPanel(page)).not.toContainText('No git history');

  // A diff the server cut says so.
  await page.unroute(changesRoute);
  const diff = 'diff --git a/big.txt b/big.txt\n--- a/big.txt\n+++ b/big.txt\n@@ -1 +1,2 @@\n kept\n+added line\n';
  await page.route(changesRoute, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ state: 'ok', files: [{ path: 'big.txt', status: 'modified', added: 1, removed: 0 }], diff, truncated: true }) }),
  );
  await viewTab(page, 'Conversation').tap();
  await viewTab(page, 'Changes').tap();
  await expect(changeFile(page, 'big.txt')).toContainText('modified');
  await expect(viewPanel(page)).toContainText(/cut|truncated/i);
});
