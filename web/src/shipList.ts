import { reasonWords, waited } from './inbox';
import type { WireCrew, WireShip } from './ships';

const at = (iso: string | undefined) => Date.parse(iso ?? '') || 0;
// A stamp that does not parse sorts after every real one.
const atOrLast = (iso: string | undefined) => Date.parse(iso ?? '') || Infinity;

const needing = (s: Pick<WireShip, 'crew'>): WireCrew[] => (s.crew ?? []).filter((c) => c.status === 'needs_you');

// Whether a ship is in the needs-you group: it asks, or one of its crew boats does. An anchored ship stays
// anchored on the wire, so its boat is the only sign.
export const asksYou = (s: WireShip): boolean => s.status === 'needs_you' || needing(s).length > 0;

// How many ships ask you. The Ships tab badge and the Ships hint show it. A ship sailing off is left out.
export const askingCount = (list: WireShip[]): number => list.filter((s) => s.status !== 'sailing_off' && asksYou(s)).length;

// Since when a ship in the needs-you group has waited: its own stamp, else its earliest needing boat's.
function waitingSince(s: WireShip): string | undefined {
  if (s.stop_since && Date.parse(s.stop_since)) return s.stop_since;
  const stamps = needing(s).map((c) => c.last_activity ?? c.launched);
  return stamps.sort((a, b) => atOrLast(a) - atOrLast(b))[0];
}

// The Ships row's status line. A ship that needs you only through its crew names the boats, not the raw code.
export function shipStatusLine(s: WireShip, now: number): string {
  const boats = needing(s);
  if (boats.length > 0 && (s.status === 'anchored' || (s.status === 'needs_you' && s.reason === 'crew'))) {
    return `Needs you · ${boats.map((c) => c.name).join(', ')} · waiting ${waited(waitingSince(s) ?? '', now)}`;
  }
  if (s.status === 'needs_you') return `Needs you · ${reasonWords(s.reason ?? 'question')} · waiting ${waited(s.stop_since ?? '', now)}`;
  if (s.status === 'anchored') return `Anchored, ${s.exit_signal ? `killed (${s.exit_signal})` : `exited ${s.exit_code ?? '?'}`}`;
  return 'Underway';
}

// The phone Ships list in order: ships that need you first, the longest waiting first, then every other
// ship, newest first. Ships sailing off are left out; anchored ships stay.
export function sortShips(list: WireShip[]): WireShip[] {
  const shown = list.filter((s) => s.status !== 'sailing_off');
  const asking = shown.filter(asksYou).sort((a, b) => atOrLast(waitingSince(a)) - atOrLast(waitingSince(b)));
  const rest = shown.filter((s) => !asksYou(s)).sort((a, b) => at(b.launched) - at(a.launched));
  return [...asking, ...rest];
}
