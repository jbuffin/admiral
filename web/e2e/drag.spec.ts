import type { Page } from '@playwright/test';
import { type Admiral, expect, test } from './fixtures';
import { TouchPointer, World, centerOf, dialog, dock, marinas, selectedText, sidebar, touchSetup } from './world';

// Drag and tap-to-select (u9 S1-S3). The names below are the contract the
// drag code is built to; the prototype supplies the copy
// (designs/prototype/index.html:963, 985, 1117, 1129).
//
//   a card is found by its title text inside complementary "Orders"
//   dock buttons keep u3's names ("<Harness> dock, <Fleet>"); their status line reads
//     "All in port" at rest, "Drop here" while a card is dragged or selected,
//     "Let go to launch" for the dock under the pointer
//   each marina shows a pill: "Drop on a dock" while dragging, "Tap a dock to launch" while a card is selected
//   the tilted copy that follows the pointer shows the title and the hint
//     "Drop on a dock" / "Let go to give launch orders"
//   releasing on a dock opens dialog "Launch orders" with the text "<Fleet> · <Harness> dock"
//
// A drag is driven with the mouse on desktop and with dispatched pointer events
// on touch (see TouchPointer in world.ts); Playwright's touchscreen can only tap.
// A vertical swipe cannot scroll under synthetic events, so these tests check
// that a vertical move picks nothing up; that the sidebar then scrolls is left
// to the steward's check on a device.

const order = 'Fix the flaky test\nIt fails on CI only.';
const title = 'Fix the flaky test';
const DOCKS = 4; // Work: Claude, Codex. Home: Claude, Codex.

async function harbor(page: Page, admiral: Admiral, touch: boolean) {
  const w = new World(admiral);
  w.writeConfig();
  await admiral.start();
  await w.guardData();
  await w.seedOrder(order);
  if (touch) await touchSetup(page);
  await page.goto(admiral.url);
  const card = sidebar(page).getByText(title, { exact: true });
  await expect(card).toBeVisible();
  await expect(dock(page, 'Home', 'Codex')).toBeVisible();
  return { w, card, start: await centerOf(card) };
}

const dropHere = (page: Page) => page.getByText('Drop here', { exact: true });
const letGo = (page: Page) => page.getByText('Let go to launch', { exact: true });
const pills = (page: Page, text: string) => marinas(page).filter({ hasText: text });

async function expectArmed(page: Page, pill: string) {
  await expect(dropHere(page).or(letGo(page))).toHaveCount(DOCKS);
  await expect(pills(page, pill)).toHaveCount(2);
}

async function expectAtRest(page: Page) {
  await expect(dropHere(page)).toHaveCount(0);
  await expect(letGo(page)).toHaveCount(0);
  await expect(pills(page, 'Drop on a dock')).toHaveCount(0);
  await expect(pills(page, 'Tap a dock to launch')).toHaveCount(0);
  await expect(page.getByText('All in port')).toHaveCount(DOCKS);
}

test.describe('S1: touch drag', () => {
  test.beforeEach(({}, info) => test.skip(info.project.name !== 'ipad-landscape'));

  test('a horizontal drag arms every dock, the dock under the finger is gold, release opens launch orders for it', async ({ page, admiral }) => {
    const { card, start } = await harbor(page, admiral, true);
    const finger = await TouchPointer.on(card);

    await finger.down(start);
    // Under the 8 px threshold nothing has been picked up.
    await finger.moveTo({ x: start.x + 5, y: start.y + 1 }, 2);
    await expectAtRest(page);

    // Across it, mainly horizontal: picked up, with the copy following.
    await finger.moveTo({ x: start.x + 60, y: start.y + 4 }, 4);
    await expectArmed(page, 'Drop on a dock');
    await expect(page.getByText('Drop on a dock', { exact: true }).first()).toBeVisible();

    // Over Home's Codex dock: a fleet and a harness that are not the first ones.
    const target = dock(page, 'Home', 'Codex');
    await finger.moveTo(await centerOf(target), 12);
    await expect(letGo(page)).toHaveCount(1);
    await expect(target.getByText('Let go to launch')).toBeVisible();
    await expect(dropHere(page)).toHaveCount(DOCKS - 1);
    await expect(page.getByText('Let go to give launch orders')).toBeVisible();
    expect(await selectedText(page)).toBe('');

    await finger.up();
    const launch = dialog(page);
    await expect(launch).toBeVisible();
    await expect(launch.getByText('Home · Codex dock')).toBeVisible();
    await expect(launch.getByText(title)).toBeVisible();
    await expect(dropHere(page)).toHaveCount(0);
    expect(await selectedText(page)).toBe('');
  });

  test('a vertical swipe on a card picks nothing up and selects nothing', async ({ page, admiral }) => {
    const { card, start } = await harbor(page, admiral, true);

    const swipe = await TouchPointer.on(card);
    await swipe.down(start);
    await swipe.moveTo({ x: start.x + 2, y: start.y + 70 }, 7);
    await expectAtRest(page);
    await swipe.up();
    await expectAtRest(page);
    await expect(dialog(page)).toHaveCount(0);

    // The browser cancels the pointer when it takes the touch over to scroll.
    const scroll = await TouchPointer.on(card);
    await scroll.down(start);
    await scroll.moveTo({ x: start.x, y: start.y + 70 }, 7);
    await scroll.cancel();
    await expectAtRest(page);
    await expect(dialog(page)).toHaveCount(0);
    await expect(card).toBeVisible();
  });

  test('releasing off any dock, or a pointercancel mid-drag, opens nothing and leaves the harbor at rest', async ({ page, admiral }) => {
    const { card, start } = await harbor(page, admiral, true);
    const water = await centerOf(page.getByRole('main', { name: 'Open water' }));

    const off = await TouchPointer.on(card);
    await off.down(start);
    await off.moveTo({ x: start.x + 60, y: start.y }, 4);
    await expectArmed(page, 'Drop on a dock');
    await off.moveTo(water, 10);
    await expect(letGo(page)).toHaveCount(0);
    await off.up();
    await expectAtRest(page);
    await expect(dialog(page)).toHaveCount(0);

    const cancelled = await TouchPointer.on(card);
    await cancelled.down(start);
    await cancelled.moveTo({ x: start.x + 60, y: start.y }, 4);
    await cancelled.moveTo(await centerOf(dock(page, 'Work', 'Claude')), 10);
    await expect(letGo(page)).toHaveCount(1);
    await cancelled.cancel();
    await expectAtRest(page);
    await expect(dialog(page)).toHaveCount(0);
    expect(await selectedText(page)).toBe('');

    // The order is still there to be dragged again.
    await expect(card).toBeVisible();
  });
});

