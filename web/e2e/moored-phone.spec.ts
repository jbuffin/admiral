import { expect, test } from './fixtures';
import { addGitHubSource, addWakeReviewers, answeringEnv, answerReviewFromThePRTab, mergeFailsInThePRTab, mergeFromThePRTab, mooredFromTheShell, mooringEnv, myPRsMergeACarriedPR, myPRsMergeFailsOnTheRow, myPRsOfferCommodoreOnAShiplessPR, myPRsShowANotReadyCarriedPR, noWakeWithoutResume, prTabOfARunningStack, READY_PR, readyStackMergesFromThePRTab, seedFleets, seedMyPRs, SHIPLESS_PR, unfollowFromThePRTab, WAITING_PR, wakeFromTheShell, wakeFromTheShipView, watchPage, stopShipMoors, withFakeGh } from './shell';

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

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a moored ship with no valid resume text shows Wake and the message box off, with the reason', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await noWakeWithoutResume(page, admiral, (l) => l.tap());
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

test('on the phone, the PR tab of a ready stack offers Merge for the whole stack, and one tap runs one gh stack merge', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await readyStackMergesFromThePRTab(page, admiral, { phone: true });

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

test('on the phone, a ready PR a ship carries shows its chips, the ship and Ready to merge in My PRs, and Merge there merges only after the tap', async ({ page, admiral }) => {
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

test('on the phone, a PR a ship carries that is not ready shows its chips, the ship and its next step in My PRs, with no Merge and no Launch', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [WAITING_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsShowANotReadyCarriedPR(page, admiral, { phone: true });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('on the phone, a PR no ship carries shows its chips and Launch Commodore in My PRs, which opens the launch form on the PR', async ({ page, admiral }) => {
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

test('on the phone, a merge gh refuses shows in the alert on the row in My PRs and the ship stays moored', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [READY_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsMergeFailsOnTheRow(page, admiral);

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
