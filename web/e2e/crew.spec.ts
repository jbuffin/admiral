import type { Locator, Page, TestInfo } from '@playwright/test';
import { closeCrew, crewOf, crewPaneWrite, launchCrew, paneWrite, release, releaseCrew, screenText } from './bridge';
import { boarded, expect, gesture, inboxButton, isTouch, needsYouIn, notes, spyOnPage, test, underwayIn, ships, water } from './sea';

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

// Crew boats and subagents on the sea (#154 u2; #72's crew flow, the sea half). The names below are the contract the
// open-water UI is built to; the prototype (scenes crew, crew-attn) supplies copy and geometry.
//
//   a crew boat is a button in open water, a sibling of its ship's button, named
//     "Board <crew>, teammate, crew of <ship>", plus ", needs you" or ", anchored"
//   tapping it boards the boat (board(session, crew)): the head reads "<ship> › <crew>", __admiral.boarded gets
//     "<session>/<crew>", and the ship does not move
//   a crew boat that needs you: the ship's slot holds `.ship-needs-you` (the shake), the ship's plaque reads
//     "<crew> needs you", and the chime plays once (3 notes)
//   subagents: a button "N subagent(s) on <ship>: <types>. They have no terminal" on the ship's deck; tapping it
//     shows a tip ("N subagent(s) · Explore just started · no terminal") and boards nothing
//   more than three crew boats: three boats and a button "+N crew" named "N more crew boats; board the ship to see
//     them"; tapping it boards the ship
//
// The ship and the boat run the same cue file. The ship is released at `sub` and `unsub`; the boat is released at
// all of them before `needs`, which makes it needs-you; `end` makes it exit.

const SUB_START = '{"hook_event_name":"SubagentStart","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore"}';
const SUB_STOP = '{"hook_event_name":"SubagentStop","agent_id":"a0f790cb4a518e0f9","agent_type":"Explore","background_tasks":[]}';

type Wire = { session: string; status: string; reason?: string; position: { x: number; y: number } | null; subagents: { type: string }[]; crew: { name: string; status: string }[] };
const wireOf = async (url: string, session: string) => {
  const body = await (await fetch(`${url}api/ships`)).json();
  return ((Array.isArray(body) ? body : body.ships) as Wire[]).find((s) => s.session === session);
};
const escape = (t: string) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

