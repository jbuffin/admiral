import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'preact/hooks';
import { Actions, Button } from './Button';
import { FindingsSheet } from './FindingsSheet';
import { Icon } from './Icon';
import { activeFleet, fleetPaint, fleetSearch } from './fleets';
import { probeChatSession, type Fleet, type Result } from './api';
import { computed } from '@preact/signals';
import { ships, shipsLink } from './shipsLink';
import { shipLinkSet, shipsOfLinkSet } from './shipLinkSet';
import { fetchLaunchState, launchCardView, promoteLaunch, stopShip, undoLaunch } from './launchCard';
import { useBridgeTerminal } from '../useBridgeTerminal';
import { KEYS, touchKeyboard } from '../keys';
import { commitmentCardView, launchLineView, needsTerminal, shipCardView, questionEyebrow, resetLabel, threadOf, youAnswered, type ThreadItem, type ThreadMessage } from './chat';
import { Row, useInboxQueue, usePeekEntry, useStacked } from './InboxPanel';
import { MuteRule } from './MuteRule';
import { ReviewerNoteView } from './ReviewerNoteView';
import { Peek } from './Peek';
import { campaignPath, go, loc, chatMessageOf, chatPeekOf, chatPeekPath, chatSettingsPath, chatTerminalOf, chatTerminalPath } from './router';
import { safeHref } from './makeCampaign';
import { renderMarkdown } from './markdown';
import { PeekQuestions } from './PeekQuestions';
import { useChat } from './useChat';
import { MessageField } from './Composer';
import { workingLabel } from './turns';
import type { Surface } from './surfaces';
import { blankDraft } from '../questions';

type Chat = ReturnType<typeof useChat>;

function QuestionCard({ item, chat }: { item: Extract<ThreadItem, { kind: 'question' }>; chat: Chat }) {
  const { card } = item;
  if (item.state === 'open') {
    const draft = chat.drafts[card.id] ?? blankDraft();
    return (
      <div class="convo-card chat-card">
        <p class="chat-card-eyebrow">{questionEyebrow(card.id)}</p>
        <PeekQuestions questions={card.question?.questions ?? []} draft={draft} edit={(fn) => chat.editDraft(card.id, fn)} onSend={(b) => void chat.answer(card.id, b)} />
      </div>
    );
  }
  return (
    <div class="convo-card chat-card quiet">
      <p class="convo-card-title">{card.title}</p>
      <p class="line">{item.answer ? youAnswered(item.answer) : item.kept !== undefined ? `You answered: ${item.kept}` : 'Withdrawn'}</p>
    </div>
  );
}

function NeedCardView({ item, chat }: { item: Extract<ThreadItem, { kind: 'need' }>; chat: Chat }) {
  const { card } = item;
  const name = ships.value.find((s) => s.session === card.ship)?.name || card.ship;
  if (card.state === 'open') {
    const draft = chat.drafts[item.id] ?? blankDraft();
    return (
      <div class="convo-card chat-card">
        <p class="chat-card-eyebrow">{name}</p>
        <p class="line">{item.text}</p>
        <PeekQuestions questions={card.questions} draft={draft} edit={(fn) => chat.editDraft(item.id, fn)} onSend={(b) => void chat.answerNeed(item.id, b)} />
      </div>
    );
  }
  return (
    <div class="convo-card chat-card quiet">
      <p class="convo-card-title">{item.text}</p>
      <p class="line">
        {name} · {card.state === 'removed' ? 'Removed' : card.answered_in === 'inbox' ? 'Answered in the Inbox' : 'Answered in the chat'}
      </p>
    </div>
  );
}

