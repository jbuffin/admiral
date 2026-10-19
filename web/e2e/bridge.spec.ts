import type { Locator, Page } from '@playwright/test';
import { launchFromOrder, orderTitle, paneSize, paneWrite, reachable, release, screenText, stdinOf } from './bridge';
import { expect, header, isTouch, needsYouIn, ships, spyOnPage, test, underwayIn } from './sea';

// The bridge (u11 S5-S7). The names below are the contract the bridge UI is
// built to; the prototype supplies the copy and labels
// (designs/prototype/index.html:340-410, 1044-1067).
//
//   tapping a ship opens the bridge over open water (u10's board(session)):
//     a navigation "Session flotilla" with the text "Flotilla · 1" and one button, the lead row:
//       the ship's name, then "<skill> · lead" ("review · lead" here)
//     a region "Attached terminal" holding
//       the ship's name, then "<Harness> · <Fleet> · <skill> · <repo>" ("Claude · Work · review · alpha")
//         and under it the order's first line (the ship's `title`)
//       a status pill: "Underway", "Needs you", "Anchored · exit N" or "Sailing off"
//       a button "Close terminal and return to open water"
//       the terminal (xterm.js: a `.xterm` element)
//       a textbox "Input to <name>" (placeholder "type here and press Enter")
//       buttons "Send Enter", "Send Esc", "Send Tab", "Send Up", "Send Down", "Send Ctrl-C"
//       no Approve or Deny buttons: a ship that needs you is answered in its terminal (engineer, 2026-10-18)
//       the note "Keys go straight to the session"
//     an anchored ship has instead a button "Dismiss", the note "The harness exited. This is its last screen.",
//       and no textbox and no key buttons
//   Enter at the prompt sends the text as typed (not trimmed), then CR;
//     the key buttons send Enter \r, Esc \x1b, Tab \t, Up \x1b[A, Down \x1b[B and Ctrl-C \x03; typing into the terminal sends the keys
//   while the server is away the region shows the text "Reconnecting…", an ancestor of the terminal inside the region has
//     opacity below 1, the textbox and the key buttons are disabled, and nothing typed is queued; the header banner
//     (role alert inside the banner) says "Reconnecting…" too
//   a 401 on reconnect stops the retries and shows the text "Open the URL Admiral printed"
//   window.__admiral.screen() returns the terminal's text (see bridge.ts)
//   board(session) keeps pushing the session onto window.__admiral.boarded when that object exists (u10's tests
//     still read it), and now also opens the bridge

const BLUE = '#2E7DD7';
const NAME_OF = /^Board (.+?), /;

/** The value once it has stopped changing for half a second. */
async function settled(read: () => string) {
  let last = read();
  for (let still = 0; still < 3; ) {
    await new Promise((r) => setTimeout(r, 200));
    const now = read();
    still = now === last && now !== '' ? still + 1 : 0;
    last = now;
  }
  return last;
}

const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());

const terminal = (page: Page) => page.getByRole('region', { name: 'Attached terminal' });
const flotilla = (page: Page) => page.getByRole('navigation', { name: 'Session flotilla' });
const closeButton = (page: Page) => terminal(page).getByRole('button', { name: 'Close terminal and return to open water' });
const prompt = (page: Page, name: string) => terminal(page).getByRole('textbox', { name: `Input to ${name}` });
const keyButton = (page: Page, key: string) => terminal(page).getByRole('button', { name: `Send ${key}` });

/** Taps the ship and returns its name from its accessible name. */
async function board(page: Page, re: RegExp, touch: boolean) {
  const ship = ships(page, re);
  await expect(ship).toBeVisible();
  const name = NAME_OF.exec((await ship.getAttribute('aria-label')) ?? '')?.[1];
  expect(name, 'the ship button carries its accessible name').toBeTruthy();
  await press(ship, touch);
  await expect(terminal(page)).toBeVisible();
  return name!;
}

