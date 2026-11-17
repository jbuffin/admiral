import { type Locator, type Page, expect } from '@playwright/test';
import type { Admiral } from './fixtures';
import { expectChipTone, ghMergeSlow, launchMooredShip, mergeCalls, PR_BASE, prAnswer, prBehindStrictBase, prReview, readyOnTheServer, readyToWake, RESUME, rowLink, seedMergeMethod, shipViewUrl, updateBranchCalls, viewPanel, viewTab, wakeButton, wokenPrompt, type LaunchedShip } from './shell';

type ShipRow = LaunchedShip & { status: string; prs?: { url: string; review: string; answer?: string }[] };

async function listShips(a: Admiral): Promise<ShipRow[]> {
  return ((await (await fetch(`${a.url}api/v1/ships`)).json()) as { ships: ShipRow[] }).ships;
}

const noHorizontalScroll = (page: Page) => page.evaluate(() => [document.documentElement, document.querySelector('main')!].every((e) => e.scrollWidth <= e.clientWidth));

/** The PR tab of a ship's view, opened from the Ships screen by touch; on the phone the conversation that opens first must sit on the tab bar. */
async function openPRTab(page: Page, a: Admiral, ship: LaunchedShip, opts: { phone: boolean }) {
  await page.goto(a.url + 'app/ships?fleet=zulu');
  await rowLink(page, ship).tap();
  await expect(page).toHaveURL(shipViewUrl(a, ship));
  if (opts.phone) {
    const panel = (await viewPanel(page).boundingBox())!;
    const tabBar = (await page.getByRole('navigation', { name: 'Main' }).boundingBox())!;
    expect(tabBar.y - (panel.y + panel.height)).toBeLessThanOrEqual(8);
  }
  await viewTab(page, 'PR').tap();
  return viewPanel(page);
}

/** Waits until the server lists the moored ship's PR as polled with the poll's answer. */
async function polledWithAnswer(a: Admiral, ship: LaunchedShip, number: number) {
  await expect
    .poll(async () => {
      await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
      const got = (await listShips(a)).find((s) => s.session === ship.session);
      const pr = got?.prs?.find((p) => p.url === `${PR_BASE}/${number}`);
      return pr === undefined || pr.review === '' ? 'not polled yet' : pr.answer ? 'answer' : 'no answer';
    }, { timeout: 30_000 })
    .toBe('answer');
}

/**
 * The PR tab of a ready ship shows the PR and its chips, Merge with the method hint and Don't follow reviews, and nothing merges before the
 * tap; one tap records one `gh pr merge <url> --squash` and the ship sails off. On the phone the page also fits with no sideways scroll.
 */
export async function mergeFromThePRTab(page: Page, a: Admiral, opts: { phone: boolean }) {
  seedMergeMethod(a, 'squash');
  ghMergeSlow(a, 2);
  prAnswer(a, 7, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  await readyOnTheServer(a, ship, 'squash');

  // Until GitHub answers, the PR's detail area stays open with a spinner and the header claims no failure.
  let release!: () => void;
  const held = new Promise<void>((r) => (release = r));
  const isDetail = (u: URL) => /\/ships\/[^/]+\/pr$/.test(u.pathname);
  await page.route(isDetail, async (route) => {
    await held;
    await route.continue();
  });
  const panel = await openPRTab(page, a, ship, opts);
  const loading = panel.getByRole('status').filter({ hasText: 'Loading from GitHub' });
  await expect(loading).toBeVisible();
  expect((await loading.boundingBox())!.height, 'loading area height').toBeGreaterThanOrEqual(100);
  await expect(panel).not.toContainText("Couldn't load this PR from GitHub.");
  release();
  await expect(page.getByRole('main')).toContainText('Ready to merge');
  await expect(panel).toContainText('#7');
  for (const chip of ['Approved', 'Checks passing', 'Mergeable']) await expectChipTone(page, panel.getByText(chip, { exact: true }), 'ok');
  // The journey seeds no PR detail, so the header falls back to the number and a muted line, and the area says it tries again.
  await expect(panel).toContainText("Couldn't load this PR from GitHub.");
  await expect(loading).toHaveCount(0);
  await expect(panel.getByRole('status').filter({ hasText: 'Trying again' })).toBeVisible();
  await page.unroute(isDetail);
  await expect(panel.getByRole('tablist')).toHaveCount(0);
  const merge = panel.getByRole('button', { name: 'Merge', exact: true });
  const unfollow = panel.getByRole('button', { name: "Don't follow reviews" });
  for (const [name, b] of [['Merge', merge], ["Don't follow reviews", unfollow]] as const) {
    await expect(b).toBeInViewport();
    expect((await b.boundingBox())!.height, `${name} height`).toBeGreaterThanOrEqual(44);
  }
  await expect(panel).toContainText('squash');
  await expect(panel.getByRole('button', { name: /Merge up to/ })).toHaveCount(0);
  await expect(panel.getByRole('button', { name: 'Dismiss' })).toHaveCount(0);
  await expect(wakeButton(page)).toHaveCount(1);
  await expect(page.getByRole('alert')).toHaveCount(0);
  if (opts.phone) expect(await noHorizontalScroll(page)).toBe(true);

  // Opening the tab, and the pushes and polls that follow, merge nothing: a merge before the tap would already fail the exact checks below.
  expect(mergeCalls(a), 'no merge before the tap').toEqual([]);

  await merge.tap();
  await expect(merge).toHaveAttribute('aria-busy', 'true');
  await expect(unfollow).toBeDisabled();
  await expect.poll(() => mergeCalls(a)).toEqual([['pr', 'merge', `${PR_BASE}/7`, '--squash']]);
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status ?? 'gone', { timeout: 30_000 }).not.toBe('moored');
  expect(mergeCalls(a), 'one merge, not one per render or poll').toHaveLength(1);
}

