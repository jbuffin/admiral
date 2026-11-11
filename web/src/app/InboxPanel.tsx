import { useEffect, useRef, useState } from 'preact/hooks';
import { fleets, fleetSearch } from './fleets';
import { createInboxQueue, type Entry } from './inbox';
import { Peek } from './Peek';
import { peekKey } from './peekLink';
import { entryAge, fleetLabel, rowShip, rowVerb } from './inboxRow';
import { go, loc, peekOf, peekPath, surfacePath } from './router';
import { inboxItems, inboxLoaded, ships, shipsLoaded } from './shipsLink';
import { emptyLine, type Surface } from './surfaces';

const ANSWER = new Set(['question', 'stop']);

function Row({ e, now, phone, selected }: { e: Entry; now: number; phone: boolean; selected: boolean }) {
  const [open, setOpen] = useState(false);
  const fleet = fleetLabel(e, fleets.value);
  const notice = e.item?.kind === 'notice';
  const ship = e.ship ?? ships.value.find((s) => s.session === e.session);
  const title = e.item?.title ?? (e.crew ? '' : ship?.title ?? '');
  const age = entryAge(e, now);
  const search = fleetSearch.value;
  const href = peekPath({ session: e.session, ...(e.type === 'item' ? { item: e.id } : e.crew ? { crew: e.crew.name } : {}) });
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
    <li class={`ship-row inbox-row${selected ? ' selected' : ''}`}>
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
          href={href + search}
          aria-current={selected ? 'true' : undefined}
          onClick={(ev) => {
            if (ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return;
            ev.preventDefault();
            go(href, search);
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

  const peek = peekOf(loc.value.path);
  const key = peek ? peekKey(peek) : '';
  const open = peek ? entries.find((e) => e.key === key) : undefined;
  const ready = peek ? (peek.item !== undefined ? inboxLoaded.value : shipsLoaded.value) : false;
  const search = fleetSearch.value;
  // The entry as last seen: an answer's push can reach the page before its response, and the fold still needs the entry.
  const seen = useRef<Entry | undefined>(undefined);
  if (open) seen.current = open;
  const known = seen.current?.key === key ? seen.current : undefined;
  // Peek closes as soon as its entry folds or leaves the queue, and the list shows the fold. A path whose entry never arrived stays open.
  const closing = peek !== null && ready && (open ? open.folded !== null : known !== undefined);
  useEffect(() => {
    if (closing) go(surfacePath(surface), search, true);
  }, [closing]);

  const fold = (text: string) => {
    queue.fold(key, text, Date.now(), known);
    setRev((n) => n + 1);
  };
  const detail = open && !closing ? <Peek key={open.key} entry={open} fold={fold} /> : null;

  if (entries.length === 0) {
    return (
      <main class="panel">
        {title ? <h1>{surface.title}</h1> : null}
        <p class="line">{emptyLine(surface, false, false)}</p>
      </main>
    );
  }
  if (phone && detail) return <main class="panel peek-panel">{detail}</main>;
  const list = (
    <main class={phone ? 'panel' : 'panel inbox-list'}>
      {title ? <h1>{surface.title}</h1> : null}
      {phone ? null : <p class="line">Oldest first</p>}
      {groups.map((g) => (
        <section key={g.id} class="ship-group" aria-labelledby={`inbox-${g.id}`}>
          <h2 id={`inbox-${g.id}`} class="group-head">
            {g.title} <span class="count">{g.rows.length}</span>
          </h2>
          <ul class="ship-list">
            {g.rows.map((e) => (
              <Row key={e.key} e={e} now={now} phone={phone} selected={open?.key === e.key && !closing} />
            ))}
          </ul>
        </section>
      ))}
      {phone ? <p class="line">Every fleet, oldest first.</p> : null}
    </main>
  );
  if (phone) return list;
  return (
    <div class="inbox-split">
      {list}
      <div class="panel peek-panel">{detail}</div>
    </div>
  );
}
