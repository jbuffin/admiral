import { type ComponentChildren } from 'preact';
import { useId, useRef, useState } from 'preact/hooks';
import { isCollapsed, toggleCollapsed } from './collapsedRepoGroups';
import { fleetSearch } from './fleets';
import { Flag } from './Flag';
import { useRefocus } from './hooks/refocus';
import { rowButtons } from './moored';
import { MooredActions } from './MooredActions';
import { MooredPRs } from './MooredPRs';
import { applyRepoOrder, moveRepo } from './repoGroupOrder';
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

type Drop = 'before' | 'after' | null;

type Move = { lifted: boolean; drop: Drop; refocus: boolean; onRefocused: () => void; onPointerDown: (e: PointerEvent) => void; onPointerMove: (e: PointerEvent) => void; onPointerUp: (e: PointerEvent) => void; onEnd: () => void; onKeyDown: (e: KeyboardEvent) => void };

function RepoGroup({ status, repo, ships, bar, now, move }: { status: string; repo: string; ships: Ship[]; bar: string; now: number; move: Move | null }) {
  const listId = useId();
  const handleRef = useRef<HTMLButtonElement>(null);
  useRefocus(move?.refocus ?? false, handleRef, move?.onRefocused ?? (() => {}));
  const shut = isCollapsed(status, repo);
  const cls = ['repo-group', move?.lifted ? 'lifted' : '', move?.drop ? `drop-${move.drop}` : ''].filter(Boolean).join(' ');
  return (
    <div class={cls} data-repo={repo.toLowerCase()}>
      <div class="repo-row">
        {move ? (
          <button
            type="button"
            class="repo-grip"
            aria-label={`Move ${repo}`}
            ref={handleRef}
            onPointerDown={move.onPointerDown}
            onPointerMove={move.onPointerMove}
            onPointerUp={move.onPointerUp}
            onPointerCancel={move.onEnd}
            onLostPointerCapture={move.onEnd}
            onKeyDown={move.onKeyDown}
          >
            <span aria-hidden="true">⠿</span>
          </button>
        ) : null}
        <button type="button" class="repo-head" aria-expanded={!shut} aria-controls={listId} onClick={() => toggleCollapsed(status, repo)}>
          <span class="repo-chevron" aria-hidden="true" />
          <span class="repo-name">{repo}</span> <span class="count">{ships.length}</span>
        </button>
      </div>
      <ul class="ship-list" id={listId} hidden={shut}>
        {ships.map((s) => (
          <ShipRow key={s.session} ship={s} bar={bar} now={now} label={workRepo(s).rehomed ? <RehomedFrom ship={s} /> : null} />
        ))}
      </ul>
    </div>
  );
}

// A press must travel this far before it is a drag, so a tap never reorders.
const DRAG_THRESHOLD = 4;

type Gesture = { repo: string; id: number; startY: number; dragging: boolean };

function RepoGroups({ status, ships, bar, now }: { status: string; ships: Ship[]; bar: string; now: number }) {
  const groups = applyRepoOrder(byWorkRepo(ships));
  const shown = groups.map((r) => r.repo);
  const ref = useRef<HTMLDivElement>(null);
  const gesture = useRef<Gesture | null>(null);
  const [dragState, setDrag] = useState<{ repo: string; to: number } | null>(null);
  const [refocus, setRefocus] = useState('');
  const movable = groups.length > 1;
  // A drag whose group is gone or no longer movable must not outlive it: its handle can't report the end.
  const drag = dragState && movable && shown.includes(dragState.repo) ? dragState : null;
  if (dragState && !drag) {
    gesture.current = null;
    setDrag(null);
  }

  // Counts the other groups whose midpoint is above the pointer: that is the dragged group's new place.
  const placeAt = (repo: string, y: number) =>
    [...(ref.current?.querySelectorAll<HTMLElement>('.repo-group') ?? [])].filter((el) => el.dataset.repo !== repo.toLowerCase()).filter((el) => {
      const r = el.getBoundingClientRect();
      return y > (r.top + r.bottom) / 2;
    }).length;

  const end = () => {
    gesture.current = null;
    setDrag(null);
  };

  // The marker sits in front of the group the dragged one would land before, or behind the last group at the end.
  const dropMarker = (repo: string): Drop => {
    if (!drag || drag.repo === repo) return null;
    const others = shown.filter((r) => r !== drag.repo);
    const at = others.indexOf(repo);
    if (at === drag.to) return 'before';
    return drag.to === others.length && at === others.length - 1 ? 'after' : null;
  };

  const moveFor = (repo: string): Move => {
    return {
      lifted: drag?.repo === repo,
      drop: dropMarker(repo),
      refocus: refocus === repo,
      onRefocused: () => setRefocus(''),
      onPointerDown: (e) => {
        if (e.button !== 0) return;
        gesture.current = { repo, id: e.pointerId, startY: e.clientY, dragging: false };
        try {
          (e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId);
        } catch {
          /* the pointer is already gone */
        }
      },
      onPointerMove: (e) => {
        const g = gesture.current;
        if (!g || g.id !== e.pointerId) return;
        if (!g.dragging && Math.abs(e.clientY - g.startY) < DRAG_THRESHOLD) return;
        g.dragging = true;
        setDrag({ repo, to: placeAt(repo, e.clientY) });
      },
      onPointerUp: (e) => {
        const g = gesture.current;
        if (g?.dragging && g.id === e.pointerId) moveRepo(shown, repo, placeAt(repo, e.clientY));
        end();
      },
      onEnd: end,
      onKeyDown: (e) => {
        const at = shown.indexOf(repo);
        const to = e.key === 'ArrowUp' ? at - 1 : e.key === 'ArrowDown' ? at + 1 : at;
        if (to === at) return;
        e.preventDefault();
        if (to < 0 || to >= shown.length) return;
        moveRepo(shown, repo, to);
        setRefocus(repo);
      },
    };
  };

  return (
    <div ref={ref}>
      {groups.map((r) => (
        <RepoGroup key={r.repo.toLowerCase()} status={status} repo={r.repo} ships={r.ships} bar={bar} now={now} move={movable ? moveFor(r.repo) : null} />
      ))}
    </div>
  );
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
        <RepoGroups status={g.status} ships={g.ships} bar={bar} now={now} />
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