test.describe('crew boats and subagents on the sea', () => {
  test('a crew boat rafts beside its ship, boards on a tap without moving the ship, asks for you, and anchors; subagents ride the deck', async ({ page, admiral, sea }, info) => {
    test.setTimeout(90_000);
    const touch = isTouch(info);
    const s = sea({
      work: {
        name: 'Work',
        color: BLUE,
        cue: ['wait sub', `event SubagentStart ${SUB_START}`, 'wait unsub', `event SubagentStop ${SUB_STOP}`, 'wait needs', 'hook needs_you', 'wait end', 'exit 0'],
      },
    });
    await admiral.start();
    const ship = await s.launch('work', 'a-long-repository-name', 'recorded'); // a long plaque makes the slot wider than the 100 px ship
    // Fixture proof: the boat is really running and listed before any sea assertion.
    await launchCrew(s, ship.session, 'api');
    expect(await crewOf(s, ship.session)).toEqual([expect.objectContaining({ name: 'api', status: 'underway' })]);
    expect((await wireOf(admiral.url, ship.session))?.subagents).toEqual([]);

    await spyOnPage(page);
    await page.goto(admiral.url);
    const shipButton = ships(page, underwayIn('Work'));
    await expect(shipButton).toBeVisible();
    const boat = (crew: string, tail = '') => water(page).getByRole('button', { name: `Board ${crew}, teammate, crew of ${ship.name}${tail}`, exact: true });

    // It rafts beside its ship, as its own button.
    await expect(boat('api')).toBeVisible();
    await expect.poll(async () => (await wireOf(admiral.url, ship.session))?.position).not.toBeNull();
    const spot = (await wireOf(admiral.url, ship.session))?.position;

    // Tapping it boards the boat and leaves the ship where it was.
    await press(boat('api'), touch);
    const t = terminal(page);
    await expect(t.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    expect((await boarded(page)).at(-1)).toBe(`${ship.session}/api`);
    await press(t.getByRole('button', { name: 'Close terminal and return to open water' }), touch);
    await expect(t).toHaveCount(0);
    await page.waitForTimeout(500);
    expect((await wireOf(admiral.url, ship.session))?.position).toEqual(spot);

    // A subagent starts: a deck button, a tip on a tap, nothing boarded; it stops: the button goes.
    const deck = water(page).getByRole('button', { name: new RegExp(`^1 subagent on ${escape(ship.name)}`) });
    await expect(deck).toHaveCount(0);
    release(s, ship.session, 'sub');
    await expect.poll(async () => (await wireOf(admiral.url, ship.session))?.subagents.length).toBe(1);
    await expect(deck).toBeVisible();

    // Boats and deck button are placed from the ship drawing, whatever the slot's width: the boat sits at its raft
    // spot (-30, 44) from the drawing's left, and the deck button covers the first bot (x 8-16.5). The drawing is
    // 100 px wide, centred in the slot; the slot's box is measured, not the svg's, which the bob rotates.
    const slot = await water(page).locator('.ship-slot').first().boundingBox();
    const box = await boat('api').boundingBox();
    const deckBox = await deck.boundingBox();
    expect(slot!.width).toBeGreaterThan(104);
    const left = slot!.x + slot!.width / 2 - 50;
    expect(Math.abs(box!.x - (left - 30))).toBeLessThan(1);
    expect(deckBox!.x).toBeLessThanOrEqual(left + 8);
    expect(deckBox!.x + deckBox!.width).toBeGreaterThanOrEqual(left + 16.5);

    // A boat tapped while the ship has a subagent still boards the boat (the deck button is under it), and shows no tip.
    // The tap lands in the overlap of the boat and the deck button, not on the boat's centre.
    await (touch ? boat('api').tap({ position: { x: 34, y: 30 } }) : boat('api').click({ position: { x: 34, y: 30 } }));
    await expect(t.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    expect((await boarded(page)).at(-1)).toBe(`${ship.session}/api`);
    await expect(water(page).getByText(/Explore.*no terminal/)).toHaveCount(0);
    await press(t.getByRole('button', { name: 'Close terminal and return to open water' }), touch);
    await expect(t).toHaveCount(0);
    const afterBoat = (await boarded(page)).length;

    await press(deck, touch);
    await expect(water(page).getByText(/Explore.*no terminal/)).toBeVisible();
    await expect(terminal(page)).toHaveCount(0);
    expect((await boarded(page)).length).toBe(afterBoat);
    release(s, ship.session, 'unsub');
    await expect.poll(async () => (await wireOf(admiral.url, ship.session))?.subagents.length).toBe(0);
    await expect(deck).toHaveCount(0);

    // The boat asks for you: the ship shakes, the plaque names the boat, the boat is named so, one chime.
    await gesture(page, touch); // arms audio, as the first tap on an iPad does
    expect(await notes(page)).toBe(0);
    for (const name of ['sub', 'unsub', 'needs']) releaseCrew(s, ship.session, 'api', name);
    await expect.poll(async () => (await wireOf(admiral.url, ship.session))?.crew[0]?.status).toBe('needs_you');
    await expect(boat('api', ', needs you')).toBeVisible();
    await expect(ships(page, needsYouIn('Work'))).toBeVisible();
    await expect(water(page).locator('.ship-slot .ship-needs-you')).toHaveCount(1);
    await expect(water(page).locator('.plaque.asking')).toContainText('api needs you');
    await expect.poll(() => notes(page)).toBe(3); // one chime: the prototype's three notes
    await page.waitForTimeout(800);
    expect(await notes(page)).toBe(3);

    // The boat exits: anchored.
    releaseCrew(s, ship.session, 'api', 'end');
    await expect.poll(async () => (await wireOf(admiral.url, ship.session))?.crew[0]?.status).toBe('anchored');
    await expect(boat('api', ', anchored')).toBeVisible();

    // More than three boats: three raft, the rest are a "+N crew" button that boards the ship.
    for (const name of ['b', 'c', 'd']) await launchCrew(s, ship.session, name);
    await expect(water(page).getByRole('button', { name: /^Board .+, teammate, crew of / })).toHaveCount(3);
    const more = water(page).getByRole('button', { name: /^1 more crew boats?; board the ship to see them$/ });
    await expect(more).toHaveText(/\+1 crew/);
    await press(more, touch);
    await expect(t.getByText(ship.name, { exact: true })).toBeVisible();
    await expect(t.getByText(`${ship.name} ›`)).toHaveCount(0);
    expect((await boarded(page)).at(-1)).toBe(ship.session);
  });
});

// A crew boat in the Needs You inbox (#154 u3). The names below are the contract the inbox is built to:
//   a crew boat that needs you while its ship is underway is one live stop, with the name line "<ship> › <crew>",
//     and the ship has no stop of its own; it reads the boat's reason ("Wants to run Bash")
//   its Attach button is named "Attach to <crew>, crew of <ship> and open its bridge" and boards the boat
//     (window.__admiral.boarded ends in "<session>/<crew>")
//   a key typed on the crew bridge clears the boat, and the stop folds to "Back underway" (role status)
test.describe('a crew boat in the inbox', () => {
  const wide = (info: TestInfo) => {
    const v = info.project.use.viewport;
    return !!v && v.width >= 1024 && v.width > v.height;
  };
  const panel = (page: Page, info: TestInfo) => page.getByRole(wide(info) ? 'complementary' : 'dialog', { name: 'Needs you inbox' });

  test('a waiting crew boat is a stop of its own; Attach boards it; answering there folds it', async ({ page, admiral, sea }, info) => {
    test.setTimeout(60_000);
    const touch = isTouch(info);
    // The boat runs the ship's fake and cue: both park on `wait needs`, and only the boat's file is ever written.
    const s = sea({ work: { name: 'Work', color: BLUE, cue: ['wait needs', 'event PermissionRequest {"tool_name":"Bash"}', 'wait end', 'exit 0'] } });
    await admiral.start();
    const ship = await s.launch('work', 'alpha', 'recorded');
    await launchCrew(s, ship.session, 'api');
    await spyOnPage(page);
    await page.goto(admiral.url);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, nothing waiting');

    // The boat needs you; the ship stays underway. Fixture proof: the wire says so before any inbox assertion.
    releaseCrew(s, ship.session, 'api', 'needs');
    await expect.poll(() => crewOf(s, ship.session)).toEqual([expect.objectContaining({ name: 'api', status: 'needs_you' })]);
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, 1 item');

    await press(inboxButton(page), touch);
    const drawer = panel(page, info);
    await expect(drawer.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    await expect(drawer.getByText('Wants to run Bash')).toBeVisible();
    await expect(drawer.getByText(/^Waiting /)).toBeVisible();
    // No stop for the ship itself.
    await expect(drawer.getByRole('button', { name: new RegExp(`^Attach to ${ship.name}`) })).toHaveCount(0);

    await press(drawer.getByRole('button', { name: `Attach to api, crew of ${ship.name} and open its bridge` }), touch);
    await expect.poll(async () => (await boarded(page)).at(-1)).toMatch(new RegExp(`${ship.session}/api$`));
    const t = terminal(page);
    await expect(t.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();

    // A key typed on the crew bridge answers the boat, and the stop folds. The bridge shows the boat's screen first,
    // so the key has a socket to travel on.
    crewPaneWrite(s, ship.session, 'api', 'crew-screen\r\n');
    await expect.poll(() => screenText(page)).toContain('crew-screen');
    // The sheet closed at Attach; open it again before the key, so the fold shows while it is open whenever the
    // push lands (the fold is held for 5 s only).
    if (!wide(info)) await press(inboxButton(page), touch);
    await expect(panel(page, info).getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    await termInput(page).focus();
    await page.keyboard.type('y');
    const fold = panel(page, info).getByRole('status').filter({ hasText: 'Back underway' });
    await expect(fold).toBeVisible();
    await expect(fold.getByText(`${ship.name} › api`, { exact: true })).toBeVisible();
    await expect(inboxButton(page)).toHaveAccessibleName('Needs you, nothing waiting');
  });
});
