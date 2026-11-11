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
import { KEYS, touchKeyboard } from '../keys';

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
  const open = atSea(ship);
  const { host, link, linkState, activity } = useBridgeTerminal({
    session: ship.session,
    open,
    gone: () => {},
    wsBase: '/ws/v1',
    probe: probeShipList,
    signedOut: () => (shipsLink.value = 'signed-out'),
  });
  const canType = open && linkState === 'open';
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };
  return (
    <>
      <div class={linkState === 'reconnecting' ? 'term-body dim' : 'term-body'}>
        <div class="term-host" ref={host} />
      </div>
      {ship.status === 'anchored' ? (
        <div class="term-keys">
          <span class="term-note">The harness exited. This is its last screen.</span>
        </div>
      ) : (
        open &&
        touchKeyboard() && (
          <div class="term-keys">
            {KEYS.map(([name, label, bytes]) => (
              <button key={name} type="button" class="term-key" aria-label={`Send ${name}`} disabled={!canType} onClick={() => input(bytes)}>
                {label}
              </button>
            ))}
          </div>
        )
      )}
    </>
  );
}

function ShipTabs({ ship, title }: { ship: Ship; title: boolean }) {
  const [tab, setTab] = useState<Tab>('Conversation');
  const [conv, setConv] = useState<Conversation>({ state: 'loading', items: [], more: false });
  const follow = useRef<ReturnType<typeof followConversation> | undefined>(undefined);
  const latest = useRef(ship);
  latest.current = ship;
  useEffect(() => {
    // Only a running Claude ship has a transcript still to come.
    const f = followConversation(ship.session, setConv, { retryNone: () => latest.current.harness === 'claude' && latest.current.status !== 'anchored' });
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
          <ConversationView conv={conv} harness={ship.harness} anchored={ship.status === 'anchored'} working={ship.status === 'underway'} openTerminal={() => setTab('Terminal')} loadOlder={() => void follow.current?.loadOlder()} />
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
