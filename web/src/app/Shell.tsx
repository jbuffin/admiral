import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, loaded } from './fleets';
import { Flag } from './Flag';
import { FleetSwitcher } from './FleetSwitcher';
import { Icon } from './Icon';
import { Preferences } from './Preferences';
import { Sheet } from './Sheet';
import { GROUP_TITLES, groupShips, shipAge } from './shipList';
import { ships, shipsLoaded } from './shipsLink';
import { StatusGlyph } from './StatusGlyph';
import type { Ship } from './api';
import { go, loc, surfaceOf, surfacePath } from './router';
import { MORE, PHONE_TABS, SURFACES, surfaceById, type Surface } from './surfaces';

const PHONE_QUERY = '(max-width: 640px)';

function usePhone() {
  const mq = matchMedia(PHONE_QUERY);
  const [phone, setPhone] = useState(mq.matches);
  useEffect(() => {
    const on = () => setPhone(mq.matches);
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, []);
  return phone;
}

function Link({ surface, class: cls, onNavigate, children }: { surface: Surface; class: string; onNavigate?: () => void; children: ComponentChildren }) {
  const current = surfaceOf(loc.value.path)?.id === surface.id;
  const search = fleetSearch.value;
  return (
    <a
      class={cls}
      href={surfacePath(surface) + search}
      aria-current={current ? 'page' : undefined}
      onClick={(e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        go(surfacePath(surface), search);
        onNavigate?.();
      }}
    >
      {children}
    </a>
  );
}

function EmptyState({ surface, title }: { surface: Surface; title: boolean }) {
  const fleet = activeFleet.value;
  return (
    <main class="panel">
      {title ? <h1>{surface.title}</h1> : null}
      <p class="line">{surface.scoped && loaded.value && !fleet ? `Set up a fleet and its ${surface.title.toLowerCase()} will appear here.` : surface.line}</p>
      {surface.scoped && fleet ? (
        <p class="fleet-label">
          <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
          {fleet.name}
        </p>
      ) : null}
    </main>
  );
}

const HEX = /^#[0-9a-f]{3,8}$/i;

// Ages count from the launch time, so they refresh on a timer rather than on a ships message.
function useNow() {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  return now;
}

function ShipRow({ ship, bar, now }: { ship: Ship; bar: string; now: number }) {
  const meta = ship.crew_live > 0 ? `${ship.crew_live} crew` : '';
  return (
    <li class="ship-row">
      <span class="ship-bar" style={{ background: HEX.test(ship.fleet_color) ? ship.fleet_color : bar }} aria-hidden="true" />
      <Flag harness={ship.harness} size="row" />
      <div class="ship-main">
        <span class="ship-name">{ship.name}</span>
        {ship.order_id ? (
          <span class="ship-order">
            <span class="mono">{ship.order_id}</span> {ship.title}
          </span>
        ) : null}
        {ship.tail ? <span class="ship-tail">{ship.tail}</span> : null}
      </div>
      <div class="ship-side">
        {meta ? <span class="ship-meta">{meta}</span> : null}
        <span class="ship-age">{shipAge(ship.launched, now)}</span>
      </div>
    </li>
  );
}

function ShipsPanel({ surface, title }: { surface: Surface; title: boolean }) {
  const now = useNow();
  const fleet = activeFleet.value;
  const groups = fleet ? groupShips(ships.value, fleet.key) : [];
  if (shipsLoaded.value && groups.length === 0) return <EmptyState surface={surface} title={title} />;
  return (
    <main class="panel">
      {title ? <h1>{surface.title}</h1> : null}
      {fleet ? (
        <p class="fleet-label">
          <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
          {fleet.name}
        </p>
      ) : null}
      {groups.map((g) => (
        <section key={g.status} class="ship-group" aria-labelledby={`group-${g.status}`}>
          <h2 id={`group-${g.status}`} class={`group-head group-${g.status}`}>
            <StatusGlyph status={g.status} />
            {GROUP_TITLES[g.status]}{' '}
            <span class="count">{g.ships.length}</span>
          </h2>
          <ul class="ship-list">
            {g.ships.map((s) => (
              <ShipRow key={s.session} ship={s} bar={fleetPaint(fleet!).fill} now={now} />
            ))}
          </ul>
        </section>
      ))}
    </main>
  );
}

const Panel = ({ surface, title }: { surface: Surface; title: boolean }) =>
  surface.id === 'ships' ? <ShipsPanel surface={surface} title={title} /> : <EmptyState surface={surface} title={title} />;

function MoreSheet({ onClose }: { onClose: () => void }) {
  return (
    <Sheet label="More" onClose={onClose}>
      {MORE.map((id) => (
        <Link key={id} surface={surfaceById(id)} class="menu-item" onNavigate={onClose}>
          <Icon name={id} />
          {surfaceById(id).title}
        </Link>
      ))}
    </Sheet>
  );
}

function PhoneShell({ surface }: { surface: Surface }) {
  const [more, setMore] = useState(false);
  const moreBtn = useRef<HTMLButtonElement>(null);
  const closeMore = () => {
    setMore(false);
    moreBtn.current?.focus();
  };
  return (
    <div class="shell phone">
      <header class="phone-header">
        <FleetSwitcher sheet />
        <h1>{surface.title}</h1>
      </header>
      {surface.id === 'preferences' ? <Preferences phone /> : <Panel surface={surface} title={false} />}
      <nav class="tabbar" aria-label="Main">
        {PHONE_TABS.map((id) => (
          <Link key={id} surface={surfaceById(id)} class="tab">
            <Icon name={id} size={20} />
            {surfaceById(id).title}
          </Link>
        ))}
        <button ref={moreBtn} type="button" class="tab" aria-haspopup="dialog" aria-expanded={more} onClick={() => setMore(true)}>
          <Icon name="more" size={20} />
          More
        </button>
      </nav>
      {more ? <MoreSheet onClose={closeMore} /> : null}
    </div>
  );
}

export function Shell() {
  const phone = usePhone();
  const surface = surfaceOf(loc.value.path) ?? SURFACES[0];
  if (phone) return <PhoneShell surface={surface} />;
  return (
    <div class="shell">
      <nav class="side" aria-label="Main">
        <FleetSwitcher sheet={false} />
        {SURFACES.map((s) => (
          <Link key={s.id} surface={s} class="nav-link">
            <Icon name={s.id} />
            {s.title}
          </Link>
        ))}
      </nav>
      {surface.id === 'preferences' ? <Preferences phone={false} /> : <Panel surface={surface} title />}
    </div>
  );
}
