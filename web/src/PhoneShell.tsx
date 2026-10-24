import { signal } from '@preact/signals';
import { harnesses } from './harnesses';
import { reasonWords, waited } from './inbox';
import { InboxTab } from './InboxPanel';
import { clock, inbox } from './inboxState';
import { LogoFlag } from './Logos';
import { Sidebar } from './Sidebar';
import { board, boardedSession, ships, type WireShip } from './ships';
import { sortShips } from './shipList';

// The phone's shell: a tab bar under one tab body (prototype :2210-2262). Opening or closing a bridge never
// writes the tab, so Back lands on the tab the bridge was opened from.

type Tab = 'inbox' | 'chat' | 'ships' | 'orders';
export const phoneTab = signal<Tab>('inbox');

const needsYou = () => ships.value.filter((s) => s.status === 'needs_you').length;

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
  const tabs: { id: Tab; label: string; count: number; name: string }[] = [
    { id: 'inbox', label: 'Inbox', count: items, name: `Inbox, ${items} ${items === 1 ? 'item' : 'items'}` },
    { id: 'chat', label: 'Chat', count: 0, name: 'Chat' },
    { id: 'ships', label: 'Ships', count: asking, name: `Ships, ${asking} needing you` },
    { id: 'orders', label: 'Orders', count: 0, name: 'Orders' },
  ];
  return (
    <nav class="phone-tabs" aria-label="Sections">
      {tabs.map((t) => (
        <button
          key={t.id}
          type="button"
          class={`press phone-tab${phoneTab.value === t.id ? ' on' : ''}`}
          aria-current={phoneTab.value === t.id ? 'page' : undefined}
          aria-label={t.count > 0 ? t.name : t.label}
          onClick={() => (phoneTab.value = t.id)}
        >
          <span class="phone-tab-icon">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              {icon[t.id]}
            </svg>
            {t.count > 0 && <span class={`phone-badge ${t.id}`}>{t.count}</span>}
          </span>
          <span class="phone-tab-label">{t.label}</span>
        </button>
      ))}
    </nav>
  );
}

const exitWords = (s: WireShip) => (s.exit_signal ? `killed (${s.exit_signal})` : `exited ${s.exit_code ?? '?'}`);

function statusLine(s: WireShip): string {
  if (s.status === 'needs_you') return `Needs you · ${reasonWords(s.reason ?? 'unknown')} · waiting ${waited(s.stop_since ?? '', clock.value)}`;
  if (s.status === 'anchored') return `Anchored, ${exitWords(s)}`;
  return 'Underway';
}

function Row({ ship }: { ship: WireShip }) {
  const h = harnesses[ship.harness]?.label ?? ship.harness;
  const status = statusLine(ship);
  const asking = ship.status === 'needs_you';
  return (
    <button type="button" class={`press ship-row${asking ? ' asking' : ''}`} aria-label={`Board ${ship.name}, ${h} in ${ship.fleet_name}, ${status}`} onClick={() => board(ship.session)}>
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

function ShipsTab() {
  const list = sortShips(ships.value);
  const asking = needsYou();
  const hint = `${list.length} ${list.length === 1 ? 'ship' : 'ships'}${asking ? `, ${asking} ${asking === 1 ? 'needs' : 'need'} you` : ''}`;
  return (
    <section class="phone-pane ships-tab" aria-label="Ships">
      <div class="phone-pane-head">
        <h2>Ships</h2>
        <span>{hint}</span>
      </div>
      {list.map((s) => (
        <Row key={s.session} ship={s} />
      ))}
    </section>
  );
}

// The chat is u6's: until then its tab is empty.
const ChatTab = () => <section class="phone-pane" aria-label="Chat" />;

export function PhoneShell({ children }: { children?: preact.ComponentChildren }) {
  return (
    <>
      <div class="phone-body">
        {phoneTab.value === 'inbox' && <InboxTab />}
        {phoneTab.value === 'chat' && <ChatTab />}
        {phoneTab.value === 'ships' && <ShipsTab />}
        {phoneTab.value === 'orders' && <Sidebar />}
        {children}
      </div>
      {boardedSession.value === null && <TabBar />}
    </>
  );
}
