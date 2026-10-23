import { useEffect, useRef } from 'preact/hooks';
import { harnesses } from './harnesses';
import { type Entry, type InboxQuestion, reasonWords, safeHref, waited } from './inbox';
import {
  answeredQuestion,
  choose,
  chooseOther,
  clock,
  closeInbox,
  draftOf,
  editDraft,
  inbox,
  inboxOpen,
  pickOf,
  sendAnswers,
  wide,
} from './inboxState';
import { LogoFlag } from './Logos';
import { board, ships } from './ships';

// Item files are written by ships and are untrusted: every field below is rendered as text, and the only
// links are http(s) addresses (safeHref).

export function InboxButton() {
  const n = inbox.count();
  const label = n === 0 ? 'Needs you, nothing waiting' : `Needs you, ${n} ${n === 1 ? 'item' : 'items'}`;
  return (
    <button
      type="button"
      id="inbox-button"
      class="pill-hit"
      aria-label={label}
      aria-expanded={inboxOpen.value}
      onClick={() => (inboxOpen.value ? closeInbox() : (inboxOpen.value = true))}
    >
      <span class={`pill-face needs${n === 0 ? ' idle' : ''}${inbox.pulsing() ? ' ping' : ''}`}>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M3 13l2.5-8h13L21 13v6H3z" />
          <path d="M3 13h5l1.5 3h5L16 13h5" />
        </svg>
        Needs you
        {n > 0 && <span class="inbox-badge">{n}</span>}
      </span>
    </button>
  );
}

const KIND: Record<string, [string, string]> = {
  question: ['Question', '#DDF7F3'],
  review: ['Review', '#C9B6FF'],
  findings: ['Findings', '#FFE08A'],
  stop: ['Needs you', '#FF9A7A'],
};

function Empty() {
  return (
    <div class="inbox-empty" role="status">
      <svg aria-hidden="true" width="52" height="96" viewBox="0 0 26 48" fill="none">
        <path d="M8 44 L10 14 L16 14 L18 44 Z" fill="#FFFDF6" stroke="#14304A" stroke-width="2.2" stroke-linejoin="round" />
        <path d="M9.2 26 L16.8 26 L17.4 34 L8.6 34 Z" fill="#E5484D" />
        <rect x="9" y="7" width="8" height="7" fill="#FFC23D" stroke="#14304A" stroke-width="2" />
        <path d="M7 7 L13 2 L19 7 Z" fill="#E5484D" stroke="#14304A" stroke-width="2" stroke-linejoin="round" />
        <path d="M5 44 L21 44" stroke="#14304A" stroke-width="2.5" stroke-linecap="round" />
      </svg>
      <h3>Nothing needs you</h3>
      <p>Every ship is underway or in port. New questions, reviews and stops show up here.</p>
    </div>
  );
}

function Fold({ text, name }: { text: string; name: string }) {
  const ref = useRef<HTMLDivElement>(null);
  // After an action the folded line is brought into view in the list.
  useEffect(() => ref.current?.scrollIntoView({ block: 'nearest' }), []);
  return (
    <div class="inbox-fold" role="status" ref={ref}>
      <span class="inbox-check">
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M5 12.5l4.5 4.5L19 7.5" />
        </svg>
      </span>
      <span class="inbox-fold-text">{text}</span>
      <span class="inbox-fold-ship">{name}</span>
    </div>
  );
}

function Option({
  role,
  on,
  label,
  desc,
  recommended,
  onPick,
}: {
  role: 'radio' | 'checkbox';
  on: boolean;
  label: string;
  desc?: string;
  recommended?: boolean;
  onPick: () => void;
}) {
  return (
    <button type="button" class={`inbox-option press${on ? ' on' : ''}`} role={role} aria-checked={on} onClick={onPick}>
      <span class={`inbox-dot ${role}`} aria-hidden="true" />
      <span class="inbox-option-text">
        <span class="inbox-option-label">
          {label}
          {recommended && <span class="inbox-recommended">Recommended</span>}
        </span>
        {desc && <span class="inbox-option-desc">{desc}</span>}
      </span>
    </button>
  );
}

