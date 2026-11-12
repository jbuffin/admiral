import { useRef, useState } from 'preact/hooks';
import { call } from './api';
import type { Entry } from './inbox';
import { prLink } from '../inbox';
import { createDecisions, type Decisions, type FindingCard, readCards, type Verdict } from '../findings';
import { REMOVED_FOLD } from './itemActions';

// Item files are written by ships and are untrusted: every field below is rendered as text, and the only links are
// the PR's own https address (prLink).

export type ReviewStep = 'idle' | 'choose' | 'confirm' | 'discard';
export interface ReviewUi {
  step: ReviewStep;
  event: string;
  edited: boolean;
}
export const blankReview = (): ReviewUi => ({ step: 'idle', event: '', edited: false });

export const EVENTS: [string, string][] = [
  ['COMMENT', 'Leave comments without approving or blocking.'],
  ['APPROVE', 'Approve the changes.'],
  ['REQUEST_CHANGES', 'Block the merge until changes are made.'],
];

/** What an action on the open item is doing; `locked` is a refusal that cannot be retried. */
export interface Acting {
  busy: boolean;
  error: string;
  /** The note a refusal names, else ''. */
  note: string;
  locked: boolean;
  run: (verb: string, body: unknown, fold: (data: never) => string) => Promise<void>;
  clear: () => void;
}

export function useActing(entry: Entry, fold: (text: string) => void, lockedBy: (status: number | undefined, message: string) => boolean): Acting {
  const [state, setState] = useState({ busy: false, error: '', note: '', locked: false });
  const busy = useRef(false);
  return {
    ...state,
    clear: () => setState((s) => ({ ...s, error: '', note: '' })),
    async run(verb, body, text) {
      if (busy.current) return;
      busy.current = true;
      setState((s) => ({ ...s, busy: true, error: '', note: '' }));
      const r = await call<Record<string, unknown>>('POST', `/inbox/${encodeURIComponent(entry.session)}/${encodeURIComponent(entry.id!)}/${verb}`, body);
      busy.current = false;
      if (r.ok) return fold(text((r.data ?? {}) as never));
      setState({ busy: false, error: r.error, note: r.note ?? '', locked: lockedBy(r.status, r.error) });
    },
  };
}

function PrLine({ pr }: { pr: ReturnType<typeof prLink> }) {
  if (!pr) return null;
  return (
    <span class="inbox-pr">
      <a href={pr.href} target="_blank" rel="noopener noreferrer">
        {pr.label} ↗
      </a>
      <span class="inbox-host"> {pr.host}</span>
    </span>
  );
}

export function Failure({ message }: { message: string }) {
  return message ? (
    <p class="inbox-error" role="alert">
      {message}
    </p>
  ) : null;
}

export function ReviewBody({ entry, ui, set, acting, submit, discard }: { entry: Entry; ui: ReviewUi; set: (p: Partial<ReviewUi>) => void; acting: Acting; submit: () => void; discard: () => void }) {
  const r = entry.item?.review;
  const pr = prLink(r?.pr_url);
  const off = acting.busy || acting.locked;
  const go = (step: ReviewStep, extra: Partial<ReviewUi> = {}) => {
    acting.clear();
    set({ step, ...extra });
  };
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
      {(ui.step === 'idle' || acting.locked) && (
        <div class="inbox-send-row">
          <button type="button" class="btn primary" disabled={off} onClick={() => go('choose')}>
            Submit
          </button>
          {pr && (
            <a class="btn" href={pr.files} target="_blank" rel="noopener noreferrer" onClick={() => set({ edited: true })}>
              Edit ↗
            </a>
          )}
          <button type="button" class="btn danger" disabled={off} onClick={() => go('discard')}>
            Discard
          </button>
        </div>
      )}
      {ui.step === 'choose' && (
        <div class="inbox-ask" role="group" aria-label="Submit the review as">
          {EVENTS.map(([ev, desc]) => (
            <button key={ev} type="button" class="inbox-option" onClick={() => go('confirm', { event: ev })}>
              <span class="inbox-option-text">
                <span class="inbox-option-label">{ev}</span>
                <span class="inbox-option-desc">{desc}</span>
              </span>
            </button>
          ))}
          <button type="button" class="btn" onClick={() => go('idle')}>
            Cancel
          </button>
        </div>
      )}
      {ui.step === 'confirm' && (
        <div class="inbox-ask confirm">
          <p>
            Submit this review as <b>{ui.event}</b>? It is posted to the PR as you.
          </p>
          <div class="inbox-send-row">
            <button type="button" class="btn primary" disabled={off} onClick={submit}>
              Submit {ui.event}
            </button>
            <button type="button" class="btn" disabled={off} onClick={() => go('choose')}>
              Change
            </button>
          </div>
        </div>
      )}
      {ui.step === 'discard' && (
        <div class="inbox-ask discard">
          <p>Discard this pending review? The draft comments are deleted from GitHub.</p>
          <div class="inbox-send-row">
            <button type="button" class="btn danger" disabled={off} onClick={discard}>
              Discard review
            </button>
            <button type="button" class="btn" disabled={off} onClick={() => go('idle')}>
              Keep it
            </button>
          </div>
        </div>
      )}
      {acting.busy && (
        <p class="inbox-hint" role="status" aria-live="polite">
          Asking GitHub…
        </p>
      )}
    </>
  );
}