function CommitmentCard({ item, fleet }: { item: Extract<ThreadItem, { kind: 'commitment' }>; fleet: string }) {
  const v = commitmentCardView(item, fleet);
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">{v.eyebrow}</p>
      <div class="line md" dangerouslySetInnerHTML={{ __html: renderMarkdown(v.text) }} />
      {v.childLabel ? <p class="line">{v.childLabel}</p> : null}
      {v.reason ? <div class="line md" dangerouslySetInnerHTML={{ __html: renderMarkdown(v.reason) }} /> : null}
      {v.shipHref ? (
        <Button
          label={`Open ship ${item.card.ship}`}
          href={v.shipHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.shipHref, fleetSearch.value);
          }}
        >
          {item.card.ship}
        </Button>
      ) : null}
      {v.settingsHref ? (
        <Button
          label="Review in Preferences"
          href={v.settingsHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.settingsHref, fleetSearch.value);
          }}
        />
      ) : null}
    </div>
  );
}

function CampaignDraftCardView({ item }: { item: Extract<ThreadItem, { kind: 'campaign_draft' }> }) {
  const { title, url, created, failed } = item.card;
  const href = created ? safeHref(url ?? '') : undefined;
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">{created ? 'Campaign created' : 'Draft thrown away'}</p>
      <p class="line">{title}</p>
      {href ? <Button label="Open the campaign issue" href={href} external /> : null}
      {failed ? <p class="line">{failed}</p> : null}
    </div>
  );
}

function FollowingCardView({ item }: { item: Extract<ThreadItem, { kind: 'following' }> }) {
  const href = campaignPath(item.card.campaign_id);
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">Following</p>
      <p class="line">{item.card.title}</p>
      <Button
        label="Open the campaign"
        href={href + fleetSearch.value}
        onClick={(e) => {
          if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
          e.preventDefault();
          go(href, fleetSearch.value);
        }}
      />
    </div>
  );
}

function ShipCardView({ item }: { item: Extract<ThreadItem, { kind: 'ship' }> }) {
  const v = shipCardView(item, ships.value);
  const [showFindings, setShowFindings] = useState(false);
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">{v.eyebrow}</p>
      <p class="line">{v.result}</p>
      {v.shipHref ? (
        <Button
          label={`Open ship ${v.name}`}
          href={v.shipHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.shipHref, fleetSearch.value);
          }}
        >
          {v.name}
        </Button>
      ) : (
        <p class="line">{v.name}</p>
      )}
      {v.title ? <p class="line">{v.title}</p> : null}
      {v.branch ? <p class="line"><code>{v.branch}</code></p> : null}
      {v.prs.map((p) => (p.href ? <Button key={p.url} label={p.label} href={p.href} external /> : <p key={p.url} class="line">{p.url}</p>))}
      {v.findings ? <Button label="Findings" onClick={() => setShowFindings(true)} /> : null}
      {showFindings ? <FindingsSheet ship={item.card.ship} firstLaunched={item.card.first_launched} onClose={() => setShowFindings(false)} /> : null}
    </div>
  );
}

function LaunchLineView({ item }: { item: Extract<ThreadItem, { kind: 'launch_line' }> }) {
  const v = launchLineView(item, ships.value);
  const [open, setOpen] = useState(false);
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow launch-sentence">{v.sentence}</p>
      {v.shipHref ? (
        <Button
          label={`Open ship ${v.name}`}
          href={v.shipHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.shipHref, fleetSearch.value);
          }}
        >
          {v.name}
        </Button>
      ) : (
        <p class="line">{v.name}</p>
      )}
      {v.title ? <p class="line">{v.title}</p> : null}
      {v.text ? (
        <>
          <button type="button" class="tool-head" aria-expanded={open} onClick={() => setOpen(!open)}>
            <span class={`fold-chevron${open ? ' open' : ''}`}>
              <Icon name="chevron" size={14} />
            </span>
            Launch text
          </button>
          {open ? <p class="line launch-text">{v.text}</p> : null}
        </>
      ) : null}
    </div>
  );
}

