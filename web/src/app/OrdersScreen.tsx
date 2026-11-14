import { useState } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch } from './fleets';
import { Icon } from './Icon';
import { LaunchForm, type OrderRef } from './LaunchDialog';
import { sourcesForFleet } from './launchForm';
import { FOR_YOU, filterRows, forYou, headerTime, launchRef, orderRef, refreshTarget, repoOptions, sourceName, tabs, viewErrors, viewRows, type OrderRow } from './ordersView';
import { refreshSource } from './api';
import { go, loc, shipPath } from './router';
import { shipAge } from './shipList';
import { sources } from './shipsLink';
import { useNow } from './useNow';

/** The Needs you count the nav carries; zero for no fleet or no GitHub source. */
export function needsYouCount(): number {
  const fleet = activeFleet.value;
  return fleet ? forYou(sourcesForFleet(sources.value, fleet.key), Date.now()).needsYou.length : 0;
}

export function NeedsYouBadge() {
  const n = needsYouCount();
  return n > 0 ? <span class="nav-count needs">{n}</span> : null;
}

const MARK_PR = <path d="M4 5.1v5.8M12 10.9V6.5a2 2 0 0 0-2-2H7.5M9 3 7.5 4.5 9 6" />;

function Mark({ pr }: { pr: boolean }) {
  return (
    <svg class="orow-mark" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
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

function Row({ row, phone, muted, onLaunch }: { row: OrderRow; phone: boolean; muted: boolean; onLaunch: (o: OrderRef) => void }) {
  const o = row.order;
  const state = row.state ? <span class={`orow-state${row.needsYou ? ' needs' : ''}`}>{row.state}</span> : null;
  const arrow = (
    <button type="button" class="orow-go" aria-label={`Launch ${o.title}`} onClick={() => onLaunch(launchRef(row))}>
      <Icon name="arrow" size={18} />
    </button>
  );
  if (phone) {
    return (
      <li class="orow phone">
        <div class="orow-main">
          <span class="orow-title">{o.title}</span>
          <span class="orow-meta">
            <span class="mono">{orderRef(o)}</span>
            {row.state ? <span>{' · '}</span> : null}
            {state}
          </span>
        </div>
        {arrow}
      </li>
    );
  }
  return (
    <li class={`orow${muted ? ' muted' : ''}`}>
      <Mark pr={!!o.pr} />
      <div class="orow-main">
        <span class="orow-title">{o.title}</span>
        <span class="orow-meta">
          <span class="mono">{orderRef(o)}</span>
          {row.reason ? <span>{row.reason}</span> : null}
        </span>
      </div>
      {(o.labels ?? []).map((l) => (
        <span key={l} class="orow-label">
          {l}
        </span>
      ))}
      {state}
      {arrow}
    </li>
  );
}

function Group({ id, title, rows, phone, muted, onLaunch }: { id: string; title: string; rows: OrderRow[]; phone: boolean; muted: boolean; onLaunch: (o: OrderRef) => void }) {
  if (rows.length === 0) return null;
  return (
    <section class="ogroup" aria-labelledby={id}>
      <h2 id={id} class="ogroup-head">
        {muted ? null : <span class="dot needs" aria-hidden="true" />}
        {title} <span class="count">{rows.length}</span>
      </h2>
      <ul class="ship-list">
        {rows.map((r) => (
          <Row key={r.key} row={r} phone={phone} muted={muted} onLaunch={onLaunch} />
        ))}
      </ul>
    </section>
  );
}

const tabOf = () => new URLSearchParams(loc.value.search).get('tab') ?? FOR_YOU;

export function OrdersPanel({ phone }: { phone: boolean }) {
  const now = useNow();
  const fleet = activeFleet.value;
  const [query, setQuery] = useState('');
  const [repo, setRepo] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [launching, setLaunching] = useState<OrderRef | null>(null);
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
  const rows = viewRows(list, id, now);
  const repos = repoOptions(rows);
  const shown = filterRows(rows, { query, repo: repos.includes(repo) ? repo : '' });
  const errors = viewErrors(list, id);
  const needs = shown.filter((r) => r.needsYou);
  const waiting = shown.filter((r) => !r.needsYou);
  const onLaunch = (o: OrderRef) => setLaunching(o);
  const age = time ? `refreshed ${shipAge(time, now)} ago` : '';
  const name = sourceName(list, id);
  return (
    <>
      <main class="panel orders-screen">
        <header class="orders-head">
          <div class="orders-line">
            {phone && fleet ? <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" /> : null}
            <span class="orders-source">{phone ? age : [name, age].filter(Boolean).join(' · ')}</span>
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
            <Group id="g-needs" title="Needs you" rows={needs} phone={phone} muted={false} onLaunch={onLaunch} />
            <Group id="g-waiting" title="Waiting on others" rows={waiting} phone={phone} muted onLaunch={onLaunch} />
          </>
        ) : (
          <ul class="ship-list">
            {shown.map((r) => (
              <Row key={r.key} row={r} phone={phone} muted={false} onLaunch={onLaunch} />
            ))}
          </ul>
        )}
        {shown.length === 0 ? <p class="line">Nothing here.</p> : null}
      </main>
      {launching && fleet ? <LaunchForm fleet={fleet} order={launching} onClose={() => setLaunching(null)} onLaunched={(session) => go(shipPath(session), fleetSearch.value)} /> : null}
    </>
  );
}
