import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, loaded } from './fleets';
import { Flag } from './Flag';
import { FleetSwitcher } from './FleetSwitcher';
import { Icon } from './Icon';
import { Sheet } from './Sheet';
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

function Docks() {
  const fleet = activeFleet.value;
  return fleet && fleet.docks.length > 0 ? (
    <ul class="docks">
      {fleet.docks.map((d) => (
        <li key={d.harness}>
          <Flag harness={d.harness} size="header" />
          <span>{d.harness[0].toUpperCase() + d.harness.slice(1)}</span>
        </li>
      ))}
    </ul>
  ) : null;
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
      {surface.id === 'ships' ? <Docks /> : null}
    </main>
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
      <EmptyState surface={surface} title={false} />
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
      <EmptyState surface={surface} title />
    </div>
  );
}
