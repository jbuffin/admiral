import type { InboxQuestion } from './inbox';
import { type AnswerBody, type Draft, answerBody, answeredQuestion, choose, chooseOther, pickOf, setOtherText } from './questions';

// The question card of the inbox and of the chat. It owns no state: the caller holds the draft and says how
// to edit it and what to do with the answer.

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

interface CardProps {
  questions: InboxQuestion[];
  draft: Draft;
  edit: (fn: (d: Draft) => Draft) => void;
  onSend: (body: AnswerBody) => void;
  /** The chat's rule: a tap on the option of a lone single-choice question sends it, and Send answers waits for a reason. */
  tapSends?: boolean;
}

function Question({ q, i, of, draft, edit, tap }: { q: InboxQuestion; i: number; of: number; draft: Draft; edit: CardProps['edit']; tap: (label: string) => void }) {
  const p = pickOf(draft, q);
  const role = q.multi ? 'checkbox' : 'radio';
  return (
    <div class="inbox-question" role="group" aria-label={`Question ${i + 1} of ${of}`}>
      <span class="inbox-eyebrow">
        {of > 1 ? `Question ${i + 1} of ${of} · ` : ''}
        {q.multi ? 'Choose any' : 'Choose one'}
      </span>
      <p class="inbox-question-text">{q.text}</p>
      {q.options.map((o) => (
        <Option key={o.label} role={role} on={p.sel.includes(o.label)} label={o.label} desc={o.description} recommended={q.recommended === o.label} onPick={() => tap(o.label)} />
      ))}
      <Option role={role} on={p.other} label="Other" desc="Write your own answer" onPick={() => edit((d) => chooseOther(d, q))} />
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
            edit((d) => setOtherText(d, q, text));
          }}
        />
      )}
    </div>
  );
}

export function QuestionCard({ questions: qs, draft: d, edit, onSend, tapSends = false }: CardProps) {
  const done = qs.filter((q) => answeredQuestion(pickOf(d, q))).length;
  const extra = qs.some((q) => q.multi || pickOf(d, q).other) || d.note.trim() !== '';
  const tap = (q: InboxQuestion, label: string) => {
    const next = choose(d, q, label);
    edit(() => next);
    if (tapSends && !q.multi && qs.length === 1 && d.note.trim() === '' && !pickOf(d, q).other && !d.sending) onSend(answerBody(qs, next));
  };
  return (
    <>
      {qs.map((q, i) => (
        <Question key={q.id} q={q} i={i} of={qs.length} draft={d} edit={edit} tap={(label) => tap(q, label)} />
      ))}
      <textarea
        class="inbox-note"
        aria-label="Note (optional)"
        placeholder="Add a note (optional)"
        rows={2}
        value={d.note}
        onInput={(e) => {
          const note = (e.target as HTMLTextAreaElement).value;
          edit((x) => ({ ...x, note }));
        }}
      />
      {d.error && (
        <p class="inbox-error" role="alert">
          {d.error}
        </p>
      )}
      {(!tapSends || extra || done > 0) && (
        <div class="inbox-send-row">
          <button type="button" class="press inbox-primary" disabled={qs.length === 0 || done < qs.length || d.sending} onClick={() => onSend(answerBody(qs, d))}>
            Send answers
          </button>
          <span class="inbox-progress">
            {done} of {qs.length} answered
          </span>
        </div>
      )}
    </>
  );
}