test.describe('S5: boarding a ship and typing on the bridge', () => {
  test('tapping a ship opens the bridge; the prompt, key buttons and typing reach the harness', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    s.world.skill('work', 'claude', 'review');
    await admiral.start();
    await s.world.guardData();
    const ship = await launchFromOrder(s, 'work', 'alpha');
    await spyOnPage(page);
    await page.goto(admiral.url);

    const name = await board(page, underwayIn('Work'), touch);

    // Header, flotilla and terminal.
    const t = terminal(page);
    await expect(t.getByText(name, { exact: true })).toBeVisible();
    await expect(t.getByText('Claude · Work · review · alpha', { exact: true })).toBeVisible();
    await expect(t.getByText(orderTitle, { exact: true })).toBeVisible();
    await expect(t.getByText('Underway', { exact: true })).toBeVisible();
    await expect(t.getByText('Keys go straight to the session', { exact: true })).toBeVisible();
    await expect(t.locator('.xterm')).toBeVisible();
    await expect(flotilla(page).getByText('Flotilla · 1', { exact: true })).toBeVisible();
    const lead = flotilla(page).getByRole('button');
    await expect(lead).toHaveCount(1);
    await expect(lead.getByText(name, { exact: true })).toBeVisible();
    await expect(lead.getByText('review · lead', { exact: true })).toBeVisible();
    await expect(prompt(page, name)).toHaveAttribute('placeholder', 'type here and press Enter');

    // Not needs-you: no Approve and no Deny.
    await expect(t.getByRole('button', { name: 'Approve · y' })).toHaveCount(0);
    await expect(t.getByRole('button', { name: 'Deny · n' })).toHaveCount(0);

    // The ❯ prompt sends the line as typed (not trimmed), then CR.
    await prompt(page, name).fill('  hi ');
    await prompt(page, name).press('Enter');
    await expect.poll(() => stdinOf(s, ship.session)).toBe('  hi \r');
    await expect(prompt(page, name)).toHaveValue('');
    // An empty line sends just CR.
    await prompt(page, name).press('Enter');
    await expect.poll(() => stdinOf(s, ship.session)).toBe('  hi \r\r');

    // The key buttons.
    let want = '  hi \r\r';
    for (const [key, bytes] of [
      ['Enter', '\r'],
      ['Esc', '\x1b'],
      ['Tab', '\t'],
      ['Up', '\x1b[A'],
      ['Down', '\x1b[B'],
      ['Ctrl-C', '\x03'],
    ] as const) {
      await press(keyButton(page, key), touch);
      want += bytes;
      await expect.poll(() => stdinOf(s, ship.session), `after Send ${key}`).toBe(want);
    }

    // Typing into the terminal works too.
    await press(t.locator('.xterm'), touch);
    await page.keyboard.type('ab');
    want += 'ab';
    await expect.poll(() => stdinOf(s, ship.session)).toBe(want);

    // A ship that needs you shows no Approve or Deny: you answer in the terminal, and the keystroke clears needs-you.
    s.setState(ship.session, 'needs_you');
    await expect(t.getByText('Needs you', { exact: true })).toBeVisible();
    await expect(t.getByRole('button', { name: /Approve|Deny/ })).toHaveCount(0);
    await press(t.getByRole('button', { name: 'Send Enter' }), touch);
    want += '\r';
    await expect.poll(() => stdinOf(s, ship.session)).toBe(want);
    await expect(t.getByText('Underway', { exact: true })).toBeVisible();

    // Closing the bridge leaves the session running; the ship is still at sea.
    await press(closeButton(page), touch);
    await expect(terminal(page)).toHaveCount(0);
    await expect(ships(page, underwayIn('Work'))).toBeVisible();
    expect((await s.ship(ship.session))?.status).toBe('underway');
  });

  test('typing at the prompt makes that browser the one that sizes the pane', async ({ page, browser, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    s.world.skill('work', 'claude', 'review');
    await admiral.start();
    await s.world.guardData();
    const ship = await launchFromOrder(s, 'work', 'alpha');
    await spyOnPage(page);
    await page.goto(admiral.url);
    const name = await board(page, underwayIn('Work'), touch);

    // A second browser, smaller, boards the same ship and sizes the pane when it joins.
    const full = page.viewportSize()!;
    const first = await settled(() => paneSize(s, ship.session));
    // (its own context: a second tab in one context would leave this page hidden, and a hidden browser can't size the pane)
    const { hasTouch, isMobile } = info.project.use;
    const ctx = await browser.newContext({ viewport: { width: full.width - 360, height: full.height - 300 }, hasTouch, isMobile });
    const other = await ctx.newPage();
    await spyOnPage(other);
    await other.goto(admiral.url);
    await board(other, underwayIn('Work'), touch);
    const small = await settled(() => paneSize(s, ship.session));
    expect(small).not.toBe(first);

    // The first browser's prompt: Enter (no pointer on the terminal) takes the size back.
    await prompt(page, name).fill('x');
    await prompt(page, name).press('Enter');
    await expect.poll(() => paneSize(s, ship.session)).not.toBe(small);
    const large = paneSize(s, ship.session);

    // And the second one's prompt takes it again.
    await prompt(other, name).fill('y');
    await prompt(other, name).press('Enter');
    await expect.poll(() => paneSize(s, ship.session)).toBe(small);
    expect(large).not.toBe(small);
    await ctx.close();
  });

  test('the bridge fits every evaluated viewport: prompt and key bar on screen, the bridge inside the screen, and a pane no taller than the box', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    s.world.skill('work', 'claude', 'review');
    await admiral.start();
    await s.world.guardData();
    const ship = await launchFromOrder(s, 'work', 'alpha');
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(admiral.url);
    const name = await board(page, underwayIn('Work'), touch);

    // 1440x900 first, then narrower and shorter windows, resized with the bridge open.
    for (const [width, height] of [[1440, 900], [1000, 700], [834, 1194], [1194, 700], [1194, 834]]) {
      await page.setViewportSize({ width, height });
      const at = `${width}x${height}`;
      await expect(keyButton(page, 'Ctrl-C'), at).toBeVisible();
      await expect
        .poll(
          async () => {
            const keys = await keyButton(page, 'Ctrl-C').boundingBox();
            const input = await prompt(page, name).boundingBox();
            const doc = await page.evaluate(() => ({
              box: (() => { const b = document.querySelector('.bridge')!.getBoundingClientRect(); return b.bottom - innerHeight; })(),
              bridge: (document.querySelector('.bridge') as HTMLElement).scrollHeight - (document.querySelector('.bridge') as HTMLElement).clientHeight,
            }));
            return keys && input && keys.y + keys.height <= height && input.y + input.height <= height && doc.box <= 0 && doc.bridge <= 0;
          },
          { message: `${at}: the prompt and key bar are on screen and the bridge holds them without scrolling`, timeout: 5000 },
        )
        .toBe(true);
      // The pane takes the box's grid: what fits under the viewport, never more.
      await expect.poll(() => Number(paneSize(s, ship.session).split('x')[1]), { message: `${at}: the pane's rows` }).toBeLessThanOrEqual(Math.floor(height / 17));
    }
  });

  test('the bridge follows open water when the orders panel collapses and expands', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    s.world.skill('work', 'claude', 'review');
    await admiral.start();
    await s.world.guardData();
    await launchFromOrder(s, 'work', 'alpha');
    for (const [width, height] of [[834, 1194], [1194, 834]]) {
      await page.setViewportSize({ width, height });
      await page.goto(admiral.url);
      await board(page, underwayIn('Work'), touch);
      const matchesMain = () =>
        page.evaluate(() => {
          const b = document.querySelector('.bridge')!.getBoundingClientRect();
          const m = document.querySelector('main')!.getBoundingClientRect();
          return Math.abs(b.left - Math.max(0, m.left)) < 1 && Math.abs(b.top - Math.max(0, m.top)) < 1;
        });
      await expect.poll(matchesMain, { message: `${width}x${height}: at first` }).toBe(true);
      const side = page.getByRole('complementary', { name: 'Orders', exact: true });
      await press(side.getByRole('button', { name: 'Collapse orders' }), touch);
      await expect(page.getByRole('complementary', { name: 'Orders, collapsed', exact: true })).toBeVisible();
      await expect.poll(matchesMain, { message: `${width}x${height}: after collapsing` }).toBe(true);
      await press(page.getByRole('button', { name: 'Expand orders' }), touch);
      await expect(side).toBeVisible();
      await expect.poll(matchesMain, { message: `${width}x${height}: after expanding` }).toBe(true);
    }
  });

  test('a ship that needs you opens its bridge from the header pill', async ({ page, admiral, sea }, info) => {
    test.skip(info.project.name !== 'ipad-landscape');
    const s = sea({ work: { name: 'Work', color: BLUE } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha');
    s.setState(ship.session, 'needs_you');
    await spyOnPage(page);
    await page.goto(admiral.url);
    await expect(ships(page, needsYouIn('Work'))).toBeVisible();
    await header(page).getByRole('button', { name: '1 needs you' }).tap();
    await expect(terminal(page)).toBeVisible();
    await expect(terminal(page).getByText('Needs you', { exact: true })).toBeVisible();
    await expect(terminal(page).getByRole('button', { name: /Approve|Deny/ })).toHaveCount(0);
  });
});

