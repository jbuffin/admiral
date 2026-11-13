import { signal } from '@preact/signals';
import { Fragment, type ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, loaded } from './fleets';
import { Flag } from './Flag';
import { FleetSwitcher } from './FleetSwitcher';
import { Icon } from './Icon';
import { Preferences } from './Preferences';
import { Sheet } from './Sheet';
import { GROUP_TITLES, groupShips, shipRowAge } from './shipList';
import { carryingShip, nextStep, prChips, rowButtons } from './moored';
import { MooredActions } from './MooredActions';
import { MooredPRs } from './MooredPRs';
import { LaunchForm, type OrderRef } from './LaunchDialog';
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
import { call, type Order, type Ship } from './api';
import { ShipOrder, webUrl } from './ShipOrder';
import { CrewPage } from './CrewPage';
import { RoutinesPage } from './RoutinesPage';
import { StandingOrdersPage } from './StandingOrdersPage';
import { holdFire } from './holdFireLink';
import { holdFireBanner } from './routines';
import { automateSearch, campaignOf, crewOf, go, loc, routineNewPath, routineCreating, routineOf, shipPath, shipSession, standingOrdersTab, surfaceOf, surfacePath } from './router';
import { CampaignPage, CampaignSidebar, CampaignsList } from './CampaignPage';
import { followReference } from './campaignsLink';
import { ChatPanel } from './ChatPanel';
import { InboxPanel } from './InboxPanel';
import { watchAttention } from './attention';
import { watchPush } from './pushLink';
import { MergeControl } from './PeekMerge';
import { Failure } from './PeekItem';
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
          {meta ? <span class="ship-meta">{meta}</span> : null}
          <span class="ship-age">{shipRowAge(ship, now)}</span>
        </div>
      </div>
    </li>
  );
}

// Outside the row, since the row remounts when its ship leaves the list while the source still lists the PR.
const mergedOrders = signal<ReadonlySet<string>>(new Set());

function OrderRow({ order: o, source, onLaunch, onFollowError }: { order: Order; source: string; onLaunch: (o: OrderRef) => void; onFollowError: (e: string) => void }) {
  const [error, setError] = useState('');
  const mergedKey = `${source}/${o.id}`;
  const merged = mergedOrders.value.has(mergedKey);
  const ship = o.pr && o.url ? carryingShip(ships.value, o.url) : undefined;
  const step = ship ? nextStep(ship) : undefined;
  const launch = () => onLaunch({ source, id: o.id, title: o.title, text: o.text || o.title });
  return (
    <li class="order-row">
      <span class="mono order-id">{o.id}</span>
      {webUrl(o.url) ? (
        <a class="order-title issue-link" href={o.url} target="_blank" rel="noopener noreferrer">
          {o.title}
        </a>
      ) : (
        <span class="order-title">{o.title}</span>
      )}
      {ship || merged ? null : (
        <button type="button" class="btn" aria-label={o.pr ? undefined : `Launch ${o.title}`} onClick={launch}>
          {o.pr ? 'Launch Commodore' : 'Launch'}
        </button>
      )}
      {o.url && (o.sub_issues ?? 0) > 0 ? (
        <button type="button" class="btn" aria-label={`Follow ${o.title}`} onClick={async () => onFollowError((await followReference(o.url!)) ?? '')}>
          Follow
        </button>
      ) : null}
      {o.pr ? (
        <div class="order-state">
          {prChips(o.pr).map((c) => (
            <span key={c.text} class={`chip chip-${c.tone}`}>
              {c.text}
            </span>
          ))}
          {ship ? (
            <>
              <a
                class="ship-name-link"
                href={shipPath(ship.session) + fleetSearch.value}
                onClick={(e) => {
                  if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                  e.preventDefault();
                  go(shipPath(ship.session), fleetSearch.value);
                }}
              >
                {ship.name}
              </a>
              {step?.ready ? <ReadyToMerge ship={ship} /> : step?.text ? <span class="ship-tail">{step.text}</span> : null}
            </>
          ) : null}
        </div>
      ) : null}
      {merged ? (
        <p class="line">Merged</p>
      ) : ship?.stack_ready ? (
        <MergeControl ship={ship} onMerged={() => (mergedOrders.value = new Set(mergedOrders.value).add(mergedKey))} onError={setError} />
      ) : null}
      <Failure message={error} />
    </li>
  );
}

function OrdersList({ fleetKey, onLaunch }: { fleetKey: string; onLaunch: (o: OrderRef) => void }) {
  const [followError, setFollowError] = useState('');
  const groups = sourcesForFleet(sources.value, fleetKey)
    .map((src) => ({ src, orders: src.views.flatMap((v) => v.orders) }))
    .filter((g) => g.orders.length > 0);
  if (groups.length === 0) return null;
  return (
    <section class="orders" aria-labelledby="orders-head">
      <h2 id="orders-head" class="group-head">
        Orders
      </h2>
      {followError ? (
        <p class="alert" role="alert">
          {followError}
        </p>
      ) : null}
      {groups.map(({ src }) =>
        src.views
          .filter((v) => v.orders.length > 0)
          .map((v) => (
            <div key={`${src.key}/${v.id}`} class="order-view">
              <div class="order-view-bar">
                <h3 class="order-view-head">{src.views.length > 1 ? `${src.name}: ${v.title}` : src.name}</h3>
                <button type="button" class="btn" onClick={() => go(routineNewPath, automateSearch(src.key, v.id, fleetKey))}>
                  Automate…
                </button>
              </div>
              <ul class="ship-list">
                {v.orders.map((o) => (
                  <OrderRow key={o.id} order={o} source={src.key} onLaunch={onLaunch} onFollowError={setFollowError} />
                ))}
              </ul>
            </div>
          )),
      )}
    </section>
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
  const [launching, setLaunching] = useState<{ order?: OrderRef } | null>(null);
  const launch = fleet && fleet.docks.length > 0 ? (
    <button type="button" class="btn primary launch-open" onClick={() => setLaunching({})}>
      Launch
    </button>
  ) : null;
  const form = launching && fleet ? <LaunchForm fleet={fleet} order={launching.order} onClose={() => setLaunching(null)} onLaunched={(session) => go(shipPath(session), fleetSearch.value)} /> : null;
  const orders = fleet ? <OrdersList fleetKey={fleet.key} onLaunch={(order) => setLaunching({ order })} /> : null;
  if (shipsLoaded.value && groups.length === 0) {
    return (
      <>
        <EmptyState surface={surface} title={title}>
          {launch}
          {orders}
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
        {orders}
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
            </Link>
            {s.id === 'campaigns' ? <CampaignSidebar /> : null}
          </Fragment>
        ))}
      </nav>
      <Stage surface={surface} title />
    </div>
  );
}
