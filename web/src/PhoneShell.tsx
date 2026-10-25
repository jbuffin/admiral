import { useEffect, useRef } from 'preact/hooks';
import { boxFocused, chatUnread } from './chat';
import { ChatTab } from './ChatPane';
import { dockSheet, newShip, takeShipFocus } from './launch';
import { harnesses } from './harnesses';
import { InboxTab } from './InboxPanel';
import { clock, inbox } from './inboxState';
import { LogoFlag } from './Logos';
import { phoneTab } from './phoneTab';
import { Sidebar } from './Sidebar';
import { board, boarded, ships, type WireCrew, type WireShip } from './ships';
import { age, crewRowText } from './crew';
import { askingCount, asksYou, shipStatusLine, sortShips } from './shipList';

// The phone's shell: a tab bar under one tab body (prototype :2210-2262). Opening or closing a bridge never
// writes the tab, so Back lands on the tab the bridge was opened from.

export { phoneTab };

const needsYou = () => askingCount(ships.value);

const icon = {
  inbox: (
    <>
      <path d="M3 13l2.5-8h13L21 13v6H3z" />
      <path d="M3 13h5l1.5 3h5L16 13h5" />
    </>
  ),
  chat: <path d="M4 5h16v11H11l-5 4v-4H4z" />,
  ships: (
    <>
      <path d="M3 17c2 0 2-1.5 4.5-1.5S9.5 17 12 17s2.5-1.5 4.5-1.5S19 17 21 17" />
      <path d="M6 13l6-9 6 9z" />
    </>
  ),
  orders: (
    <>
      <path d="M7 4h10a1 1 0 0 1 1 1v15H6V5a1 1 0 0 1 1-1z" />
      <path d="M9 9h6M9 13h6M9 17h3" />
    </>
  ),
};

function TabBar() {
  const items = inbox.count();
  const asking = needsYou();
  const dot = chatUnread.value && phoneTab.value !== 'chat';
  const tabs: { id: keyof typeof icon; label: string; count: number; name: string }[] = [
    { id: 'inbox', label: 'Inbox', count: items, name: `Inbox, ${items} ${items === 1 ? 'item' : 'items'}` },
    { id: 'chat', label: 'Chat', count: 0, name: 'Chat, new reply' },
    { id: 'ships', label: 'Ships', count: asking, name: `Ships, ${asking} needing you` },
    { id: 'orders', label: 'Orders', count: 0, name: 'Orders' },
  ];
  return (
    <nav class="phone-tabs" aria-label="Sections" inert={dockSheet.value !== null}>
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          class={`press phone-tab${phoneTab.value === t.id ? ' on' : ''}`}
          aria-current={phoneTab.value === t.id ? 'page' : undefined}
          aria-label={t.count > 0 || (t.id === 'chat' && dot) ? t.name : t.label}
          onClick={() => (phoneTab.value = t.id)}
        >
          <span class="phone-tab-icon">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              {icon[t.id]}
            </svg>
            {t.count > 0 && <span class={`phone-badge ${t.id}`}>{t.count}</span>}
            {t.id === 'chat' && dot && <span class="phone-dot" aria-hidden="true" />}
          </span>
          <span class="phone-tab-label">{t.label}</span>
        </button>
      ))}
    </nav>
  );
}

function Row({ ship }: { ship: WireShip }) {
  const h = harnesses[ship.harness]?.label ?? ship.harness;
  const status = shipStatusLine(ship, clock.value);
  const asking = asksYou(ship);
  const fresh = newShip.value === ship.session;
  const ref = useRef<HTMLButtonElement>(null);
  // The row of a ship this phone just launched takes focus once, whenever it appears.
  useEffect(() => {
    if (takeShipFocus(ship.session)) ref.current?.focus();
  }, []);
  return (
    <button ref={ref} type="button" class={`press ship-row${asking ? ' asking' : ''}${fresh ? ' fresh' : ''}`} aria-label={`Board ${ship.name}, ${h} in ${ship.fleet_name}, ${status}`} onClick={() => board(ship.session)}>
      <span class="ship-row-stripe" style={`background-color:${ship.fleet_color}`} aria-hidden="true" />
      <svg width="40" height="28" viewBox="0 0 40 28" aria-hidden="true" class="ship-row-flag">
        <LogoFlag harness={ship.harness} kind="pier" />
      </svg>
      <span class="ship-row-text">
        <span class="ship-row-name">
          <b>{ship.name}</b>
          <small>
            {ship.fleet_name} · {h}
          </small>
        </span>
        <span class="ship-row-status">{status}</span>
        {(ship.order_id || ship.title) && <span class="ship-row-order">{[ship.order_id, ship.title].filter(Boolean).join(' ')}</span>}
      </span>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="ship-row-go">
        <path d="M8 5l8 7-8 7" />
      </svg>
    </button>
  );
}