function EventCardView({ item, fleet }: { item: Extract<ThreadItem, { kind: 'event' }>; fleet: string }) {
  const c = item.card;
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">
        {c.routine_name ?? 'Routine'} · Untrusted
      </p>
      <p class="line">{item.text}</p>
      {c.sender ? <p class="line">From {c.sender}</p> : null}
      {c.tier ? <p class="line">Tier {c.tier}</p> : null}
      <ReviewerNoteView r={c} />
      {c.link?.startsWith('https://') ? (
        <a href={c.link} target="_blank" rel="noopener noreferrer">
          {c.link} ↗
        </a>
      ) : null}
      <MuteRule target={{ fleet, routine: c.routine, sender: c.sender, dedupe_key: c.dedupe_key, mute_by: c.mute_by }} />
    </div>
  );
}

function LaunchCardView({ item, fleet }: { item: Extract<ThreadItem, { kind: 'launch' }>; fleet: string }) {
  const c = item.card;
  const [undone, setUndone] = useState(false);
  const [promotedShip, setPromotedShip] = useState('');
  const [confirming, setConfirming] = useState<'' | 'undo' | 'promote'>('');
  const [error, setError] = useState('');
  useEffect(() => {
    let live = true;
    void fetchLaunchState(fleet, c.routine, item.id).then((st) => {
      if (!live || !st) return;
      if (st.undone) setUndone(true);
      if (st.promotedShip) setPromotedShip(st.promotedShip);
    });
    return () => {
      live = false;
    };
  }, [fleet, c.routine, item.id]);
  const v = launchCardView(item, { ships: ships.value, undone, promotedShip });
  const run = async <T,>(act: () => Promise<Result<T>>, then?: (data: T | null) => void) => {
    setError('');
    const r = await act();
    setError(r.ok ? '' : r.error);
    if (r.ok) then?.(r.data);
  };
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">{v.eyebrow}</p>
      <p class="line">{v.text}</p>
      {c.sender ? <p class="line">From {c.sender}</p> : null}
      {c.tier ? <p class="line">Tier {c.tier}</p> : null}
      {c.link?.startsWith('https://') ? (
        <a href={c.link} target="_blank" rel="noopener noreferrer">
          {c.link} ↗
        </a>
      ) : null}
      <p class="line">
        <a
          href={v.shipHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.shipHref, fleetSearch.value);
          }}
        >
          {v.ship}
        </a>
        {v.status ? ` · ${v.status}` : ''}
      </p>
      {v.promotedShip ? (
        <p class="line">
          Promoted to{' '}
          <a
            href={v.promotedHref + fleetSearch.value}
            onClick={(e) => {
              if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
              e.preventDefault();
              go(v.promotedHref, fleetSearch.value);
            }}
          >
            {v.promotedShip}
          </a>
        </p>
      ) : v.notice ? (
        <p class="line">{v.notice}</p>
      ) : null}
      {confirming ? (
        <div class="confirm" role="group" aria-label={confirming === 'undo' ? 'Undo' : 'Promote'}>
          <p class="line">{confirming === 'undo' ? v.undoConfirm : v.promoteConfirm}</p>
          <Actions class="confirm-actions">
          {confirming === 'undo' ? (
            <Button variant="danger" label="Confirm undo" pending={['launch', fleet, c.routine, item.id, 'undo']} onClick={() => run(() => undoLaunch(fleet, c.routine, item.id), () => { setUndone(true); setConfirming(''); })} />
          ) : (
            <Button variant="danger" label="Confirm promote" pending={['launch', fleet, c.routine, item.id, 'promote']} onClick={() => run(() => promoteLaunch(fleet, c.routine, item.id), (d) => { setPromotedShip(d?.promoted_ship ?? ''); setConfirming(''); })} />
          )}
          <Button label="Cancel" onClick={() => setConfirming('')} />
          </Actions>
        </div>
      ) : null}
      {error ? <p class="inbox-error" role="alert">{error}</p> : null}
      <Actions class="peek-actions">
        {v.actions.stop && !confirming ? <Button label="Stop" busyLabel="Stopping" pending={['ship', c.ship, 'stop']} onClick={() => run(() => stopShip(c.ship))} /> : null}
        {v.actions.promote && !confirming ? <Button label="Promote" onClick={() => setConfirming('promote')} /> : null}
        {v.actions.undo && !confirming ? <Button label="Undo" onClick={() => setConfirming('undo')} /> : null}
        {v.actions.mute ? <MuteRule target={{ fleet, routine: c.routine, sender: c.sender, dedupe_key: c.dedupe_key, mute_by: c.mute_by }} /> : null}
      </Actions>
    </div>
  );
}