/**
 * On the phone the Fix PR sheet, the tallest of the five sheets, fits: no sideways scroll and the command wrapped inside it. The other sheets
 * share its `Button` and sheet layout.
 */
export async function fixSheetFitsThePhone(page: Page, a: Admiral) {
  prAnswer(a, 8, { state: 'OPEN', review: 'APPROVED', checks: 'SUCCESS', mergeable: 'CONFLICTING' });
  const ship = await launchMooredShip(a, 8);
  await expect
    .poll(async () => {
      await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
      const pr = (await listShips(a)).find((s) => s.session === ship.session)?.prs?.find((p) => p.url === `${PR_BASE}/8`);
      return pr?.review ?? '';
    }, { timeout: 30_000 })
    .not.toBe('');
  const command = `${RESUME} feedback round 1 on ${PR_BASE}/8`;

  const panel = await openPRTab(page, a, ship, { phone: true });
  const fix = panel.locator('.fix-bar').getByRole('button', { name: 'Fix PR', exact: true });
  await expect(fix).toBeVisible();
  expect(await noHorizontalScroll(page)).toBe(true);
  await fix.tap();
  const dialog = page.getByRole('dialog', { name: 'Fix PR' });
  await expect(dialog).toContainText(command);
  const box = (await dialog.getByText(command).boundingBox())!;
  const frame = (await dialog.boundingBox())!;
  expect(box.x + box.width, 'the command stays inside the sheet').toBeLessThanOrEqual(frame.x + frame.width + 1);
  expect(await noHorizontalScroll(page)).toBe(true);
}

/**
 * A moored ship whose PR got a waking review shows the feedback bar in the PR tab. Answer review opens a sheet with the exact command, and
 * the confirm relaunches the ship with that command as its prompt.
 */
export async function answerReviewFromThePRTab(page: Page, a: Admiral) {
  prAnswer(a, 7, { state: 'OPEN', review: 'CHANGES_REQUESTED', checks: 'SUCCESS', mergeable: 'MERGEABLE' });
  const ship = await launchMooredShip(a, 7);
  prReview(a, 7, { author: 'alice', body: 'please rename the helper', state: 'CHANGES_REQUESTED' });
  await polledWithAnswer(a, ship, 7);
  const command = `${RESUME} feedback round 1 on ${PR_BASE}/7`;

  const panel = await openPRTab(page, a, ship, { phone: false });
  const bar: Locator = panel.locator('.feedback-bar');
  await expect(bar).toHaveText(/^New feedback since the ship's last round: changes requested, 1 new item\./);
  const answer = bar.getByRole('button', { name: 'Answer review', exact: true });
  expect((await answer.boundingBox())!.height, 'Answer review height').toBeGreaterThanOrEqual(44);
  await expect(panel.getByRole('button', { name: 'Merge', exact: true })).toHaveCount(0);

  readyToWake(a, ship);
  await answer.tap();
  const dialog = page.getByRole('dialog', { name: 'Answer review' });
  await expect(dialog).toContainText(command);
  const box = (await dialog.getByText(command).boundingBox())!;
  const frame = (await dialog.boundingBox())!;
  expect(box.x + box.width, 'the command stays inside the sheet').toBeLessThanOrEqual(frame.x + frame.width + 1);

  const sent = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === `/api/v1/ships/${ship.session}/answer-review`);
  await dialog.getByRole('button', { name: 'Answer review', exact: true }).tap();
  const res = await sent;
  expect(JSON.parse(res.request().postData() ?? 'null')).toEqual({ pr: `${PR_BASE}/7` });
  expect(res.status()).toBe(201);
  expect(await wokenPrompt(a, ship)).toBe(command);
  await expect.poll(async () => (await listShips(a)).find((s) => s.session === ship.session)?.status).not.toBe('moored');
  await expect(page.getByRole('alert')).toHaveCount(0);
}

/** A moored ship's PR that is behind a strict base says so on the PR tab; one tap on Update branch records one `gh pr update-branch <url>`, the merge token is accepted, and the tab stops showing behind. */
export async function updateBranchFromThePRTab(page: Page, a: Admiral, opts: { phone: boolean }) {
  prBehindStrictBase(a, 7);
  const ship = await launchMooredShip(a, 7);
  await expect
    .poll(async () => {
      await fetch(`${a.url}api/v1/moored/refresh`, { method: 'POST' });
      return (await listShips(a)).find((s) => s.session === ship.session)?.prs?.find((p) => p.url === `${PR_BASE}/7`)?.review ?? '';
    }, { timeout: 30_000 })
    .not.toBe('');

  const panel = await openPRTab(page, a, ship, opts);
  await expect(panel).toContainText('Behind main');
  await expect(panel).toContainText('main requires branches to be up to date before they merge');
  await expect(panel.getByRole('button', { name: 'Merge', exact: true })).toHaveCount(0);
  const update = panel.getByRole('button', { name: 'Update branch', exact: true });
  await expect(update).toBeInViewport();
  expect((await update.boundingBox())!.height, 'Update branch height').toBeGreaterThanOrEqual(44);
  expect(updateBranchCalls(a), 'no update before the tap').toEqual([]);

  await update.tap();
  await expect.poll(() => updateBranchCalls(a)).toEqual([['pr', 'update-branch', `${PR_BASE}/7`]]);
  await expect(page.getByRole('alert')).toHaveCount(0);
  await expect(panel).not.toContainText('Behind main');
  await expect(update).toHaveCount(0);
  expect(updateBranchCalls(a), 'one update, not one per render or poll').toHaveLength(1);
}
