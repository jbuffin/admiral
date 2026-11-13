import type { Ship } from './api';

export const webUrl = (u?: string): u is string => !!u && /^https?:\/\//i.test(u);

const issueNumber = (url: string) => /\/(?:issues|pull)\/(\d+)(?:[/?#]|$)/.exec(url)?.[1];

const Titling = ({ on }: { on?: boolean }) => (on ? <span class="titling" role="status" aria-label="Working out the title" /> : null);

/** What a ship started from: its order's number and title, or the title worked out from its first prompt,
 * linking to the URL it was launched from or the link its prompt is about. */
export function ShipOrder({ ship }: { ship: Ship }) {
  const href = webUrl(ship.url) ? ship.url : webUrl(ship.link) ? ship.link : undefined;
  const n = href ? issueNumber(href) : undefined;
  // A campaign's order id is its id and the issue URL, which the link already carries.
  const id = n ? `#${n}` : ship.source === 'campaign' ? '' : ship.order_id;
  if (href) {
    return (
      <span class="ship-order">
        <a class="issue-link" href={href} target="_blank" rel="noopener noreferrer">
          {id ? <span class="mono">{id}</span> : null} {ship.title || href}
        </a>
        <Titling on={ship.titling} />
      </span>
    );
  }
  if (!ship.order_id && !ship.title && !ship.titling) return null;
  return (
    <span class="ship-order">
      {ship.order_id ? <span class="mono">{ship.order_id}</span> : null} {ship.title}
      <Titling on={ship.titling} />
    </span>
  );
}