/** The text of a report turn Admiral typed, closed until the engineer asks for it; plain text, never markdown. */
function ReportSent({ item, flash }: { item: ThreadMessage; flash: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div class={flash ? 'report-sent flash' : 'report-sent'} data-message-id={item.id}>
      <Button label="Report sent to AdmiralBot" expanded={open} onClick={() => setOpen(!open)} />
      {open ? <p class="report-text">{item.text}</p> : null}
    </div>
  );
}

function MessageView({ item, linkShips, fleet, flash }: { item: ThreadMessage; linkShips: ReturnType<typeof shipsOfLinkSet>; fleet: string; flash: boolean }) {
  const cls = flash ? ' flash' : '';
  if (item.report) return <ReportSent item={item} flash={flash} />;
  return item.role === 'user' ? (
    <div class={`msg user${cls}`} data-message-id={item.id}>
      {item.text}
    </div>
  ) : (
    <div class={`msg assistant md${cls}`} data-message-id={item.id} dangerouslySetInnerHTML={{ __html: renderMarkdown(item.text, { ships: linkShips, fleet, at: item.at, search: fleetSearch.value }) }} />
  );
}

/** Scrolls to the message a push or toast named once the thread has it, and lights it for a moment. It keeps looking as items render until the engineer scrolls off the bottom or the route changes; a message that has left the window leaves the chat at the bottom. */
function useFocusMessage(scroller: { current: HTMLDivElement | null }, pinned: { current: boolean }, focusId: string | null, items: ThreadItem[]) {
  const [flashId, setFlashId] = useState<string | null>(null);
  const done = useRef<string | null>(null);
  const started = useRef<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => {
    if (!focusId || items.length === 0 || done.current === focusId) return;
    if (started.current === focusId && !pinned.current) {
      done.current = focusId;
      return;
    }
    started.current = focusId;
    const el = Array.from(scroller.current?.querySelectorAll('[data-message-id]') ?? []).find((e) => e.getAttribute('data-message-id') === focusId);
    if (!el) return;
    done.current = focusId;
    pinned.current = false;
    el.scrollIntoView({ block: 'center' });
    setFlashId(focusId);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setFlashId(null), 2500);
  }, [focusId, items]);
  useEffect(() => () => clearTimeout(timer.current), []);
  return flashId;
}

function Thread({ items, working, chat, fleet }: { items: ThreadItem[]; working: boolean; chat: Chat; fleet: string }) {
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const target = chatMessageOf(loc.value.path);
  const flashId = useFocusMessage(scroller, pinned, target?.fleet === fleet ? target.message : null, items);
  const linkSet = useMemo(() => computed(() => shipLinkSet(ships.value, fleet)), [fleet]).value;
  const linkShips = useMemo(() => shipsOfLinkSet(linkSet), [linkSet]);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [items.length, working]);
  return (
    <div
      class="convo"
      ref={scroller}
      onClick={(e) => {
        const a = (e.target as HTMLElement).closest?.('a[data-ship]');
        if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        e.preventDefault();
        const [path, search] = (a.getAttribute('href') ?? '').split('?');
        go(path, search ? `?${search}` : '');
      }}
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}
    >
      {items.length === 0 && !working ? <p class="line">No messages yet.</p> : null}
      {items.map((i) =>
        i.kind === 'reset' || i.kind === 'interrupted' ? (
          <div key={i.id} class="compaction" role="separator">
            <span>{resetLabel(i.text, i.at)}</span>
          </div>
        ) : i.kind === 'question' ? (
          <QuestionCard key={i.id} item={i} chat={chat} />
        ) : i.kind === 'event' ? (
          <EventCardView key={i.id} item={i} fleet={fleet} />
        ) : i.kind === 'launch' ? (
          <LaunchCardView key={i.id} item={i} fleet={fleet} />
        ) : i.kind === 'commitment' ? (
          <CommitmentCard key={i.id} item={i} fleet={fleet} />
        ) : i.kind === 'launch_line' ? (
          <LaunchLineView key={i.id} item={i} />
        ) : i.kind === 'ship' && i.thread?.length ? (
          <div key={i.id} class="report-thread" data-thread={i.id}>
            <ShipCardView item={i} />
            {i.thread.map((m) => (
              <MessageView key={m.id} item={m} linkShips={linkShips} fleet={fleet} flash={flashId === m.id} />
            ))}
          </div>
        ) : i.kind === 'ship' ? (
          <ShipCardView key={i.id} item={i} />
        ) : i.kind === 'campaign_draft' ? (
          <CampaignDraftCardView key={i.id} item={i} />
        ) : i.kind === 'following' ? (
          <FollowingCardView key={i.id} item={i} />
        ) : i.kind === 'need' ? (
          <NeedCardView key={i.id} item={i} chat={chat} />
        ) : (
          <MessageView key={i.id} item={i} linkShips={linkShips} fleet={fleet} flash={flashId === i.id} />
        ),
      )}
      {working ? <p class="working">{workingLabel([], Date.now())}</p> : null}
    </div>
  );
}

