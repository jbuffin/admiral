import { useEffect } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, fleets, loaded } from './fleets';
import { go, loc, preferencesFleet, sourcesPath } from './router';
import { OrderSources } from './OrderSources';

function PreferencesNav({ current }: { current: string }) {
  const search = fleetSearch.value;
  return (
    <nav class="prefs-nav" aria-label="Preferences">
      {fleets.value.map((f) => (
        <div key={f.key} role="group" aria-labelledby={`prefs-${f.key}`}>
          <div class="sec" id={`prefs-${f.key}`}>
            <span class="dot" style={{ background: fleetPaint(f).fill }} aria-hidden="true" />
            {f.name} fleet
          </div>
          <a
            class="nav-link"
            href={sourcesPath(f.key) + search}
            aria-current={f.key === current ? 'page' : undefined}
            onClick={(e) => {
              if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(sourcesPath(f.key), search);
            }}
          >
            Order sources
          </a>
        </div>
      ))}
    </nav>
  );
}

export function Preferences({ phone }: { phone: boolean }) {
  const key = preferencesFleet(loc.value.path);
  const fleet = fleets.value.find((f) => f.key === key);
  const fallback = activeFleet.value;

  // Bare /app/preferences, and a path naming no fleet, open the active fleet's sources.
  useEffect(() => {
    if (loaded.value && !fleet && fallback) go(sourcesPath(fallback.key), fleetSearch.value, true);
  }, [loaded.value, fleet, fallback]);

  const Title = phone ? 'h2' : 'h1';
  return (
    <main class="panel prefs">
      {fleets.value.length > 0 ? <PreferencesNav current={fleet?.key ?? ''} /> : null}
      <div class="prefs-page">
        {fleet ? (
          <>
            <Title>Order sources</Title>
            <p class="fleet-label">
              <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
              {fleet.name} fleet
            </p>
            <OrderSources key={fleet.key} fleet={fleet} />
          </>
        ) : loaded.value && !fallback ? (
          <p class="line">Set up a fleet and its settings will appear here.</p>
        ) : null}
      </div>
    </main>
  );
}
