import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { AdmiralMark } from './AdmiralMark';
import { openAppSettings } from './AppSettings';
import { config } from './config';
import {
  CHAT_BUTTON_ID,
  CHAT_FAB_ID,
  CHAT_SESSION,
  type ChatQuestion,
  answerQuestion,
  asked,
  boxFocused,
  chatError,
  chatOpen,
  chatUnread,
  chatView,
  clockTime,
  closeChat,
  confirmNew,
  draft,
  editQDraft,
  newChat,
  probeChat,
  qdraftOf,
  sendMessage,
  session,
  startChat,
  startError,
  starting,
  thread,
  toggleChat,
  viewport,
} from './chat';
import { signedOut } from './connection';
import { KEYS, touchKeyboard } from './keys';
import { QuestionCard } from './QuestionCard';
import { foldText } from './questions';
import { phone, wide } from './sizeClass';
import { useBridgeTerminal } from './useBridgeTerminal';

// The AdmiralBot chat (#119). One pane, three hosts: the phone's Chat tab, a full-screen sheet at the middle
// size and a floating panel on wide (designs/prototype/index.html :825-922). Everything the server sends is
// rendered as text.

type Mode = 'tab' | 'sheet' | 'pop';

const FOCUSABLE = 'button:not([disabled]), a[href], input, textarea, [tabindex]:not([tabindex="-1"])';

const entryName = (open: boolean, dot: boolean) => `${open ? 'Close' : 'Open'} the chat with AdmiralBot${dot && !open ? ', new reply' : ''}`;

const chatIcon = <path d="M4 5h16v11H11l-5 4v-4H4z" />;

/** The round button of the wide layout, bottom right of open water. */
export function ChatFab() {
  const open = chatOpen.value;
  return (
    <button type="button" id={CHAT_FAB_ID} class="press chat-fab" aria-label={entryName(open, chatUnread.value)} aria-expanded={open} onClick={toggleChat}>
      {open ? (
        <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
          <path d="M6 6l12 12M18 6L6 18" />
        </svg>
      ) : (
        <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          {chatIcon}
        </svg>
      )}
      {chatUnread.value && !open && <span class="chat-dot-badge" aria-hidden="true" />}
    </button>
  );
}

/** The middle layout's Chat button, in the header. */
export function ChatHeaderButton() {
  if (phone.value || wide.value) return null;
  const open = chatOpen.value;
  return (
    <button type="button" id={CHAT_BUTTON_ID} class="press chat-header-button" aria-label={entryName(open, chatUnread.value)} aria-expanded={open} onClick={toggleChat}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        {chatIcon}
      </svg>
      Chat
      {chatUnread.value && !open && <span class="chat-dot-badge small" aria-hidden="true" />}
    </button>
  );
}

/** The panel (wide) or the sheet (middle), while the chat is open. */
export function ChatHost() {
  if (phone.value || !chatOpen.value) return null;
  return <ChatPane mode={wide.value ? 'pop' : 'sheet'} />;
}

export const ChatTab = () => <ChatPane mode="tab" />;

function subtitle(): string {
  const c = config.value;
  const chat = c?.chat;
  if (!chat) return 'Claude';
  const fleet = c?.fleets.find((f) => f.key === chat.fleet)?.name ?? chat.fleet;
  return ['Claude', fleet, chat.model, chat.effort].filter(Boolean).join(' · ');
}

function Close({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button type="button" class="press chat-close" aria-label={label} onClick={onClick}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
        <path d="M6 6l12 12M18 6L6 18" />
      </svg>
    </button>
  );
}

