import { existsSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';
import { seedCrew } from './crew';
import { addGitHubSource, watchPage } from './shell';

const REPO = 'octocat/admiral-crew';
const LISTING = `repos/${REPO}/contents/crew`;
const FILE = `${LISTING}/bosun.md`;
const COMMIT = { commit: { sha: 'c'.repeat(40) }, content: { sha: 'f'.repeat(40) } };
const LONG = 'x'.repeat(500);

// The share journey of one engineer: turn Crew sharing on, create the share repo, publish a member, change it and publish
// again with the diff, then unpublish. Only the fake gh and the fake gitleaks stand in for GitHub and the scanner.
test('a member is published to a new share repo, republished with its diff, and unpublished, and the local member is never touched', async ({ page, admiral }) => {
  seedCrew(admiral);
  addGitHubSource(admiral);
  const gh = admiral.fakeGh();
  gh.viewer('octocat');
  const rest = join(gh.dir, 'rest');
  mkdirSync(rest, { recursive: true });
  const answer = (key: string, method: string, endpoint: string, body: unknown, status = 200) => {
    writeFileSync(join(rest, `${key}.endpoint`), endpoint);
    writeFileSync(join(rest, `${key}.method`), method);
    writeFileSync(join(rest, `${key}.status`), String(status));
    writeFileSync(join(rest, `${key}.json`), JSON.stringify(body));
  };
  const repo = { full_name: REPO, visibility: 'public', private: false, archived: false, permissions: { push: true }, default_branch: 'main' };
  answer('a-user', 'GET', 'user', { login: 'octocat' });
  answer('b-create', 'POST', 'user/repos', repo);
  answer('c-readme', 'PUT', `repos/${REPO}/contents/README.md`, COMMIT);
  answer('d-repo', 'GET', `repos/${REPO}`, repo);
  answer('e-listing', 'GET', LISTING, {}, 404);
  answer('f-put', 'PUT', FILE, COMMIT);
  answer('g-del', 'DELETE', FILE, COMMIT);
  answer('h-file', 'GET', FILE, {}, 404);

  await admiral.start({ env: { ...gh.env, ...admiral.fakeGitleaks(gh.env.PATH).env } });
  const watch = watchPage(page, admiral.url);
  const main = page.getByRole('main');
  const localPath = join(admiral.home, 'fleets', 'zulu', 'crew', 'bosun.md');

  // Sharing is off: no Share button yet.
  await page.goto(admiral.url + 'app/crew/bosun?fleet=zulu');
  await expect(main.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
  await expect(main.getByRole('button', { name: 'Share' })).toHaveCount(0);

  await page.goto(admiral.url + 'app/preferences/zulu/fleet');
  await page.getByRole('switch', { name: 'Crew sharing' }).click();
  await expect(page.getByRole('switch', { name: 'Crew sharing' })).toHaveAttribute('aria-checked', 'true');

  // Share with no repo yet: create it, then the preview of the exact file.
  await page.goto(admiral.url + 'app/crew/bosun?fleet=zulu');
  await main.getByRole('button', { name: 'Share' }).click();
  await page.getByRole('button', { name: 'Create repo' }).click();
  await expect(page.getByText(/crew\/bosun\.md/)).toBeVisible();
  await expect(page.getByText('Work as bosun.')).toBeVisible();
  await expect(page.getByText('Secret scan: nothing found.')).toBeVisible();
  await expect(page.getByRole('checkbox')).toHaveCount(0);

  // Publish: the fake repo now lists the file, and Done closes the panel and shows the Published pill, which links to the file.
  const published = (await page.getByLabel('File that goes public').textContent()) ?? '';
  expect(published).toContain('Work as bosun.');
  answer('e-listing', 'GET', LISTING, [{ name: 'bosun.md', path: 'crew/bosun.md', type: 'file', sha: 'a'.repeat(40) }]);
  answer('h-file', 'GET', FILE, { name: 'bosun.md', sha: 'a'.repeat(40), size: Buffer.byteLength(published), encoding: 'base64', content: Buffer.from(published).toString('base64') });
  await page.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(page.getByRole('link', { name: /commit/i })).toHaveAttribute('href', `https://github.com/${REPO}/commit/${'c'.repeat(40)}`);
  await page.getByRole('button', { name: 'Done' }).click();
  await expect(page.getByRole('button', { name: 'Publish', exact: true })).toHaveCount(0);
  await expect(main.getByRole('link', { name: 'Published' })).toHaveAttribute('href', `https://github.com/${REPO}/blob/HEAD/crew/bosun.md`);

  // Edit, then Share again: the preview shows the diff against the published file and replaces its blob.
  await main.getByRole('button', { name: 'Edit', exact: true }).click();
  await main.getByLabel(/pick me when/i).fill(`Pick for rigging, twice. ${LONG}`);
  await main.getByRole('button', { name: 'Save' }).click();
  await main.getByRole('button', { name: 'Share' }).click();
  await expect(page.getByText('Replaces the published version')).toBeVisible();
  await expect(page.locator('[data-kind="del"]').first()).toBeVisible();
  await expect(page.locator('[data-kind="add"]').first()).toContainText('twice');
  // A 500-character line wraps inside the preview instead of scrolling it.
  const bytes = page.getByLabel('File that goes public');
  await expect(bytes).toContainText(LONG);
  expect(await bytes.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await page.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(page.getByRole('link', { name: /commit/i })).toBeVisible();
  await page.getByRole('button', { name: 'Done' }).click();
  await expect(main.getByRole('link', { name: 'Published' })).toBeVisible();

  // Unpublish: confirmed with the exact text; the local member stays.
  const beforeUnpublish = readFileSync(localPath, 'utf8');
  await main.getByRole('button', { name: 'Unpublish' }).click();
  await expect(page.getByText(`Delete crew/bosun.md from ${REPO}?`, { exact: false })).toBeVisible();
  await expect(page.getByLabel('Published file')).toContainText('name: bosun');
  answer('e-listing', 'GET', LISTING, {}, 404);
  await page.getByRole('button', { name: 'Confirm unpublish' }).click();
  await expect(main.getByRole('link', { name: 'Published' })).toHaveCount(0);
  expect(readFileSync(localPath, 'utf8')).toBe(beforeUnpublish);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});

const SHA = 'd'.repeat(40);
const THEIRS = '---\nname: navigator\nrole: triager\npick-me-when: Pick **navigator** for triage.\n---\n\nWork as navigator.\n';

// The import journey of one engineer: paste a ref, read the card and the exact file at the pinned commit, import it,
// and see the member credited to its owner. Only the fake gh stands in for GitHub.
test('a crew member is imported from a ref, read before it is written, and shows who shared it', async ({ page, admiral }) => {
  seedCrew(admiral);
  addGitHubSource(admiral);
  const gh = admiral.fakeGh();
  gh.viewer('octocat');
  const rest = join(gh.dir, 'rest');
  mkdirSync(rest, { recursive: true });
  const answer = (key: string, endpoint: string, body: unknown) => {
    writeFileSync(join(rest, `${key}.endpoint`), endpoint);
    writeFileSync(join(rest, `${key}.method`), 'GET');
    writeFileSync(join(rest, `${key}.status`), '200');
    writeFileSync(join(rest, `${key}.json`), JSON.stringify(body));
  };
  answer('a-user', 'user', { login: 'octocat' });
  answer('b-repo', 'repos/hubot/crew', { full_name: 'hubot/crew', visibility: 'public', private: false, archived: false, permissions: { push: false }, default_branch: 'main' });
  answer('c-commit', 'repos/hubot/crew/commits/main', { sha: SHA });
  answer('d-file', `repos/hubot/crew/contents/crew/navigator.md?ref=${SHA}`, { name: 'navigator.md', sha: 'e'.repeat(40), size: Buffer.byteLength(THEIRS), encoding: 'base64', content: Buffer.from(THEIRS).toString('base64') });
  answer('e-tags', 'repos/hubot/crew/tags?per_page=100', [{ name: 'v1', commit: { sha: SHA } }]);

  await admiral.start({ env: { ...gh.env, ...admiral.fakeGitleaks(gh.env.PATH).env } });
  const watch = watchPage(page, admiral.url);
  const main = page.getByRole('main');
  const imported = join(admiral.home, 'fleets', 'zulu', 'crew', 'navigator.md');

  await page.goto(admiral.url + 'app/preferences/zulu/fleet');
  await page.getByRole('switch', { name: 'Crew sharing' }).click();
  await expect(page.getByRole('switch', { name: 'Crew sharing' })).toHaveAttribute('aria-checked', 'true');

  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  await main.getByRole('button', { name: 'Import' }).click();
  await page.getByLabel(/ref or url/i).fill('hubot/crew/navigator');
  await page.getByRole('button', { name: 'Preview' }).click();

  // Nothing is written until Import: the card, the owner, the short commit, the tag and the exact file show first.
  const panel = page.getByRole('region', { name: 'Import' });
  await expect(panel.getByText(/shared by/i)).toBeVisible();
  await expect(panel).toContainText(SHA.slice(0, 7));
  await expect(panel).toContainText('v1');
  const file = panel.getByLabel('File to import');
  await expect(file).toContainText('Work as navigator.');
  // A long line wraps inside the file block instead of scrolling it.
  expect(await file.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  expect(existsSync(imported)).toBe(false);

  await page.getByRole('region', { name: 'Import' }).getByRole('button', { name: 'Import' }).click();
  await expect.poll(() => existsSync(imported)).toBe(true);
  expect(readFileSync(imported, 'utf8')).toBe(THEIRS);

  await page.goto(admiral.url + 'app/crew/navigator?fleet=zulu');
  await expect(main.getByRole('link', { name: /shared by hubot/i })).toHaveAttribute('href', `https://github.com/hubot/crew/blob/${SHA}/crew/navigator.md`);
  await expect(main.getByRole('button', { name: 'Share' })).toHaveCount(0);

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});

const SHA2 = 'b'.repeat(40);
const NEWER = '---\nname: scout\nrole: triager\npick-me-when: Pick **scout** for triage.\n---\n\nWork as scout, and keep the log short.\n';

// The update journey of one engineer: an imported member's upstream file moves on, the page says so, Update shows the
// exact change before anything is written, Apply pins the new commit, and the badge goes. Only the fake gh stands in for GitHub.
test('an imported member shows an update, previews the change, applies it, and the badge goes', async ({ page, admiral }) => {
  seedCrew(admiral);
  addGitHubSource(admiral);
  const gh = admiral.fakeGh();
  gh.viewer('octocat');
  const rest = join(gh.dir, 'rest');
  mkdirSync(rest, { recursive: true });
  const answer = (key: string, endpoint: string, body: unknown) => {
    writeFileSync(join(rest, `${key}.endpoint`), endpoint);
    writeFileSync(join(rest, `${key}.method`), 'GET');
    writeFileSync(join(rest, `${key}.status`), '200');
    writeFileSync(join(rest, `${key}.json`), JSON.stringify(body));
  };
  const OLD_BLOB = 'e'.repeat(40);
  const NEW_BLOB = 'd'.repeat(40);
  answer('a-user', 'user', { login: 'octocat' });
  answer('b-repo', 'repos/hubot/crew', { full_name: 'hubot/crew', visibility: 'public', private: false, archived: false, permissions: { push: false }, default_branch: 'main' });
  answer('c-commit', 'repos/hubot/crew/commits/main', { sha: SHA });
  answer('d-file', `repos/hubot/crew/contents/crew/scout.md?ref=${SHA}`, { name: 'scout.md', sha: OLD_BLOB, size: Buffer.byteLength(THEIRS), encoding: 'base64', content: Buffer.from(THEIRS).toString('base64') });
  answer('e-tags', 'repos/hubot/crew/tags?per_page=100', [{ name: 'v1', commit: { sha: SHA } }]);
  answer('f-listing', 'repos/hubot/crew/contents/crew', [{ name: 'scout.md', path: 'crew/scout.md', type: 'file', sha: OLD_BLOB }]);

  await admiral.start({ env: { ...gh.env, ...admiral.fakeGitleaks(gh.env.PATH).env } });
  const watch = watchPage(page, admiral.url);
  const main = page.getByRole('main');
  const member = join(admiral.home, 'fleets', 'zulu', 'crew', 'scout.md');

  await page.goto(admiral.url + 'app/fleets/zulu');
  await page.getByRole('switch', { name: 'Crew sharing' }).click();
  await expect(page.getByRole('switch', { name: 'Crew sharing' })).toHaveAttribute('aria-checked', 'true');
  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  await main.getByRole('button', { name: 'Import' }).click();
  await page.getByLabel(/ref or url/i).fill('hubot/crew/scout');
  await page.getByRole('button', { name: 'Preview' }).click();
  await page.getByRole('region', { name: 'Import' }).getByRole('button', { name: 'Import' }).click();
  await expect.poll(() => existsSync(member)).toBe(true);

  // Upstream moves on: a new commit, a new blob and a new tag.
  answer('c-commit', 'repos/hubot/crew/commits/main', { sha: SHA2 });
  answer('d-file', `repos/hubot/crew/contents/crew/scout.md?ref=${SHA2}`, { name: 'scout.md', sha: NEW_BLOB, size: Buffer.byteLength(NEWER), encoding: 'base64', content: Buffer.from(NEWER).toString('base64') });
  answer('e-tags', 'repos/hubot/crew/tags?per_page=100', [{ name: 'v2', commit: { sha: SHA2 } }]);
  answer('f-listing', 'repos/hubot/crew/contents/crew', [{ name: 'scout.md', path: 'crew/scout.md', type: 'file', sha: NEW_BLOB }]);

  await page.goto(admiral.url + 'app/crew/scout?fleet=zulu');
  await expect(main.locator('.crew-head').getByText('Update available')).toBeVisible();

  // Update reads the change first; nothing is written until Apply.
  await main.getByRole('button', { name: 'Update', exact: true }).click();
  await expect(main).toContainText(SHA2.slice(0, 7));
  await expect(main).toContainText('v2');
  await expect(main).toContainText('keep the log short');
  expect(readFileSync(member, 'utf8')).toBe(THEIRS);

  await main.getByRole('button', { name: 'Apply' }).click();
  await expect.poll(() => readFileSync(member, 'utf8')).toBe(NEWER);
  await expect(main.locator('.crew-head').getByText('Update available')).toHaveCount(0);
  await expect(main.getByRole('link', { name: /shared by hubot/i })).toBeVisible();

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