function Question({ entry, q, i, of }: { entry: Entry; q: InboxQuestion; i: number; of: number }) {
  const p = pickOf(draftOf(entry.key), q);
  const role = q.multi ? 'checkbox' : 'radio';
  return (
    <div class="inbox-question" role="group" aria-label={`Question ${i + 1} of ${of}`}>
      <span class="inbox-eyebrow">
        {of > 1 ? `Question ${i + 1} of ${of} · ` : ''}
        {q.multi ? 'Choose any' : 'Choose one'}
      </span>
      <p class="inbox-question-text">{q.text}</p>
      {q.options.map((o) => (
        <Option key={o.label} role={role} on={p.sel.includes(o.label)} label={o.label} desc={o.description} recommended={q.recommended === o.label} onPick={() => choose(entry.key, q, o.label)} />
      ))}
      <Option role={role} on={p.other} label="Other" desc="Write your own answer" onPick={() => chooseOther(entry.key, q)} />
      {p.other && (
        <input
          type="text"
          class="inbox-input"
          aria-label={`Your answer to question ${i + 1}`}
          placeholder="Your answer"
          autocomplete="off"
          value={p.text}
          onInput={(e) => {
            const text = (e.target as HTMLInputElement).value;
            editDraft(entry.key, (d) => ({ ...d, picks: { ...d.picks, [q.id]: { ...pickOf(d, q), text } } }));
          }}
        />
      )}
    </div>
  );
}

function QuestionBody({ entry }: { entry: Entry }) {
  const qs = entry.item?.question?.questions ?? [];
  const d = draftOf(entry.key);
  const done = qs.filter((q) => answeredQuestion(pickOf(d, q))).length;
  return (
    <>
      {qs.map((q, i) => (
        <Question key={q.id} entry={entry} q={q} i={i} of={qs.length} />
      ))}
      <textarea
        class="inbox-note"
        aria-label="Note (optional)"
        placeholder="Add a note (optional)"
        rows={2}
        value={d.note}
        onInput={(e) => {
          const note = (e.target as HTMLTextAreaElement).value;
          editDraft(entry.key, (x) => ({ ...x, note }));
        }}
      />
      {d.error && (
        <p class="inbox-error" role="alert">
          {d.error}
        </p>
      )}
      <div class="inbox-send-row">
        <button type="button" class="press inbox-primary" disabled={qs.length === 0 || done < qs.length || d.sending} onClick={() => void sendAnswers(entry)}>
          Send answers
        </button>
        <span class="inbox-progress">
          {done} of {qs.length} answered
        </span>
      </div>
    </>
  );
}

function StopBody({ entry, name }: { entry: Entry; name: string }) {
  const code = entry.ship?.reason ?? 'unknown';
  return (
    <>
      <div class="inbox-reason">
        <span class="inbox-reason-line">
          <span class="inbox-reason-words">{reasonWords(code)}</span>
          <span class="inbox-reason-code">{code}</span>
        </span>
        <span class="inbox-waiting">Waiting {waited(entry.ship?.stop_since ?? '', clock.value)}</span>
      </div>
      <div class="inbox-send-row">
        <button
          type="button"
          class="press inbox-primary inbox-attach"
          aria-label={`Attach to ${name} and open its bridge`}
          onClick={() => {
            board(entry.session);
            if (!wide.value) closeInbox(false); // the drawer stays beside the bridge; the sheet would cover it
          }}
        >
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M5 12h14M13 6l6 6-6 6" />
          </svg>
          Attach
        </button>
        <span class="inbox-hint">Opens the bridge. The inbox never types for you.</span>
      </div>
    </>
  );
}

function Item({ entry }: { entry: Entry }) {
  const ship = entry.ship ?? ships.value.find((s) => s.session === entry.session);
  const name = entry.item?.name ?? ship?.name ?? entry.session;
  if (entry.folded !== null) return <Fold text={entry.folded} name={name} />;
  const harness = entry.item?.harness ?? ship?.harness ?? '';
  const [tag, tagBg] = KIND[entry.type === 'stop' ? 'stop' : (entry.item?.kind ?? 'question')] ?? KIND.question;
  const title = entry.item ? entry.item.title : ship?.title || ship?.order_id || ship?.repo || name;
  const href = safeHref(entry.item?.order_url);
  const since = entry.item?.created ?? ship?.stop_since;
  return (
    <article class="inbox-item">
      <div class="inbox-head">
        <svg width="40" height="28" viewBox="0 0 40 28" aria-hidden="true" class="inbox-flag">
          <LogoFlag harness={harness} kind="pier" />
        </svg>
        <div class="inbox-who">
          <span class="inbox-name">{name}</span>
          {ship && (
            <span class="inbox-fleet">
              <span class="inbox-fleet-dot" style={`background-color:${ship.fleet_color}`} aria-hidden="true" />
              {ship.fleet_name} · {harnesses[ship.harness]?.label ?? ship.harness}
            </span>
          )}
        </div>
        {since && entry.type === 'item' && <span class="inbox-age">{waited(since, clock.value)}</span>}
      </div>
      <div class="inbox-titles">
        <span class="inbox-kind" style={`background-color:${tagBg}`}>
          {tag}
        </span>
        <h3>{title}</h3>
        {href && (
          <a class="inbox-link" href={href} target="_blank" rel="noopener noreferrer">
            Open the order ↗
          </a>
        )}
      </div>
      {entry.type === 'stop' ? <StopBody entry={entry} name={name} /> : entry.item?.kind === 'question' && <QuestionBody entry={entry} />}
    </article>
  );
}