function ChatPane({ mode }: { mode: Mode }) {
  const root = useRef<HTMLElement>(null);
  const terminal = chatView.value === 'terminal';
  useEffect(() => void startChat(), []);
  useEffect(() => () => void (boxFocused.value = false), []);
  // The panel and the sheet take focus when they open; the tab does not, or a phone's keyboard would pop up.
  useLayoutEffect(() => {
    if (mode !== 'tab') root.current?.focus({ preventScroll: true });
  }, []);
  const vv = viewport.value;
  const kbd = mode !== 'tab' && vv.keyboard;
  const style =
    kbd && mode === 'sheet'
      ? `top:${vv.top}px;height:${vv.height}px`
      : kbd
        ? `bottom:${vv.client - (vv.top + vv.height) + 12}px;max-height:${vv.height - 24}px`
        : undefined;
  const onKeyDown = (e: KeyboardEvent) => {
    if (mode !== 'sheet' || e.key !== 'Tab') return;
    const stops = [...root.current!.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
    if (stops.length === 0) return;
    e.preventDefault();
    const i = stops.indexOf(document.activeElement as HTMLElement);
    stops[(i + (e.shiftKey ? -1 : 1) + stops.length) % stops.length].focus();
  };
  // A bottom bar that has no tab bar under it takes the safe-area inset itself.
  const inset = mode === 'sheet' || (mode === 'tab' && boxFocused.value);
  return (
    <section
      ref={root}
      class={`chat chat-${mode}${inset ? ' chat-inset' : ''}`}
      style={style}
      role={mode === 'tab' ? 'region' : 'dialog'}
      aria-modal={mode === 'sheet' ? 'true' : undefined}
      aria-label="Chat with AdmiralBot"
      tabIndex={-1}
      onKeyDown={onKeyDown}
    >
      <div class="chat-head">
        <span class="chat-avatar big" aria-hidden="true">
          <AdmiralMark size={28} />
        </span>
        <div class="chat-titles">
          <h2>AdmiralBot</h2>
          <span>{subtitle()}</span>
        </div>
        {mode !== 'tab' && <Close label="Close the chat" onClick={closeChat} />}
      </div>
      {terminal ? <ChatTerminal /> : <Conversation mode={mode} />}
    </section>
  );
}

function ActionButton({ name, labels, onClick, children }: { name: string; labels: boolean; onClick: (e: MouseEvent) => void; children: preact.ComponentChildren }) {
  return (
    <button type="button" class="press chat-action" aria-label={name} title={touchKeyboard() ? undefined : name} onClick={onClick}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        {children}
      </svg>
      {labels && name}
    </button>
  );
}

function Conversation({ mode }: { mode: Mode }) {
  const labels = mode !== 'pop';
  const s = session.value;
  const items = thread.value;
  const typing = !!s?.running && s.state === 'working';
  const list = useRef<HTMLDivElement>(null);
  const lastId = items.length > 0 ? items[items.length - 1].id : '';
  // After a change the list shows the newest AdmiralBot message, or the open question card, from its top;
  // after the engineer's own message, or the typing dots, it goes to the bottom (prototype chatScroll).
  useLayoutEffect(() => {
    const el = list.current;
    const last = el?.lastElementChild;
    if (!el || !last) return;
    const kind = last.getAttribute('data-kind');
    if (kind === 'bot' || kind === 'card') el.scrollTop += last.getBoundingClientRect().top - el.getBoundingClientRect().top - 8;
    else el.scrollTop = el.scrollHeight;
  }, [lastId, typing]);

  const notice = startError.value || s?.error || (s && !s.running && !starting.value && asked.value ? 'The chat session is not running.' : '');
  const hint = !notice && s?.running && s.state === 'waiting' && !s.questions.some((q) => !q.answer);
  const running = !!s?.running;
  return (
    <>
      <div class="chat-actions">
        <ActionButton name="New chat" labels={labels} onClick={() => (confirmNew.value = true)}>
          <path d="M12 5v14M5 12h14" />
        </ActionButton>
        <ActionButton name="Open terminal" labels={labels} onClick={() => ((chatView.value = 'terminal'), (confirmNew.value = false))}>
          <path d="M4 6l6 6-6 6M12 19h8" />
        </ActionButton>
        <ActionButton name="Chat settings" labels={labels} onClick={(e) => openAppSettings(e.currentTarget as HTMLElement)}>
          <circle cx="12" cy="12" r="3" />
          <path d="M12 2.8v2.4M12 18.8v2.4M2.8 12h2.4M18.8 12h2.4M5.5 5.5l1.7 1.7M16.8 16.8l1.7 1.7M5.5 18.5l1.7-1.7M16.8 7.2l1.7-1.7" />
        </ActionButton>
      </div>
      {confirmNew.value && (
        <div class="chat-confirm" role="group" aria-label="Start a new chat">
          <p>Start a new chat? This clears the conversation.</p>
          <div class="chat-confirm-row">
            <button type="button" class="press chat-confirm-go" onClick={() => void newChat()}>
              Start new chat
            </button>
            <button type="button" class="press chat-confirm-keep" onClick={() => (confirmNew.value = false)}>
              Keep chatting
            </button>
          </div>
        </div>
      )}
      {(notice || hint) && (
        <div class="chat-notice" role="status">
          <span>{notice || 'AdmiralBot is waiting on a prompt in the terminal.'}</span>
          {running || (!notice && hint) ? (
            <button type="button" class="press chat-notice-button" onClick={() => ((chatView.value = 'terminal'), (confirmNew.value = false))}>
              Open the terminal
            </button>
          ) : (
            <button type="button" class="press chat-notice-button" onClick={() => void startChat()}>
              Start the chat again
            </button>
          )}
        </div>
      )}
      <div class="chat-list" ref={list}>
        {items.map((it) => (it.kind === 'card' ? <Card key={it.id} q={it.q} /> : it.kind === 'fold' ? <Folded key={it.id} q={it.q} /> : <Bubble key={it.id} me={it.kind === 'me'} text={it.text} at={it.at} />))}
        {typing && (
          <div class="chat-row bot" data-kind="typing">
            <Avatar />
            <div class="chat-typing" role="status" aria-label="AdmiralBot is typing">
              <span class="chat-dot" />
              <span class="chat-dot" style="animation-delay:.18s" />
              <span class="chat-dot" style="animation-delay:.36s" />
            </div>
          </div>
        )}
      </div>
      <Box />
    </>
  );
}

const Avatar = () => (
  <span class="chat-avatar" aria-hidden="true">
    <AdmiralMark size={20} />
  </span>
);

function Bubble({ me, text, at }: { me: boolean; text: string; at: string }) {
  return (
    <div class={`chat-row ${me ? 'me' : 'bot'}`} data-kind={me ? 'me' : 'bot'}>
      {!me && <Avatar />}
      <div class="chat-col">
        <span class="chat-bubble">{text}</span>
        <span class="chat-meta">
          {me ? 'You' : 'AdmiralBot'} · {clockTime(at)}
        </span>
      </div>
    </div>
  );
}

function Card({ q }: { q: ChatQuestion }) {
  const qs = q.question?.questions ?? [];
  return (
    <div class="chat-row bot" data-kind="card">
      <Avatar />
      <article class="chat-card">
        <div class="chat-card-head">
          <span class="inbox-kind" style="background-color:#DDF7F3">
            Question
          </span>
          <span class="chat-meta">AdmiralBot asks · {clockTime(q.created)}</span>
        </div>
        <QuestionCard questions={qs} draft={qdraftOf(q.id)} edit={(fn) => editQDraft(q.id, fn)} onSend={(body) => void answerQuestion(q.id, body)} tapSends />
      </article>
    </div>
  );
}

function Folded({ q }: { q: ChatQuestion }) {
  return (
    <div class="chat-row" data-kind="fold">
      <div class="inbox-fold chat-fold" role="status">
        <span class="inbox-check">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M5 12.5l4.5 4.5L19 7.5" />
          </svg>
        </span>
        <span class="inbox-fold-text">{foldText(q.answer ?? {})}</span>
        <span class="inbox-fold-ship">{clockTime(q.answer?.answered)}</span>
      </div>
    </div>
  );
}

function Box() {
  const ta = useRef<HTMLTextAreaElement>(null);
  const text = draft.value;
  const send = () => {
    void sendMessage();
    // Keep the keyboard up on iOS: focus stays inside the tap.
    ta.current?.focus({ preventScroll: true });
  };
  return (
    <>
      {chatError.value && (
        <p class="chat-error" role="alert">
          {chatError.value}
        </p>
      )}
      <form
        class="chat-box"
        onSubmit={(e) => {
          e.preventDefault();
          send();
        }}
      >
        <textarea
          ref={ta}
          aria-label="Message AdmiralBot"
          placeholder="Message AdmiralBot"
          rows={Math.min(4, text.split('\n').length)}
          value={text}
          onInput={(e) => (draft.value = (e.target as HTMLTextAreaElement).value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && e.keyCode !== 229) {
              e.preventDefault();
              send();
            }
          }}
          onFocus={() => {
            boxFocused.value = true;
            if (phone.value) setTimeout(() => window.scrollTo(0, 0), 50);
          }}
          onBlur={() =>
            setTimeout(() => {
              if (document.activeElement !== ta.current) boxFocused.value = false;
            }, 200)
          }
        />
        <button type="button" class="press chat-send" aria-label="Send message" onClick={send}>
          Send
        </button>
      </form>
    </>
  );
}

