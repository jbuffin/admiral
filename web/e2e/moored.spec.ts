import { expect, test } from './fixtures';
import { addWakeReviewers, addGitHubSource, answeringEnv, mooringEnv, seedFleets, watchPage } from './shell';
import { answerReviewFromThePRTab, mergeFromThePRTab } from './moored';

test('the PR tab of a ready moored ship shows the PR, Merge and Don\'t follow reviews, merges only after the tap, and the ship sails off', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mergeFromThePRTab(page, admiral, { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('the PR tab of a moored ship with a waking review shows the feedback bar, and Answer review shows its command and relaunches the ship with it', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  addWakeReviewers(admiral, 'zulu', ['alice']);
  await admiral.start({ env: answeringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await answerReviewFromThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
