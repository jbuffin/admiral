import { useEffect, useRef, useState } from 'preact/hooks';
import { fleets, fleetSearch } from './fleets';
import { syncSharedQueue, queueRev } from './attention';
import { sharedInboxQueue, type Entry, type InboxQueue } from './inbox';
import { call } from './api';
import { isChatSession, Peek, peekKeys } from './Peek';
import { REMOVED_FOLD } from './itemActions';
import { bridgeSearch } from './peekLink';
import { keyAction, keyHint } from './peekKeys';
import { peekKey } from './peekLink';
import { entryAge, fleetLabel, rowShip, rowVerb } from './inboxRow';
import { go, loc, peekOf, peekPath, type PeekTarget, shipPath, surfacePath } from './router';
import { inboxItems, inboxLoaded, ships, shipsLoaded } from './shipsLink';
import { emptyLine, type Surface } from './surfaces';
import { noticeEntryLinks } from './routines';

const ANSWER = new Set(['question', 'stop']);

// Below this width (a portrait iPad) the shell's sidebar, the list and a usable Peek no longer fit side by side.
export const STACKED_QUERY = '(max-width: 1099px)';

export function useStacked(phone: boolean) {
  const mq = matchMedia(STACKED_QUERY);
  const [narrow, setNarrow] = useState(mq.matches);
  useEffect(() => {
    const on = () => setNarrow(mq.matches);
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, []);
  return phone || narrow;
}

const targetOf = (e: Entry) => ({ session: e.session, ...(e.type === 'merge' ? { merge: true as const } : e.type === 'rereview' ? { rereview: true as const } : e.type === 'item' ? { item: e.id } : e.crew ? { crew: e.crew.name } : {}) });

// What an entry asks of you: an answer to a stop, a question or a merge; the rest is a review.
const needsAnswer = (e: Entry) => e.type !== 'item' || ANSWER.has(e.item!.kind);

export function Row({ e, now, phone, selected, fold, peekHref }: { e: Entry; now: number; phone: boolean; selected: boolean; fold: (text: string) => void; peekHref?: (t: PeekTarget) => string | null }) {
  const [open, setOpen] = useState(false);
  const [dismissing, setDismissing] = useState(false);
  const [error, setError] = useState('');
  const dismiss = async () => {
    setDismissing(true);
    setError('');
    const r = await call('POST', `/inbox/${encodeURIComponent(e.session)}/${encodeURIComponent(e.id!)}/remove`);
    if (r.ok) return fold(REMOVED_FOLD);
    setDismissing(false);
    setError(r.error);
  };
  const fleet = fleetLabel(e, fleets.value);
  const notice = e.item?.kind === 'notice';
  const ship = e.ship ?? ships.value.find((s) => s.session === e.session);
  const title = e.item?.title ?? (e.crew ? '' : ship?.title ?? '');
  const age = entryAge(e, now);
  const search = fleetSearch.value;
  const target: PeekTarget = targetOf(e);
  const href = peekHref?.(target) ?? peekPath(target);
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
          {open ? (
            <>
              <p class="inbox-body">{e.item?.notice?.body ?? ''}</p>
              {noticeEntryLinks(e.item?.notice).map((l) => (
                <p key={l.path} class="inbox-body">
                  <a class="btn" href={l.path} onClick={(ev) => { if (ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey) return; ev.preventDefault(); const [path, q] = l.path.split('?'); go(path, q ? `?${q}` : ''); }}>
                    {l.label}
                  </a>
                </p>
              ))}
              <div class="inbox-send-row inbox-body">
                <button type="button" class="btn" disabled={dismissing} onClick={() => void dismiss()}>
                  Dismiss
                </button>
              </div>
              {error ? (
                <p class="inbox-error inbox-body" role="alert">
                  {error}
                </p>
              ) : null}
            </>
          ) : null}
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

/** The shared inbox queue as the list and the Chat surface's rail show it: its folds time out in watchAttention. */
export function useInboxQueue() {
  const queue = sharedInboxQueue();
  syncSharedQueue(inboxItems.value, ships.value);
  void queueRev.value;
  const entries = queue.entries();
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  return { queue, entries, now, rerender: () => queueRev.value++ };
}

/** The entry a Peek path opens, and the fold that closes it. */
export function usePeekEntry(peek: PeekTarget | null, entries: Entry[], queue: InboxQueue, rerender: () => void, leave: () => void) {
  const key = peek ? peekKey(peek) : '';
  const open = peek ? entries.find((e) => e.key === key) : undefined;
  const ready = peek ? (peek.item !== undefined ? inboxLoaded.value : shipsLoaded.value) : false;
  // The entry as last seen: an answer's push can reach the page before its response, and the fold still needs the entry.
  const seen = useRef<Entry | undefined>(undefined);
  if (open) seen.current = open;
  const known = seen.current?.key === key ? seen.current : undefined;
  // Peek closes as soon as its entry folds or leaves the queue, and the list shows the fold. A path whose entry never arrived stays open.
  // A chat question that leaves stays open on a notice: only the person's own answer closes it.
  const chat = peek !== null && isChatSession(peek.session);
  const gone = chat && ready && !open && known !== undefined;
  const closing = peek !== null && ready && (open ? open.folded !== null : known !== undefined && !chat);
  useEffect(() => {
    if (closing) leave();
  }, [closing]);
  const foldKey = (k: string, text: string) => {
    queue.fold(k, text, Date.now(), known?.key === k ? known : undefined);
    rerender();
  };
  return { open: gone ? known : open, closing, gone, foldKey };
}

export function InboxPanel({ surface, title, phone }: { surface: Surface; title: boolean; phone: boolean }) {
  const stacked = useStacked(phone);
  const { queue, entries, now, rerender } = useInboxQueue();

  const groups = [
    { id: 'answer', title: 'Needs an answer', rows: entries.filter(needsAnswer) },
    { id: 'review', title: 'To review', rows: entries.filter((e) => !needsAnswer(e)) },
  ].filter((g) => g.rows.length > 0);

  const peek = peekOf(loc.value.path);
  const search = fleetSearch.value;
  const { open, closing, gone, foldKey } = usePeekEntry(peek, entries, queue, rerender, () => go(surfacePath(surface), search, true));
  const detail = open && !closing ? <Peek key={open.key} entry={open} gone={gone} fold={(text) => foldKey(open.key, text)} /> : null;

  // Rows that open a Peek, in the order the list shows them.
  const peekable = groups.flatMap((g) => g.rows).filter((e) => e.folded === null && e.item?.kind !== 'notice');
  const onKey = useRef<(e: KeyboardEvent) => void>(() => {});
  onKey.current = (ev) => {
    // A modal sheet or dialog over the Inbox owns its keys.
    if (ev.defaultPrevented || (ev.target instanceof Element && ev.target.closest('[aria-modal="true"]'))) return;
    const a = keyAction(ev, open?.type);
    if (!a) return;
    if (a.kind === 'bridge') {
      // On a button or link Enter is that control's own action.
      if (ev.target instanceof Element && ev.target.closest('button, a[href], summary')) return;
      if (!open) return;
      const ship = ships.value.find((s) => s.session === open.session);
      if (!ship) return;
      ev.preventDefault();
      go(shipPath(ship.session), bridgeSearch(open.fleet?.key ?? ship.fleet));
    } else if (a.kind === 'move') {
      if (peekable.length === 0) return;
      ev.preventDefault();
      const at = peekable.findIndex((e) => e.key === open?.key);
      const next = at < 0 ? peekable[a.by === 1 ? 0 : peekable.length - 1] : peekable[at + a.by];
      if (!next) return;
      go(peekPath(targetOf(next)), search);
    } else if (open && !closing) {
      peekKeys.current?.(a);
    }
  };
  useEffect(() => {
    const h = (ev: KeyboardEvent) => onKey.current(ev);
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, []);

  if (entries.length === 0 && !(gone && detail)) {
    return (
      <main class="panel">
        {title ? <h1>{surface.title}</h1> : null}
        <p class="line">{emptyLine(surface, false, false)}</p>
      </main>
    );
  }
  if (stacked && detail) return <main class="panel peek-panel">{detail}</main>;
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
              <Row key={e.key} e={e} now={now} phone={phone} selected={open?.key === e.key && !closing} fold={(text) => foldKey(e.key, text)} />
            ))}
          </ul>
        </section>
      ))}
      {phone ? <p class="line">Every fleet, oldest first.</p> : <p class="key-hint">{keyHint(open?.type)}</p>}
    </main>
  );
  if (stacked) return list;
  return (
    <div class="inbox-split">
      {list}
      <div class="panel peek-panel">{detail}</div>
    </div>
  );
}
