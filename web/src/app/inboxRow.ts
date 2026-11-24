import type { Fleet } from './api';
import { readCrewMember, readMission } from './crewItems';
import { fleetPaint } from './fleetPaint';
import type { Entry } from './inbox';
import { respondedPRs } from './moored';
import { prNumber } from './prTab';
import { shipAge } from './shipList';

export function reasonWords(code: string): string {
  if (code.startsWith('permission:')) {
    const tool = code.slice('permission:'.length);
    return `${tool === 'Bash' ? 'Wants to run' : 'Wants to use'} ${tool}`;
  }
  if (code === 'notification:permission_prompt') return 'Needs a permission answer';
  if (code.startsWith('notification:')) return 'Sent a notification';
  return (
    { question: 'Asked a question', stop: 'Finished and is waiting', bell: 'Rang the bell', idle: 'Has gone quiet', not_started: 'Has not started: waiting on you' }[code as 'stop'] ?? code
  );
}

export function rowShip(e: Entry): string {
  const name = e.item?.name || e.ship?.name || '';
  return e.crew ? `${name} › ${e.crew.name}` : name;
}

// What the ship is about, shown beside its name. A crew boat's row has none, and an item of a ship that sailed off carries none.
export function rowShipTitle(e: Entry): string {
  if (e.crew) return '';
  if (e.item) return e.item.ship_title || e.item.wake_proposal?.ship_title || '';
  return e.ship?.title ?? '';
}

// A wake proposal's heading is what names the moored ship, so its title follows the heading; elsewhere the ship's title comes first.
export function titleLines(e: Entry, heading: string, shipTitle: string): string[] {
  return (e.item?.wake_proposal ? [heading, shipTitle] : [shipTitle, heading]).filter(Boolean);
}

/** A draft's second line names the campaign, since the item's own title repeats the verb. */
export const rowTitle = (e: Entry): string => (e.item?.kind === 'campaign_draft' ? (e.item.campaign_draft?.title ?? '') : (e.item?.title ?? ''));

/** Who a Bot-made item speaks for, shown muted after the verb; empty on every other item. */
export const rowVia = (e: Entry): string => (e.item?.kind === 'campaign_draft' ? 'AdmiralBot' : '');

const lower = (s: string) => s.charAt(0).toLowerCase() + s.slice(1);

// The phrase reads after the ship name: "Dory asked", "Dory wants to run Bash".
export function rowVerb(e: Entry): string {
  if (e.type === 'merge') return 'is ready to merge';
  if (e.type === 'rereview') {
    const [first, ...more] = respondedPRs(e.ship ?? {});
    const rest = more.length === 0 ? '' : more.length === 1 ? ` and #${prNumber(more[0].url)}` : ` and ${more.length} more`;
    return `${first?.author ? `@${first.author} ` : ''}responded on #${first ? prNumber(first.url) : ''}${rest}`;
  }
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
    case 'campaign_draft':
      return 'drafted a campaign';
    case 'drafting_ended':
      return 'a drafting ship ended';
    case 'event_proposal':
      return 'proposes a routine run for an outside event';
    case 'allow_rules': {
      const n = Array.isArray(e.item.allow_rules?.cards) ? e.item.allow_rules.cards.length : 0;
      return `proposes ${n} allow rule${n === 1 ? '' : 's'} for ${e.fleet?.name || e.fleet?.key || ''}`;
    }
    case 'crew_notes':
      return 'proposes crew notes';
    case 'crew_member': {
      const from = readCrewMember(e.item.crew_member).ship;
      return from ? `asks to keep a crew member from ${from}` : 'asks to keep a crew member';
    }
    case 'mission': {
      const m = readMission(e.item.mission);
      return `proposes a mission${m.ship ? ` from ${m.ship}` : ''}${m.scope === 'repo' && m.repo ? ` for ${m.repo}` : ''}`;
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