test.describe('S6: the server goes away', () => {
  test('restarting with the bridge open: Reconnecting…, dimmed, input disabled and unqueued; back, the screen is replaced and input works', async ({ page, admiral, sea }, info) => {
    test.setTimeout(60_000);
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    s.world.skill('work', 'claude', 'review');
    await admiral.start();
    await s.world.guardData();
    const ship = await launchFromOrder(s, 'work', 'alpha');
    await spyOnPage(page);
    await page.goto(admiral.url);
    const name = await board(page, underwayIn('Work'), touch);
    const t = terminal(page);
    await expect(t.getByText('Reconnecting…')).toHaveCount(0);
    paneWrite(s, ship.session, 'before-restart\r\n');
    await expect.poll(() => screenText(page)).toContain('before-restart');

    await admiral.stopServer();
    await expect(t.getByText('Reconnecting…')).toBeVisible({ timeout: 10_000 });
    await expect(header(page).getByRole('alert').getByText('Reconnecting…')).toBeVisible();
    await expect(prompt(page, name)).toBeDisabled();
    await expect(keyButton(page, 'Esc')).toBeDisabled();
    // The terminal itself takes no keys either: xterm.js's text box is switched off with stdin.
    await expect(t.locator('.xterm textarea')).toHaveAttribute('aria-hidden', 'true');
    // It keeps its last screen and dims: some ancestor of the terminal, inside the region, is translucent.
    await expect(t.locator('.xterm')).toBeVisible();
    expect(await screenText(page)).toContain('before-restart');
    expect(
      await t.locator('.xterm').evaluate((el) => {
        let min = 1;
        for (let n: Element | null = el; n && n.getAttribute('aria-label') !== 'Attached terminal'; n = n.parentElement) {
          min = Math.min(min, Number(getComputedStyle(n).opacity));
        }
        return min;
      }),
    ).toBeLessThan(1);

    // Keys pressed now go nowhere, and are not replayed later.
    await t.locator('.xterm').click({ force: true });
    await page.keyboard.type('q');
    paneWrite(s, ship.session, 'while-away\r\n');
    await admiral.resumeServer();

    await expect(t.getByText('Reconnecting…')).toHaveCount(0, { timeout: 20_000 });
    await expect(header(page).getByRole('alert')).toHaveCount(0);
    await expect(prompt(page, name)).toBeEnabled();
    await expect(t.locator('.xterm textarea')).toHaveAttribute('aria-hidden', 'false');
    await expect.poll(() => screenText(page)).toContain('while-away');
    expect(stdinOf(s, ship.session), 'nothing typed while disconnected is queued').toBe('');
    await prompt(page, name).fill('back');
    await prompt(page, name).press('Enter');
    await expect.poll(() => stdinOf(s, ship.session)).toBe('back\r');
  });

  test('a ship that anchored while the server was away reopens read-only; one that sailed off closes the bridge', async ({ page, admiral, sea }, info) => {
    test.setTimeout(90_000);
    const touch = isTouch(info);
    const s = sea({
      zero: { name: 'Zero', color: BLUE, cue: ['wait go', 'exit 0'] },
      three: { name: 'Three', color: '#E0762B', cue: ['lines 2', 'wait go', 'exit 3'] },
    });
    await admiral.start();
    const zero = await s.launch('zero', 'alpha', 'recorded');
    const three = await s.launch('three', 'beta', 'recorded');
    await spyOnPage(page);
    await page.goto(admiral.url);

    await board(page, underwayIn('Three'), touch);
    const t = terminal(page);
    await admiral.stopServer();
    await expect(t.getByText('Reconnecting…')).toBeVisible({ timeout: 10_000 });
    release(s, three.session);
    await admiral.resumeServer();
    await expect(t.getByText('Anchored · exit 3', { exact: true })).toBeVisible({ timeout: 20_000 });
    await expect(t.getByRole('button', { name: 'Dismiss' })).toBeVisible();
    await expect(t.getByRole('textbox')).toHaveCount(0);
    await expect(t.getByText('Reconnecting…')).toHaveCount(0);
    await expect.poll(() => screenText(page)).toContain('line 0002');
    await press(closeButton(page), touch);
    await expect(terminal(page)).toHaveCount(0);

    await board(page, underwayIn('Zero'), touch);
    await admiral.stopServer();
    await expect(t.getByText('Reconnecting…')).toBeVisible({ timeout: 10_000 });
    release(s, zero.session);
    await admiral.resumeServer();
    // Admiral ends a ship that sailed off when it starts; the ship is gone, so the bridge closes.
    await expect(terminal(page)).toHaveCount(0, { timeout: 20_000 });
    await expect.poll(async () => (await s.ships()).map((x) => x.session)).toEqual([three.session]);
  });

  test('a rotated token stops the retries and says to open the URL Admiral printed', async ({ page, admiral, sea }, info) => {
    test.skip(info.project.name !== 'desktop');
    test.setTimeout(90_000);
    const s = sea({ work: { name: 'Work', color: BLUE } });
    s.world.skill('work', 'claude', 'review');
    // Loopback peers need no token (u1), so the page is loaded from the Mac's own LAN address.
    await admiral.start({ lan: true });
    if (!admiral.lanUrl || !(await reachable(admiral.lanUrl))) {
      if (process.env.CI) throw new Error(`the Mac's address ${admiral.lanUrl || '(none)'} does not answer: this must run on CI`);
      test.skip(true, `no answer from ${admiral.lanUrl || 'a LAN address'} within 3s (macOS firewall dialog? VPN? no network?)`);
    }
    await launchFromOrder(s, 'work', 'alpha');
    await spyOnPage(page);
    await page.goto(`${admiral.lanUrl}?token=${admiral.token}`);
    await board(page, underwayIn('Work'), false);

    const sockets: string[] = [];
    page.on('websocket', (ws) => sockets.push(ws.url()));
    admiral.rotateToken();
    await admiral.restart();

    await expect(page.getByText('Open the URL Admiral printed').first()).toBeVisible({ timeout: 30_000 });
    // The retries stop: no WebSocket is opened for longer than the 10 s cap.
    const seen = sockets.length;
    await page.waitForTimeout(12_000);
    expect(sockets.length, 'WebSockets opened after the message appeared').toBe(seen);
    await expect(page.getByText('Open the URL Admiral printed').first()).toBeVisible();
  });
});

