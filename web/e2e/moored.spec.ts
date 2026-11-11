import { expect, test } from './fixtures';
import { addGitHubSource, mooredFromTheShell, mooringEnv, seedFleets, watchPage } from './shell';

test('a moored ship shows in its own group with chips from the poll, and Don\'t follow reviews and Dismiss take it out', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mooredFromTheShell(page, admiral, (l) => l.click(), { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
