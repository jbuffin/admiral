import { Button } from './Button';
import { useEffect, useRef, useState } from 'preact/hooks';
import { activeFleet, fleetSearch } from './fleets';
import { Icon } from './Icon';
import { OrderPane } from './OrderPane';
import { selectedRow, selectionGone, withSelectedOrder } from './orderDetail';
import { AnswerReviewSheet } from './AnswerReview';
import { orderRowAction, type RowAction } from './moored';
import { ReReviewSheet } from './ReReview';
import type { MooredPR, Ship } from './api';
import { Sheet } from './Sheet';
import { LaunchForm, type OrderRef } from './LaunchDialog';
import { sourcesForFleet } from './launchForm';
import { FOR_YOU, filterRows, forYou, headerTime, launchRef, orderRef, refreshedLine, refreshTarget, repoOptions, sourceName, tabs, viewErrors, viewRows, type OrderRow } from './ordersView';
import { refreshSource } from './api';
import { go, loc, shipPath } from './router';
import { emptyLine, surfaceById } from './surfaces';
import { ships, sources } from './shipsLink';
import { useNow } from './useNow';

/** The Needs you count the nav carries; zero for no fleet or no GitHub source. */
export function needsYouCount(): number {
  const fleet = activeFleet.value;
  return fleet ? forYou(sourcesForFleet(sources.value, fleet.key), Date.now(), ships.value).needsYou.length : 0;
}

export function NeedsYouBadge() {
  const n = needsYouCount();
  return n > 0 ? <span class="nav-count needs">{n}</span> : null;
}

const MARK_PR = <path d="M4 5.1v5.8M12 10.9V6.5a2 2 0 0 0-2-2H7.5M9 3 7.5 4.5 9 6" />;

function Mark({ pr, needs }: { pr: boolean; needs: boolean }) {
  return (
    <svg class={`orow-mark${needs ? (pr ? ' pr' : ' issue') : ''}`} width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
      {pr ? (
        <>
          <circle cx="4" cy="3.5" r="1.6" />
          <circle cx="4" cy="12.5" r="1.6" />
          <circle cx="12" cy="12.5" r="1.6" />
          {MARK_PR}
        </>
      ) : (
        <>
          <circle cx="8" cy="8" r="6" />
          <circle cx="8" cy="8" r="1.2" fill="currentColor" />
        </>
      )}
    </svg>
  );
}

interface RowProps {
  row: OrderRow;
  phone: boolean;
  muted: boolean;
  selected: boolean;
  onOpen: (r: OrderRow) => void;
  onLaunch: (o: OrderRef) => void;
  onAgain: (ship: Ship, again: NonNullable<Extract<RowAction, { kind: 'ship' }>['again']>) => void;
}

function Row({ row, phone, muted, selected, onOpen, onLaunch, onAgain }: RowProps) {
  const o = row.order;
  const act = orderRowAction(ships.value, o);
  const state = row.state ? <span class={`orow-state${row.needsYou ? ' needs' : ''}`}>{row.state}</span> : null;
  const arrow =
    act.kind === 'launch' ? (
      <button type="button" class="orow-go" aria-label={`Launch ${o.title}`} onClick={() => onLaunch(launchRef(row))}>
        <Icon name="arrow" size={18} />
      </button>
    ) : (
      <>
        {act.again ? (
          <Button class="orow-again" label={act.again.kind === 'rereview' ? 'Re-review' : 'Wake'} onClick={() => onAgain(act.ship, act.again!)} />
        ) : null}
        <button type="button" class={`orow-pill${act.needsYou ? ' needs' : ''}`} aria-label={`${act.ship.name}, ${act.word}. Open ship`} onClick={() => go(shipPath(act.ship.session), fleetSearch.value)}>
          <span class="orow-pill-name">{act.ship.name}</span>
          <span class="orow-pill-word">{act.word}</span>
        </button>
      </>
    );
  if (phone) {
    return (
      <li class={`orow-phone${selected ? ' selected' : ''}`}>
        <button type="button" class="orow-open" aria-label={`Open ${o.title}`} aria-pressed={selected} onClick={() => onOpen(row)}>
          <span class="orow-main">
            <span class="orow-title">{o.title}</span>
            <span class="orow-meta">
              <span class="mono">{orderRef(o)}</span>
              {row.state ? <span>{' · '}</span> : null}
              {state}
            </span>
          </span>
        </button>
        {arrow}
      </li>
    );
  }
  return (
    <li class={`orow${muted ? ' waiting' : ''}${selected ? ' selected' : ''}`}>
      <button type="button" class="orow-open" aria-label={`Open ${o.title}`} aria-pressed={selected} onClick={() => onOpen(row)}>
        <Mark pr={!!o.pr} needs={row.needsYou} />
        <span class="orow-main">
          <span class="orow-title">{o.title}</span>
          <span class="orow-meta">
            <span class="mono">{orderRef(o)}</span>
            {row.reason ? <span>{row.reason}</span> : null}
          </span>
        </span>
        {(o.labels ?? []).map((l) => (
          <span key={l} class="orow-label">
            {l}
          </span>
        ))}
        {state}
      </button>
      {arrow}
    </li>
  );
}

