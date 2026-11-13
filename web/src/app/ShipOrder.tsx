import type { Ship } from './api';

export const webUrl = (u?: string): u is string => !!u && /^https?:\/\//i.test(u);

const issueNumber = (url: string) => /\/(?:issues|pull)\/(\d+)\/?$/.exec(url)?.[1];

/** What a ship started from: its order's number and title, linking to the URL it was launched from whatever that is. */
export function ShipOrder({ ship }: { ship: Ship }) {
  const n = webUrl(ship.url) ? issueNumber(ship.url) : undefined;
  // A campaign's order id is its id and the issue URL, which the link already carries.
  const id = n ? `#${n}` : ship.source === 'campaign' ? '' : ship.order_id;
  const text = ship.title || (webUrl(ship.url) ? ship.url : '');
  if (webUrl(ship.url)) {
    return (
      <a class="ship-order issue-link" href={ship.url} target="_blank" rel="noopener noreferrer">
        {id ? <span class="mono">{id}</span> : null} {text}
      </a>
    );
  }
  if (!ship.order_id) return null;
  return (
    <span class="ship-order">
      <span class="mono">{ship.order_id}</span> {ship.title}
    </span>
  );
}