function ChatComposer({ chat, fleet }: { chat: Chat; fleet: string }) {
  const { text, setText, send, sending, error, snap, interrupt } = chat;
  const running = snap?.state === 'working' || snap?.state === 'waiting';
  return (
    <div class="msg-box">
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      {running ? (
        <div class="msg-tools">
          <Button label="Interrupt" variant="quiet" disabled={sending} pending={['fleet', fleet, 'interrupt']} onClick={interrupt} />
        </div>
      ) : null}
      <MessageField label="Message AdmiralBot" placeholder="Ask AdmiralBot" text={text} setText={setText} send={() => void send()} disabled={false} canSend={!sending && text.trim() !== ''} primary />
    </div>
  );
}

function OpenTerminal({ fleet }: { fleet: string }) {
  return (
    <Button label="Open terminal" onClick={() => go(chatTerminalPath(fleet), fleetSearch.value)} />
  );
}

/** The fleet's chat session through the bridge, in place of its conversation. */
function ChatTerminal({ fleet, head }: { fleet: string; head: (action: ComponentChildren) => ComponentChildren }) {
  const back = () => go('/app/chat', fleetSearch.value);
  const { host, link, linkState, activity } = useBridgeTerminal({
    session: `_chat-${fleet}`,
    open: true,
    gone: () => go('/app/chat', fleetSearch.value, true),
    wsBase: '/ws/v1',
    probe: () => probeChatSession(fleet),
    signedOut: () => (shipsLink.value = 'signed-out'),
  });
  const canType = linkState === 'open';
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };
  return (
    <>
      {head(
        <Button label="Back to chat" onClick={back} />,
      )}
      <section class={linkState === 'reconnecting' ? 'term-body dim' : 'term-body'}>
        <div class="term-host" ref={host} />
      </section>
      <div class="term-keys">
        {linkState === 'reconnecting' ? <span class="term-note">Reconnecting…</span> : null}
        {touchKeyboard()
          ? KEYS.map(([name, label, bytes]) => (
              <button key={name} type="button" class="term-key" aria-label={`Send ${name}`} disabled={!canType} onClick={() => input(bytes)}>
                {label}
              </button>
            ))
          : null}
      </div>
    </>
  );
}

