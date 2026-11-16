import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { call, probeShipList, SLOW_TIMEOUT_MS, type CrewBoat, type Ship } from './api';
import { isRunning, runningFor } from './shipList';
import { useNow } from './useNow';
import { ShipOrder, ShipRepo } from './ShipOrder';
import { followConversation, type Conversation } from './conversation';
import { cardFor } from './answerCard';
import { ConversationView } from './ConversationView';
import { hasConversationAdapter } from './conversationGate';
import { Actions, Button } from './Button';
import { Composer, InterruptButton, useComposer, type Target } from './Composer';
import { boatBoxState, chipState, flotilla } from './flotilla';
import { Icon } from './Icon';
import { PRTab } from './ShipPRs';
import { openCount, prSummary } from './prTab';
import { ReadyToMerge } from './StatusGlyph';
import { useSeenOnLeave } from './seen';
import { NO_RESUME_NOTE, reviews } from './moored';
import { usageRow, watchUsage, type ShipUsage } from './shipUsage';
import { boxState } from './messageBox';
import { Sheet, useCancelFocus } from './Sheet';
import { OpenPRs } from './OpenPRs';
import { fleetSearch } from './fleets';
import { go, loc, surfacePath } from './router';
import { ships, shipsLink, shipsLoaded } from './shipsLink';
import { signedOut as signedOutSignal } from './session';
import { SURFACES } from './surfaces';
import { useBridgeTerminal } from '../useBridgeTerminal';
import { atSea } from '../crew';
import { KEYS, touchKeyboard } from '../keys';


const TABS = ['Conversation', 'Terminal', 'PR'] as const;
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

interface ShipActionProps {
  ship: Ship;
  label: string;
  variant: 'primary' | 'danger';
  cls: string;
  action: string;
  question: string;
  done: (moored: boolean) => void;
  // The ship's own state while the transition runs, past the request's answer.
  busy?: boolean;
  busyLabel: string;
  // The sheet offers Moor beside the ending when the ship has open PRs as it opens.
  moor: { end: string; endVariant?: 'danger'; explain: ComponentChildren };
}

