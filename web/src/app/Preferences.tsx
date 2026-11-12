import { useEffect } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch, fleets, loaded } from './fleets';
import { capabilitiesPath, chatSettingsPath, fleetPath, go, loc, preferencesFleet, preferencesPage, remotePath, sourcesPath, usageAllPath, usagePath, type PreferencesPage } from './router';
import { AllUsagePage, FleetUsagePage } from './UsagePage';
import { RemoteAccess } from './RemoteAccessPage';
import { ChatSettingsPage } from './ChatSettingsPage';
import { FleetPage } from './FleetPage';
import { HarnessCapabilities } from './HarnessCapabilities';
import { OrderSources } from './OrderSources';

const TITLES: Record<PreferencesPage, string> = { fleet: 'Fleet', admiralbot: 'AdmiralBot', sources: 'Order sources', capabilities: 'Harness capabilities', usage: 'Usage', 'all-usage': 'Usage', remote: 'Remote access' };

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

function PreferencesNav({ current, page }: { current: string; page: PreferencesPage }) {
  return (
    <nav class="prefs-nav" aria-label="Preferences">
      <div role="group" aria-labelledby="admiral-global-prefs">
        <div class="sec" id="admiral-global-prefs">
          Admiral · all fleets
        </div>
        <NavLink path={remotePath} current={page === 'remote'}>
          Remote access
        </NavLink>
        <NavLink path={usageAllPath} current={page === 'all-usage'}>
          Usage
        </NavLink>
      </div>
      {fleets.value.map((f) => (
        <div key={f.key} role="group" aria-labelledby={`prefs-${f.key}`}>
          <div class="sec" id={`prefs-${f.key}`}>
            <span class="dot" style={{ background: fleetPaint(f).fill }} aria-hidden="true" />
            {f.name} fleet
          </div>
          <NavLink path={fleetPath(f.key)} current={f.key === current && page === 'fleet'}>
            Fleet
          </NavLink>
          <NavLink path={chatSettingsPath(f.key)} current={f.key === current && page === 'admiralbot'}>
            AdmiralBot
          </NavLink>
          <NavLink path={sourcesPath(f.key)} current={f.key === current && page === 'sources'}>
            Order sources
          </NavLink>
          <NavLink path={capabilitiesPath(f.key)} current={f.key === current && page === 'capabilities'}>
            Harness capabilities
          </NavLink>
          <NavLink path={usagePath(f.key)} current={f.key === current && page === 'usage'}>
            Usage
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

  // Bare and unknown paths open the active fleet's sources, else Remote access; signals are read at run time because a render can see the config half-loaded.
  useEffect(() => {
    const open = activeFleet.value;
    if (!loaded.value || page === 'remote' || page === 'all-usage' || fleets.value.some((f) => f.key === key)) return;
    go(open ? sourcesPath(open.key) : remotePath, fleetSearch.value, true);
  }, [loaded.value, fleets.value, key, page]);

  const Title = phone ? 'h2' : 'h1';
  return (
    <main class="panel prefs">
      <PreferencesNav current={fleet?.key ?? ''} page={page} />
      <div class="prefs-page">
        {page === 'remote' ? (
          <>
            <Title>{TITLES.remote}</Title>
            <p class="fleet-label">All fleets</p>
            <RemoteAccess />
          </>
        ) : page === 'all-usage' ? (
          <>
            <Title>{TITLES.usage}</Title>
            <p class="fleet-label">All fleets</p>
            <AllUsagePage phone={phone} />
          </>
        ) : fleet ? (
          <>
            <Title>{TITLES[page]}</Title>
            <p class="fleet-label">
              <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
              {fleet.name} fleet
            </p>
            {page === 'fleet' ? <FleetPage key={fleet.key} fleet={fleet} /> : page === 'admiralbot' ? <ChatSettingsPage key={fleet.key} fleet={fleet} /> : page === 'usage' ? <FleetUsagePage key={fleet.key} fleetKey={fleet.key} phone={phone} /> : page === 'capabilities' ? <HarnessCapabilities key={fleet.key} fleet={fleet} /> : <OrderSources key={fleet.key} fleet={fleet} />}
          </>
        ) : null}
      </div>
    </main>
  );
}