test.describe('S7: an anchored ship and a ship that exits with the bridge open', () => {
  test('boarding an anchored ship opens it read-only on its last screen; Dismiss removes the ship and closes the bridge', async ({ page, admiral, sea }, info) => {
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE, cue: ['lines 5', 'exit 3'] } });
    await admiral.start();
    await s.launch('work', 'alpha');
    await spyOnPage(page);
    await page.goto(admiral.url);

    const name = await board(page, /^Board .+, Claude in Work, anchored, exited 3$/, touch);
    const t = terminal(page);
    await expect(t.getByText('Anchored · exit 3', { exact: true })).toBeVisible();
    await expect(t.getByText('The harness exited. This is its last screen.', { exact: true })).toBeVisible();
    await expect.poll(() => screenText(page)).toContain('line 0005');
    await expect(prompt(page, name)).toHaveCount(0);
    await expect(t.getByRole('button', { name: /^Send / })).toHaveCount(0);
    await expect(t.getByText('Keys go straight to the session')).toHaveCount(0);

    await press(t.getByRole('button', { name: 'Dismiss' }), touch);
    await expect(terminal(page)).toHaveCount(0);
    await expect(ships(page)).toHaveCount(0);
    await expect.poll(async () => (await s.ships()).length).toBe(0);
  });

  test('exit 0 with the bridge open closes it as the ship sails off; a failure turns it read-only with Dismiss', async ({ page, admiral, sea }, info) => {
    test.setTimeout(60_000);
    const touch = isTouch(info);
    const s = sea({
      zero: { name: 'Zero', color: BLUE, cue: ['wait go', 'exit 0'] },
      three: { name: 'Three', color: '#E0762B', cue: ['lines 2', 'wait go', 'exit 3'] },
    });
    await admiral.start();
    const zero = await s.launch('zero', 'alpha', 'recorded');
    const three = await s.launch('three', 'beta', 'recorded');
    await spyOnPage(page);
    await page.goto(admiral.url);

    await board(page, underwayIn('Zero'), touch);
    release(s, zero.session);
    await expect(terminal(page)).toHaveCount(0, { timeout: 10_000 });
    await expect(ships(page, /Claude in Zero,/)).toHaveCount(0, { timeout: 10_000 });

    const name = await board(page, underwayIn('Three'), touch);
    const t = terminal(page);
    await expect(prompt(page, name)).toBeVisible();
    release(s, three.session);
    await expect(t.getByText('Anchored · exit 3', { exact: true })).toBeVisible({ timeout: 10_000 });
    await expect(prompt(page, name)).toHaveCount(0);
    await expect(t.getByRole('button', { name: 'Dismiss' })).toBeVisible();
    await expect.poll(() => screenText(page)).toContain('line 0002');
  });
});