function ShipAction({ ship, label, variant, cls, action, question, done, busy: shipBusy, busyLabel, moor }: ShipActionProps) {
  const [asking, setAsking] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  // Frozen when the sheet opens, so a tap aimed at one button never lands on another that appeared meanwhile.
  const [prs, setPrs] = useState<string[]>([]);
  const withMoor = prs.length > 0;
  const key = ['ship', ship.session, action] as const;
  useCancelFocus(asking);
  const run = async (moored = false) => {
    setSending(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/${action}`, moored ? { moor: true } : undefined, undefined, moored ? { timeoutMs: SLOW_TIMEOUT_MS } : undefined);
    setSending(false);
    if (!r.ok) return setError(r.error);
    setAsking(false);
    done(moored);
  };
  return (
    <>
      <Button
        label={label}
        variant={variant}
        class={cls}
        busyLabel={busyLabel}
        busy={shipBusy}
        pending={key}
        onClick={() => {
          setError('');
          setPrs(ship.open_prs ?? []);
          setAsking(true);
        }}
      />
      {asking ? (
        <Sheet label={label} kind="launch-backdrop stop-backdrop" onClose={() => !sending && setAsking(false)}>
          {withMoor ? (
            <>
              <OpenPRs name={ship.name} urls={prs} />
              <p class="line">{moor.explain}</p>
            </>
          ) : (
            <p class="line">{question}</p>
          )}
          {error ? (
            <p class="launch-error" role="alert">
              {error}
            </p>
          ) : null}
          <Actions class="launch-foot">
            <Button label="Cancel" onClick={() => setAsking(false)} />
            <Button label={withMoor ? moor.end : label} variant={withMoor ? moor.endVariant : variant} pending={key} onClick={() => run()} />
            {withMoor ? <Button label="Moor" variant="primary" pending={['ship', ship.session, 'moor']} onClick={() => run(true)} /> : null}
          </Actions>
        </Sheet>
      ) : null}
    </>
  );
}

function ReturnToPort({ ship }: { ship: Ship }) {
  return (
    <ShipAction
      ship={ship}
      label="Return to port"
      variant="primary"
      cls="return-port"
      action="return"
      busy={ship.returning}
      busyLabel="Returning to port"
      question={`Return ${ship.name} to port? Once its turn ends it's told to clean up after itself, and then it's stopped with its crew.`}
      done={(moored) => moored && back()}
      moor={{
        end: 'Return to port',
        explain: (
          <>
            <strong>Moor</strong> ends the session now with no clean-up prompt and keeps the PRs. <strong>Return to port</strong> tells it to clean up, then stops it and forgets the PRs.
          </>
        ),
      }}
    />
  );
}

function StopShip({ ship }: { ship: Ship }) {
  return (
    <ShipAction
      ship={ship}
      label="Stop ship"
      variant="danger"
      cls="stop-ship"
      action="stop"
      busyLabel="Stopping"
      question={
        ship.status === 'moored'
          ? `Stop ${ship.name} for good? Admiral forgets it; its PRs stay open on GitHub.`
          : `Stop ${ship.name}? Its harness and crew end now.`
      }
      done={back}
      moor={{
        end: 'Stop for good',
        endVariant: 'danger',
        explain: (
          <>
            <strong>Moor</strong> ends the session now and keeps the PRs; the ship wakes when reviews come in. <strong>Stop for good</strong> ends it and forgets them.
          </>
        ),
      }}
    />
  );
}

function SessionTerminal({ ship, boat }: { ship: Ship; boat?: CrewBoat }) {
  const open = boat ? boat.status !== 'anchored' : atSea(ship);
  const { host, link, linkState, activity } = useBridgeTerminal({
    session: ship.session,
    crew: boat?.name,
    open,
    gone: () => {},
    wsBase: '/ws/v1',
    probe: () => probeShipList(boat?.name),
    signedOut: () => {
      shipsLink.value = 'signed-out';
      signedOutSignal.value = true;
    },
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
      {!open && (boat || ship.status === 'anchored') ? (
        <div class="term-keys">
          <span class="term-note">{boat ? 'This boat exited. This is its last screen.' : 'The harness exited. This is its last screen.'}</span>
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

// A moored ship has no session, so there is no bridge to join.
function TerminalTab({ ship, boat }: { ship: Ship; boat?: CrewBoat }) {
  if (ship.status === 'moored')
    return (
      <div class="term-keys">
        <span class="term-note">A moored ship has no session until it's woken.</span>
      </div>
    );
  return <SessionTerminal ship={ship} boat={boat} />;
}

/** The ship's tokens and money, read on open, every USAGE_POLL_MS and when the page becomes visible again. */
function UsageRow({ session }: { session: string }) {
  const [usage, setUsage] = useState<ShipUsage | null>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    setUsage(null);
    return watchUsage(session, setUsage);
  }, [session]);
  const row = usageRow(usage);
  if (!row) return null;
  const figures = (
    <>
      {row.tokens ? <span class="ship-usage-tokens">{row.tokens.headline}</span> : null}
      {row.money ? (
        <span class="ship-usage-money">
          {row.money.text}
          {row.money.premium ? `, ${row.money.premium}` : ''}
          {row.money.incomplete ? ' (incomplete)' : ''}
        </span>
      ) : null}
      <span class="ship-usage-fresh">{row.fresh}</span>
    </>
  );
  if (!row.tokens)
    return (
      <div class="ship-usage" role="group" aria-label="Usage">
        <div class="ship-usage-line">{figures}</div>
      </div>
    );
  return (
    <div class="ship-usage" role="group" aria-label="Usage">
      <details onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
        <summary class="ship-usage-line">
          <span class={`fold-chevron${open ? ' open' : ''}`}>
            <Icon name="chevron" size={14} />
          </span>
          {figures}
        </summary>
        <dl>
          {row.tokens.classes.map((c) => (
            <div key={c.label}>
              <dt>{c.label}</dt>
              <dd>{c.value}</dd>
            </div>
          ))}
        </dl>
      </details>
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

/** A ship's conversation, or the picked boat's, followed while the caller stays mounted. */
export function useShipConversation(ship: Ship, boat: CrewBoat | undefined) {
  const [conv, setConv] = useState<Conversation>({ state: 'loading', items: [], more: false });
  const follow = useRef<ReturnType<typeof followConversation> | undefined>(undefined);
  const latest = useRef(ship);
  latest.current = ship;
  const boatName = boat?.name;
  const boatHasConversation = boat?.conversation ?? false;
  const moored = ship.status === 'moored';
  useEffect(() => {
    setConv({ state: 'loading', items: [], more: false });
    if (boatName !== undefined && !boatHasConversation) {
      setConv({ state: 'none', items: [], more: false });
      follow.current = undefined;
      return;
    }
    // Only a running Claude, Codex or Copilot ship has a transcript still to come; a boat is followed once its transcript is recorded.
    const f = followConversation(ship.session, setConv, { crew: boatName, retryNone: () => boatName === undefined && hasConversationAdapter(latest.current.harness) && latest.current.status !== 'anchored' && latest.current.status !== 'moored' });
    follow.current = f;
    return f.stop;
  }, [ship.session, boatName, boatHasConversation, moored]);
  return { conv, loadOlder: () => void follow.current?.loadOlder() };
}

/** What the message box talks to: the picked boat, else the ship. */
export function targetFor(ship: Ship, boat: CrewBoat | undefined, conv: Conversation): Target {
  return boat
    ? { session: ship.session, crew: boat.name, state: boatBoxState(boat), interruptible: boat.status === 'underway', hasConversation: (boat.conversation ?? false) && conv.state === 'ok', subject: 'This boat' }
    : { session: ship.session, state: boxState(ship), interruptible: ship.status === 'underway', hasConversation: conv.state === 'ok' || (conv.state === 'none' && hasConversationAdapter(ship.harness)), subject: 'This ship', wake: ship.status === 'moored', mooredAt: ship.moored_at };
}

const OPEN_TAB: Record<string, Tab> = { terminal: 'Terminal', pr: 'PR' };
const openTab = (): Tab => {
  const v = new URLSearchParams(loc.value.search).get('tab') ?? '';
  return Object.hasOwn(OPEN_TAB, v) ? OPEN_TAB[v] : 'Conversation';
};

function ShipTabs({ ship, title }: { ship: Ship; title: boolean }) {
  const [tab, setTab] = useState<Tab>(openTab);
  const [crew, setCrew] = useState<string | null>(null);
  // Read once at mount; dropping it keeps a remount from reopening Terminal.
  useEffect(() => {
    const q = new URLSearchParams(loc.value.search);
    if (!q.has('tab')) return;
    q.delete('tab');
    const rest = q.toString();
    go(loc.value.path, rest ? `?${rest}` : '', true);
  }, []);
  // A boat that left the ship's list hands the view back to the ship.
  const boat = crew === null ? undefined : ship.crew.find((b) => b.name === crew);
  const boatName = boat?.name;
  // Forget a boat that left the crew, so a later boat of the same name does not take the view back.
  useEffect(() => {
    if (crew !== null && !boat) setCrew(null);
  }, [crew, boat]);
  const { conv, loadOlder } = useShipConversation(ship, boat);
  const target = targetFor(ship, boat, conv);
  const box = useComposer(target, conv);
  useSeenOnLeave(ship);
  const moored = ship.status === 'moored';
  const summary = prSummary(ship.prs, ship);
  const count = openCount(ship.prs);
  const Heading = title ? 'h1' : 'h2';
  const mooredCard = (
    <div class="convo-card">
      <p class="convo-card-title">{reviews(ship) ? 'Moored, waiting on the author' : 'Moored, waiting on review'}</p>
      <p class="line">Send a message or tap Wake to start the next round.</p>
    </div>
  );
  const anchored = boat ? chipState(boat) !== 'running' : ship.status === 'anchored';
  return (
    <section class={`ship-view on-${tab.toLowerCase()}`}>
      <div class="ship-view-head">
        <BackLink label="Back" />
        <Heading>{ship.name}</Heading>
        <ShipRepo ship={ship} />
        <ShipOrder ship={ship} />
        <RunningClock ship={ship} />
        {moored ? (
          <div class="ship-actions">
            <Button label="Wake" variant="primary" class="stop-ship" busyLabel="Waking" busy={box.waking} pending={['ship', ship.session, 'wake']} onClick={() => box.wake()} />
            <StopShip ship={ship} />
          </div>
        ) : (
          <div class="ship-actions">
            {ship.status === 'anchored' || ship.status === 'sailing_off' || ship.carries_prs ? null : <ReturnToPort ship={ship} />}
            <StopShip ship={ship} />
          </div>
        )}
      </div>
      <UsageRow session={ship.session} />
      {moored ? (
        <div class="moored-info">
          <p class="line">This ship is moored: its PRs are open and it has no session until it's woken.</p>
          <ReadyToMerge ship={ship} />
          {ship.no_resume ? <p class="msg-note">{NO_RESUME_NOTE}</p> : null}
          {tab !== 'Conversation' && box.error ? (
            <p class="alert" role="alert">
              {box.error}
            </p>
          ) : null}
        </div>
      ) : null}
      {summary ? (
        <button type="button" class="pr-summary" onClick={() => setTab('PR')}>
          <span class="sum-text">{summary}</span>
          <span class="sum-go">View PRs</span>
        </button>
      ) : null}
      {ship.crew.length > 0 ? <Flotilla ship={ship} picked={boatName ?? null} pick={setCrew} /> : null}
      <div class="view-tabs-row">
        <div role="tablist" class="view-tabs" aria-label={`${ship.name} views`}>
          {TABS.map((t) => (
            <button key={t} type="button" role="tab" id={`tab-${t}`} aria-selected={tab === t} aria-controls="view-panel" class="view-tab" onClick={() => setTab(t)}>
              {t}
              {t === 'PR' && count > 0 ? <span class="tab-count">{count}</span> : null}
            </button>
          ))}
        </div>
        {target.interruptible ? <InterruptButton target={target} box={box} /> : null}
      </div>
      <div role="tabpanel" id="view-panel" aria-labelledby={`tab-${tab}`} class={`view-panel${tab === 'Terminal' ? ' terminal' : ''}`}>
        {tab === 'Conversation' ? (
          <>
            <div class="convo-area">
              {moored && !(conv.state === 'ok' && conv.items.length > 0) ? (
                mooredCard
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
                footer={moored ? mooredCard : undefined}
                openTerminal={() => setTab('Terminal')}
                loadOlder={loadOlder}
              />
              )}
            </div>
            {/* A new ship waits on its first message. */}
            <Composer target={target} box={box} interrupt={false} focus={!boat && !anchored && !moored && conv.state === 'none' && hasConversationAdapter(ship.harness)} />
          </>
        ) : tab === 'Terminal' ? (
          <TerminalTab ship={ship} boat={boat} />
        ) : (
          <PRTab ship={ship} />
        )}
      </div>
    </section>
  );
}

/** One ship: Conversation, Terminal and PR. The view opens on Conversation, or on Terminal or PR when the URL says `?tab=terminal` or `?tab=pr`. */
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

function RunningClock({ ship }: { ship: Ship }) {
  const now = useNow();
  const time = isRunning(ship) ? runningFor(ship.launched, now) : '';
  if (!time) return null;
  return (
    <span class="ship-clock" title={`Running for ${time}`} aria-label={`Running for ${time}`}>
      {time}
    </span>
  );
}
