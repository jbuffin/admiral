import type { Locator, Page } from '@playwright/test';
import {
  animationsOn,
  boarded,
  expect,
  gesture,
  hex,
  header,
  isTouch,
  needsYouIn,
  notes,
  otherBrowser,
  placedAt,
  sameSpot,
  ships,
  soundToggle,
  spyOnPage,
  test,
  underwayIn,
  water,
} from './sea';
import { TouchPointer, centerOf, dialog, dock, sidebar, touchSetup } from './world';

// Ships in open water (u10 S2-S5; S1 is web/src/placement.test.ts). The names
// below are the contract the ships layer is built to; the prototype supplies
// the copy (designs/prototype/index.html:1012-1025, 1103-1110).
//
//   every ship in open water is a button inside main "Open water", named
//     "Board <name>, <Harness> in <Fleet>, <status>" with <status> one of
//     "underway", "needs you", "anchored, exited <code>" (or "anchored, killed (<SIGNAL>)"), "sailing off"
//   its plaque is text inside the ship: the name, then "<repo> · <skill>" (just "<repo>" with no skill),
//     "<repo> · exited <code>" when anchored; a ship that needs you shows the text "!"
//   the header (banner) says "N at sea" (underway and needs-you ships) and, when any ship needs you,
//     a button "N needs you" that boards the needs-you ship with the earliest launch
//   the sound toggle is a button in the header: text "Sound on" with the name "Mute chime",
//     or text "Muted" with the name "Turn chime on"; turning sound on plays the chime
//   the chime is WebAudio, the prototype's recipe: three oscillators started, one per note
//   boarding goes through board(session); until the bridge lands, board() pushes the session onto
//     window.__admiral.boarded when window.__admiral exists (the tests create it, nothing else does)
//   a ship that finishes (exit 0) shows a check, sails off and is gone before ~3 s
//
// Drags are dispatched pointer events (TouchPointer, world.ts); taps are real touch taps.

const BLUE = '#2E7DD7';
const ORANGE = '#E0762B';

const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

async function shipNamed(page: Page, re: RegExp) {
  const ship = ships(page, re);
  await expect(ship).toBeVisible();
  const name = /^Board (.+?), /.exec((await ship.getAttribute('aria-label')) ?? '')?.[1];
  expect(name, 'the ship button carries its accessible name').toBeTruthy();
  return { ship, name: name! };
}

test.describe('S2: a launched ship appears in open water', () => {
  test('launching through the dock puts a ship at sea with the fleet stripe, flag and plaque; another browser sees it in the same spot', async ({ page, browser, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE }, home: { name: 'Home', color: ORANGE } });
    s.world.checkout('alpha');
    s.world.skill('work', 'claude', 'commodore');
    await admiral.start();
    await s.world.guardData();
    await s.world.seedOrder('Fix the flaky test\nIt fails on CI only.');
    await page.goto(admiral.url);

    await press(sidebar(page).getByText('Fix the flaky test', { exact: true }), touch);
    await press(dock(page, 'Work', 'Claude'), touch);
    const d = dialog(page);
    await expect(d).toBeVisible();
    await d.getByRole('combobox', { name: 'Directory' }).fill('alp');
    await press(page.getByRole('option', { name: /^alpha/ }), touch);
    await press(d.getByRole('button', { name: 'Hoist sails' }), touch);
    await expect(d).toHaveCount(0);

    const { ship, name } = await shipNamed(page, underwayIn('Work'));
    await expect(ship.getByText(name, { exact: true })).toBeVisible();
    await expect(ship.getByText('alpha · commodore', { exact: true })).toBeVisible();
    await expect(header(page).getByText('1 at sea', { exact: true })).toBeVisible();
    // The fleet's stripe: something in the ship is painted the fleet color. The flag carries the harness logo.
    await expect
      .poll(() => ship.evaluate((el, rgb) => [...el.querySelectorAll('*')].some((n) => getComputedStyle(n).fill === rgb), hex(BLUE)))
      .toBe(true);
    await expect(ship.locator('use[href="#logo-claude"]')).not.toHaveCount(0);

    // A second browser sees the same ship at the same spot as the server has it.
    const here = await s.ships().then((l) => l[0]);
    await expect.poll(async () => (await s.ship(here.session))?.position).not.toBeNull();
    const spot = (await s.ship(here.session))!.position!;
    await expect.poll(async () => sameSpot(await placedAt(ship, page), spot)).toBe(true);

    const other = await otherBrowser(browser, info);
    await other.page.goto(admiral.url);
    const there = ships(other.page, underwayIn('Work'));
    await expect(there).toBeVisible();
    await expect.poll(async () => sameSpot(await placedAt(there, other.page), spot)).toBe(true);
    await expect(header(other.page).getByText('1 at sea', { exact: true })).toBeVisible();
    await other.context.close();
  });
});

