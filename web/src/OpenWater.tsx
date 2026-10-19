import { useRef } from 'preact/hooks';
import { harnesses } from './harnesses';
import { Ship } from './Ship';
import { arrivals, board, botFor, bobDelay, drop, dragged, spotOf, visible, type WireShip } from './ships';

const DRAG_THRESHOLD = 8;

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
  if (e.button !== 0 || !e.isPrimary) return;
  const el = e.currentTarget as HTMLElement;
  const layer = el.parentElement!;
  const id = e.pointerId;
  const from = { x: e.clientX, y: e.clientY };
  const start = el.getBoundingClientRect();
  const water = layer.getBoundingClientRect();
  // A ship stays on the sea, not on the marinas' land.
  const sea = layer.parentElement?.querySelector('.sea')?.getBoundingClientRect() ?? water;
  const room = {
    x: [water.left, Math.min(water.right, sea.right) - start.width],
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
    else if (!moved && !cancelled) board(s.session);
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
  return (
    <button
      type="button"
      class={cls}
      // Clamped by the slot's largest box, so a ship stays in view when the window shrinks.
      style={`left:clamp(0px,${spot.x * 100}%,100% - 140px);top:clamp(0px,${spot.y * 100}%,100% - 142px)`}
      aria-label={`Board ${s.name}, ${label} in ${s.fleet_name}, ${statusWords(s)}`}
      onPointerDown={(e) => press(e, s)}
      // A keyboard or assistive-technology activation has no pointer; a pointer's tap boards on release.
      onClick={(e) => e.detail === 0 && board(s.session)}
    >
      <Ship fleetColor={s.fleet_color} bot={botFor(s.session)} harness={s.harness} state={state} bobDelay={bobDelay(s.session)} />
      <span class={needsYou ? 'plaque asking' : 'plaque'}>
        <span class="plaque-name">{s.name}</span>
        <span class="plaque-detail">{detail(s)}</span>
      </span>
    </button>
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