function Group({ id, title, rows, phone, muted, chosen, onOpen, onLaunch, onAgain }: { id: string; title: string; rows: OrderRow[]; phone: boolean; muted: boolean; chosen?: string; onOpen: (r: OrderRow) => void; onLaunch: (o: OrderRef) => void; onAgain: RowProps['onAgain'] }) {
  if (rows.length === 0) return null;
  return (
    <section class="ogroup" aria-labelledby={id}>
      <h2 id={id} class="ogroup-head">
        {muted ? null : <span class="dot needs" aria-hidden="true" />}
        {title} <span class="count">{rows.length}</span>
      </h2>
      <ul class="ship-list">
        {rows.map((r) => (
          <Row key={r.key} row={r} phone={phone} muted={muted} selected={r.key === chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} />
        ))}
      </ul>
    </section>
  );
}

const tabOf = () => new URLSearchParams(loc.value.search).get('tab') ?? FOR_YOU;

export function OrdersPanel({ phone, title }: { phone: boolean; title: boolean }) {
  const now = useNow();
  const fleet = activeFleet.value;
  const [query, setQuery] = useState('');
  const [repo, setRepo] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [launching, setLaunching] = useState<OrderRef | null>(null);
  const [again, setAgain] = useState<{ ship: Ship; kind: 'rereview'; url: string } | { ship: Ship; kind: 'wake'; pr: MooredPR } | null>(null);
  const list = fleet ? sourcesForFleet(sources.value, fleet.key) : [];
  const all = tabs(list);
  const id = all.some((t) => t.id === tabOf()) ? tabOf() : FOR_YOU;
  const choose = (next: string) => {
    const q = new URLSearchParams(fleetSearch.value);
    if (next !== FOR_YOU) q.set('tab', next);
    setQuery('');
    setRepo('');
    setError('');
    go('/app/orders', q.size ? `?${q}` : '', true);
  };
  const time = headerTime(list, id);
  const target = refreshTarget(list, id);
  const refresh = async () => {
    if (!target) return;
    setBusy(true);
    setError('');
    const r = await refreshSource(target);
    setBusy(false);
    if (!r.ok) setError(r.error);
  };
  const rows = viewRows(list, id, now, ships.value);
  const repos = repoOptions(rows);
  const shown = filterRows(rows, {
    query,
    repo: repos.includes(repo) ? repo : '',
  });
  const errors = viewErrors(list, id);
  const needs = shown.filter((r) => r.needsYou);
  const waiting = shown.filter((r) => !r.needsYou);
  const onLaunch = (o: OrderRef) => setLaunching(o);
  const onAgain: RowProps['onAgain'] = (ship, a) => {
    if (a.kind === 'rereview') return setAgain({ ship, kind: 'rereview', url: a.url });
    const pr = (ship.prs ?? []).find((p) => p.url === a.pr);
    if (pr) setAgain({ ship, kind: 'wake', pr });
  };
  const selected = selectedRow(list, id, loc.value.search, now, ships.value);
  const chosen = selected?.key;
  const onOpen = (r: OrderRow) => go('/app/orders', withSelectedOrder(loc.value.search, r.key));
  const close = (restoreFocus = false) => {
    // The pane unmounts with the focus in it; the row's button is the same element before and after.
    const row = restoreFocus ? document.querySelector<HTMLElement>('.orow-open[aria-pressed="true"]') : null;
    go('/app/orders', withSelectedOrder(loc.value.search, null), true);
    row?.focus();
  };
  const pane = !!selected;
  const gone = selectionGone(list, id, loc.value.search, now, ships.value);
  useEffect(() => {
    if (gone) close();
  }, [gone]);
  // The listener lives as long as the panel and reads refs, so it is there the moment the pane renders, before any effect could attach it.
  // It listens in the capture phase: the Launch form's own Escape closes it and re-renders before a bubbling listener would run, so the flag would already be clear.
  const state = useRef({ pane: false, launching: false });
  state.current = { pane: pane && !phone, launching: !!launching };
  // Whether the Escape being handled began with the Launch form open; the phone sheet reads it, as it closes on the same key after the form is gone.
  const formHadEscape = useRef(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      formHadEscape.current = e.key === 'Escape' && state.current.launching;
      if (e.key !== 'Escape' || e.defaultPrevented || !state.current.pane || state.current.launching) return;
      if (e.target instanceof Element && e.target.closest('[aria-modal="true"], [role="menu"]')) return;
      close(true);
    };
    const onKeyUp = () => (formHadEscape.current = false);
    document.addEventListener('keydown', onKey, true);
    document.addEventListener('keyup', onKeyUp, true);
    return () => {
      document.removeEventListener('keydown', onKey, true);
      document.removeEventListener('keyup', onKeyUp, true);
    };
  }, []);
  const age = time ? refreshedLine(time, now) : '';
  const name = sourceName(list, id);
  const sourceLine = [name, age].filter(Boolean).join(' · ');
  return (
    <>
      <div class={`orders-split${selected && !phone ? ' has-pane' : ''}`}>
        <main class="panel orders-screen">
          <header class="orders-head">
            <div class="orders-line">
              {title ? <h1>Orders</h1> : null}
              <span class="orders-source">{phone ? age : sourceLine && `${title ? '· ' : ''}${sourceLine}`}</span>
              {target ? (
                <button type="button" class="orow-go" aria-label="Refresh now" aria-busy={busy} disabled={busy} onClick={refresh}>
                  <Icon name="refresh" size={16} />
                </button>
              ) : null}
              {phone ? null : (
                <>
                  <span class="orders-spacer" />
                  <label class="orders-filter">
                    <Icon name="search" size={14} />
                    <input type="search" placeholder="Filter by title, repo, label" value={query} onInput={(e) => setQuery(e.currentTarget.value)} />
                  </label>
                  <select class="orders-repo" aria-label="Repo" value={repo} onChange={(e) => setRepo(e.currentTarget.value)}>
                    <option value="">All repos</option>
                    {repos.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                </>
              )}
            </div>
            <div class="otabs" role="tablist" aria-label="Views">
              {all.map((t) => (
                <button key={t.id} type="button" role="tab" class="otab" aria-selected={t.id === id} onClick={() => choose(t.id)}>
                  {t.title} <span class="count">{t.count}</span>
                </button>
              ))}
            </div>
          </header>
          {error ? (
            <p class="alert" role="alert">
              {error}
            </p>
          ) : null}
          {errors.map((e) => (
            <p key={e} class="alert" role="alert">
              {e}
            </p>
          ))}
          {id === FOR_YOU ? (
            <>
              <Group id="g-needs" title="Needs you" rows={needs} phone={phone} muted={false} chosen={chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} />
              <Group id="g-waiting" title="Waiting on others" rows={waiting} phone={phone} muted chosen={chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} />
            </>
          ) : (
            <ul class="ship-list">
              {shown.map((r) => (
                <Row key={r.key} row={r} phone={phone} muted={false} selected={r.key === chosen} onOpen={onOpen} onLaunch={onLaunch} onAgain={onAgain} />
              ))}
            </ul>
          )}
          {shown.length === 0 ? <p class="line">{all.length === 1 && rows.length === 0 ? emptyLine(surfaceById('orders'), true, true) : 'Nothing here.'}</p> : null}
        </main>
        {selected && !phone ? (
          <aside class="odetail-pane" aria-label="Selected order">
            <OrderPane key={selected.key} row={selected} sources={list} now={now} heading onLaunch={onLaunch} onClose={() => close(true)} />
          </aside>
        ) : null}
      </div>
      {selected && phone ? (
        <Sheet label={selected.order.title} kind="order-sheet" onClose={() => !launching && !formHadEscape.current && close()}>
          <OrderPane key={selected.key} row={selected} sources={list} now={now} heading={false} onLaunch={onLaunch} />
        </Sheet>
      ) : null}
      {again?.kind === 'rereview' ? <ReReviewSheet ship={again.ship} sendUrl={again.url} onClose={() => setAgain(null)} /> : null}
      {again?.kind === 'wake' ? <AnswerReviewSheet ship={again.ship} pr={again.pr} onClose={() => setAgain(null)} onBusy={() => {}} /> : null}
      {launching && fleet ? <LaunchForm fleet={fleet} order={launching} onClose={() => setLaunching(null)} onLaunched={(session) => go(shipPath(session), fleetSearch.value)} /> : null}
    </>
  );
}