test.describe('S2: placement on screen', () => {
  test('four ships of one fleet sit inside the sea and clear of each other and of every dock', async ({ page, admiral, sea }, info) => {
    test.skip(info.project.name !== 'ipad-portrait'); // the narrowest layout, where the sea is shortest of width
    const s = sea({ work: { name: 'Work', color: BLUE }, home: { name: 'Home', color: ORANGE } });
    await admiral.start();
    for (const repo of ['alpha', 'bravo', 'charlie', 'delta']) await s.launch('work', repo);
    await page.goto(admiral.url);
    await expect(ships(page, underwayIn('Work'))).toHaveCount(4);
    await expect.poll(async () => (await s.ships()).every((x) => x.position)).toBe(true);
    await expect(header(page).getByText('4 at sea', { exact: true })).toBeVisible();
    await page.waitForTimeout(2800); // past the sail-in, so the boxes are where they rest

    const boxes = await page.evaluate(() => {
      const r = (el: Element) => {
        const b = el.getBoundingClientRect();
        return { left: b.left, top: b.top, right: b.right, bottom: b.bottom };
      };
      return {
        sea: r(document.querySelector('.sea')!),
        ships: [...document.querySelectorAll('.ship-slot')].map(r),
        docks: [...document.querySelectorAll('[data-dock]')].map(r),
      };
    });
    expect(boxes.ships).toHaveLength(4);
    const apart = (a: typeof boxes.sea, b: typeof boxes.sea) => a.right <= b.left + 0.5 || b.right <= a.left + 0.5 || a.bottom <= b.top + 0.5 || b.bottom <= a.top + 0.5;
    boxes.ships.forEach((b, i) => {
      expect(b.left, `ship ${i} left of the sea`).toBeGreaterThanOrEqual(boxes.sea.left - 0.5);
      expect(b.right, `ship ${i} right of the sea`).toBeLessThanOrEqual(boxes.sea.right + 0.5);
      expect(b.top, `ship ${i} above the sea`).toBeGreaterThanOrEqual(boxes.sea.top - 0.5);
      expect(b.bottom, `ship ${i} below the sea`).toBeLessThanOrEqual(boxes.sea.bottom + 0.5);
      boxes.docks.forEach((d, k) => expect(apart(b, d), `ship ${i} over dock ${k}`).toBe(true));
      boxes.ships.slice(i + 1).forEach((o, k) => expect(apart(b, o), `ships ${i} and ${i + 1 + k} overlap`).toBe(true));
    });
  });
});

