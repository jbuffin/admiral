import type { Fleet } from './api';
import { readCrewMember } from './crewItems';
import { fleetPaint } from './fleetPaint';
import type { Entry } from './inbox';
import { shipAge } from './shipList';

export function reasonWords(code: string): string {
  if (code.startsWith('permission:')) {
    const tool = code.slice('permission:'.length);
    return `${tool === 'Bash' ? 'Wants to run' : 'Wants to use'} ${tool}`;
  }
  if (code === 'notification:permission_prompt') return 'Needs a permission answer';
  if (code.startsWith('notification:')) return 'Sent a notification';
  return (
    { question: 'Asked a question', stop: 'Finished and is waiting', bell: 'Rang the bell', idle: 'Has gone quiet' }[code as 'stop'] ?? code
  );
}

export function rowShip(e: Entry): string {
  const name = e.item?.name || e.ship?.name || '';
  return e.crew ? `${name} › ${e.crew.name}` : name;
}

const lower = (s: string) => s.charAt(0).toLowerCase() + s.slice(1);

// The phrase reads after the ship name: "Dory asked", "Dory wants to run Bash".
export function rowVerb(e: Entry): string {
  if (e.type === 'merge') return 'is ready to merge';
  if (e.type === 'stop') return lower(reasonWords(e.crew?.reason ?? e.ship?.reason ?? ''));
  switch (e.item?.kind) {
    case 'question':
      return 'asked';
    case 'review':
      return 'requested a review';
    case 'findings': {
      const cards = e.item.findings?.cards;
      return Array.isArray(cards) ? `sent ${cards.length} finding ${cards.length === 1 ? 'card' : 'cards'}` : 'sent finding cards';
    }
    case 'routine_proposal':
      return e.item.routine_proposal?.reason ? `proposes a routine run, ${lower(e.item.routine_proposal.reason.split(':')[0])}` : 'proposes a routine run';
    case 'crew_notes':
      return 'proposes crew notes';
    case 'crew_member': {
      const from = readCrewMember(e.item.crew_member).ship;
      return from ? `asks to keep a crew member from ${from}` : 'asks to keep a crew member';
    }
    default:
      return 'posted a notice';
  }
}

export function fleetLabel(e: Entry, fleets: Fleet[]): { name: string; fill: string } | null {
  if (!e.fleet) return null;
  const found = fleets.find((f) => f.key === e.fleet!.key);
  return { name: e.fleet.name || found?.name || e.fleet.key, fill: found ? fleetPaint(found).fill : e.fleet.color };
}

export const entryAge = (e: Entry, now: number): string => (e.since ? shipAge(new Date(e.since).toISOString(), now) : '');
