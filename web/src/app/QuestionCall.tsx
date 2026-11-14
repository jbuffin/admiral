import { useEffect, useRef, useState } from 'preact/hooks';
import { call as post, shipUrl, SLOW_TIMEOUT_MS, type Item } from './api';
import type { AnswerCard, CardQuestion } from './answerCard';
import { Actions, Button } from './Button';
import { renderRestrictedInline, renderRestrictedMarkdown } from './markdown';
import { questionCallView, type QuestionView } from './questionView';

const Inline = ({ src, class: cls }: { src: string; class?: string }) => <span class={`md ${cls ?? ''}`} dangerouslySetInnerHTML={{ __html: renderRestrictedInline(src) }} />;
const Md = ({ src, class: cls }: { src: string; class?: string }) => <div class={`md ${cls ?? ''}`} dangerouslySetInnerHTML={{ __html: renderRestrictedMarkdown(src) }} />;

function Header({ view }: { view: QuestionView }) {
  return (
    <>
      {view.header ? <span class="q-chip">{view.header}</span> : null}
      <Md src={view.text} class="q-text" />
    </>
  );
}

/** One question as the transcript keeps it: what was asked, which options were chosen, and any free-text answer. */
function ReadOnlyQuestion({ view }: { view: QuestionView }) {
  return (
    <div class="q-block">
      <Header view={view} />
      <ul class="q-options">
        {view.options.map((o) => (
          <li key={o.label} class={`q-option${o.chosen ? ' chosen' : ''}`}>
            <span class="q-mark" aria-hidden="true">
              {o.chosen ? '✔' : view.multiSelect ? '☐' : '○'}
            </span>
            <span class="q-label">
              <strong>{o.label}</strong>
              {o.chosen ? <span class="visually-hidden"> (chosen)</span> : null}
              {o.description ? <Md src={o.description} class="muted q-desc" /> : null}
            </span>
          </li>
        ))}
      </ul>
      {view.freeText !== undefined ? <p class="q-free">Other: {view.freeText}</p> : null}
    </div>
  );
}

interface Pick {
  picked: number[];
  other: string;
  otherOn: boolean;
}
const blank = (): Pick => ({ picked: [], other: '', otherOn: false });
const answered = (q: CardQuestion, p: Pick) => (q.multiSelect ? p.picked.length > 0 || (p.otherOn && p.other.trim() !== '') : p.picked.length === 1 || (p.otherOn && p.other.trim() !== ''));

const chosen = (q: CardQuestion, p: Pick) => [...p.picked.map((n) => q.options[n - 1]), ...(p.otherOn && p.other.trim() ? [p.other.trim()] : [])].join(', ');

