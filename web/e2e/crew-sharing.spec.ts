import { createHash } from 'node:crypto';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from './fixtures';
import { seedCrew } from './crew';

const REPO = 'octocat/admiral-crew';

/** Marks zulu's bosun as shared from REPO, the way an import leaves it; returns the two file paths. */
function seedSharedBosun(home: string) {
  const crew = join(home, 'fleets', 'zulu', 'crew');
  const md = join(crew, 'bosun.md');
  const sidecar = join(crew, 'bosun.shared.json');
  writeFileSync(
    sidecar,
    JSON.stringify({
      version: 1,
      host: 'github.com',
      repo: REPO,
      path: 'crew/bosun.md',
      upstream_name: 'bosun',
      commit: 'a'.repeat(40),
      tag: 'v1.0',
      upstream_blob: 'b'.repeat(40),
      content_sha256: createHash('sha256').update(readFileSync(md)).digest('hex'),
      imported_at: '2026-11-10T12:00:00Z',
      updated_at: '2026-11-10T12:00:00Z',
    }),
  );
  return { md, sidecar };
}

test('the fleet Preferences page turns crew sharing on, and it stays on after a reload', async ({ page, admiral }) => {
  seedCrew(admiral);
  await admiral.start();
  await page.goto(admiral.url + 'app/preferences/zulu/fleet');
  const main = page.getByRole('main');

  const sharing = main.getByRole('switch', { name: 'Crew sharing' });
  await expect(main.getByText("Lets you publish this fleet's crew members to a public GitHub repo.")).toBeVisible();
  await expect(sharing).toHaveAttribute('aria-checked', 'false');
  await sharing.click();
  await expect(sharing).toHaveAttribute('aria-checked', 'true');

  await page.reload();
  await expect(page.getByRole('main').getByRole('switch', { name: 'Crew sharing' })).toHaveAttribute('aria-checked', 'true');
});

test('on the iPad, Make mine on a shared fleet member asks first, then stops tracking the repo and keeps the member', async ({ page, admiral }) => {
  seedCrew(admiral);
  const { md, sidecar } = seedSharedBosun(admiral.home);
  const before = readFileSync(md, 'utf8');
  await admiral.start();
  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  const main = page.getByRole('main');

  await main.getByRole('link', { name: /\bbosun\b/ }).click();
  const makeMine = main.getByRole('button', { name: 'Make mine' });
  await expect(makeMine).toBeVisible();
  await makeMine.click();
  await expect(main.getByText(`Stop tracking ${REPO}? This member will no longer get updates or show who shared it.`)).toBeVisible();
  expect(existsSync(sidecar)).toBe(true);

  await main.getByRole('button', { name: 'Confirm' }).click();
  await expect(main.getByRole('button', { name: 'Make mine' })).toHaveCount(0);
  await expect(main.getByRole('heading', { name: 'bosun', level: 2 })).toBeVisible();
  await expect.poll(() => existsSync(sidecar)).toBe(false);
  expect(readFileSync(md, 'utf8')).toBe(before);
});

test('a member that was never shared has no Make mine', async ({ page, admiral }) => {
  seedCrew(admiral);
  await admiral.start();
  await page.goto(admiral.url + 'app/crew?fleet=zulu');
  const main = page.getByRole('main');
  await main.getByRole('link', { name: /\bbosun\b/ }).click();
  await expect(main.getByRole('heading', { name: 'bosun', level: 2 })).toBeVisible();
  await expect(main.getByRole('button', { name: 'Make mine' })).toHaveCount(0);
});