// An entry's wrapper keeps its place while it folds and slides out, so the others keep their nodes.
const Wrap = ({ entry }: { entry: Entry }) => (
  <div class={entry.folded !== null ? 'inbox-wrap folded' : 'inbox-wrap'} data-key={entry.key}>
    <Item entry={entry} />
  </div>
);

function CloseButton() {
  return (
    <button type="button" class="press inbox-close" aria-label="Close the inbox" onClick={() => closeInbox()}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#14304A" stroke-width="3" stroke-linecap="round" aria-hidden="true">
        <path d="M6 6l12 12M18 6L6 18" />
      </svg>
    </button>
  );
}

// The wide layout: a drawer on the right, beside the bridge.
function Drawer() {
  const all = inbox.entries();
  const n = inbox.count();
  return (
    <aside class="inbox-drawer" aria-label="Needs you inbox">
      <div class="inbox-top">
        <h2>Needs you</h2>
        <span class="inbox-waiting-count">{n > 0 ? `${n} waiting` : ''}</span>
        <CloseButton />
      </div>
      <div class="inbox-list">
        {all.map((e) => (
          <Wrap key={e.key} entry={e} />
        ))}
        {all.length === 0 && <Empty />}
      </div>
    </aside>
  );
}

const FOCUSABLE = 'button:not([disabled]), a[href], input, textarea, [tabindex]:not([tabindex="-1"])';

// Everything else: a full-screen modal sheet with one item at a time.
function Sheet() {
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const all = inbox.entries();
  const pos = inbox.position();
  const cur = all.find((e) => e.key === inbox.currentKey());
  useEffect(() => root.current?.focus({ preventScroll: true }), []);
  // Its scroll resets only when the current item changes.
  useEffect(() => void list.current?.scrollTo(0, 0), [cur?.key]);
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation(); // launch.ts and Menu.tsx also listen for Escape
      closeInbox();
    } else if (e.key === 'Tab') {
      const stops = [...root.current!.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.offsetParent !== null);
      if (stops.length === 0) return;
      e.preventDefault();
      const i = stops.indexOf(document.activeElement as HTMLElement);
      stops[(i + (e.shiftKey ? -1 : 1) + stops.length) % stops.length].focus();
    }
  };
  return (
    <div class="inbox-sheet" role="dialog" aria-modal="true" aria-label="Needs you inbox" tabIndex={-1} ref={root} onKeyDown={onKeyDown}>
      <div class="inbox-top">
        <h2>Needs you</h2>
        <span class="inbox-waiting-count" />
        <CloseButton />
      </div>
      <div class="inbox-list" ref={list}>
        {cur ? <Wrap key={cur.key} entry={cur} /> : <Empty />}
      </div>
      {pos && (
        <nav class="inbox-nav" aria-label="Inbox items">
          <button type="button" class="press inbox-step" disabled={pos.n <= 1} onClick={() => inbox.move(-1)}>
            ← Back
          </button>
          <span class="inbox-pos" aria-live="polite">
            {pos.n} of {pos.total}
          </span>
          <button type="button" class="press inbox-step next" disabled={pos.n >= pos.total} onClick={() => inbox.move(1)}>
            Next →
          </button>
        </nav>
      )}
    </div>
  );
}

export function InboxDrawer() {
  return inboxOpen.value && wide.value ? <Drawer /> : null;
}

export function InboxSheet() {
  return inboxOpen.value && !wide.value ? <Sheet /> : null;
}
