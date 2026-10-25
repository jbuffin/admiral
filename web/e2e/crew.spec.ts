import type { Locator, Page } from '@playwright/test';
import { closeCrew, crewOf, crewPaneWrite, launchCrew, paneWrite, release, releaseCrew, screenText } from './bridge';
import { boarded, expect, isTouch, spyOnPage, test, underwayIn, ships } from './sea';

// Crew boats on the bridge (#154 u1; #72's crew flow, the flotilla half). The names below are the contract the
// bridge UI is built to; the prototype (scenes crew-bridge, crew-attn, crew-dismiss) supplies copy and labels.
//
//   the flotilla (navigation "Session flotilla") reads "Flotilla · N", N = the ship plus its crew boats, and has one
//     button per boat after the lead row. A crew boat's button holds its name and "Teammate · working",
//     "Teammate · needs you" or "Teammate · anchored, exit N"; it carries
//       data-state="working" | "needs_you" | "anchored"   (the boat's wire status: underway shows as working)
//       aria-current="true"                                while that boat is the boarded one
//   tapping a crew row boards the boat: the bridge socket is /ws/bridge/<session>/crew/<name>, the terminal shows
//     the boat's own screen, and the head reads "<ship name> › <crew name>" with the boat's pill
//   tapping the lead row boards the ship again (head with no "›")
//   window.__admiral.boarded gets "<session>/<crew>" for a crew boat, the bare session for a ship
//   while a crew boat is boarded the head has a button "More for <crew>" (menu button, 44 px); it opens a menu with
//     the item "Close crew boat", disabled while the boat is live with the reason "Still working. Its ship's skill
//     closes it, or it anchors when its harness exits." shown, enabled once anchored. Choosing it closes the boat
//     (POST /api/ships/<session>/crew/<name>/close); the row goes and the bridge returns to the ship
//   an anchored crew boat takes no keys (xterm's text box aria-hidden), has no Dismiss, and the footer says
//     "This teammate's harness exited. Close its crew boat from ⋯."
//   a live crew boat takes keys, and a key reaching it clears its needs-you (its row goes back to working)
//   Dismiss on a ship with live crew asks first: an alertdialog "Dismiss <ship>?" with "1 crew boat is still working.",
//     a chip "<crew> · working", and the buttons "Keep it" and "Dismiss ship and crew"

const BLUE = '#2E7DD7';

const press = (l: Locator, touch: boolean) => (touch ? l.tap() : l.click());
const terminal = (page: Page) => page.getByRole('region', { name: 'Attached terminal' });
const flotilla = (page: Page) => page.getByRole('navigation', { name: 'Session flotilla' });
const crewRow = (page: Page, crew: string) => flotilla(page).getByRole('button', { name: new RegExp(`^${crew}\\b`) });
const termInput = (page: Page) => terminal(page).locator('.xterm textarea');

test.beforeEach(({ page }) => page.addInitScript(() => localStorage.setItem('admiral.sidebar.drawer', '0')));