function Conversation({ fleet, head }: { fleet: string; head: (action: ComponentChildren) => ComponentChildren }) {
  const chat = useChat(fleet);
  const { snap } = chat;
  const items = snap ? threadOf(snap) : [];
  return (
    <>
      {head(
        <>
          {snap?.running ? <OpenTerminal fleet={fleet} /> : null}
          <Button label="New chat" busy={chat.resetting} pending={['fleet', fleet, 'new-chat']} onClick={() => chat.newChat()} />
        </>,
      )}
      {snap?.error ? (
        <p class="msg-note">
          {snap.error} {snap.running && needsTerminal(snap) ? <OpenTerminal fleet={fleet} /> : null}
        </p>
      ) : null}
      {snap ? <Thread items={items} working={snap.state === 'working'} chat={chat} fleet={fleet} /> : <p class="line">Loading…</p>}
      <ChatComposer chat={chat} fleet={fleet} />
    </>
  );
}

/** The Inbox beside the chat; an item opened from it shows in Peek in its place, so the chat stays. */
function ChatRail({ fleet }: { fleet: Fleet }) {
  const { queue, entries, now, rerender } = useInboxQueue();
  const peek = chatPeekOf(loc.value.path);
  const search = fleetSearch.value;
  const chatHref = `/app/chat`;
  const leave = () => go(chatHref, search, true);
  const { open, closing, gone, foldKey } = usePeekEntry(peek, entries, queue, rerender, leave);
  if (open && !closing) {
    return (
      <aside class="chat-rail chat-peek" aria-label="Inbox">
        <Peek key={open.key} entry={open} gone={gone} fold={(text) => foldKey(open.key, text)} back={{ href: chatHref + search, go: () => go(chatHref, search) }} />
      </aside>
    );
  }
  return (
    <aside class="chat-rail" aria-label="Inbox">
      <h2 class="rail-title">Inbox · All fleets · oldest first</h2>
      {entries.length === 0 ? (
        <p class="line">Nothing needs you.</p>
      ) : (
        <ul class="ship-list">
          {entries.map((e) => (
            <Row
              key={e.key}
              e={e}
              now={now}
              phone={false}
              selected={false}
              peekHref={(t) => (t.item !== undefined ? chatPeekPath(fleet.key, t.session, t.item) : null)}
              fold={(text) => foldKey(e.key, text)}
            />
          ))}
        </ul>
      )}
    </aside>
  );
}

function Off({ fleet }: { fleet: Fleet }) {
  const href = chatSettingsPath(fleet.key) + fleetSearch.value;
  return (
    <div class="chat-off">
      <p class="line">{fleet.chat.off_reason}</p>
      <Button
        label="AdmiralBot settings"
        href={href}
        onClick={(e) => {
          if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
          e.preventDefault();
          go(chatSettingsPath(fleet.key), fleetSearch.value);
        }}
      />
    </div>
  );
}

/** The Chat surface: the shown fleet's AdmiralBot, with the Inbox as a rail beside it when the width allows. */
export function ChatPanel({ surface, title, phone }: { surface: Surface; title: boolean; phone: boolean }) {
  const fleet = activeFleet.value;
  const stacked = useStacked(phone);
  const stray = fleet !== null && chatTerminalOf(loc.value.path) !== null && chatTerminalOf(loc.value.path) !== fleet.key;
  // A terminal path for another fleet shows this fleet's conversation, so the address says so.
  useEffect(() => {
    if (stray) go('/app/chat', fleetSearch.value, true);
  }, [stray]);
  if (!fleet) return null;
  const Heading = title ? 'h1' : 'h2';
  const head = (action: ComponentChildren) => (
    <div class="chat-head">
      <Heading class="chat-title">
        <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
        <span class="chat-title-text">AdmiralBot · {fleet.name} fleet</span>
      </Heading>
      <div class="chat-actions">{action}</div>
    </div>
  );
  return (
    <main class="panel chat-panel" aria-label={surface.title}>
      <div class="chat-main">
        {fleet.chat.on && chatTerminalOf(loc.value.path) === fleet.key ? (
          <ChatTerminal key={fleet.key} fleet={fleet.key} head={head} />
        ) : fleet.chat.on ? (
          <Conversation key={fleet.key} fleet={fleet.key} head={head} />
        ) : (
          <>
            {head(null)}
            <Off fleet={fleet} />
          </>
        )}
      </div>
      {stacked ? null : <ChatRail fleet={fleet} />}
    </main>
  );
}
