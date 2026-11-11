import { useEffect, useRef, useState } from 'preact/hooks';
import { call, probeShipList, type Changes, type CrewBoat, type Ship } from './api';
import { splitDiff } from './changes';
import { followConversation, type Conversation } from './conversation';
import { cardFor } from './answerCard';
import { ConversationView } from './ConversationView';
import { Composer, useComposer, type Target } from './Composer';
import { boatBoxState, chipState, flotilla } from './flotilla';
import { Icon } from './Icon';
import { MooredPRs } from './MooredPRs';
import { NO_RESUME_NOTE } from './moored';
import { boxState } from './messageBox';
import { Sheet } from './Sheet';
import { fleetSearch } from './fleets';
import { go, surfacePath } from './router';
import { ships, shipsLink, shipsLoaded } from './shipsLink';
import { SURFACES } from './surfaces';
import { useBridgeTerminal } from '../useBridgeTerminal';
import { atSea } from '../crew';
import { KEYS, touchKeyboard } from '../keys';

// Harnesses whose own record Admiral reads, so a ship's conversation is still to come while it runs.
const hasTranscript = (harness: string) => harness === 'claude' || harness === 'copilot';

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

function StopShip({ ship }: { ship: Ship }) {
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cancel = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (asking) cancel.current?.focus();
  }, [asking]);
  const stop = async () => {
    setBusy(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/stop`);
    setBusy(false);
    if (!r.ok) return setError(r.error);
    setAsking(false);
    back();
  };
  return (
    <>
      <button
        type="button"
        class="btn danger stop-ship"
        onClick={() => {
          setError('');
          setAsking(true);
        }}
      >
        Stop ship
      </button>
      {asking ? (
        <Sheet label="Stop ship" kind="launch-backdrop stop-backdrop" onClose={() => !busy && setAsking(false)}>
          <p class="line">Stop {ship.name}? Its harness and crew end now.</p>
          {error ? (
            <p class="launch-error" role="alert">
              {error}
            </p>
          ) : null}
          <div class="launch-foot">
            <button type="button" ref={cancel} class="btn" disabled={busy} onClick={() => setAsking(false)}>
              Cancel
            </button>
            <button type="button" class="btn danger" disabled={busy} onClick={() => void stop()}>
              Stop ship
            </button>
          </div>
        </Sheet>
      ) : null}
    </>
  );
}

function TerminalTab({ ship, boat }: { ship: Ship; boat?: CrewBoat }) {
  const open = boat ? boat.status !== 'anchored' : atSea(ship);
  const { host, link, linkState, activity } = useBridgeTerminal({
    session: ship.session,
    crew: boat?.name,
    open,
    gone: () => {},
    wsBase: '/ws/v1',
    probe: () => probeShipList(boat?.name),
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
      {!open && (boat || ship.status === 'anchored' || ship.status === 'moored') ? (
        <div class="term-keys">
          <span class="term-note">{boat ? 'This boat exited. This is its last screen.' : ship.status === 'moored' ? "A moored ship has no session until it's woken." : 'The harness exited. This is its last screen.'}</span>
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

type Load = { state: 'loading' } | { state: 'error'; error: string } | { state: 'done'; changes: Changes };

/** The ship's diff against its base, read each time the tab opens. It never joins the bridge. */
function ChangesTab({ ship }: { ship: Ship }) {
  const [load, setLoad] = useState<Load>({ state: 'loading' });
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    setLoad({ state: 'loading' });
    void call<Changes>('GET', `/ships/${encodeURIComponent(ship.session)}/changes`).then((r) => {
      if (live) setLoad(r.ok ? { state: 'done', changes: r.data } : { state: 'error', error: r.error });
    });
    return () => {
      live = false;
    };
  }, [ship.session, attempt]);

  if (load.state === 'loading') return <p class="line">Loading…</p>;
  if (load.state === 'error')
    return (
      <div class="changes-error">
        <p class="alert" role="alert">
          {load.error}
        </p>
        <button type="button" class="btn" onClick={() => setAttempt(attempt + 1)}>
          Retry
        </button>
      </div>
    );
  const { changes } = load;
  if (changes.state === 'none') return <p class="line">No git history to compare</p>;
  if (changes.files.length === 0) return <p class="line">No changes</p>;
  const diffs = splitDiff(changes.diff);
  return (
    <div class="changes">
      <ul class="change-files">
        {changes.files.map((f) => (
          <li key={f.path} class="change-file">
            <span class="change-path">{f.path}</span>
            <span class={`change-status ${f.status}`}>{f.status}</span>
            <span class="change-add">+{f.added}</span>
            <span class="change-del">−{f.removed}</span>
          </li>
        ))}
      </ul>
      {changes.truncated ? <p class="msg-note">The diff was cut: it is too large to show in full.</p> : null}
      {diffs.map((d, n) => (
        <section key={`${d.path}\0${n}`} class="diff" aria-labelledby={`diff-path-${n}`}>
          <h3 class="diff-path" id={`diff-path-${n}`}>
            {d.path}
          </h3>
          <pre class="diff-body">
            {d.hunks.length === 0 ? <span class="diff-note">No text changes</span> : null}
            {d.hunks.map((h, i) => (
              <span key={i} class="diff-hunk">
                <span class="diff-head">{h.header}</span>
                {h.lines.map((l, n) => (
                  <span key={n} class={l[0] === '+' ? 'diff-line add' : l[0] === '-' ? 'diff-line del' : 'diff-line'}>
                    {l}
                  </span>
                ))}
              </span>
            ))}
          </pre>
        </section>
      ))}
    </div>
  );
}

/** One chip for the ship and one per boat. A boat's chip stays after it exits and says how it ended. */
function Flotilla({ ship, picked, pick }: { ship: Ship; picked: string | null; pick: (crew: string | null) => void }) {
  const chips = flotilla(ship);
  const group = useRef<HTMLDivElement>(null);
  // The picked chip's "finished" or "error" word, which appears after the pick, must not sit cut off at the row's edge.
  useEffect(() => {
    group.current?.querySelector<HTMLElement>('[aria-checked=true]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  }, [picked, chips.find((c) => c.crew === picked)?.state]);
  const move = (e: KeyboardEvent, i: number) => {
    const step = e.key === 'ArrowRight' || e.key === 'ArrowDown' ? 1 : e.key === 'ArrowLeft' || e.key === 'ArrowUp' ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = chips[(i + step + chips.length) % chips.length];
    pick(next.crew);
    (e.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLElement>('[role=radio]')[chips.indexOf(next)]?.focus();
  };
  return (
    <div role="radiogroup" aria-label="Flotilla" class="flotilla" ref={group}>
      {chips.map((c, i) => (
        <button key={c.crew ?? ''} type="button" role="radio" aria-checked={c.crew === picked} tabIndex={c.crew === picked ? 0 : -1} class={`chip ${c.state}`} onClick={() => pick(c.crew)} onKeyDown={(e) => move(e, i)}>
          <Icon name={c.crew === null ? 'ships' : 'crew'} size={16} />
          <span class="chip-label">{c.label}</span>
          {c.crew !== null ? <span class="chip-name">{c.crew}</span> : null}
          {c.state === 'finished' ? <span class="chip-state">finished</span> : c.state === 'error' ? <span class="chip-state">error</span> : null}
        </button>
      ))}
    </div>
  );
}

function ShipTabs({ ship, title }: { ship: Ship; title: boolean }) {
  const [tab, setTab] = useState<Tab>('Conversation');
  const [crew, setCrew] = useState<string | null>(null);
  const [conv, setConv] = useState<Conversation>({ state: 'loading', items: [], more: false });
  const follow = useRef<ReturnType<typeof followConversation> | undefined>(undefined);
  const latest = useRef(ship);
  latest.current = ship;
  // A boat that left the ship's list hands the view back to the ship.
  const boat = crew === null ? undefined : ship.crew.find((b) => b.name === crew);
  const boatName = boat?.name;
  // Forget a boat that left the crew, so a later boat of the same name does not take the view back.
  useEffect(() => {
    if (crew !== null && !boat) setCrew(null);
  }, [crew, boat]);
  const boatHasConversation = boat?.conversation ?? false;
  const moored = ship.status === 'moored';
  useEffect(() => {
    setConv({ state: 'loading', items: [], more: false });
    // A moored ship has no session to read a conversation from; its rounds' transcripts come later.
    if (moored) {
      setConv({ state: 'none', items: [], more: false });
      follow.current = undefined;
      return;
    }
    if (boatName !== undefined && !boatHasConversation) {
      setConv({ state: 'none', items: [], more: false });
      follow.current = undefined;
      return;
    }
    // Only a running Claude or Copilot ship has a transcript still to come; a boat is followed once its transcript is recorded.
    const f = followConversation(ship.session, setConv, { crew: boatName, retryNone: () => boatName === undefined && hasTranscript(latest.current.harness) && latest.current.status !== 'anchored' });
    follow.current = f;
    return f.stop;
  }, [ship.session, boatName, boatHasConversation, moored]);

  const target: Target = boat
    ? { session: ship.session, crew: boat.name, state: boatBoxState(boat), interruptible: boat.status === 'underway', hasConversation: boatHasConversation && conv.state === 'ok', subject: 'This boat' }
    : { session: ship.session, state: boxState(ship), interruptible: ship.status === 'underway', hasConversation: conv.state === 'ok' || (conv.state === 'none' && hasTranscript(ship.harness)), subject: 'This ship', wake: moored };
  const box = useComposer(target, conv);
  const Heading = title ? 'h1' : 'h2';
  const anchored = boat ? chipState(boat) !== 'running' : ship.status === 'anchored';
  return (
    <section class="ship-view">
      <div class="ship-view-head">
        <BackLink label="Back" />
        <Heading>{ship.name}</Heading>
        {moored ? (
          <button type="button" class="btn primary stop-ship" disabled={ship.no_resume || box.waking} onClick={() => void box.wake()}>
            Wake
          </button>
        ) : (
          <StopShip ship={ship} />
        )}
      </div>
      {moored ? (
        <div class="moored-info">
          <p class="line">This ship is moored: its PRs are open and it has no session until it's woken.</p>
          <MooredPRs ship={ship} now={Date.now()} />
          {ship.no_resume ? <p class="msg-note">{NO_RESUME_NOTE}</p> : null}
          {tab !== 'Conversation' && box.error ? (
            <p class="alert" role="alert">
              {box.error}
            </p>
          ) : null}
        </div>
      ) : null}
      {ship.crew.length > 0 ? <Flotilla ship={ship} picked={boatName ?? null} pick={setCrew} /> : null}
      <div role="tablist" class="view-tabs" aria-label={`${ship.name} views`}>
        {TABS.map((t) => (
          <button key={t} type="button" role="tab" id={`tab-${t}`} aria-selected={tab === t} aria-controls="view-panel" class="view-tab" onClick={() => setTab(t)}>
            {t}
          </button>
        ))}
      </div>
      <div role="tabpanel" id="view-panel" aria-labelledby={`tab-${tab}`} class={`view-panel${tab === 'Terminal' ? ' terminal' : ''}`}>
        {tab === 'Conversation' ? (
          <>
            <div class="convo-area">
              {moored ? (
                <div class="convo-card">
                  <p class="convo-card-title">Moored, waiting on review</p>
                  <p class="line">Send a message or tap Wake to start the next round.</p>
                </div>
              ) : (
                <ConversationView
                conv={conv}
                harness={boat ? boat.harness : ship.harness}
                card={cardFor(ship, boat, conv)}
                session={ship.session}
                crew={boat?.name}
                anchored={anchored}
                working={boat ? boat.status === 'underway' : ship.status === 'underway'}
                noneTitle={boat ? 'No conversation for this boat' : undefined}
                openTerminal={() => setTab('Terminal')}
                loadOlder={() => void follow.current?.loadOlder()}
              />
              )}
            </div>
            <Composer target={target} box={box} />
          </>
        ) : tab === 'Terminal' ? (
          <TerminalTab ship={ship} boat={boat} />
        ) : tab === 'Changes' ? (
          <ChangesTab ship={ship} />
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