test.describe('a crew boat on the bridge', () => {
  test('it joins the flotilla; boarding it shows its own terminal; it asks for you; closing it once anchored returns to the ship', async ({ page, admiral, sea }, info) => {
    test.setTimeout(60_000);
    const touch = isTouch(info);
    // The boat runs the ship's fake and cue: both park on `wait needs`, and only the boat's file is ever written.
    const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait needs', 'hook needs_you', 'wait end', 'exit 0'] } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha', 'recorded');
    paneWrite(s, ship.session, 'ship-screen\r\n');
    const sockets: string[] = [];
    page.on('websocket', (ws) => sockets.push(ws.url()));
    await spyOnPage(page);
    await page.goto(admiral.url);
    await press(ships(page, underwayIn('Work')), touch);
    const t = terminal(page);
    await expect(t).toBeVisible();
    await expect(flotilla(page).getByText('Flotilla · 1', { exact: true })).toBeVisible();
    await expect.poll(() => screenText(page)).toContain('ship-screen');
    await expect(t.getByRole('button', { name: /^More for / })).toHaveCount(0);

    // Fixture proof: the boat is really running and listed before any crew assertion.
    await launchCrew(s, ship.session, 'api');
    expect(await crewOf(s, ship.session)).toEqual([expect.objectContaining({ name: 'api', status: 'underway' })]);

    // It joins the flotilla while the bridge is open.
    await expect(flotilla(page).getByText('Flotilla · 2', { exact: true })).toBeVisible();
    const row = crewRow(page, 'api');
    await expect(row).toHaveText(/Teammate · working/);
    await expect(row).toHaveAttribute('data-state', 'working');

    // Boarding it: its socket, its screen, its head.
    crewPaneWrite(s, ship.session, 'api', 'crew-screen\r\n');
    await press(row, touch);
    await expect.poll(() => sockets.some((u) => u.endsWith(`/ws/bridge/${ship.session}/crew/api`))).toBe(true);
    await expect.poll(() => screenText(page)).toContain('crew-screen');
    expect(await screenText(page)).not.toContain('ship-screen');
    await expect(t.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    await expect(row).toHaveAttribute('aria-current', 'true');
    expect(await boarded(page)).toContain(`${ship.session}/api`);
    await expect(t.getByText('Underway', { exact: true })).toBeVisible();

    // ⋯ is there while a crew boat is boarded; Close crew boat is disabled while it is live, with its reason.
    const more = t.getByRole('button', { name: 'More for api' });
    await press(more, touch);
    await expect(page.getByRole('menuitem', { name: 'Close crew boat' })).toBeDisabled();
    await expect(page.getByText("Still working. Its ship's skill closes it, or it anchors when its harness exits.")).toBeVisible();
    await page.keyboard.press('Escape');

    // It needs you: its row says so; the ship's own pill is not the boat's.
    releaseCrew(s, ship.session, 'api', 'needs');
    await expect(row).toHaveAttribute('data-state', 'needs_you');
    await expect(row).toHaveText(/Teammate · needs you/);
    await expect(t.getByText('Needs you', { exact: true })).toBeVisible();
    // A key typed on the crew bridge reaches the boat and clears it.
    await termInput(page).focus();
    await page.keyboard.type('y');
    await expect(row).toHaveAttribute('data-state', 'working');

    // The lead row boards the ship again.
    await press(flotilla(page).getByRole('button').first(), touch);
    await expect.poll(() => sockets.filter((u) => u.endsWith(`/ws/bridge/${ship.session}`)).length).toBeGreaterThan(1);
    await expect.poll(() => screenText(page)).toContain('ship-screen');
    await expect(t.getByText(`${ship.name} › api`)).toHaveCount(0);
    await expect(t.getByRole('button', { name: /^More for / })).toHaveCount(0);

    // The boat exits: anchored, read-only, with the crew footer and no Dismiss.
    releaseCrew(s, ship.session, 'api', 'end');
    await expect(row).toHaveAttribute('data-state', 'anchored');
    await expect(row).toHaveText(/Teammate · anchored, exit 0/);
    await press(row, touch);
    await expect(t.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    await expect(t.getByText("This teammate's harness exited. Close its crew boat from ⋯.", { exact: true })).toBeVisible();
    await expect(t.getByRole('button', { name: 'Dismiss' })).toHaveCount(0);
    await expect(termInput(page)).toHaveAttribute('aria-hidden', 'true');
    await expect(t.getByRole('button', { name: /^Send / })).toHaveCount(0);

    // Closing it: the row goes and the bridge returns to the ship.
    await press(t.getByRole('button', { name: 'More for api' }), touch);
    const close = page.getByRole('menuitem', { name: 'Close crew boat' });
    await expect(close).toBeEnabled();
    await press(close, touch);
    await expect(crewRow(page, 'api')).toHaveCount(0);
    await expect(flotilla(page).getByText('Flotilla · 1', { exact: true })).toBeVisible();
    await expect(t.getByText(`${ship.name} › api`)).toHaveCount(0);
    await expect(t.getByText(ship.name, { exact: true })).toBeVisible();
    await expect.poll(() => screenText(page)).toContain('ship-screen');
    await expect.poll(() => crewOf(s, ship.session)).toEqual([]);
  });

  test('a crew boat closed elsewhere while it is boarded sends the bridge back to the ship and does not reconnect', async ({ page, admiral, sea }, info) => {
    test.setTimeout(60_000);
    const touch = isTouch(info);
    const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait needs', 'exit 0'] } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha', 'recorded');
    paneWrite(s, ship.session, 'ship-screen\r\n');
    await launchCrew(s, ship.session, 'api');
    const sockets: string[] = [];
    page.on('websocket', (ws) => sockets.push(ws.url()));
    await spyOnPage(page);
    await page.goto(admiral.url);
    await press(ships(page, underwayIn('Work')), touch);
    const t = terminal(page);
    await press(crewRow(page, 'api'), touch);
    await expect(t.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();

    closeCrew(s, ship.session, 'api');
    await expect(t.getByText(`${ship.name} › api`)).toHaveCount(0);
    await expect(flotilla(page).getByRole('button').first()).toHaveAttribute('aria-current', 'true');
    await expect.poll(() => screenText(page)).toContain('ship-screen');
    // Nothing opens a socket to the closed boat again.
    const crewSockets = () => sockets.filter((u) => u.endsWith('/crew/api')).length;
    const before = crewSockets();
    await page.waitForTimeout(3000);
    expect(crewSockets()).toBe(before);
  });

  test('Dismiss on an anchored ship with a live crew boat asks first; Keep it leaves both, Dismiss ship and crew ends both', async ({ page, admiral, sea }, info) => {
    test.setTimeout(60_000);
    const touch = isTouch(info);
    // The ship's `go` is released to anchor it (exit 3); the boat's never is, so it stays live.
    const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait go', 'exit 3'] } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha', 'recorded');
    await launchCrew(s, ship.session, 'api');
    release(s, ship.session);
    await expect.poll(async () => (await s.ship(ship.session))?.status).toBe('anchored');
    await spyOnPage(page);
    await page.goto(admiral.url);
    await press(ships(page, /^Board .+, Claude in Work, anchored, exited 3$/), touch);
    const t = terminal(page);
    await expect(t.getByRole('button', { name: 'Dismiss' })).toBeVisible();
    // A live crew boat of an anchored ship is still typeable.
    await press(crewRow(page, 'api'), touch);
    await expect(termInput(page)).toHaveAttribute('aria-hidden', 'false');
    await press(flotilla(page).getByRole('button').first(), touch);

    await press(t.getByRole('button', { name: 'Dismiss' }), touch);
    const dialog = page.getByRole('alertdialog', { name: `Dismiss ${ship.name}?` });
    await expect(dialog.getByText('1 crew boat is still working.')).toBeVisible();
    await expect(dialog.getByText('api · working', { exact: true })).toBeVisible();
    await press(dialog.getByRole('button', { name: 'Keep it' }), touch);
    await expect(dialog).toHaveCount(0);
    expect((await s.ship(ship.session))?.status).toBe('anchored');
    expect(await crewOf(s, ship.session)).toHaveLength(1);

    await press(t.getByRole('button', { name: 'Dismiss' }), touch);
    await press(dialog.getByRole('button', { name: 'Dismiss ship and crew' }), touch);
    await expect(terminal(page)).toHaveCount(0);
    await expect.poll(async () => (await s.ships()).length).toBe(0);
  });
});