test.describe('S2: mouse drag', () => {
  test.beforeEach(({}, info) => test.skip(info.project.name !== 'desktop'));

  test('a drag in any direction picks the card up; release on a dock opens launch orders, off a dock opens nothing', async ({ page, admiral }) => {
    const { card, start } = await harbor(page, admiral, false);

    // The press starts on the title text, where a drag would select it.
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await page.mouse.move(start.x, start.y + 3, { steps: 2 });
    await expectAtRest(page);
    // Vertical first: the mouse picks up in any direction (only touch needs a horizontal move).
    await page.mouse.move(start.x + 2, start.y + 40, { steps: 5 });
    await expectArmed(page, 'Drop on a dock');
    const target = dock(page, 'Work', 'Claude');
    const to = await centerOf(target);
    await page.mouse.move(to.x, to.y, { steps: 12 });
    await expect(letGo(page)).toHaveCount(1);
    await expect(target.getByText('Let go to launch')).toBeVisible();
    await expect(page.getByText('Let go to give launch orders')).toBeVisible();
    expect(await selectedText(page)).toBe('');
    await page.mouse.up();

    const launch = dialog(page);
    await expect(launch).toBeVisible();
    await expect(launch.getByText('Work · Claude dock')).toBeVisible();
    await launch.getByRole('button', { name: 'Stay in port' }).click();
    await expect(launch).toHaveCount(0);

    // Off any dock: nothing.
    const water = await centerOf(page.getByRole('main', { name: 'Open water' }));
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await page.mouse.move(start.x + 80, start.y + 10, { steps: 6 });
    await expectArmed(page, 'Drop on a dock');
    await page.mouse.move(water.x, water.y, { steps: 10 });
    await page.mouse.up();
    await expectAtRest(page);
    await expect(dialog(page)).toHaveCount(0);
    expect(await selectedText(page)).toBe('');
    await expect(card).toBeVisible();
  });

  test('with reduced motion the drag still works', async ({ page, admiral }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const { start } = await harbor(page, admiral, false);

    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await page.mouse.move(start.x + 50, start.y, { steps: 5 });
    await expectArmed(page, 'Drop on a dock');
    const to = await centerOf(dock(page, 'Home', 'Claude'));
    await page.mouse.move(to.x, to.y, { steps: 10 });
    await page.mouse.up();
    await expect(dialog(page).getByText('Home · Claude dock')).toBeVisible();
  });
});

test.describe('S3: tap to select', () => {
  test.beforeEach(({}, info) => test.skip(info.project.name !== 'ipad-landscape'));

  test('a tap selects the card and arms the docks, a tap on a dock opens launch orders, a second tap on the card deselects', async ({ page, admiral }) => {
    const { card } = await harbor(page, admiral, false);

    // A dock tapped with nothing selected opens nothing.
    await dock(page, 'Work', 'Claude').tap();
    await expect(dialog(page)).toHaveCount(0);

    await card.tap();
    await expect(dropHere(page)).toHaveCount(DOCKS);
    await expect(pills(page, 'Tap a dock to launch')).toHaveCount(2);
    await expect(pills(page, 'Drop on a dock')).toHaveCount(0);

    await card.tap();
    await expectAtRest(page);

    await card.tap();
    await expect(dropHere(page)).toHaveCount(DOCKS);
    await dock(page, 'Home', 'Claude').tap();
    const launch = dialog(page);
    await expect(launch).toBeVisible();
    await expect(launch.getByText('Home · Claude dock')).toBeVisible();
    await expect(launch.getByText(title)).toBeVisible();
    await launch.getByRole('button', { name: 'Stay in port' }).tap();
    await expect(launch).toHaveCount(0);
  });
});
