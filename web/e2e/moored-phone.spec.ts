import { expect, test } from './fixtures';
import { addGitHubSource, fixPRFromThePRTab, fixStackFromThePRTab, addWakeReviewers, answeringEnv, answerAllReviewsFromThePRTab, answerReviewFromThePRTab, mergeFailsInThePRTab, mergeFromThePRTab, mooredFromTheShell, mooringEnv, myPRsMergeACarriedPR, myPRsOfferCommodoreOnAShiplessPR, wakeAndStopWithoutResume, prTabOfARunningStack, READY_PR, readyStackMergesFromThePRTab, seedFleets, seedMyPRs, SHIPLESS_PR, unfollowFromThePRTab, wakeFromTheShell, wakeFromTheShipView, watchPage, stopShipMoors, withFakeGh, reviewedPRIsReReviewed, seedReview, chainedRunningShipWaitsToStack, independentPRsMergeOneAtATime } from './shell';

test('on the phone, a moored ship shows in its own group with chips from the poll, and Don\'t follow reviews and Dismiss take it out by touch', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mooredFromTheShell(page, admiral, (l) => l.tap(), { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, Wake on a moored row starts a round under the same name, with the resume text as the prompt', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await wakeFromTheShell(page, admiral, (l) => l.tap());
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a moored row opens its ship view, and a message sent there wakes the ship with the text after the resume text', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await wakeFromTheShipView(page, admiral, (l) => l.tap());
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);
  // The conversation and its box take most of the screen and sit on the tab bar.
  const panel = (await page.getByRole('tabpanel').boundingBox())!;
  const tabBar = (await page.getByRole('navigation', { name: 'Main' }).boundingBox())!;
  expect(panel.height).toBeGreaterThan(page.viewportSize()!.height * 0.7);
  expect(tabBar.y - (panel.y + panel.height)).toBeLessThanOrEqual(8);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a moored ship with no valid resume text wakes from the box with the message alone, and Stop ship ends one', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await wakeAndStopWithoutResume(page, admiral, (l) => l.tap());
  expect(await page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth))).toBe(true);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, the PR tab of a ready moored ship shows the PR, Merge and Don\'t follow reviews, merges only after the tap, and the ship sails off', { tag: '@critical' }, async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mergeFromThePRTab(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, the PR tab of chained PRs offers Stack on GitHub, which lists them bottom first and runs one gh stack link, and then Merge takes the whole stack with one gh stack merge', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await readyStackMergesFromThePRTab(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, independent PRs show no bar and no Stack on GitHub, and each sub-tab\'s Merge runs one gh pr merge for its own PR while the ship stays moored', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await independentPRsMergeOneAtATime(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a running ship carrying two chained PRs says to stack after it moors, with no Stack on GitHub and no Merge', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await chainedRunningShipWaitsToStack(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, Don\'t follow reviews in the PR tab ends the mooring and the ship leaves', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await unfollowFromThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a merge that gh fails shows its message in the PR tab and the ship stays moored', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mergeFailsInThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a ready PR a ship carries shows Ready to merge in My PRs, and Merge in its detail merges only after the tap', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [READY_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsMergeACarriedPR(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a PR no ship carries opens the launch form on the PR from the arrow on its row and from Launch ship in its detail', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [SHIPLESS_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsOfferCommodoreOnAShiplessPR(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, Stop ship on a ship with open PRs offers Moor, which moors it and shows it in the Moored group with its PRs', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await stopShipMoors(page, admiral, (l) => l.tap(), { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a ship carrying a 2-PR stack shows each PR\'s sections in one column, with Reviewers folded to a summary line that opens on a tap', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await prTabOfARunningStack(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, the feedback bar and the Answer review sheet fit, with the command wrapped and no sideways scroll', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  addWakeReviewers(admiral, 'zulu', ['alice']);
  await admiral.start({ env: answeringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await answerReviewFromThePRTab(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, the stack bar with Answer all reviews and its sheet fit, with no sideways scroll', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  addWakeReviewers(admiral, 'zulu', ['alice']);
  await admiral.start({ env: answeringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await answerAllReviewsFromThePRTab(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a reviewed PR\'s feedback bar and Re-review sheet work by tap with no horizontal scroll, and Re-review wakes the ship', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  seedReview(admiral);
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await reviewedPRIsReReviewed(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, the fix bar and the Fix PR sheet fit, with no sideways scroll', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  addWakeReviewers(admiral, 'zulu', ['alice']);
  await admiral.start({ env: answeringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await fixPRFromThePRTab(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, Fix stack in the stack bar fits, with no sideways scroll', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  addWakeReviewers(admiral, 'zulu', ['alice']);
  await admiral.start({ env: answeringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await fixStackFromThePRTab(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
