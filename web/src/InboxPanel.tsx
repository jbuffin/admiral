import { useEffect, useRef } from 'preact/hooks';
import { harnesses } from './harnesses';
import { type Entry, type InboxQuestion, prLink, reasonWords, safeHref, waited } from './inbox';
import { type FindingCard, type Verdict } from './findings';
import {
  answeredQuestion,
  choose,
  chooseOther,
  cardsOf,
  clock,
  closeInbox,
  FORGET_SUPPORTED,
  decide,
  decisionsOf,
  discardReview,
  draftOf,
  editDraft,
  editReview,
  editingOf,
  findingsUiOf,
  isEditing,
  markEdited,
  reviewOf,
  sendFindings,
  setEditing,
  submitReview,
  watchDecisions,
  inbox,
  inboxOpen,
  pickOf,
  sendAnswers,
} from './inboxState';
import { LogoFlag } from './Logos';
import { board, ships } from './ships';
import { phone, wide } from './sizeClass';

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

function StopBody({ entry, name, shipName }: { entry: Entry; name: string; shipName: string }) {
  const code = (entry.crew ? entry.crew.reason : entry.ship?.reason) ?? 'unknown';
  const crew = entry.crew;
  const wait = new Date(entry.since).toISOString();
  return (
    <>
      <div class="inbox-reason">
        <span class="inbox-reason-line">
          <span class="inbox-reason-words">{reasonWords(code)}</span>
          <span class="inbox-reason-code">{code}</span>
        </span>
        <span class="inbox-waiting">Waiting {waited(wait, clock.value)}</span>
      </div>
      <div class="inbox-send-row">
        <button
          type="button"
          class="press inbox-primary inbox-attach"
          aria-label={crew ? `Attach to ${crew.name}, crew of ${shipName} and open its bridge` : `Attach to ${name} and open its bridge`}
          onClick={() => {
            board(entry.session, crew?.name);
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

const EVENTS: [string, string, string][] = [
  ['COMMENT', 'Leave comments without approving or blocking.', '#FFF6DA'],
  ['APPROVE', 'Approve the changes.', '#DDF7F3'],
  ['REQUEST_CHANGES', 'Block the merge until changes are made.', '#FFD9CC'],
];

function PrLine({ pr }: { pr: ReturnType<typeof prLink> }) {
  if (!pr) return null;
  return (
    <span class="inbox-pr">
      <a class="inbox-link" href={pr.href} target="_blank" rel="noopener noreferrer">
        {pr.label} ↗
      </a>
      <span class="inbox-host">{pr.host}</span>
    </span>
  );
}

function Failure({ message, children }: { message: string; children?: preact.ComponentChildren }) {
  if (!message) return null;
  return (
    <div class="inbox-failure">
      <p class="inbox-error" role="alert">
        {message}
      </p>
      {children}
    </div>
  );
}

function ReviewBody({ entry }: { entry: Entry }) {
  const r = entry.item?.review;
  const ui = reviewOf(entry.key);
  const pr = prLink(r?.pr_url);
  const busy = ui.busy;
  const set = (step: 'idle' | 'choose' | 'confirm' | 'discard') => editReview(entry.key, { step, error: '', gh: false });
  return (
    <>
      <div class="inbox-summary">
        <span class="inbox-eyebrow">Summary</span>
        <p class="inbox-summary-text">{typeof r?.summary === 'string' ? r.summary : ''}</p>
      </div>
      <div class="inbox-prrow">
        <PrLine pr={pr} />
        <span class="inbox-hint">Pending review #{String(r?.review_id ?? '')}, not yet submitted</span>
      </div>
      {ui.step === 'idle' && (
        <div class="inbox-actions">
          <button type="button" class="press inbox-primary" disabled={busy} onClick={() => set('choose')}>
            Submit
          </button>
          {pr && (
            <a class="press inbox-secondary" href={pr.files} target="_blank" rel="noopener noreferrer" onClick={() => markEdited(entry.key)}>
              Edit ↗
            </a>
          )}
          <button type="button" class="press inbox-danger" disabled={busy} onClick={() => set('discard')}>
            Discard
          </button>
        </div>
      )}
      {ui.step === 'choose' && (
        <div class="inbox-ask" role="group" aria-label="Submit the review as">
          {EVENTS.map(([ev, desc, bg]) => (
            <button key={ev} type="button" class="press inbox-event" style={`background-color:${bg}`} onClick={() => editReview(entry.key, { step: 'confirm', event: ev, error: '', gh: false })}>
              <span class="inbox-event-id">{ev}</span>
              <span class="inbox-option-desc">{desc}</span>
            </button>
          ))}
          <button type="button" class="press inbox-secondary" onClick={() => set('idle')}>
            Cancel
          </button>
        </div>
      )}
      {ui.step === 'confirm' && (
        <div class="inbox-ask confirm">
          <p>
            Submit this review as <b>{ui.event}</b>? It is posted to the PR as you.
          </p>
          <div class="inbox-actions">
            <button type="button" class="press inbox-primary" disabled={busy} onClick={() => void submitReview(entry, ui.event)}>
              Submit {ui.event}
            </button>
            <button type="button" class="press inbox-secondary" disabled={busy} onClick={() => set('choose')}>
              Change
            </button>
          </div>
        </div>
      )}
      {ui.step === 'discard' && (
        <div class="inbox-ask discard">
          <p>Discard this pending review? The draft comments are deleted from GitHub.</p>
          <div class="inbox-actions">
            <button type="button" class="press inbox-danger solid" disabled={busy} onClick={() => void discardReview(entry)}>
              Discard review
            </button>
            <button type="button" class="press inbox-secondary" disabled={busy} onClick={() => set('idle')}>
              Keep it
            </button>
          </div>
        </div>
      )}
      {busy && (
        <p class="inbox-hint" role="status" aria-live="polite">
          Asking GitHub…
        </p>
      )}
      <Failure message={ui.error}>
        {ui.gh && FORGET_SUPPORTED && (
          <button type="button" class="press inbox-secondary" disabled={busy} onClick={() => void discardReview(entry, true)}>
            Remove from inbox — the review may still be pending on GitHub
          </button>
        )}
      </Failure>
    </>
  );
}

const VERDICT: Record<Verdict, [string, string]> = {
  agree: ['Agree', '#9BE08A'],
  disagree: ['Disagree', '#FF9A7A'],
  unclear: ['Unclear', '#FFE08A'],
};
const DECISIONS = [
  ['Approve', 'approved', '#BDEFB0'],
  ['Edit', 'edited', '#FFF0B8'],
  ['Reject', 'rejected', '#FFC9B8'],
] as const;

function FindingCardView({ entry, card, n }: { entry: Entry; card: FindingCard; n: number }) {
  watchDecisions();
  const d = decisionsOf(entry);
  const dec = d.decision(card.id);
  const draft = editingOf(entry.key, card.id);
  const locked = findingsUiOf(entry.key).busy; // the answer is on its way: what is sent is what the fold says
  const shown = d.proposal(card.id);
  const [verdict, vbg] = VERDICT[card.verdict];
  const press = (name: string) => {
    if (name === 'Approve') decide(entry, (x) => x.approve(card.id));
    else if (name === 'Reject') {
      setEditing(entry.key, card.id, undefined);
      decide(entry, (x) => x.reject(card.id));
    } else setEditing(entry.key, card.id, shown?.content ?? '');
  };
  return (
    <section class="inbox-finding" aria-label={`Finding card ${n}`}>
      <div class="inbox-finding-head">
        <span class="inbox-kind" style={`background-color:${vbg}`}>
          {verdict}
        </span>
        <span class="inbox-source">{card.source}</span>
      </div>
      <blockquote class="inbox-quote">{card.quote}</blockquote>
      <p class="inbox-why">
        <b>Why:</b> {card.reason}
      </p>
      {shown && (
        <div class="inbox-proposal">
          <span class="inbox-eyebrow">{shown.kind === 'reply' ? 'Proposed reply' : 'Proposed change'}</span>
          {draft === undefined ? (
            <pre class="inbox-pre">{shown.content}</pre>
          ) : (
            <>
              <textarea
                class="inbox-edit"
                aria-label={`Edit the proposal for card ${n}`}
                rows={5}
                value={draft}
                readOnly={locked}
                onInput={(e) => setEditing(entry.key, card.id, (e.target as HTMLTextAreaElement).value)}
              />
              <div class="inbox-actions">
                <button
                  type="button"
                  class="press inbox-primary"
                  disabled={draft.trim() === '' || locked}
                  onClick={() => {
                    decide(entry, (x) => x.save(card.id, draft));
                    setEditing(entry.key, card.id, undefined);
                  }}
                >
                  Save
                </button>
                <button type="button" class="press inbox-secondary" onClick={() => setEditing(entry.key, card.id, undefined)}>
                  Cancel
                </button>
              </div>
            </>
          )}
        </div>
      )}
      {card.evidence.length > 0 && (
        <div class="inbox-evidence">
          <span class="inbox-eyebrow">Evidence</span>
          {card.evidence.map((e, i) => (
            <code key={i}>{e}</code>
          ))}
        </div>
      )}
      <div class="inbox-decide" role="group" aria-label={`Decision for card ${n}`}>
        {DECISIONS.filter(([name]) => name !== 'Edit' || (shown && (shown.kind === 'diff' || shown.kind === 'reply'))).map(([name, value, bg]) => (
          <button
            key={name}
            type="button"
            class={`press inbox-decision${dec === value ? ' on' : ''}`}
            style={dec === value ? `background-color:${bg}` : undefined}
            aria-pressed={dec === value}
            disabled={locked}
            onClick={() => press(name)}
          >
            {name}
          </button>
        ))}
      </div>
    </section>
  );
}

function FindingsBody({ entry }: { entry: Entry }) {
  watchDecisions();
  const d = decisionsOf(entry);
  const ui = findingsUiOf(entry.key);
  const cards = cardsOf(entry);
  const pr = prLink(entry.item?.findings?.pr_url);
  const { decided, total } = d.progress();
  const open = isEditing(entry.key);
  return (
    <>
      <PrLine pr={pr} />
      {cards.map((c, i) => (
        <FindingCardView key={c.id} entry={entry} card={c} n={i + 1} />
      ))}
      <Failure message={ui.error} />
      <div class="inbox-send-row">
        <button type="button" class="press inbox-primary" disabled={!d.canSend() || open || ui.busy} onClick={() => void sendFindings(entry)}>
          Send
        </button>
        <span class="inbox-progress">
          {decided} of {total} decided
        </span>
        {open && <span class="inbox-hint">Save or Cancel the open edit first.</span>}
      </div>
    </>
  );
}

function Item({ entry }: { entry: Entry }) {
  const ship = entry.ship ?? ships.value.find((s) => s.session === entry.session);
  const shipName = entry.item?.name ?? ship?.name ?? entry.session;
  const name = entry.crew ? `${shipName} › ${entry.crew.name}` : shipName;
  if (entry.folded !== null) return <Fold text={entry.folded} name={name} />;
  const harness = entry.item?.harness ?? ship?.harness ?? '';
  const [tag, tagBg] = KIND[entry.type === 'stop' ? 'stop' : (entry.item?.kind ?? 'question')] ?? KIND.question;
  const title = entry.item ? entry.item.title : [ship?.order_id, ship?.title].filter(Boolean).join(' ') || ship?.repo || name;
  const href = safeHref(entry.item?.order_url);
  const since = entry.item?.created ?? (entry.type === 'stop' ? new Date(entry.since).toISOString() : undefined);
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
        {since && <span class="inbox-age">{waited(since, clock.value)}</span>}
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
      {entry.type === 'stop' ? <StopBody entry={entry} name={name} shipName={shipName} /> : entry.item?.kind === 'review' ? <ReviewBody entry={entry} /> : entry.item?.kind === 'findings' ? <FindingsBody entry={entry} /> : entry.item?.kind === 'question' && <QuestionBody entry={entry} />}
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

// Everything else: a full-screen modal sheet with one item at a time. On a phone it is the Inbox tab's
// content instead: the same one-at-a-time layout in place, a region with no Close button.
function Sheet({ tab = false }: { tab?: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const all = inbox.entries();
  const pos = inbox.position();
  const cur = all.find((e) => e.key === inbox.currentKey());
  useEffect(() => (tab ? undefined : root.current?.focus({ preventScroll: true })), []);
  // Its scroll resets only when the current item changes.
  useEffect(() => void list.current?.scrollTo(0, 0), [cur?.key]);
  const onKeyDown = (e: KeyboardEvent) => {
    if (tab) return;
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
    <div
      class={tab ? 'inbox-sheet in-tab' : 'inbox-sheet'}
      role={tab ? 'region' : 'dialog'}
      aria-modal={tab ? undefined : 'true'}
      aria-label="Needs you inbox"
      tabIndex={-1}
      ref={root}
      onKeyDown={onKeyDown}
    >
      <div class="inbox-top">
        <h2>Needs you</h2>
        <span class="inbox-waiting-count">{tab && inbox.count() > 0 ? `${inbox.count()} waiting` : ''}</span>
        {!tab && <CloseButton />}
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
  return inboxOpen.value && !wide.value && !phone.value ? <Sheet /> : null;
}

// The phone's Inbox tab: shown whatever `inboxOpen` says, since Attach closes the sheet and the tab stays.
export const InboxTab = () => <Sheet tab />;