// A crew boat under its ship, indented. The wire has no role (#157), so every boat is a Teammate.
function CrewRow({ ship, boat }: { ship: WireShip; boat: WireCrew }) {
  const asking = boat.status === 'needs_you';
  const anchored = boat.status === 'anchored';
  const state = asking ? ', needs you' : anchored ? ', anchored' : '';
  return (
    <button
      type="button"
      class={`press crew-row${asking ? ' asking' : ''}${anchored ? ' anchored' : ''}`}
      aria-label={`Board ${boat.name}, teammate, crew of ${ship.name}${state}`}
      onClick={() => board(ship.session, boat.name)}
    >
      <span class="ship-row-stripe" style={`background-color:${anchored ? '#9AA5AF' : ship.fleet_color}`} aria-hidden="true" />
      <span class="ship-row-text">
        <b class="crew-row-name">{boat.name}</b>
        <span class="ship-row-status">{crewRowText(boat)}</span>
      </span>
      {asking && <span class="crew-badge ask" aria-hidden="true">!</span>}
      {anchored && <span class="crew-badge out" aria-hidden="true">✕</span>}
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="ship-row-go">
        <path d="M8 5l8 7-8 7" />
      </svg>
    </button>
  );
}

// Subagents have no terminal, so the note is not a button.
function SubagentNote({ ship }: { ship: WireShip }) {
  return (
    <div class="subagent-note" role="note">
      <b>Subagents · {ship.subagents.length} · no terminal</b>
      {ship.subagents.map((a) => (
        <span key={a.id}>
          {a.type} · {age(a.start, clock.value)}
        </span>
      ))}
    </div>
  );
}

function ShipsTab() {
  const list = sortShips(ships.value);
  const asking = askingCount(list);
  const hint = `${list.length} ${list.length === 1 ? 'ship' : 'ships'}${asking ? `, ${asking} ${asking === 1 ? 'needs' : 'need'} you` : ''}`;
  return (
    <section class="phone-pane ships-tab" aria-label="Ships">
      <div class="phone-pane-head">
        <h2>Ships</h2>
        <span>{hint}</span>
      </div>
      {list.map((s) => (
        <div class="ship-group" key={s.session}>
          <Row ship={s} />
          {(s.crew ?? []).map((c) => (
            <CrewRow key={c.name} ship={s} boat={c} />
          ))}
          {(s.subagents ?? []).length > 0 && <SubagentNote ship={s} />}
        </div>
      ))}
    </section>
  );
}

export function PhoneShell({ children }: { children?: preact.ComponentChildren }) {
  return (
    <>
      <div class="phone-body">
        {/* The pane under a bridge is out of reach of VoiceOver and Tab. */}
        <div class="phone-tabs-pane" inert={boarded.value !== null || dockSheet.value !== null}>
        {phoneTab.value === 'inbox' && <InboxTab />}
        {phoneTab.value === 'chat' && <ChatTab />}
        {phoneTab.value === 'ships' && <ShipsTab />}
        {phoneTab.value === 'orders' && <Sidebar />}
        </div>
        {children}
      </div>
      {/* While the chat's box has focus the bar steps aside, so the keyboard has room. */}
      {boarded.value === null && !(phoneTab.value === 'chat' && boxFocused.value) && <TabBar />}
    </>
  );
}
