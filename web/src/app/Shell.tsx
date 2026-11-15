import { Fragment, type ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, loaded } from './fleets';
import { Flag } from './Flag';
import { FleetSwitcher } from './FleetSwitcher';
import { Icon } from './Icon';
import { Preferences } from './Preferences';
import { Sheet } from './Sheet';
import { GROUP_TITLES, groupShips, shipRowAge } from './shipList';
import { useNow } from './useNow';
import { rowButtons } from './moored';
import { MooredActions } from './MooredActions';
import { MooredPRs } from './MooredPRs';
import { LaunchForm } from './LaunchDialog';
import { forYouLinkText } from './orderDetail';
import { sourcesForFleet } from './launchForm';
import { watchPresence } from './presence';
import { ToastRegion } from './ToastRegion';
import { ships, shipsLink, shipsLoaded, sources } from './shipsLink';
import { signedOut } from './session';
import { Unreachable } from './Unreachable';
import { PairScreen } from './PairScreen';
import { InstallSheet } from './InstallPrompts';
import { gateOf } from './pairing';
import { ReadyToMerge, StatusGlyph } from './StatusGlyph';
import { call, type Ship } from './api';
import { ShipOrder, ShipRepo } from './ShipOrder';
import { CrewPage } from './CrewPage';
import { RoutinesPage } from './RoutinesPage';
import { StandingOrdersPage } from './StandingOrdersPage';
import { holdFire } from './holdFireLink';
import { holdFireBanner } from './routines';
import { campaignOf, crewOf, go, loc, routineCreating, routineOf, shipPath, shipSession, standingOrdersTab, surfaceOf, surfacePath } from './router';
import { CampaignPage, CampaignSidebar, CampaignsList } from './CampaignPage';
import { ChatPanel } from './ChatPanel';
import { NeedsYouBadge, OrdersPanel } from './OrdersScreen';
import { InboxPanel } from './InboxPanel';
import { watchAttention } from './attention';
import { watchPush } from './pushLink';
import { parseBuildStamp } from './buildCheck';
import { ShipView } from './ShipView';
import { emptyLine, MORE, PHONE_TABS, SURFACES, surfaceById, type Surface } from './surfaces';

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

function EmptyState({ surface, title, children }: { surface: Surface; title: boolean; children?: ComponentChildren }) {
  const fleet = activeFleet.value;
  return (
    <main class="panel">
      {title ? <h1>{surface.title}</h1> : null}
      <p class="line">{emptyLine(surface, loaded.value, !!fleet)}</p>
      {surface.scoped && fleet ? (
        <p class="fleet-label">
          <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
          {fleet.name}
        </p>
      ) : null}
      {children}
    </main>
  );
}

function ShipRow({ ship, bar, now }: { ship: Ship; bar: string; now: number }) {
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
          <ShipRepo ship={ship} />
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
          <ShipRepo ship={ship} />
          {meta ? <span class="ship-meta">{meta}</span> : null}
          <span class="ship-age">{shipRowAge(ship, now)}</span>
        </div>
      </div>
    </li>
  );
}

function ShipsPanel({ surface, title }: { surface: Surface; title: boolean }) {
  const now = useNow();
  const fleet = activeFleet.value;
  const groups = fleet ? groupShips(ships.value, fleet.key) : [];
  const fleetKey = fleet?.key;
  // Fire and forget: the poller pushes the new chips over the ships socket.
  useEffect(() => {
    if (fleetKey) void call('POST', '/moored/refresh');
  }, [fleetKey]);
  const [launching, setLaunching] = useState(false);
  const launch = fleet && fleet.docks.length > 0 ? (
    <button type="button" class="btn primary launch-open" onClick={() => setLaunching(true)}>
      Launch
    </button>
  ) : null;
  const form = launching && fleet ? <LaunchForm fleet={fleet} onClose={() => setLaunching(false)} onLaunched={(session) => go(shipPath(session), fleetSearch.value)} /> : null;
  const forYouText = fleet ? forYouLinkText(sourcesForFleet(sources.value, fleet.key), Date.now()) : '';
  const forYouLink = forYouText ? (
    <a
      class="for-you-link"
      href={'/app/orders' + fleetSearch.value}
      onClick={(e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        go('/app/orders', fleetSearch.value);
      }}
    >
      {forYouText}
    </a>
  ) : null;
  if (shipsLoaded.value && groups.length === 0) {
    return (
      <>
        <EmptyState surface={surface} title={title}>
          {launch}
        </EmptyState>
        {form}
      </>
    );
  }
  return (
    <>
      <main class="panel">
        <div class="panel-head">
          {title ? <h1>{surface.title}</h1> : null}
          {launch}
        </div>
        {fleet ? (
          <p class="fleet-label">
            <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
            {fleet.name}
          </p>
        ) : null}
        {forYouLink}
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
      {form}
    </>
  );
}

