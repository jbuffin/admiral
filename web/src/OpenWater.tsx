import { signal } from '@preact/signals';
import { CrewBoat } from './CrewBoat';
import { crewAsks, deckLabel, plaqueNeeds, raft, subagentTip } from './crewSea';
import { harnesses } from './harnesses';
import { REACH_PX } from './placement';
import { Ship } from './Ship';
import { arrivals, board, botFor, bobDelay, drop, dragged, popping, spotOf, visible, type WireShip } from './ships';

const DRAG_THRESHOLD = 8;
const TIP_MS = 2600;
// tokens.json size.crewRaft (top-left of each crew boat, in px from the ship's top-left) and size.subagentBot.
const RAFT_SPOTS = [
  [-30, 44],
  [-52, 58],
  [-72, 40],
];
const BOAT_H = 33;
// Presses that start on these act on their own tap; they never drag or board the ship. (A press on the deck button
// may drag the ship; a tap there shows the tip and never boards.)
const OWN_TAPS = '.crew-boat, .crew-more';

// The ship whose subagents the deck tip lists, for a moment.
const tip = signal<string | null>(null);
let tipTimer: ReturnType<typeof setTimeout> | undefined;
function showTip(session: string): void {
  tip.value = session;
  clearTimeout(tipTimer);
  tipTimer = setTimeout(() => (tip.value = null), TIP_MS);
}

function statusWords(s: WireShip): string {
  switch (s.status) {
    case 'underway':
      return 'underway';
    case 'needs_you':
      return 'needs you';
    case 'sailing_off':
      return 'sailing off';
    case 'anchored':
      return `anchored, ${exitWords(s)}`;
  }
}

const exitWords = (s: WireShip) => (s.exit_signal ? `killed (${s.exit_signal})` : `exited ${s.exit_code ?? '?'}`);

// The plaque's second line: the repo, then the skill; an anchored ship says how it ended.
function detail(s: WireShip): string {
  const tail = s.status === 'anchored' ? exitWords(s) : s.skill;
  return [s.repo, tail].filter(Boolean).join(' · ');
}

// A press that stays under the threshold is a tap and boards the ship; one that moves is a drag
// and moves it (Pointer Events, so touch and mouse). The spot is a fraction of open water.
function press(e: PointerEvent, s: WireShip): void {
  if (e.button !== 0 || !e.isPrimary || (e.target as Element).closest(OWN_TAPS)) return;
  const el = e.currentTarget as HTMLElement;
  const onDeck = !!(e.target as Element).closest('.deck-crew');
  const layer = el.parentElement!;
  const id = e.pointerId;
  const from = { x: e.clientX, y: e.clientY };
  const start = el.getBoundingClientRect();
  const water = layer.getBoundingClientRect();
  // A ship stays on the sea, not on the marinas' land.
  const sea = layer.parentElement?.querySelector('.sea')?.getBoundingClientRect() ?? water;
  // A ship with crew keeps the crew boats' reach on its left in view.
  const reach = s.crew?.length ? REACH_PX : 0;
  const room = {
    x: [water.left + reach, Math.min(water.right, sea.right) - start.width],
    y: [water.top, Math.min(water.bottom, sea.bottom) - start.height],
  };
  let moved = false;
  const at = (ev: PointerEvent) => {
    const left = Math.min(Math.max(start.left + ev.clientX - from.x, room.x[0]), Math.max(room.x[0], room.x[1]));
    const top = Math.min(Math.max(start.top + ev.clientY - from.y, room.y[0]), Math.max(room.y[0], room.y[1]));
    return { x: (left - water.left) / water.width, y: (top - water.top) / water.height };
  };
  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== id) return;
    if (!moved && Math.hypot(ev.clientX - from.x, ev.clientY - from.y) < DRAG_THRESHOLD) return;
    moved = true;
    dragged.value = { session: s.session, ...at(ev) };
  };
  const end = (ev: PointerEvent, cancelled: boolean) => {
    if (ev.pointerId !== id) return;
    if (el.hasPointerCapture(id)) el.releasePointerCapture(id);
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', up);
    window.removeEventListener('pointercancel', cancel);
    if (moved && !cancelled) drop(s.session, at(ev));
    else if (!moved && !cancelled) onDeck ? showTip(s.session) : board(s.session);
    dragged.value = null;
  };
  const up = (ev: PointerEvent) => end(ev, false);
  const cancel = (ev: PointerEvent) => end(ev, true);
  // Captured, so a mouse released outside the window still ends the drag.
  try {
    el.setPointerCapture(id);
  } catch {
    // a pointer that is not active (a synthetic event) can't be captured; the window listeners still work
  }
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', up);
  window.addEventListener('pointercancel', cancel);
}

