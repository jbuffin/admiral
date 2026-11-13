import { expect, test } from './fixtures';
import { addGitHubSource, mergeFailsInThePRTab, mergeFromThePRTab, mooredFromTheShell, mooringEnv, myPRsMergeACarriedPR, myPRsMergeFailsOnTheRow, myPRsOfferCommodoreOnAShiplessPR, myPRsShowANotReadyCarriedPR, noWakeWithoutResume, prTabOfARunningStack, READY_PR, readyStackMergesFromThePRTab, seedFleets, seedMyPRs, SHIPLESS_PR, stackMismatchInThePRTab, staleMergeTokenInThePRTab, unfollowFromThePRTab, WAITING_PR, wakeFromTheShell, wakeFromTheShipView, watchPage, moorRefusedWhenThePRClosed, plainReturnSheetStaysPlain, returnToPortCleansUp, returnToPortMoors, stopForGoodEnds, stopShipMoors, withFakeGh } from './shell';

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

test('Wake on a moored row starts a round under the same name, with the resume text as the prompt', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await wakeFromTheShell(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a moored row opens its ship view, and a message sent there wakes the ship with the text after the resume text', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await wakeFromTheShipView(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a moored ship with no valid resume text shows Wake and the message box off, with the reason', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await noWakeWithoutResume(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

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

test('the PR tab of a ready stack offers Merge for the whole stack, and one tap runs one gh stack merge', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await readyStackMergesFromThePRTab(page, admiral, { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('Don\'t follow reviews in the PR tab ends the mooring and the ship leaves', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await unfollowFromThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a merge that gh fails shows its message in the PR tab and the ship stays moored', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await mergeFailsInThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a Merge the server refuses as 403 in the PR tab says Admiral restarted, offers a reload, and merges nothing', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await staleMergeTokenInThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a stack whose checkout holds other PRs shows the refusal of the server in the PR tab and merges nothing', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await stackMismatchInThePRTab(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a ready PR a ship carries shows its chips, the ship and Ready to merge in My PRs, and Merge there merges only after the tap', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [READY_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsMergeACarriedPR(page, admiral, { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a PR a ship carries that is not ready shows its chips, the ship and its next step in My PRs, with no Merge and no Launch', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [WAITING_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsShowANotReadyCarriedPR(page, admiral, { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a PR no ship carries shows its chips and Launch Commodore in My PRs, which opens the launch form on the PR', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  seedMyPRs(admiral, [SHIPLESS_PR]);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await myPRsOfferCommodoreOnAShiplessPR(page, admiral, { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a merge gh refuses shows in the alert on the row in My PRs and the ship stays moored', async ({ page, admiral }) => {
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

test('Stop ship on a ship with open PRs offers Moor, which moors it and shows it in the Moored group with its PRs', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await stopShipMoors(page, admiral, (l) => l.click(), { phone: false });

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('Stop for good on a ship with open PRs ends it and moors nothing', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await stopForGoodEnds(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('Moor on the Return to port sheet moors the ship and types no clean-up prompt', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await returnToPortMoors(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('Return to port on a ship with open PRs still cleans up and then stops it', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await returnToPortCleansUp(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a Moor the server refuses because the PR closed shows its message in the Stop ship sheet and the ship stays live', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await moorRefusedWhenThePRClosed(page, admiral, (l) => l.click(), 'Stop ship');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a Moor the server refuses because the PR closed shows its message in the Return to port sheet and the ship stays live', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await moorRefusedWhenThePRClosed(page, admiral, (l) => l.click(), 'Return to port');

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a Return to port sheet opened before the ship had an open PR stays plain, and its button cleans up', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: withFakeGh(admiral) });
  const watch = watchPage(page, admiral.url);

  await plainReturnSheetStaysPlain(page, admiral, (l) => l.click());

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});

test('a running ship carrying a 2-PR stack shows its count, summary and sub-tabs in stack order, the chips take their colours, and each PR shows its checks, description, reviewers and conversation, which fold', async ({ page, admiral }) => {
  seedFleets(admiral, { live: true });
  addGitHubSource(admiral);
  await admiral.start({ env: mooringEnv(admiral) });
  const watch = watchPage(page, admiral.url);

  await prTabOfARunningStack(page, admiral);

  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
  expect(watch.errors).toEqual([]);
});
