import { Fragment, type ComponentChildren } from 'preact';
import { useState } from 'preact/hooks';
import { collapsedKey, isCollapsed, toggleCollapsed } from './collapsedRepoGroups';
import { fleetSearch } from './fleets';
import { Flag } from './Flag';
import { rowButtons } from './moored';
import { MooredActions } from './MooredActions';
import { MooredPRs } from './MooredPRs';
import { go, shipPath } from './router';
import { ShipOrder, ShipRepo } from './ShipOrder';
import { byWorkRepo, GROUP_TITLES, shipRowAge, workRepo, type ShipGroup } from './shipList';
import { ReadyToMerge, StatusGlyph } from './StatusGlyph';
import type { Ship } from './api';

function ShipRow({ ship, bar, now, label }: { ship: Ship; bar: string; now: number; label: ComponentChildren }) {
  const meta = ship.crew_live > 0 ? `${ship.crew_live} crew` : '';
  const moored = ship.status === 'moored';
  const [error, setError] = useState('');
  const search = fleetSearch.value;
  if (moored) {
    return (
      <li class="ship-row moored">
        <span class="ship-bar" style={{ background: bar }} aria-hidden="true" />
        <Flag harness={ship.harness} size="row" />
        <div class="ship-main">
          <a
            class="ship-name ship-name-link"
            href={shipPath(ship.session) + search}
            onClick={(e) => {
              if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(shipPath(ship.session), search);
            }}
          >
            {ship.name}
          </a>
          <ShipOrder ship={ship} />
          {ship.tail ? <span class="ship-tail">{ship.tail}</span> : null}
          {rowButtons(ship, false).note ? <span class="ship-tail">{rowButtons(ship, false).note}</span> : null}
          <ReadyToMerge ship={ship} />
          <MooredPRs ship={ship} now={now} />
          <MooredActions ship={ship} onError={setError} />
        </div>
        <div class="ship-side">
          {label}
          {meta ? <span class="ship-meta">{meta}</span> : null}
          <span class="ship-age">{shipRowAge(ship, now)}</span>
        </div>
        {error ? (
          <p class="alert" role="alert">
            {error}
          </p>
        ) : null}
      </li>
    );
  }
  return (
    <li class="ship-row">
      {/* The name's link covers the row, so the issue link can sit inside it without nesting one link in another. */}
      <div class="ship-link ship-link-cover">
        <span class="ship-bar" style={{ background: bar }} aria-hidden="true" />
        <Flag harness={ship.harness} size="row" />
        <div class="ship-main">
          <a
            class="ship-name ship-cover"
            href={shipPath(ship.session) + search}
            onClick={(e) => {
              if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(shipPath(ship.session), search);
            }}
          >
            {ship.name}
          </a>
          <ShipOrder ship={ship} />
          {ship.tail ? <span class="ship-tail">{ship.tail}</span> : null}
        </div>
        <div class="ship-side">
          {label}
          {meta ? <span class="ship-meta">{meta}</span> : null}
          <span class="ship-age">{shipRowAge(ship, now)}</span>
        </div>
      </div>
    </li>
  );
}

/** Renders "from <folder>" for a re-homed row, and nothing when `ship.repo` is empty. */
function RehomedFrom({ ship }: { ship: Ship }) {
  return ship.repo ? (
    <span class="ship-repo" title={ship.dir}>
      <span class="visually-hidden">Launched </span>
      from {ship.repo}
    </span>
  ) : null;
}

export function ShipStatusGroup({ group, bar, now, byRepo }: { group: ShipGroup; bar: string; now: number; byRepo: boolean }) {
  const g = group;
  return (
    <section class="ship-group" aria-labelledby={`group-${g.status}`}>
      <h2 id={`group-${g.status}`} class={`group-head group-${g.status}`}>
        <StatusGlyph status={g.status} />
        {GROUP_TITLES[g.status]}{' '}
        <span class="count">{g.ships.length}</span>
      </h2>
      {byRepo ? (
        byWorkRepo(g.ships).map((r) => {
          const key = collapsedKey(g.status, r.repo);
          const shut = isCollapsed(key);
          const listId = `repo-${key.replace(/[^a-z0-9_-]/g, '_')}`;
          return (
            <Fragment key={r.repo.toLowerCase()}>
              <button type="button" class="repo-head" aria-expanded={!shut} aria-controls={listId} onClick={() => toggleCollapsed(key)}>
                <span class="repo-chevron" aria-hidden="true" />
                <span class="repo-name">{r.repo}</span> <span class="count">{r.ships.length}</span>
              </button>
              <ul class="ship-list" id={listId} hidden={shut}>
                {r.ships.map((s) => (
                  <ShipRow key={s.session} ship={s} bar={bar} now={now} label={workRepo(s).rehomed ? <RehomedFrom ship={s} /> : null} />
                ))}
              </ul>
            </Fragment>
          );
        })
      ) : (
        <ul class="ship-list">
          {g.ships.map((s) => (
            <ShipRow key={s.session} ship={s} bar={bar} now={now} label={<ShipRepo ship={s} />} />
          ))}
        </ul>
      )}
    </section>
  );
}
