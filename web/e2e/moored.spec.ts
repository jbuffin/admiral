import { expect, test } from './fixtures';
import { addGitHubSource, mooredFromTheShell, mooringEnv, noWakeWithoutResume, seedFleets, wakeFromTheShell, wakeFromTheShipView, watchPage } from './shell';

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
