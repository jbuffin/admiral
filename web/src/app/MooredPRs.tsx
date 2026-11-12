import type { Ship } from './api';
import { prChips, prLabel, staleNote } from './moored';

export function MooredPRs({ ship, now }: { ship: Ship; now: number }) {
  const note = staleNote(ship, now);
  return (
    <>
      {(ship.prs ?? []).map((pr) => (
        <span key={pr.url} class="ship-pr">
          <a class="pr-link" href={pr.url} target="_blank" rel="noreferrer">
            {prLabel(pr.url)}
          </a>
          {prChips(pr).map((c) => (
            <span key={c.text} class={`chip chip-${c.tone}`}>
              {c.text}
            </span>
          ))}
        </span>
      ))}
      {note ? <span class="ship-tail">{note}</span> : null}
    </>
  );
}
