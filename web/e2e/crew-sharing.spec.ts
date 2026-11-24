import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from './fixtures';
import { seedCrew } from './crew';
import { addGitHubSource, watchPage } from './shell';

const fakeGitleaks = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..', 'e2e', 'testdata', 'fakegitleaks');
const REPO = 'octocat/admiral-crew';
const LISTING = `repos/${REPO}/contents/crew`;
const FILE = `${LISTING}/bosun.md`;
const COMMIT = { commit: { html_url: `https://github.com/${REPO}/commit/abc123` }, content: { sha: 'f'.repeat(40) } };

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

  await admiral.start({ env: { ...gh.env, PATH: `${fakeGitleaks}:${gh.env.PATH}`, FAKEGITLEAKS_DIR: join(admiral.home, 'fakegitleaks') } });
  const watch = watchPage(page, admiral.url);
  const main = page.getByRole('main');
  const localPath = join(admiral.home, 'fleets', 'zulu', 'crew', 'bosun.md');
  const before = readFileSync(localPath, 'utf8');

  // Sharing is off: no Share button yet.
  await page.goto(admiral.url + 'app/crew/bosun?fleet=zulu');
  await expect(main.getByRole('button', { name: 'Edit' })).toBeVisible();
  await expect(main.getByRole('button', { name: 'Share' })).toHaveCount(0);

  await page.goto(admiral.url + 'app/fleets/zulu');
  await page.getByRole('switch', { name: 'Crew sharing' }).click();
  await expect(page.getByRole('switch', { name: 'Crew sharing' })).toHaveAttribute('aria-checked', 'true');

  // Share with no repo yet: create it, then the preview of the exact file.
  await page.goto(admiral.url + 'app/crew/bosun?fleet=zulu');
  await main.getByRole('button', { name: 'Share' }).click();
  await page.getByRole('button', { name: 'Create repo' }).click();
  await expect(page.getByText(/crew\/bosun\.md/)).toBeVisible();
  await expect(page.getByText('Work as bosun.')).toBeVisible();
  await expect(page.getByRole('checkbox')).toHaveCount(0);

  // Publish: the fake repo now lists the file, and the badge shows.
  answer('e-listing', 'GET', LISTING, [{ name: 'bosun.md', path: 'crew/bosun.md', type: 'file', sha: 'a'.repeat(40) }]);
  answer('h-file', 'GET', FILE, { name: 'bosun.md', sha: 'a'.repeat(40), size: 20, encoding: 'base64', content: Buffer.from('published\n').toString('base64') });
  await page.getByRole('button', { name: 'Publish' }).click();
  await expect(page.getByRole('link', { name: /commit/i })).toBeVisible();
  await page.reload();
  await expect(main.getByText('Published')).toBeVisible();

  // Edit, then Share again: the preview shows the diff against the published file and replaces its blob.
  await main.getByRole('button', { name: 'Edit' }).click();
  await main.getByLabel(/pick me when/i).fill('Pick for rigging, twice.');
  await main.getByRole('button', { name: 'Save' }).click();
  await main.getByRole('button', { name: 'Share' }).click();
  await expect(page.locator('[data-kind="del"]').first()).toBeVisible();
  await expect(page.locator('[data-kind="add"]').first()).toContainText('twice');
  await page.getByRole('button', { name: 'Publish' }).click();
  await expect(page.getByRole('link', { name: /commit/i })).toBeVisible();

  // Unpublish: confirmed with the exact text; the local member stays.
  answer('e-listing', 'GET', LISTING, {}, 404);
  await main.getByRole('button', { name: 'Unpublish' }).click();
  await expect(page.getByText(`Delete \`crew/bosun.md\` from \`${REPO}\`?`, { exact: false })).toBeVisible();
  await page.getByRole('button', { name: 'Confirm unpublish' }).click();
  await expect(main.getByText('Published')).toHaveCount(0);
  expect(readFileSync(localPath, 'utf8')).not.toBe('');
  expect(readFileSync(localPath, 'utf8')).toContain('twice');
  expect(before).toContain('Pick for rigging.');

  expect(watch.errors).toEqual([]);
  expect(watch.unversioned()).toEqual([]);
  expect(watch.foreign).toEqual([]);
});
