import { useEffect, useRef, useState } from 'preact/hooks';
import { probeShipList, type Ship } from './api';
import { followConversation, type Conversation } from './conversation';
import { ConversationView } from './ConversationView';
import { fleetSearch } from './fleets';
import { go, surfacePath } from './router';
import { ships, shipsLink, shipsLoaded } from './shipsLink';
import { SURFACES } from './surfaces';
import { useBridgeTerminal } from '../useBridgeTerminal';
import { atSea } from '../crew';

const TABS = ['Conversation', 'Terminal', 'Changes', 'PR'] as const;
type Tab = (typeof TABS)[number];

const shipsSurface = SURFACES.find((s) => s.id === 'ships')!;
const back = () => go(surfacePath(shipsSurface), fleetSearch.value);

function BackLink({ label }: { label: string }) {
  return (
    <a
      class="back-link"
      href={surfacePath(shipsSurface) + fleetSearch.value}
      onClick={(e) => {
        if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        back();
      }}
    >
      {label}
    </a>
  );
}

function TerminalTab({ ship }: { ship: Ship }) {
  const { host } = useBridgeTerminal({
    session: ship.session,
    open: atSea(ship),
    gone: () => {},
    wsBase: '/ws/v1',
    probe: probeShipList,
    signedOut: () => (shipsLink.value = 'signed-out'),
  });
  return <div class="term-host" ref={host} />;
}

function ShipTabs({ ship, title }: { ship: Ship; title: boolean }) {
  const [tab, setTab] = useState<Tab>('Conversation');
  const [conv, setConv] = useState<Conversation>({ state: 'loading', items: [], more: false });
  const follow = useRef<ReturnType<typeof followConversation> | undefined>(undefined);
  useEffect(() => {
    const f = followConversation(ship.session, setConv, { retryNone: ship.harness === 'claude' });
    follow.current = f;
    return f.stop;
  }, [ship.session]);

  const Heading = title ? 'h1' : 'h2';
  return (
    <section class="ship-view">
      <div class="ship-view-head">
        <BackLink label="Back" />
        <Heading>{ship.name}</Heading>
      </div>
      <div role="tablist" class="view-tabs" aria-label={`${ship.name} views`}>
        {TABS.map((t) => (
          <button key={t} type="button" role="tab" id={`tab-${t}`} aria-selected={tab === t} aria-controls="view-panel" class="view-tab" onClick={() => setTab(t)}>
            {t}
          </button>
        ))}
      </div>
      <div role="tabpanel" id="view-panel" aria-labelledby={`tab-${tab}`} class={`view-panel${tab === 'Terminal' ? ' terminal' : ''}`}>
        {tab === 'Conversation' ? (
          <ConversationView conv={conv} harness={ship.harness} working={ship.status === 'underway'} openTerminal={() => setTab('Terminal')} loadOlder={() => void follow.current?.loadOlder()} />
        ) : tab === 'Terminal' ? (
          <TerminalTab ship={ship} />
        ) : tab === 'Changes' ? (
          <p class="line">Changes arrive in a later update.</p>
        ) : (
          <p class="line">No pull request yet</p>
        )}
      </div>
    </section>
  );
}

/** One ship: Conversation, Terminal, Changes and PR. The view opens on Conversation every time. */
export function ShipView({ session, title }: { session: string; title: boolean }) {
  const ship = ships.value.find((s) => s.session === session);
  if (!ship) {
    // The list has not arrived yet, or the ship left it: sailed off, dismissed or stopped.
    return (
      <main class="panel ship-gone">
        {shipsLoaded.value ? (
          <>
            <p class="convo-card-title">This ship is gone.</p>
            <BackLink label="Back" />
          </>
        ) : (
          <p class="line">Loading…</p>
        )}
      </main>
    );
  }
  return (
    <main class="panel ship-main-view">
      <ShipTabs key={session} ship={ship} title={title} />
    </main>
  );
}