test.describe('S3: a ship that needs you', () => {
  test.beforeEach(({}, info) => test.skip(info.project.name !== 'ipad-landscape'));

  test('shows the "!" and the header within 2 s, chimes once unless muted, boards from the header, and the mute is remembered', async ({ page, admiral, sea }) => {
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const one = await s.launch('work', 'alpha');
    await spyOnPage(page);
    await page.goto(admiral.url);

    const { ship } = await shipNamed(page, underwayIn('Work'));
    await expect(header(page).getByText('1 at sea', { exact: true })).toBeVisible();
    await expect(ship.getByText('!', { exact: true })).toHaveCount(0);
    await gesture(page, true); // arms audio, as the first tap on an iPad does
    expect(await notes(page)).toBe(0);

    s.setState(one.session, 'needs_you');
    await expect(ships(page, needsYouIn('Work'))).toBeVisible({ timeout: 3000 });
    await expect(ships(page, needsYouIn('Work')).getByText('!', { exact: true })).toBeVisible();
    const pill = header(page).getByRole('button', { name: '1 needs you' });
    await expect(pill).toBeVisible();
    await expect.poll(() => notes(page)).toBe(3); // one chime: the prototype's three notes
    await page.waitForTimeout(800);
    expect(await notes(page)).toBe(3);

    // Tapping "1 needs you" boards that ship, through board(session).
    await pill.tap();
    await expect.poll(() => boarded(page)).toEqual([one.session]);

    // Muted: no chime for the next ship that needs you.
    await expect(header(page).getByText('Sound on', { exact: true })).toBeVisible();
    await soundToggle(page).tap();
    await expect(header(page).getByText('Muted', { exact: true })).toBeVisible();
    await expect(soundToggle(page)).toHaveAccessibleName('Turn chime on');
    s.setState(one.session, 'working');
    await expect(ships(page, underwayIn('Work'))).toBeVisible({ timeout: 3000 });
    await expect(header(page).getByRole('button', { name: /needs you$/ })).toHaveCount(0);
    s.setState(one.session, 'needs_you');
    await expect(ships(page, needsYouIn('Work'))).toBeVisible({ timeout: 3000 });
    await page.waitForTimeout(800);
    expect(await notes(page)).toBe(3);

    // The mute survives a reload; turning sound back on plays the chime.
    await page.reload();
    await expect(header(page).getByText('Muted', { exact: true })).toBeVisible();
    await expect(ships(page, needsYouIn('Work'))).toBeVisible();
    expect(await notes(page)).toBe(0);
    await soundToggle(page).tap();
    await expect(header(page).getByText('Sound on', { exact: true })).toBeVisible();
    await expect.poll(() => notes(page)).toBe(3);
  });

  test('"N needs you" boards the needs-you ship that launched first', async ({ page, admiral, sea }) => {
    const s = sea({ work: { name: 'Work', color: BLUE }, home: { name: 'Home', color: ORANGE } });
    await admiral.start();
    const earlier = await s.launch('work', 'alpha');
    await page.waitForTimeout(1200); // `launched` is whole seconds
    const later = await s.launch('home', 'beta');
    await spyOnPage(page);
    await page.goto(admiral.url);
    await expect(ships(page)).toHaveCount(2);

    // The later ship asks first, so the order of asking is not the order of launch.
    s.setState(later.session, 'needs_you');
    await expect(ships(page, needsYouIn('Home'))).toBeVisible({ timeout: 3000 });
    s.setState(earlier.session, 'needs_you');
    await expect(ships(page, needsYouIn('Work'))).toBeVisible({ timeout: 3000 });
    await header(page).getByRole('button', { name: '2 needs you' }).tap();
    await expect.poll(() => boarded(page)).toEqual([earlier.session]);
  });

  test('a ship that already needs you when the page loads plays no chime', async ({ page, admiral, sea }) => {
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const one = await s.launch('work', 'alpha');
    s.setState(one.session, 'needs_you');
    await expect.poll(async () => (await s.ship(one.session))?.status).toBe('needs_you');

    // The first `ships` message is held back until a tap has armed audio, so a
    // chime that fired on it would be heard.
    const held: (() => void)[] = [];
    let released = false;
    await page.routeWebSocket(/\/ws(\?|$)/, (ws) => {
      const server = ws.connectToServer();
      ws.onMessage((m) => server.send(m));
      server.onMessage((m) => {
        if (!released && typeof m === 'string' && /"type"\s*:\s*"ships"/.test(m)) held.push(() => ws.send(m));
        else ws.send(m);
      });
    });
    await spyOnPage(page);
    await page.goto(admiral.url);
    await expect(header(page).getByText('Sound on', { exact: true })).toBeVisible();
    await gesture(page, true);
    released = true;
    held.forEach((send) => send());

    await expect(ships(page, needsYouIn('Work'))).toBeVisible();
    await expect(header(page).getByRole('button', { name: '1 needs you' })).toBeVisible();
    await page.waitForTimeout(1200);
    expect(await notes(page)).toBe(0);
  });
});

test.describe('S4: dragging and boarding ships', () => {
  test.beforeEach(({}, info) => test.skip(info.project.name !== 'ipad-landscape'));

  test('a dragged ship stays where it was dropped on reload and in another browser; a tap boards it, a drag never does', async ({ page, browser, admiral, sea }, info) => {
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const one = await s.launch('work', 'alpha');
    await spyOnPage(page);
    await touchSetup(page);
    await page.goto(admiral.url);
    const { ship } = await shipNamed(page, underwayIn('Work'));
    await expect.poll(async () => (await s.ship(one.session))?.position).not.toBeNull();
    const from = (await s.ship(one.session))!.position!;
    await expect.poll(async () => sameSpot(await placedAt(ship, page), from)).toBe(true);

    // Drag it 240 px right and 90 px down.
    const start = await centerOf(ship);
    const finger = await TouchPointer.on(ship);
    await finger.down(start);
    await finger.moveTo({ x: start.x + 240, y: start.y + 90 }, 10);
    await finger.up();
    const moved = await water(page).boundingBox();
    expect(moved).not.toBeNull();
    await expect
      .poll(async () => {
        const p = (await s.ship(one.session))!.position!;
        return Math.abs((p.x - from.x) * moved!.width - 240) < 14 && Math.abs((p.y - from.y) * moved!.height - 90) < 14;
      })
      .toBe(true);
    expect(await boarded(page)).toEqual([]); // a drag never boards
    const dropped = (await s.ship(one.session))!.position!;

    // Still there after a reload, and in a second browser.
    await page.reload();
    const again = ships(page, underwayIn('Work'));
    await expect(again).toBeVisible();
    await expect.poll(async () => sameSpot(await placedAt(again, page), dropped)).toBe(true);
    const other = await otherBrowser(browser, info);
    await other.page.goto(admiral.url);
    const there = ships(other.page, underwayIn('Work'));
    await expect(there).toBeVisible();
    await expect.poll(async () => sameSpot(await placedAt(there, other.page), dropped)).toBe(true);
    await other.context.close();

    // A tap boards it once; a press that moves under the 8 px threshold is still a tap.
    await spyOnPage(page);
    await page.reload();
    await expect(again).toBeVisible();
    const c = await centerOf(again);
    await page.touchscreen.tap(c.x, c.y);
    await expect.poll(() => boarded(page)).toEqual([one.session]);
    await page.waitForTimeout(400);
    expect(await boarded(page)).toEqual([one.session]);

    // A drag past the edge of open water is clamped: the server takes it, and the ship stays in the water.
    const edge = await TouchPointer.on(again);
    const at = await centerOf(again);
    const box = (await water(page).boundingBox())!;
    await edge.down(at);
    await edge.moveTo({ x: box.x + box.width + 300, y: box.y + box.height + 300 }, 12);
    await edge.up();
    await expect
      .poll(async () => {
        const p = (await s.ship(one.session))!.position!;
        return p.x > dropped.x && p.y > dropped.y && p.x <= 1 && p.y <= 1;
      })
      .toBe(true);
    expect(await boarded(page)).toEqual([one.session]);
  });
});