/** A pending call: one question at a time, a review, then one request with every answer. */
function Answering({ items, card, session, crew, openTerminal }: { items: Item[]; card: Extract<AnswerCard, { kind: 'question' }>; session: string; crew?: string; openTerminal: () => void }) {
  const views = questionCallView(items);
  const qs = card.questions;
  const [step, setStep] = useState(0);
  const [picks, setPicks] = useState<Pick[]>(() => qs.map(blank));
  const [busy, setBusy] = useState(false);
  // Which button sent, so only it spins while the card waits to go.
  const [from, setFrom] = useState('');
  const [refused, setRefused] = useState('');
  // Set before the first re-render, so a double tap sends once.
  const sending = useRef(false);
  const key = `${card.reason}:${card.item}`;
  useEffect(() => {
    sending.current = false;
    setBusy(false);
    setFrom('');
    setRefused('');
    setStep(0);
    setPicks(qs.map(blank));
  }, [key]);
  if (refused) return <Record views={views} note={refused} openTerminal={openTerminal} />;
  const body = () => ({ answers: qs.map((x, i) => ({ options: picks[i].picked, ...(picks[i].otherOn && picks[i].other.trim() ? { other: picks[i].other.trim() } : {}) })) });
  const send = async (body: object, button = '') => {
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    setFrom(button);
    const res = await post('POST', `${shipUrl(session, crew)}/answer`, { card: 'question', reason: card.reason, item: card.item, ...body }, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
    if (!res.ok) setRefused(res.error);
  };
  // After the last question the card shows what will be sent, as Claude's own review screen does, so Submit is a confirmation.
  if (step === qs.length) {
    return (
      <div class="convo-card q-card">
        <p class="q-review-title">Review your answers</p>
        <ul class="q-review">
          {qs.map((x, i) => (
            <li key={i}>
              <Inline src={x.text} class="q-review-q" />
              <span class="q-review-a">{`→ ${chosen(x, picks[i])}`}</span>
            </li>
          ))}
        </ul>
        <Actions class="card-actions">
          <Button label="Back" onClick={() => setStep(step - 1)} />
          <Button label="Submit answers" busy={from === 'submit'} pending={['ship', session, 'answer-submit', crew ?? '']} onClick={() => send(body(), 'submit')} />
        </Actions>
      </div>
    );
  }
  const q = qs[step];
  const view = views[step];
  const pick = picks[step];
  if (!q || !view || !pick) return null;
  // One single-choice question answers on the tap of its option, as the old card did.
  const direct = qs.length === 1 && !q.multiSelect;
  const update = (p: Pick) => setPicks(picks.map((x, i) => (i === step ? p : x)));
  const toggle = (n: number) => {
    if (q.multiSelect) update({ ...pick, picked: pick.picked.includes(n) ? pick.picked.filter((x) => x !== n) : [...pick.picked, n].sort((a, b) => a - b) });
    else update({ ...pick, picked: [n], other: '', otherOn: false });
  };
  const ready = answered(q, pick);
  return (
    <div class="convo-card q-card">
      {qs.length > 1 ? <p class="muted q-step">{`Question ${step + 1} of ${qs.length}`}</p> : null}
      <div class="q-block">
        <Header view={view} />
        <div class="q-options" role={q.multiSelect ? 'group' : 'radiogroup'} aria-label={q.text}>
          {q.options.map((label, i) => {
            const on = pick.picked.includes(i + 1);
            return (
              <button
                key={i}
                type="button"
                class={`btn card-option${on ? ' on' : ''}`}
                role={q.multiSelect ? 'checkbox' : direct ? undefined : 'radio'}
                aria-checked={q.multiSelect || !direct ? on : undefined}
                disabled={busy}
                onClick={() => (direct ? void send({ option: i + 1 }) : toggle(i + 1))}
              >
                <span>{label}</span>
                {q.descriptions[i] ? <Inline src={q.descriptions[i]} class="muted card-option-note" /> : null}
              </button>
            );
          })}
          {q.other ? (
            <div class="q-other">
              <input
                type="text"
                class="q-other-input"
                placeholder="Other…"
                aria-label="Other answer"
                maxLength={500}
                value={pick.other}
                disabled={busy}
                onInput={(e) => update({ ...pick, other: e.currentTarget.value, otherOn: e.currentTarget.value.trim() !== '', picked: q.multiSelect || e.currentTarget.value.trim() === '' ? pick.picked : [] })}
              />
              {direct ? (
                <Button label="Send" busy={from === 'send'} disabled={busy || !ready} pending={['ship', session, 'answer-send', crew ?? '']} onClick={() => send(body(), 'send')} />
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
      {!direct ? (
        <Actions class="card-actions">
          {step > 0 ? <Button label="Back" disabled={busy} onClick={() => setStep(step - 1)} /> : null}
          <Button label="Next" disabled={busy || !ready} onClick={() => setStep(step + 1)} />
        </Actions>
      ) : null}
    </div>
  );
}

function Record({ views, note, openTerminal }: { views: QuestionView[]; note?: string; openTerminal: () => void }) {
  return (
    <div class="convo-card q-card q-record">
      {views.map((v) => (
        <ReadOnlyQuestion key={v.id} view={v} />
      ))}
      {note ? (
        <div class="card-actions">
          <p class="muted q-note" role="alert">
            {note}
          </p>
          <Button label="Open terminal" onClick={openTerminal} />
        </div>
      ) : null}
    </div>
  );
}

/** A question call in the transcript: the card that answers it while the pane asks, else the read-only record. */
export function QuestionCall({ items, card, session, crew, openTerminal }: { items: Item[]; card: AnswerCard; session: string; crew?: string; openTerminal: () => void }) {
  if (card.kind === 'question' && card.item === items[0].id) return <Answering items={items} card={card} session={session} crew={crew} openTerminal={openTerminal} />;
  return <Record views={questionCallView(items)} openTerminal={openTerminal} />;
}