// The chat session's terminal, in the pane's place: the bridge's terminal on `_chat`. The keys are for touch screens.
function ChatTerminal() {
  const { host, link, linkState, activity } = useBridgeTerminal({ session: CHAT_SESSION, open: true, gone: () => (chatView.value = 'chat'), probe: probeChat });
  const reconnecting = linkState === 'reconnecting';
  const canType = linkState === 'open';
  const input = (data: string) => {
    activity.current();
    link.current?.send({ type: 'input', data });
  };
  return (
    <>
      <div class="chat-term-bar">
        <button type="button" class="press chat-back" aria-label="Back to chat" onClick={() => (chatView.value = 'chat')}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M15 5l-7 7 7 7" />
          </svg>
          Back to chat
        </button>
        <span class="chat-term-title">Chat session</span>
        {signedOut.value ? <span class="term-notice">Open the URL Admiral printed</span> : reconnecting && <span class="term-notice">Reconnecting…</span>}
      </div>
      <div class="chat-term">
        <div class={reconnecting ? 'term-body dim' : 'term-body'}>
          <div class="term-host" ref={host} />
        </div>
      </div>
      <div class="term-keys chat-keys">
        {touchKeyboard() &&
          KEYS.map(([name, label, bytes]) => (
            <button key={name} type="button" class="term-key" aria-label={`Send ${name} to the chat session`} disabled={!canType} onClick={() => input(bytes)}>
              {label}
            </button>
          ))}
        <span class="term-grow" />
        <span class="term-note">The session behind the chat</span>
      </div>
    </>
  );
}
