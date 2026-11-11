import { useEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import type { Conversation } from './conversation';
import { boatRoutes } from './flotilla';
import { bubblesShowing, snapshotOf, type BoxState, type Send } from './messageBox';

/** What the box talks to: the ship, or one of its boats when `crew` is set. */
export interface Target {
  session: string;
  crew?: string;
  state: BoxState;
  /** Whether Interrupt shows: the ship is underway, or the boat is working. */
  interruptible: boolean;
  /** Whether a transcript is there or still to come, so a sent bubble waits for the record and not just the answer. */
  hasConversation: boolean;
  /** Who the box speaks to in a refusal: "This ship" or "This boat". */
  subject: string;
  /** A moored ship: a message wakes it with the text, there is no conversation for a bubble to wait on. */
  wake?: boolean;
}

interface Box {
  text: string;
  sends: Send[];
  error: string;
  interrupting: boolean;
  waking: boolean;
}
const EMPTY: Box = { text: '', sends: [], error: '', interrupting: false, waking: false };
const keyOf = (t: Pick<Target, 'session' | 'crew'>) => t.crew ?? '';

/** The box's state. It lives in the ship view, above the tabs and the boat picked, so a draft, a pending message and a late answer survive a switch of either. */
export function useComposer(target: Target, conv: Conversation) {
  const [boxes, setBoxes] = useState<Record<string, Box>>({});
  const latest = useRef({ conv, key: keyOf(target) });
  latest.current = { conv, key: keyOf(target) };
  const nextId = useRef(0);
  // Sends to one target go one after another, so its harness hears them in the order they were typed.
  const queues = useRef<Record<string, Promise<void>>>({});
  const waiting = useRef<(() => void)[]>([]);
  const key = keyOf(target);

  useEffect(() => {
    if (conv.state !== 'loading') waiting.current.splice(0).forEach((go) => go());
  }, [conv.state, key]);

  const patch = (k: string, fn: (b: Box) => Partial<Box>) => setBoxes((all) => ({ ...all, [k]: { ...(all[k] ?? EMPTY), ...fn(all[k] ?? EMPTY) } }));
  const change = (k: string, id: number, p: Partial<Send>) => patch(k, (b) => ({ sends: b.sends.map((s) => (s.id === id ? { ...s, ...p } : s)) }));

  const run = async (t: Target, id: number, body: string) => {
    const k = keyOf(t);
    const here = () => latest.current.key === k;
    // A send before the first page waits for it, or the message could be in the page and never look new.
    while (here() && latest.current.conv.state === 'loading') await new Promise<void>((go) => waiting.current.push(go));
    // The view moved to another boat meanwhile: what this one holds is unknown, so any user line there counts as the reply.
    change(k, id, { snapshot: here() ? snapshotOf(latest.current.conv.state === 'ok' ? latest.current.conv.items : []) : { ids: [] } });
    const r = await call('POST', boatRoutes(t.session, t.crew).messages, { text: body });
    if (r.ok) return change(k, id, { answered: true });
    // Whatever was typed meanwhile stays; the refused text only comes back to an empty box.
    patch(k, (b) => ({
      sends: b.sends.filter((s) => s.id !== id),
      text: b.text === '' ? body : b.text,
      error: r.status === 409 ? `${t.subject} is not taking messages.` : r.error,
    }));
  };

  const box = boxes[key] ?? EMPTY;
  // Once the ship is underway the wake is over; a later mooring must not find the box still off.
  useEffect(() => {
    if (!target.wake) patch(key, () => ({ waking: false }));
  }, [target.wake, key]);

  /** Wakes the moored ship. A typed text goes with it and the draft clears; an error brings the draft back, and success leaves the box off until the ships push shows the ship underway. */
  const wake = async (body?: string) => {
    const t = target;
    const k = key;
    patch(k, () => ({ waking: true, error: '', ...(body === undefined ? {} : { text: '' }) }));
    const r = await call('POST', `/ships/${encodeURIComponent(t.session)}/wake`, body === undefined ? undefined : { text: body });
    if (r.ok) return;
    patch(k, (b) => ({ waking: false, error: r.error, ...(body !== undefined && b.text === '' ? { text: body } : {}) }));
  };

  const send = () => {
    if (!target.state.enabled || box.waking || !box.text.trim()) return;
    if (target.wake) return void wake(box.text);
    const id = nextId.current++;
    const body = box.text;
    const t = target;
    patch(key, (b) => ({ error: '', text: '', sends: [...b.sends, { id, text: body, snapshot: null, answered: false }] }));
    queues.current[key] = (queues.current[key] ?? Promise.resolve()).then(() => run(t, id, body));
  };

  const interrupt = async () => {
    const t = target;
    patch(key, () => ({ interrupting: true, error: '' }));
    const r = await call('POST', boatRoutes(t.session, t.crew).interrupt);
    patch(key, () => ({ interrupting: false, error: r.ok ? '' : r.error }));
  };

  const bubbles = bubblesShowing(box.sends, conv.items, target.hasConversation);
  return { waking: box.waking, wake, text: box.text, setText: (text: string) => patch(key, () => ({ text })), bubbles, error: box.error, interrupting: box.interrupting, send, interrupt };
}

/** The box under the Conversation tab: a message to the ship or the picked boat, a dimmed bubble per message until the conversation shows it, and Interrupt. */
export function Composer({ target, box }: { target: Target; box: ReturnType<typeof useComposer> }) {
  const field = useRef<HTMLTextAreaElement>(null);
  const { state } = target;
  const { text, setText, bubbles, error, interrupting, send, interrupt, waking } = box;
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
          disabled={!state.enabled || waking}
          placeholder={state.enabled ? (state.placeholder ?? `Message the ${target.crew === undefined ? 'ship' : 'boat'}`) : ''}
          onInput={(e) => setText(e.currentTarget.value)}
          onFocus={() => field.current?.scrollIntoView({ block: 'nearest' })}
          onKeyDown={(e) => {
            if (e.key !== 'Enter' || e.shiftKey || e.isComposing || e.keyCode === 229) return;
            e.preventDefault();
            send();
          }}
        />
        <div class="msg-actions">
          <button type="button" class="btn primary" disabled={!state.enabled || waking || !text.trim()} onClick={send}>
            Send
          </button>
          {target.interruptible ? (
            <button type="button" class="btn" disabled={interrupting} onClick={() => void interrupt()}>
              Interrupt
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}
