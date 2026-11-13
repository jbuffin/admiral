import type { ComponentChildren } from 'preact';
import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch } from './fleets';
import { probeChatSession, type Fleet } from './api';
import { shipsLink } from './shipsLink';
import { useBridgeTerminal } from '../useBridgeTerminal';
import { KEYS, touchKeyboard } from '../keys';
import { commitmentCardView, needsTerminal, questionEyebrow, resetLabel, threadOf, youAnswered, type ThreadItem } from './chat';
import { Row, useInboxQueue, usePeekEntry, useStacked } from './InboxPanel';
import { MuteRule } from './MuteRule';
import { Peek } from './Peek';
import { go, loc, chatPeekOf, chatPeekPath, chatSettingsPath, chatTerminalOf, chatTerminalPath } from './router';
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
      <p class="line">{item.answer ? youAnswered(item.answer) : 'Withdrawn'}</p>
    </div>
  );
}

function CommitmentCard({ item, fleet }: { item: Extract<ThreadItem, { kind: 'commitment' }>; fleet: string }) {
  const v = commitmentCardView(item, fleet);
  return (
    <div class="convo-card chat-card">
      <p class="chat-card-eyebrow">{v.eyebrow}</p>
      <p class="line">{v.text}</p>
      {v.childLabel ? <p class="line">{v.childLabel}</p> : null}
      {v.reason ? <p class="line">{v.reason}</p> : null}
      {v.shipHref ? (
        <a
          class="btn"
          href={v.shipHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.shipHref, fleetSearch.value);
          }}
        >
          {item.card.ship}
        </a>
      ) : null}
      {v.settingsHref ? (
        <a
          class="btn"
          href={v.settingsHref + fleetSearch.value}
          onClick={(e) => {
            if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
            e.preventDefault();
            go(v.settingsHref, fleetSearch.value);
          }}
        >
          Review in Preferences
        </a>
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
      {c.link?.startsWith('https://') ? (
        <a href={c.link} target="_blank" rel="noopener noreferrer">
          {c.link} ↗
        </a>
      ) : null}
      <MuteRule target={{ fleet, routine: c.routine, sender: c.sender, dedupe_key: c.dedupe_key, mute_by: c.mute_by }} />
    </div>
  );
}

function Thread({ items, working, chat, fleet }: { items: ThreadItem[]; working: boolean; chat: Chat; fleet: string }) {
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [items.length, working]);
  return (
    <div
      class="convo"
      ref={scroller}
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}
    >
      {items.length === 0 && !working ? <p class="line">No messages yet.</p> : null}
      {items.map((i) =>
        i.kind === 'reset' ? (
          <div key={i.id} class="compaction">
            <span>{resetLabel(i.text, i.at)}</span>
          </div>
        ) : i.kind === 'question' ? (
          <QuestionCard key={i.id} item={i} chat={chat} />
        ) : i.kind === 'event' ? (
          <EventCardView key={i.id} item={i} fleet={fleet} />
        ) : i.kind === 'commitment' ? (
          <CommitmentCard key={i.id} item={i} fleet={fleet} />
        ) : i.role === 'user' ? (
          <div key={i.id} class="msg user">
            {i.text}
          </div>
        ) : (
          <div key={i.id} class="msg assistant md" dangerouslySetInnerHTML={{ __html: renderMarkdown(i.text) }} />
        ),
      )}
      {working ? <p class="working">{workingLabel([], Date.now())}</p> : null}
    </div>
  );
}

function ChatComposer({ chat }: { chat: Chat }) {
  const { text, setText, send, sending, error } = chat;
  return (
    <div class="msg-box">
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <MessageField label="Message AdmiralBot" placeholder="Ask AdmiralBot" text={text} setText={setText} send={() => void send()} disabled={false} canSend={!sending && text.trim() !== ''} />
    </div>
  );
}

function OpenTerminal({ fleet }: { fleet: string }) {
  return (
    <button type="button" class="btn" onClick={() => go(chatTerminalPath(fleet), fleetSearch.value)}>
      Open terminal
    </button>
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
        <button type="button" class="btn" onClick={back}>
          Back to chat
        </button>,
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
          <button type="button" class="btn" disabled={chat.resetting} onClick={() => void chat.newChat()}>
            New chat
          </button>
        </>,
      )}
      {snap?.error ? (
        <p class="msg-note">
          {snap.error} {snap.running && needsTerminal(snap) ? <OpenTerminal fleet={fleet} /> : null}
        </p>
      ) : null}
      {snap ? <Thread items={items} working={snap.state === 'working'} chat={chat} fleet={fleet} /> : <p class="line">Loading…</p>}
      <ChatComposer chat={chat} />
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
      <a
        class="btn"
        href={href}
        onClick={(e) => {
          if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
          e.preventDefault();
          go(chatSettingsPath(fleet.key), fleetSearch.value);
        }}
      >
        AdmiralBot settings
      </a>
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
