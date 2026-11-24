import { CrewBoat } from './CrewBoat';
import { deckLabel, raft } from './crewSea';
import { harnesses } from './harnesses';
import { REACH_PX } from './placement';
import { Ship } from './Ship';
import { arrivals, botFor, bobDelay, popping, spotOf, visible, type WireShip } from './ships';

// tokens.json size.crewRaft (top-left of each crew boat, in px from the ship's top-left) and size.subagentBot.
const RAFT_SPOTS = [
  [-30, 44],
  [-52, 58],
  [-72, 40],
];
const BOAT_H = 33;

function statusWords(s: WireShip): string {
  switch (s.status) {
    case 'underway':
    case 'needs_you': // reads as underway: the classic view reacts to nothing
      return 'underway';
    case 'sailing_off':
      return 'sailing off';
    case 'anchored':
      return `anchored, ${exitWords(s)}`;
  }
}

const exitWords = (s: WireShip) => (s.exit_signal ? `killed (${s.exit_signal})` : `exited ${s.exit_code ?? '?'}`);

function detail(s: WireShip): string {
  const tail = s.status === 'anchored' ? exitWords(s) : s.skill;
  return [s.repo, tail].filter(Boolean).join(' · ');
}

function ShipView({ s }: { s: WireShip }) {
  const spot = spotOf(s);
  const label = (harnesses as Partial<typeof harnesses>)[s.harness]?.label ?? s.harness;
  // A ship that needs you reads as a working one: the classic view reacts to nothing.
  const state = { underway: 'underway', needs_you: 'underway', anchored: 'anchored', sailing_off: 'sailing-off' }[s.status] as 'underway' | 'anchored' | 'sailing-off';
  const cls = ['ship-slot', arrivals.value.has(s.session) && 'sail-in', s.status === 'sailing_off' && 'sail-off']
    .filter(Boolean)
    .join(' ');
  if (!spot) return null;
  const crew = s.crew ?? [];
  const subs = s.subagents ?? [];
  const { shown, pill } = raft(crew);
  const bots = subs.length > 4 ? 3 : subs.length;
  const deck = {
    colors: subs.slice(0, bots).map((_, k) => botFor(`${s.session}#${k}`)),
    more: subs.length > bots ? `+${subs.length - bots}` : '',
  };
  const last = RAFT_SPOTS[shown.length - 1];
  return (
    <div
      class={cls}
      // Clamped by the slot's largest box, so a ship stays in view when the window shrinks; a ship with crew
      // keeps their reach on its left in view too.
      style={`left:clamp(${crew.length ? REACH_PX : 0}px,${spot.x * 100}%,100% - 140px);top:clamp(0px,${spot.y * 100}%,100% - 142px)`}
    >
      <div class="ship-btn" role="img" aria-label={`${s.name}, ${label} in ${s.fleet_name}, ${statusWords(s)}`}>
        <Ship
          fleetColor={s.fleet_color}
          bot={botFor(s.session)}
          harness={s.harness}
          state={state}
          bobDelay={bobDelay(s.session)}
          deck={subs.length ? deck : undefined}
        />
        <span class="plaque">
          <span class="plaque-name">{s.name}</span>
          <span class="plaque-detail">{detail(s)}</span>
        </span>
      </div>
      <div class="ship-marks">
        {shown.map((c, j) => {
          const [x, y] = RAFT_SPOTS[j];
          const anchored = c.status === 'anchored';
          const key = `${s.session}/${c.name}`;
          return (
            <div
              key={c.name}
              role="img"
              class={popping.value.has(key) ? 'crew-boat pop' : 'crew-boat'}
              style={`left:${x}px;top:${y}px`}
              aria-label={`${c.name}, crew boat of ${s.name}${anchored ? ', anchored' : ''}`}
            >
              <span class={anchored ? 'crew-still' : 'bob'} style={`display:block;position:relative;${anchored ? '' : `animation-delay:calc(${bobDelay(s.session)} - ${(j * 0.6 + 0.3).toFixed(1)}s)`}`}>
                <CrewBoat boat={c} stripe={s.fleet_color} bot={botFor(key)} size="sea" />
              </span>
            </div>
          );
        })}
        {pill && (
          <div role="img" class="crew-more" style={`left:${last[0] + 4}px;top:${last[1] + BOAT_H + 14}px`} aria-label={pill.name}>
            <span>{pill.text}</span>
          </div>
        )}
        {subs.length > 0 && <div role="img" class="deck-crew" aria-label={deckLabel(s.name, subs)} />}
      </div>
    </div>
  );
}

export function Ships() {
  return (
    <div class="ships">
      {visible.value.map((s) => (
        <ShipView key={s.session} s={s} />
      ))}
    </div>
  );
}
