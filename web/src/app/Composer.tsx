import { useEffect, useRef, useState } from 'preact/hooks';
import { call, type Ship } from './api';
import type { Conversation } from './conversation';
import { boxState, bubblesShowing, snapshotOf, type Send } from './messageBox';

const shipPath = (session: string) => `/ships/${encodeURIComponent(session)}`;

/** The box's state. It lives in the ship view, above the tabs, so a draft, a pending message and a late answer survive a tab switch. */
export function useComposer(ship: Ship, conv: Conversation) {
  const [text, setText] = useState('');
  const [sends, setSends] = useState<Send[]>([]);
  const [error, setError] = useState('');
  const [interrupting, setInterrupting] = useState(false);
  const latest = useRef(conv);
  latest.current = conv;
  const nextId = useRef(0);
  // Sends go one after another, so the harness hears them in the order they were typed.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const waiting = useRef<(() => void)[]>([]);
  const session = ship.session;

  useEffect(() => {
    if (conv.state !== 'loading') waiting.current.splice(0).forEach((go) => go());
  }, [conv.state]);

  const change = (id: number, patch: Partial<Send>) => setSends((all) => all.map((s) => (s.id === id ? { ...s, ...patch } : s)));

  const run = async (id: number, body: string) => {
    // A send before the first page waits for it, or the message could be in the page and never look new.
    if (latest.current.state === 'loading') await new Promise<void>((go) => waiting.current.push(go));
    change(id, { snapshot: snapshotOf(latest.current.state === 'ok' ? latest.current.items : []) });
    const r = await call('POST', `${shipPath(session)}/messages`, { text: body });
    if (r.ok) return change(id, { answered: true });
    setSends((all) => all.filter((s) => s.id !== id));
    // Whatever was typed meanwhile stays; the refused text only comes back to an empty box.
    setText((now) => (now === '' ? body : now));
    setError(r.status === 409 ? 'This ship is not taking messages.' : r.error);
  };

  const send = () => {
    if (!boxState(ship).enabled || !text.trim()) return;
    const id = nextId.current++;
    const body = text;
    setError('');
    setText('');
    setSends((all) => [...all, { id, text: body, snapshot: null, answered: false }]);
    queue.current = queue.current.then(() => run(id, body));
  };

  const interrupt = async () => {
    setInterrupting(true);
    setError('');
    const r = await call('POST', `${shipPath(session)}/interrupt`);
    setInterrupting(false);
    if (!r.ok) setError(r.error);
  };

  // A Claude ship has a transcript to come even before its first line, so its bubbles wait for the record, not the answer.
  const bubbles = bubblesShowing(sends, conv.items, conv.state === 'ok' || (conv.state === 'none' && ship.harness === 'claude'));
  return { text, setText, bubbles, error, interrupting, send, interrupt };
}

/** The box under the Conversation tab: a message to the ship, a dimmed bubble per message until the conversation shows it, and Interrupt. */
export function Composer({ ship, box }: { ship: Ship; box: ReturnType<typeof useComposer> }) {
  const field = useRef<HTMLTextAreaElement>(null);
  const state = boxState(ship);
  const { text, setText, bubbles, error, interrupting, send, interrupt } = box;
  return (
    <div class="msg-box">
      {bubbles.map((b) => (
        <div key={b.id} class="msg user sent" role="status">
          {b.text}
        </div>
      ))}
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
            if (e.key !== 'Enter' || e.shiftKey || e.isComposing || e.keyCode === 229) return;
            e.preventDefault();
            send();
          }}
        />
        <div class="msg-actions">
          <button type="button" class="btn primary" disabled={!state.enabled || !text.trim()} onClick={send}>
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
