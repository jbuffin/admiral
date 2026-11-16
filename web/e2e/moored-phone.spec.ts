import { expect, test } from './fixtures';
import { addGitHubSource, mooringEnv, seedFleets, watchPage } from './shell';
import { fixSheetFitsThePhone, mergeFromThePRTab } from './moored';

test('on the phone, the PR tab of a ready moored ship shows the PR, Merge and Don\'t follow reviews, merges only after the tap, and the ship sails off', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mergeFromThePRTab(page, admiral, { phone: true });
  await fixSheetFitsThePhone(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