function ShipView({ s }: { s: WireShip }) {
  const spot = spotOf(s);
  // A harness this build doesn't know gets a plain label and an empty flag.
  const label = (harnesses as Partial<typeof harnesses>)[s.harness]?.label ?? s.harness;
  const needsYou = s.status === 'needs_you';
  const state = { underway: 'underway', needs_you: 'needs-you', anchored: 'anchored', sailing_off: 'sailing-off' }[s.status] as
    | 'underway'
    | 'needs-you'
    | 'anchored'
    | 'sailing-off';
  const lifted = dragged.value?.session === s.session;
  const cls = ['ship-slot', arrivals.value.has(s.session) && 'sail-in', s.status === 'sailing_off' && 'sail-off', lifted && 'lifted']
    .filter(Boolean)
    .join(' ');
  // The slot waits for a spot before it shows.
  if (!spot) return null;
  const crew = s.crew ?? [];
  const subs = s.subagents ?? [];
  const { shown, pill } = raft(crew);
  const needs = plaqueNeeds(crew);
  const asking = needsYou || needs !== null;
  const bots = subs.length > 4 ? 3 : subs.length;
  const deck = {
    colors: subs.slice(0, bots).map((_, k) => botFor(`${s.session}#${k}`)),
    more: subs.length > bots ? `+${subs.length - bots}` : '',
  };
  const [count, ...rest] = subagentTip(subs, Date.now()).split(' · ');
  const last = RAFT_SPOTS[shown.length - 1];
  return (
    <div
      class={cls}
      // Clamped by the slot's largest box, so a ship stays in view when the window shrinks; a ship with crew
      // keeps their reach on its left in view too.
      style={`left:clamp(${crew.length ? REACH_PX : 0}px,${spot.x * 100}%,100% - 140px);top:clamp(0px,${spot.y * 100}%,100% - 142px)`}
      onPointerDown={(e) => press(e, s)}
    >
      <button
        type="button"
        class="ship-btn"
        aria-label={`Board ${s.name}, ${label} in ${s.fleet_name}, ${statusWords(s)}`}
        // A keyboard or assistive-technology activation has no pointer; a pointer's tap boards on release.
        onClick={(e) => e.detail === 0 && board(s.session)}
      >
        <Ship
          fleetColor={s.fleet_color}
          bot={botFor(s.session)}
          harness={s.harness}
          state={state}
          bobDelay={bobDelay(s.session)}
          deck={subs.length ? deck : undefined}
        />
        <span class={asking ? 'plaque asking' : 'plaque'}>
          <span class="plaque-name">{s.name}</span>
          <span class="plaque-detail">{needs ?? detail(s)}</span>
        </span>
      </button>
      <div class="ship-marks">
        {shown.map((c, j) => {
          const [x, y] = RAFT_SPOTS[j];
          const anchored = c.status === 'anchored';
          const asks = crewAsks(c);
          const key = `${s.session}/${c.name}`;
          return (
            <button
              key={c.name}
              type="button"
              class={popping.value.has(key) ? 'crew-boat pop' : 'crew-boat'}
              style={`left:${x}px;top:${y}px`}
              aria-label={`Board ${c.name}, teammate, crew of ${s.name}${asks ? ', needs you' : anchored ? ', anchored' : ''}`}
              onClick={() => board(s.session, c.name)}
            >
              <span class={anchored ? 'crew-still' : 'bob'} style={`display:block;position:relative;${anchored ? '' : `animation-delay:calc(${bobDelay(s.session)} - ${(j * 0.6 + 0.3).toFixed(1)}s)`}`}>
                <CrewBoat boat={c} stripe={s.fleet_color} bot={botFor(key)} size="sea" />
                {asks && (
                  <span class="crew-bang ping" aria-hidden="true">
                    !
                  </span>
                )}
              </span>
            </button>
          );
        })}
        {pill && (
          <button type="button" class="crew-more" style={`left:${last[0] + 4}px;top:${last[1] + BOAT_H + 14}px`} aria-label={pill.name} onClick={() => board(s.session)}>
            <span>{pill.text}</span>
          </button>
        )}
        {subs.length > 0 && (
          <button type="button" class="deck-crew" aria-label={deckLabel(s.name, subs)} onClick={(e) => e.detail === 0 && showTip(s.session)} />
        )}
        {subs.length > 0 && tip.value === s.session && (
          <div class="deck-tip" role="status" style={`top:${-14 - Math.ceil(subs.length / 2) * 17}px`}>
            <b>{count}</b>
            {` · ${rest.join(' · ')}`}
          </div>
        )}
      </div>
    </div>
  );
}

// Ships float over all of open water; their spots are fractions of it.
export function Ships() {
  return (
    <div class="ships">
      {visible.value.map((s) => (
        <ShipView key={s.session} s={s} />
      ))}
    </div>
  );
}
