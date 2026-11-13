import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import type { Conversation } from './conversation';
import { boatRoutes } from './flotilla';
import { Icon } from './Icon';
import { bubblesShowing, snapshotOf, wakeRefused, type BoxState, type Send } from './messageBox';
import { acceptSkill, matchSkills, skillQuery, type SkillList } from './skillMenu';

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
  /** When the ship moored: a change means it woke and moored again, so the wake that was out is over. */
  mooredAt?: string;
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
  // Once the ship is underway the wake is over, and so when it has moored again between two pushes.
  useEffect(() => {
    patch(key, () => ({ waking: false }));
  }, [target.wake, target.mooredAt, key]);

  /** Wakes the moored ship. A typed text goes with it and the draft clears; an error brings the draft back, and success leaves the box off until the ships push shows the ship underway. */
  const wake = async (body?: string) => {
    const t = target;
    const k = key;
    patch(k, () => ({ waking: true, error: '', ...(body === undefined ? {} : { text: '' }) }));
    const r = await call('POST', `/ships/${encodeURIComponent(t.session)}/wake`, body === undefined ? undefined : { text: body });
    if (r.ok) return;
    patch(k, (b) => wakeRefused(b, body, r.error));
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

// Skills change rarely, so a list is reused for a minute across boxes and remounts.
const skillCache = new Map<string, { at: number; list: Promise<SkillList | null> }>();
function loadSkills(route: string) {
  const hit = skillCache.get(route);
  if (hit && Date.now() - hit.at < 60_000) return hit.list;
  const list = call<SkillList>('GET', route).then((r) => (r.ok ? r.data : null));
  skillCache.set(route, { at: Date.now(), list });
  return list;
}

/** The skills of the box's ship or boat, asked for once the box starts with a mark either harness types. */
function useSkillList(route: string | undefined, wanted: boolean) {
  const [list, setList] = useState<{ route: string; list: SkillList } | null>(null);
  useEffect(() => {
    if (!route || !wanted) return;
    let live = true;
    void loadSkills(route).then((l) => {
      if (live && l) setList({ route, list: l });
    });
    return () => {
      live = false;
    };
  }, [route, wanted]);
  return list && list.route === route ? list.list : null;
}

let menuIds = 0;

/** One line that grows with its text, up to a third of the screen, with Send inside it; Enter sends and Shift+Enter breaks the line. With `skills`, a box that starts with the harness's mark offers its skills above it, as the terminal does. */
export function MessageField({ label, placeholder, text, setText, send, disabled, canSend, focus = false, skills }: { label: string; placeholder: string; text: string; setText: (t: string) => void; send: () => void; disabled: boolean; canSend: boolean; focus?: boolean; skills?: string }) {
  const field = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    // The field enabling late must not take focus from an open sheet.
    if (focus && !disabled && !document.querySelector('[aria-modal="true"]')) field.current?.focus();
  }, [focus, disabled]);
  const menu = useRef<HTMLUListElement>(null);
  // Where a completion put the caret, set on the commit that shows its text: any later and fast typing lands on both sides of it.
  const caretTo = useRef<number | null>(null);
  const [caret, setCaret] = useState(0);
  // The highlighted row, kept with the word it was picked for, so a new word starts at the top.
  const [active, setActive] = useState({ query: '', index: 0 });
  // Esc hides the menu for the word it was showing; typing another word brings it back.
  const [dismissed, setDismissed] = useState<string | null>(null);
  const menuId = useRef(`skill-menu-${++menuIds}`).current;
  const list = useSkillList(skills, /^[/$]/.test(text));
  const query = list ? skillQuery(text, caret, list.mark) : null;
  const matches = query === null || query === dismissed ? [] : matchSkills(list!.skills, query);
  const open = !disabled && matches.length > 0;
  const pick = open ? matches[active.query === query ? Math.min(active.index, matches.length - 1) : 0] : undefined;

  useEffect(() => {
    if (pick) document.getElementById(`${menuId}-${pick.name}`)?.scrollIntoView({ block: 'nearest' });
  }, [pick?.name]);
  useLayoutEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight}px`;
    if (caretTo.current !== null) {
      el.focus();
      el.setSelectionRange(caretTo.current, caretTo.current);
      caretTo.current = null;
    }
  }, [text]);

  const readCaret = () => setCaret(field.current?.selectionStart ?? 0);
  const accept = (name: string) => {
    const next = acceptSkill(text, list!.mark, name);
    caretTo.current = next.caret;
    setText(next.text);
    setCaret(next.caret);
  };
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.isComposing || e.keyCode === 229) return;
    if (pick) {
      const n = matches.length;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        setActive({ query: query!, index: (matches.indexOf(pick) + (e.key === 'ArrowDown' ? 1 : n - 1)) % n });
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        setDismissed(query);
        return;
      }
      // Enter on a name already typed in full sends it, as the terminal runs it.
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey && query !== pick.name)) {
        e.preventDefault();
        accept(pick.name);
        return;
      }
    }
    if (e.key !== 'Enter' || e.shiftKey) return;
    e.preventDefault();
    send();
  };

  return (
    <div class="msg-field">
      {open ? (
        <ul ref={menu} id={menuId} class="skill-menu" role="listbox" aria-label="Skills">
          {matches.map((s) => (
            <li
              key={s.name}
              id={`${menuId}-${s.name}`}
              role="option"
              aria-selected={s === pick}
              class="skill-option"
              // WebKit moves focus on a tap before the click, so an option takes it and the field's blur knows not to close the menu.
              tabIndex={-1}
              onPointerDown={(e) => e.preventDefault()}
              onClick={() => accept(s.name)}
            >
              <span class="skill-name">
                {list!.mark}
                {s.name}
              </span>
              {s.description ? <span class="skill-desc">{s.description}</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
      <textarea
        ref={field}
        class="msg-input"
        aria-label={label}
        aria-autocomplete={skills ? 'list' : undefined}
        aria-controls={open ? menuId : undefined}
        aria-activedescendant={pick ? `${menuId}-${pick.name}` : undefined}
        rows={1}
        value={text}
        disabled={disabled}
        placeholder={placeholder}
        onInput={(e) => {
          setText(e.currentTarget.value);
          setCaret(e.currentTarget.selectionStart);
        }}
        // Chromium (Brave) sometimes fires a paste and its beforeinput but never inserts the text, so the box inserts it itself.
        onPaste={(e) => {
          const pasted = e.clipboardData?.getData('text/plain');
          if (!pasted) return;
          e.preventDefault();
          const el = e.currentTarget;
          el.setRangeText(pasted, el.selectionStart, el.selectionEnd, 'end');
          setText(el.value);
          setCaret(el.selectionStart);
        }}
        onKeyUp={readCaret}
        onClick={readCaret}
        onBlur={(e) => {
          if (!(e.relatedTarget instanceof Node && menu.current?.contains(e.relatedTarget))) setDismissed(query);
        }}
        onFocus={() => {
          setDismissed(null);
          field.current?.scrollIntoView({ block: 'nearest' });
        }}
        onKeyDown={onKeyDown}
      />
      <button type="button" class="msg-send" aria-label="Send" disabled={disabled || !canSend} onClick={send}>
        <span class="msg-send-dot">
          <Icon name="send" size={18} />
        </span>
      </button>
    </div>
  );
}

/** Interrupt, quiet beside the tabs or over the box: it presses Esc in the pane. */
export function InterruptButton({ box }: { box: ReturnType<typeof useComposer> }) {
  return (
    <button type="button" class="interrupt-btn" disabled={box.interrupting} onClick={() => void box.interrupt()}>
      Interrupt
    </button>
  );
}

/** The box under the Conversation tab: a message to the ship or the picked boat, and a dimmed bubble per message until the conversation shows it. Interrupt goes over the box unless the view puts it beside its tabs. */
export function Composer({ target, box, interrupt = true, focus = false }: { target: Target; box: ReturnType<typeof useComposer>; interrupt?: boolean; focus?: boolean }) {
  const { state } = target;
  const { text, setText, bubbles, error, send, waking } = box;
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
      {interrupt && target.interruptible ? (
        <div class="msg-tools">
          <InterruptButton box={box} />
        </div>
      ) : null}
      <MessageField
        label="Message"
        placeholder={state.enabled ? (state.placeholder ?? `Message the ${target.crew === undefined ? 'ship' : 'boat'}`) : ''}
        text={text}
        setText={setText}
        send={send}
        disabled={!state.enabled || waking}
        canSend={text.trim() !== ''}
        focus={focus}
        skills={boatRoutes(target.session, target.crew).skills}
      />
    </div>
  );
}