test.describe('S5: a ship that finishes, and motion', () => {
  test('exit 0 shows the check and the ship goes; exit 3 anchors it, still, with "exited 3" on its plaque', async ({ page, admiral, sea }, info) => {
    test.skip(info.project.name !== 'desktop');
    const s = sea({
      zero: { name: 'Zero', color: BLUE, cue: ['sleep 3', 'exit 0'] },
      three: { name: 'Three', color: ORANGE, cue: ['sleep 3', 'exit 3'] },
    });
    await admiral.start();
    await page.goto(admiral.url);
    // Their cues end in an exit after 3 s, so wait for the recording, not for the cues.
    await Promise.all([s.launch('zero', 'cleanrepo', 'recorded'), s.launch('three', 'brokenrepo', 'recorded')]);

    // Exit 0: the check, within the animation's 2.5 s, and then the ship is gone.
    await expect(ships(page, /^Board .+, Claude in Zero, sailing off$/)).toBeVisible({ timeout: 8000 });
    await expect(ships(page, /in Zero, /)).toHaveCount(0, { timeout: 6000 });

    // Exit 3: it stays, with the code on its plaque, and holds still.
    const anchored = ships(page, /^Board .+, Claude in Three, anchored, exited 3$/);
    await expect(anchored).toBeVisible();
    await expect(anchored.getByText('brokenrepo · exited 3', { exact: true })).toBeVisible();
    await expect.poll(() => animationsOn(anchored), { timeout: 6000 }).toBe(0);
    const before = await anchored.boundingBox();
    await page.waitForTimeout(1500);
    expect(await anchored.boundingBox()).toEqual(before);
    await expect(anchored).toBeVisible();
    await expect(header(page).getByText('0 at sea', { exact: true })).toBeVisible();
  });

  test('with reduced motion nothing bobs, shakes, sails or ripples, and the header pill stops pulsing', async ({ page, admiral, sea }, info) => {
    test.skip(info.project.name !== 'desktop');
    const s = sea({ work: { name: 'Work', color: BLUE }, ask: { name: 'Ask', color: ORANGE, cue: ['hook needs_you'] } });
    await admiral.start();
    await page.goto(admiral.url);
    await s.launch('work', 'alpha');
    await s.launch('ask', 'beta');
    const calm = ships(page, underwayIn('Work'));
    const shaking = ships(page, needsYouIn('Ask'));
    await expect(calm).toBeVisible();
    await expect(shaking).toBeVisible();
    const pill = header(page).getByRole('button', { name: '1 needs you' });
    await expect(pill).toBeVisible();

    // The control: with motion allowed, there is motion to turn off.
    expect(await animationsOn(calm)).toBeGreaterThan(0);
    expect(await animationsOn(shaking)).toBeGreaterThan(0);
    expect(await animationsOn(header(page))).toBeGreaterThan(0);

    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect.poll(() => animationsOn(calm)).toBe(0);
    await expect.poll(() => animationsOn(shaking)).toBe(0);
    await expect.poll(() => animationsOn(header(page))).toBe(0);

    // A ship launched now does not sail in, either.
    await s.launch('work', 'gamma');
    const newest = ships(page, underwayIn('Work')).nth(1);
    await expect(newest).toBeVisible();
    expect(await animationsOn(newest)).toBe(0);
  });
});
