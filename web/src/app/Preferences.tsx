import { useEffect } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, fleets, loaded } from './fleets';
import { fleetPath, go, loc, preferencesFleet, preferencesPage, sourcesPath } from './router';
import { FleetPage } from './FleetPage';
import { OrderSources } from './OrderSources';

function NavLink({ path, current, children }: { path: string; current: boolean; children: string }) {
  const search = fleetSearch.value;
  return (
    <a
      class="nav-link"
      href={path + search}
      aria-current={current ? 'page' : undefined}
      onClick={(e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        go(path, search);
      }}
    >
      {children}
    </a>
  );
}

function PreferencesNav({ current, page }: { current: string; page: 'fleet' | 'sources' }) {
  return (
    <nav class="prefs-nav" aria-label="Preferences">
      {fleets.value.map((f) => (
        <div key={f.key} role="group" aria-labelledby={`prefs-${f.key}`}>
          <div class="sec" id={`prefs-${f.key}`}>
            <span class="dot" style={{ background: fleetPaint(f).fill }} aria-hidden="true" />
            {f.name} fleet
          </div>
          <NavLink path={fleetPath(f.key)} current={f.key === current && page === 'fleet'}>
            Fleet
          </NavLink>
          <NavLink path={sourcesPath(f.key)} current={f.key === current && page === 'sources'}>
            Order sources
          </NavLink>
        </div>
      ))}
    </nav>
  );
}

export function Preferences({ phone }: { phone: boolean }) {
  const key = preferencesFleet(loc.value.path);
  const fleet = fleets.value.find((f) => f.key === key);
  const page = preferencesPage(loc.value.path);
  const fallback = activeFleet.value;

  // Bare /app/preferences, and a path naming no fleet, open the active fleet's sources.
  useEffect(() => {
    if (loaded.value && !fleet && fallback) go(sourcesPath(fallback.key), fleetSearch.value, true);
  }, [loaded.value, fleet, fallback]);

  const Title = phone ? 'h2' : 'h1';
  return (
    <main class="panel prefs">
      {fleets.value.length > 0 ? <PreferencesNav current={fleet?.key ?? ''} page={page} /> : null}
      <div class="prefs-page">
        {fleet ? (
          <>
            <Title>{page === 'fleet' ? 'Fleet' : 'Order sources'}</Title>
            <p class="fleet-label">
              <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
              {fleet.name} fleet
            </p>
            {page === 'fleet' ? <FleetPage key={fleet.key} fleet={fleet} /> : <OrderSources key={fleet.key} fleet={fleet} />}
          </>
        ) : loaded.value && !fallback ? (
          <p class="line">Set up a fleet and its settings will appear here.</p>
        ) : null}
      </div>
    </main>
  );
}
