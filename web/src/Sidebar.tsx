import { signal } from '@preact/signals';
import { useState } from 'preact/hooks';
import { AddSource } from './AddSource';
import { closeInbox, inboxDrawerOpen } from './inboxState';
import { Menu, MenuItem } from './Menu';
import { PanelSection, ReorderGrip, SourceProblem, countLabel } from './PanelSection';
import { arranged, reordering, saveSourceOrder } from './reorder';
import { SettingsDialog, openSettings } from './SettingsDialog';
import { SignInCard } from './SignInCard';
import { SourceIcon } from './SourceIcon';
import { type Source, api, can, declares, filter, panelViews, setSidebarCollapsed, sidebarCollapsed, sourcePath, sources, sourcesLoaded } from './sources';

const Chevrons = ({ right }: { right: boolean }) => (
  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d={right ? 'M11 6l6 6-6 6M5 6l6 6-6 6' : 'M13 6l-6 6 6 6M19 6l-6 6 6 6'} />
  </svg>
);

// Expanding Orders closes the inbox drawer, which collapses it while it is open.
function expand() {
  closeInbox(false);
  setSidebarCollapsed(false);
}

export function Sidebar() {
  return (
    <>
      {sidebarCollapsed.value || inboxDrawerOpen.value ? <Rail /> : <Expanded />}
      <SettingsDialog />
    </>
  );
}

function Rail() {
  return (
    <aside class="orders rail" aria-label="Orders, collapsed">
      <button type="button" class="rail-toggle press" aria-label="Expand orders" onClick={expand}>
        <Chevrons right />
      </button>
      {panelViews.value.map((v) => (
        <button
          key={v.key}
          type="button"
          class="rail-panel press"
          aria-label={`${v.title}, ${countLabel(v.orders.length)}`}
          style={{ backgroundColor: v.color }}
          onClick={() => {
            expand();
            requestAnimationFrame(() => document.getElementById(`panel-${v.key}`)?.scrollIntoView({ block: 'nearest' }));
          }}
        >
          {v.source.icon_url ? <img class="source-icon" src={v.source.icon_url} alt="" width={30} height={30} /> : v.title.charAt(0).toUpperCase()}
          <span class="rail-count" aria-hidden="true">
            {v.orders.length}
          </span>
        </button>
      ))}
    </aside>
  );
}

const orderRefusal = signal<string | null>(null);

function Expanded() {
  const list = sources.value;
  return (
    <aside class="orders" aria-label="Orders">
      <div class="orders-head">
        <div>
          <h2>Orders</h2>
          <span class="orders-sub">Drag a card onto a dock to launch</span>
        </div>
        <button type="button" class="rail-toggle small press" aria-label="Collapse orders" onClick={() => setSidebarCollapsed(true)}>
          <Chevrons right={false} />
        </button>
      </div>
      <div class="filter">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true">
          <circle cx="11" cy="11" r="6.5" />
          <path d="M16 16l4.5 4.5" />
        </svg>
        <input
          type="text"
          aria-label="Filter orders"
          placeholder="Filter by title, ID or link"
          autocomplete="off"
          value={filter.value}
          onInput={(e) => (filter.value = (e.target as HTMLInputElement).value)}
        />
      </div>
      {orderRefusal.value && <p role="alert">{orderRefusal.value}</p>}
      {sourcesLoaded.value &&
        arranged('sources', list!, (s) => s.key).map((s) => {
          const lifted = reordering.value?.group === 'sources' && reordering.value.key === s.key;
          return (
            <div class={lifted ? 'source-block lifted' : 'source-block'} role="group" aria-label={s.name} key={s.key} data-reorder="sources" data-key={s.key}>
              <SourceRow source={s} />
              {arranged(
                `panels:${s.key}`,
                panelViews.value.filter((v) => v.source.key === s.key),
                (v) => v.panel.id,
              ).map((v) => (
                <PanelSection key={v.key} view={v} />
              ))}
            </div>
          );
        })}
      <AddSource />
    </aside>
  );
}

const confirming = signal<string | null>(null);

function SourceRow({ source }: { source: Source }) {
  const [refusal, setRefusal] = useState<string | null>(null);
  const noPanels = source.panels.length === 0;
  return (
    <div class="source">
      <div class="source-row">
        {sources.value!.length > 1 && (
          <ReorderGrip
            label={`Move ${source.name}`}
            group="sources"
            itemKey={source.key}
            keys={sources.value!.map((s) => s.key)}
            commit={(keys) => {
              orderRefusal.value = null;
              saveSourceOrder(keys, (m) => (orderRefusal.value = m));
            }}
          />
        )}
        <SourceIcon source={source} size={24} />
        <span class="source-name">{source.name}</span>
        {source.state === 'starting' && <span class="source-state">Starting…</span>}
        <Menu id={`source:${source.key}`} label="Source actions">
          <MenuItem onSelect={() => openSettings(source.key)}>Settings</MenuItem>
          {declares(source, 'refresh') && (
            <MenuItem
              disabled={!can(source, 'refresh')}
              onSelect={async () => {
                const r = await api('POST', sourcePath(source.key, '/refresh'));
                setRefusal(r.ok ? null : r.error);
              }}
            >
              Refresh
            </MenuItem>
          )}
          <MenuItem onSelect={() => (confirming.value = source.key)}>Remove</MenuItem>
        </Menu>
      </div>
      {refusal && <p role="alert">{refusal}</p>}
      {confirming.value === source.key && <ConfirmRemove source={source} />}
      {noPanels && source.state === 'auth_required' && source.auth && <SignInCard auth={source.auth} />}
      {noPanels && source.state === 'error' && <SourceProblem sourceKey={source.key} message={source.error} />}
    </div>
  );
}

function ConfirmRemove({ source }: { source: Source }) {
  const [error, setError] = useState<string | null>(null);
  return (
    <div class="confirm">
      <span>Remove {source.name}?</span>
      <button
        type="button"
        class="small-button press"
        onClick={async () => {
          const r = await api('DELETE', sourcePath(source.key));
          if (r.ok) confirming.value = null;
          else setError(r.error);
        }}
      >
        Confirm remove
      </button>
      <button type="button" class="small-button press" onClick={() => (confirming.value = null)}>
        Cancel
      </button>
      {error && <p role="alert">{error}</p>}
    </div>
  );
}