const VERDICT: Record<Verdict, string> = { agree: 'Agree', disagree: 'Disagree', unclear: 'Unclear' };
const DECISIONS = [
  ['Approve', 'approved'],
  ['Edit', 'edited'],
  ['Reject', 'rejected'],
] as const;

interface CardProps {
  card: FindingCard;
  n: number;
  d: Decisions;
  draft: string | undefined;
  setDraft: (text: string | undefined) => void;
  locked: boolean;
  changed: () => void;
}

function FindingCardView({ card, n, d, draft, setDraft, locked, changed }: CardProps) {
  const dec = d.decision(card.id);
  const shown = d.proposal(card.id);
  const press = (name: string) => {
    if (name === 'Approve') d.approve(card.id);
    else if (name === 'Reject') {
      setDraft(undefined);
      d.reject(card.id);
    } else setDraft(shown?.content ?? '');
    changed();
  };
  return (
    <section class="inbox-finding" aria-label={`Finding card ${n}`}>
      <div class="inbox-finding-head">
        <span class="inbox-kind">{VERDICT[card.verdict]}</span> <span class="inbox-source">{card.source}</span>
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
              <textarea class="inbox-note" aria-label={`Edit the proposal for card ${n}`} rows={5} value={draft} readOnly={locked} onInput={(e) => setDraft((e.target as HTMLTextAreaElement).value)} />
              <div class="inbox-send-row">
                <button
                  type="button"
                  class="btn primary"
                  disabled={draft.trim() === '' || locked}
                  onClick={() => {
                    d.save(card.id, draft);
                    setDraft(undefined);
                    changed();
                  }}
                >
                  Save
                </button>
                <button type="button" class="btn" onClick={() => setDraft(undefined)}>
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
      <div class="inbox-send-row" role="group" aria-label={`Decision for card ${n}`}>
        {DECISIONS.filter(([name]) => name !== 'Edit' || (shown && (shown.kind === 'diff' || shown.kind === 'reply'))).map(([name, value]) => (
          <button key={name} type="button" class={`btn${dec === value ? ' primary' : ''}`} aria-pressed={dec === value} disabled={locked} onClick={() => press(name)}>
            {name}
          </button>
        ))}
      </div>
    </section>
  );
}

/** The findings decisions and open edits; the open Peek holds them so they survive its conversation changing shape. */
export function useFindings(entry: Entry) {
  const made = useRef<{ cards: FindingCard[]; d: Decisions } | undefined>(undefined);
  if (!made.current) {
    const cards = readCards(entry.item?.findings?.cards);
    made.current = { cards, d: createDecisions(cards) };
  }
  const [, setRev] = useState(0);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  return { ...made.current, drafts, setDrafts, changed: () => setRev((n) => n + 1) };
}
export type Findings = ReturnType<typeof useFindings>;

export function FindingsBody({ entry, acting, state }: { entry: Entry; acting: Acting; state: Findings }) {
  const { cards, d, drafts, setDrafts, changed } = state;
  const pr = prLink(entry.item?.findings?.pr_url);
  const { decided, total } = d.progress();
  const open = Object.keys(drafts).length > 0;
  const off = acting.busy || acting.locked;
  const send = () => {
    if (!d.canSend() || open) return;
    const summary = d.summary();
    void acting.run('findings', d.payload(), () => summary);
  };
  return (
    <>
      <PrLine pr={pr} />
      {cards.map((c, i) => (
        <FindingCardView
          key={c.id}
          card={c}
          n={i + 1}
          d={d}
          draft={drafts[c.id]}
          setDraft={(text) =>
            setDrafts((m) => {
              const next = { ...m };
              if (text === undefined) delete next[c.id];
              else next[c.id] = text;
              return next;
            })
          }
          locked={off}
          changed={changed}
        />
      ))}
      <div class="inbox-send-row">
        <button type="button" class="btn primary" disabled={!d.canSend() || open || off} onClick={send}>
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

const removeCopy = (kind: string | undefined, review: boolean) =>
  kind === 'crew_notes'
    ? 'Remove this item? It counts as Drop: its notes are discarded.'
    : kind === 'crew_member'
      ? 'Remove this item? It counts as Drop: its notes are discarded and the ad hoc file is deleted if unchanged.'
      : review
        ? 'Remove this review from the inbox? Nothing changes on GitHub: the pending review stays there.'
        : 'Remove this item from the inbox? The ship never gets an answer from here.';

/** Any item can leave the inbox unanswered; a review's draft stays on GitHub. */
export function RemoveRow({ entry, confirm, setConfirm, acting, fold = () => REMOVED_FOLD }: { entry: Entry; confirm: boolean; setConfirm: (on: boolean) => void; acting: Acting; fold?: (data: never) => string }) {
  const review = entry.item?.kind === 'review';
  const kind = entry.item?.kind;
  const off = acting.busy;
  if (!confirm) {
    return (
      <div class="inbox-remove-row">
        <button
          type="button"
          class="btn quiet"
          onClick={() => {
            acting.clear();
            setConfirm(true);
          }}
        >
          Remove from inbox
        </button>
      </div>
    );
  }
  return (
    <div class="inbox-ask discard">
      <p>{removeCopy(kind, review)}</p>
      <div class="inbox-send-row">
        <button type="button" class="btn danger" disabled={off} onClick={() => void acting.run('remove', undefined, fold)}>
          Remove
        </button>
        <button
          type="button"
          class="btn"
          disabled={off}
          onClick={() => {
            acting.clear();
            setConfirm(false);
          }}
        >
          Keep it
        </button>
      </div>
    </div>
  );
}