function Panel({ surface, title, phone }: { surface: Surface; title: boolean; phone?: boolean }) {
  const session = shipSession(loc.value.path);
  if (session !== null) return <ShipView session={session} title={title} />;
  if (surface.id === 'inbox') return <InboxPanel surface={surface} title={title} phone={!!phone} />;
  if (surface.id === 'chat') return <ChatPanel surface={surface} title={title} phone={!!phone} />;
  if (surface.id === 'ships') return <ShipsPanel surface={surface} title={title} />;
  if (surface.id === 'orders' && activeFleet.value) return <OrdersPanel key={activeFleet.value.key} phone={!!phone} title={title} />;
  if (surface.id === 'campaigns') {
    const id = campaignOf(loc.value.path);
    return id ? <CampaignPage key={id} id={id} title={title} /> : <CampaignsList phone={!!phone} title={title} />;
  }
  if (surface.id === 'routines') {
    const fleet = activeFleet.value;
    if (fleet && standingOrdersTab(loc.value.path)) return <StandingOrdersPage key={fleet.key} fleet={fleet} title={title} />;
    if (fleet) {
      return <RoutinesPage key={fleet.key} fleet={fleet} id={routineOf(loc.value.path)} creating={routineCreating(loc.value.path)} title={title} phone={!!phone} />;
    }
  }
  if (surface.id === 'crew') {
    const fleet = activeFleet.value;
    if (fleet) return <CrewPage key={fleet.key} fleet={fleet} name={crewOf(loc.value.path)} title={title} phone={!!phone} />;
  }
  return <EmptyState surface={surface} title={title} />;
}

function HoldFireBanner() {
  const fleet = activeFleet.value;
  const b = fleet && holdFire.value?.fleet === fleet.key ? holdFireBanner(holdFire.value.state, fleet.name, new Date()) : null;
  if (!b) return null;
  return (
    <div class="holdfire-banner" role="status">
      <span>{b.text}</span>
      <a
        class="btn"
        href={'/app/routines' + fleetSearch.value}
        onClick={(e) => {
          if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
          e.preventDefault();
          go('/app/routines', fleetSearch.value);
        }}
      >
        Routines
      </a>
    </div>
  );
}

function Stage({ surface, title, phone }: { surface: Surface; title: boolean; phone?: boolean }) {
  return (
    <div class="stage">
      <HoldFireBanner />
      {surface.id === 'preferences' ? <Preferences phone={!!phone} /> : <Panel surface={surface} title={title} phone={phone} />}
    </div>
  );
}

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
      <Stage surface={surface} title={false} phone />
      <nav class="tabbar" aria-label="Main">
        {PHONE_TABS.map((id) => (
          <Link key={id} surface={surfaceById(id)} class="tab">
            <Icon name={id} size={20} />
            {surfaceById(id).title}
            {id === 'orders' ? <NeedsYouBadge /> : null}
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
  useEffect(() => watchPresence(), []);
  const phone = usePhone();
  useEffect(() => watchAttention(), []);
  useEffect(() => watchPush(), []);
  const surface = surfaceOf(loc.value.path) ?? SURFACES[0];
  const gate = signedOut.value ? 'pair' : gateOf(shipsLink.value);
  // An overlay, not a replacement: a blip must not unmount a Launch sheet with typed work in it.
  return (
    <>
      {phone ? <PhoneShell surface={surface} /> : <DesktopShell surface={surface} />}
      <ToastRegion />
      {gate === 'unreachable' ? <Unreachable /> : null}
      {gate === 'pair' ? <PairScreen /> : null}
      {shipsLink.value === 'open' ? <InstallSheet /> : null}
    </>
  );
}

const build = parseBuildStamp(document.querySelector('meta[name="admiral-build"]')?.getAttribute('content') ?? null);

function DesktopShell({ surface }: { surface: Surface }) {
  return (
    <div class="shell">
      <nav class="side" aria-label="Main">
        <FleetSwitcher sheet={false} />
        {SURFACES.map((s) => (
          <Fragment key={s.id}>
            <Link surface={s} class="nav-link">
              <Icon name={s.id} />
              {s.title}
              {s.id === 'orders' ? <NeedsYouBadge /> : null}
            </Link>
            {s.id === 'campaigns' ? <CampaignSidebar /> : null}
          </Fragment>
        ))}
        {build ? <span class="side-version">{build.version}</span> : null}
      </nav>
      <Stage surface={surface} title />
    </div>
  );
}
