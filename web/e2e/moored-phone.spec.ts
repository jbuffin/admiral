import { expect, test } from './fixtures';
import { addGitHubSource, mergeFailsInThePRTab, mergeFromThePRTab, mooredFromTheShell, mooringEnv, noWakeWithoutResume, readyStackMergesFromThePRTab, seedFleets, unfollowFromThePRTab, wakeFromTheShell, wakeFromTheShipView, watchPage } from './shell';

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
