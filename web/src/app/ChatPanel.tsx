import type { ComponentChildren } from 'preact';
import { useLayoutEffect, useRef } from 'preact/hooks';
import { activeFleet, fleetPaint, fleetSearch } from './fleets';
import type { Fleet } from './api';
import { resetLabel, threadOf, youAnswered, type ThreadItem } from './chat';
import { Row, useInboxQueue, useStacked } from './InboxPanel';
import { go, chatSettingsPath } from './router';
import { renderMarkdown } from './markdown';
import { PeekQuestions } from './PeekQuestions';
import { useChat } from './useChat';
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
        <p class="chat-card-eyebrow">AdmiralBot asks</p>
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

function Thread({ items, working, chat }: { items: ThreadItem[]; working: boolean; chat: Chat }) {
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
            <span>{resetLabel(i.at)}</span>
          </div>
        ) : i.kind === 'question' ? (
          <QuestionCard key={i.id} item={i} chat={chat} />
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
  const field = useRef<HTMLTextAreaElement>(null);
  return (
    <div class="msg-box">
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      <div class="msg-row">
        <textarea
          class="msg-input"
          aria-label="Message AdmiralBot"
          placeholder="Ask AdmiralBot"
          rows={2}
          ref={field}
          onFocus={() => field.current?.scrollIntoView({ block: 'nearest' })}
          value={text}
          onInput={(e) => setText(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key !== 'Enter' || e.shiftKey || e.isComposing || e.keyCode === 229) return;
            e.preventDefault();
            void send();
          }}
        />
        <div class="msg-actions">
          <button type="button" class="btn primary" disabled={sending || !text.trim()} onClick={() => void send()}>
            Send
          </button>
        </div>
      </div>
    </div>
  );
}

function Conversation({ fleet, head }: { fleet: string; head: (action: ComponentChildren) => ComponentChildren }) {
  const chat = useChat(fleet);
  const { snap } = chat;
  const items = snap ? threadOf(snap) : [];
  return (
    <>
      {head(
        <button type="button" class="btn" disabled={chat.resetting} onClick={() => void chat.newChat()}>
          New chat
        </button>,
      )}
      {snap?.error ? <p class="msg-note">{snap.error}</p> : null}
      {snap ? <Thread items={items} working={snap.state === 'working'} chat={chat} /> : <p class="line">Loading…</p>}
      <ChatComposer chat={chat} />
    </>
  );
}

function ChatRail() {
  const { queue, entries, now, rerender } = useInboxQueue();
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
              fold={(text) => {
                queue.fold(e.key, text, Date.now(), undefined);
                rerender();
              }}
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
  if (!fleet) return null;
  const Heading = title ? 'h1' : 'h2';
  const head = (action: ComponentChildren) => (
    <div class="chat-head">
      <Heading class="chat-title">
        <span class="dot" style={{ background: fleetPaint(fleet).fill }} aria-hidden="true" />
        <span class="chat-title-text">AdmiralBot · {fleet.name} fleet</span>
      </Heading>
      {action}
    </div>
  );
  return (
    <main class="panel chat-panel" aria-label={surface.title}>
      <div class="chat-main">
        {fleet.chat.on ? (
          <Conversation key={fleet.key} fleet={fleet.key} head={head} />
        ) : (
          <>
            {head(null)}
            <Off fleet={fleet} />
          </>
        )}
      </div>
      {stacked ? null : <ChatRail />}
    </main>
  );
}
