import { useEffect, useRef, useState } from 'preact/hooks';
import { fleets, fleetSearch } from './fleets';
import { createInboxQueue, type Entry } from './inbox';
import { entryAge, fleetLabel, rowShip, rowVerb } from './inboxRow';
import { go, shipPath } from './router';
import { inboxItems, ships } from './shipsLink';
import { emptyLine, type Surface } from './surfaces';

const ANSWER = new Set(['question', 'stop']);

function Row({ e, now, phone }: { e: Entry; now: number; phone: boolean }) {
  const [open, setOpen] = useState(false);
  const fleet = fleetLabel(e, fleets.value);
  const notice = e.item?.kind === 'notice';
  const ship = e.ship ?? ships.value.find((s) => s.session === e.session);
  const title = e.item?.title ?? (e.crew ? '' : ship?.title ?? '');
  const age = entryAge(e, now);
  const search = fleetSearch.value;
  const body = (
    <>
      {fleet && phone ? <span class="ship-bar" style={{ background: fleet.fill }} aria-hidden="true" /> : null}
      <div class="ship-main">
        <span class="inbox-who">
          <span class="ship-name">{rowShip(e)}</span> <span class="inbox-verb">{rowVerb(e)}</span>
        </span>
        {title ? <span class="inbox-title">{title} </span> : null}
        {ship?.order_id ? <span class="ship-order mono">{ship.order_id} </span> : null}
        {fleet ? (
          <span class="inbox-fleet">
            {phone ? null : <span class="dot" style={{ background: fleet.fill }} aria-hidden="true" />}
            {fleet.name}{' '}
          </span>
        ) : null}
      </div>
      {age ? <span class="inbox-age">{age}</span> : null}
    </>
  );
  if (e.folded !== null) {
    return (
      <li class="ship-row inbox-row inbox-folded">
        <span class="ship-name">{rowShip(e)}</span> <span class="inbox-confirm" role="status">{e.folded}</span>
        {fleet ? <span class="inbox-fleet">{fleet.name}</span> : null}
      </li>
    );
  }
  return (
    <li class="ship-row inbox-row">
      {notice ? (
        <>
          <button type="button" class="ship-link inbox-link" aria-expanded={open} onClick={() => setOpen(!open)}>
            {body}
          </button>
          {open ? <p class="inbox-body">{e.item?.notice?.body ?? ''}</p> : null}
        </>
      ) : (
        <a
          class="ship-link inbox-link"
          href={shipPath(e.session) + search}
          onClick={(ev) => {
            if (ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
            ev.preventDefault();
            go(shipPath(e.session), search);
          }}
        >
          {body}
        </a>
      )}
    </li>
  );
}

export function InboxPanel({ surface, title, phone }: { surface: Surface; title: boolean; phone: boolean }) {
  const queue = useRef(createInboxQueue()).current;
  const synced = useRef<unknown[]>([]);
  const [, setRev] = useState(0);
  const items = inboxItems.value;
  const fleetShips = ships.value;
  if (synced.current[0] !== items || synced.current[1] !== fleetShips) {
    synced.current = [items, fleetShips];
    queue.sync({ items, ships: fleetShips }, Date.now());
  }
  const entries = queue.entries();
  const anyFolded = entries.some((e) => e.folded !== null);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  useEffect(() => {
    if (!anyFolded) return;
    const t = setInterval(() => {
      if (queue.tick(Date.now())) setRev((n) => n + 1);
    }, 1000);
    return () => clearInterval(t);
  }, [anyFolded]);

  const groups = [
    { id: 'answer', title: 'Needs an answer', rows: entries.filter((e) => ANSWER.has(e.type === 'stop' ? 'stop' : e.item!.kind)) },
    { id: 'review', title: 'To review', rows: entries.filter((e) => !ANSWER.has(e.type === 'stop' ? 'stop' : e.item!.kind)) },
  ].filter((g) => g.rows.length > 0);

  if (entries.length === 0) {
    return (
      <main class="panel">
        {title ? <h1>{surface.title}</h1> : null}
        <p class="line">{emptyLine(surface, false, false)}</p>
      </main>
    );
  }
  return (
    <main class="panel">
      {title ? <h1>{surface.title}</h1> : null}
      {phone ? null : <p class="line">Oldest first</p>}
      {groups.map((g) => (
        <section key={g.id} class="ship-group" aria-labelledby={`inbox-${g.id}`}>
          <h2 id={`inbox-${g.id}`} class="group-head">
            {g.title} <span class="count">{g.rows.length}</span>
          </h2>
          <ul class="ship-list">
            {g.rows.map((e) => (
              <Row key={e.key} e={e} now={now} phone={phone} />
            ))}
          </ul>
        </section>
      ))}
      {phone ? <p class="line">Every fleet, oldest first.</p> : null}
    </main>
  );
}
