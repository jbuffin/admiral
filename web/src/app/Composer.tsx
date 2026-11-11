import { useRef, useState } from 'preact/hooks';
import { call, type Ship } from './api';
import type { Conversation } from './conversation';
import { boxState, sentBubbleShows, userItemCount } from './messageBox';

const shipPath = (session: string) => `/ships/${encodeURIComponent(session)}`;

/** The box under the Conversation tab: a message to the ship, its dimmed bubble until the conversation shows it, and Interrupt. */
export function Composer({ ship, conv }: { ship: Ship; conv: Conversation }) {
  const [text, setText] = useState('');
  const [sent, setSent] = useState<{ text: string; userItemsAtSend: number } | null>(null);
  const [answered, setAnswered] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [interrupting, setInterrupting] = useState(false);
  const field = useRef<HTMLTextAreaElement>(null);
  const state = boxState(ship);

  const send = async () => {
    if (!state.enabled || busy || !text.trim()) return;
    const body = text;
    setError('');
    setAnswered(false);
    setSent({ text: body, userItemsAtSend: userItemCount(conv.items) });
    setBusy(true);
    setText('');
    const r = await call('POST', `${shipPath(ship.session)}/messages`, { text: body });
    setBusy(false);
    if (r.ok) return setAnswered(true);
    setSent(null);
    // Whatever was typed meanwhile stays; the refused text only comes back to an empty box.
    setText((now) => (now === '' ? body : now));
    setError(r.status === 409 ? 'This ship is not taking messages.' : r.error);
  };

  const interrupt = async () => {
    setInterrupting(true);
    setError('');
    const r = await call('POST', `${shipPath(ship.session)}/interrupt`);
    setInterrupting(false);
    if (!r.ok) setError(r.error);
  };

  const showBubble = sent !== null && sentBubbleShows(sent, conv.items, { hasConversation: conv.state === 'ok', answered });
  return (
    <div class="msg-box">
      {showBubble ? (
        <div class="msg user sent" role="status">
          {sent.text}
        </div>
      ) : null}
      {error ? (
        <p class="alert" role="alert">
          {error}
        </p>
      ) : null}
      {state.note ? <p class="msg-note">{state.note}</p> : null}
      <div class="msg-row">
        <textarea
          ref={field}
          class="msg-input"
          aria-label="Message"
          rows={2}
          value={text}
          disabled={!state.enabled}
          placeholder={state.enabled ? 'Message the ship' : ''}
          onInput={(e) => setText(e.currentTarget.value)}
          onFocus={() => field.current?.scrollIntoView({ block: 'nearest' })}
          onKeyDown={(e) => {
            if (e.key !== 'Enter' || e.shiftKey || e.isComposing) return;
            e.preventDefault();
            void send();
          }}
        />
        <div class="msg-actions">
          <button type="button" class="btn primary" disabled={!state.enabled || busy || !text.trim()} onClick={() => void send()}>
            Send
          </button>
          {ship.status === 'underway' ? (
            <button type="button" class="btn" disabled={interrupting} onClick={() => void interrupt()}>
              Interrupt
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
