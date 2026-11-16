import { useState } from 'preact/hooks';
import { call, type Ship } from './api';
import { Actions, Button } from './Button';
import { Sheet, useCancelFocus } from './Sheet';
import { handAction, handBody } from './hand';

const LABEL = 'Hand to Commodore';

export function HandToCommodore({ ship }: { ship: Ship }) {
  const act = handAction(ship);
  const [asking, setAsking] = useState(false);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState('');
  const [kind, setKind] = useState<'issue' | 'other'>('issue');
  const [text, setText] = useState('');
  const key = ['ship', ship.session, 'commodore'] as const;
  useCancelFocus(asking);
  if (!act.show) return null;
  const submit = async () => {
    setSending(true);
    setError('');
    const r = await call('POST', `/ships/${encodeURIComponent(ship.session)}/commodore`, handBody(kind, text));
    setSending(false);
    if (!r.ok) return setError(r.error);
    setAsking(false);
  };
  return (
    <>
      <Button
        label={LABEL}
        variant="primary"
        class="hand-commodore"
        busyLabel="Handing to Commodore"
        busy={act.busy}
        pending={key}
        disabled={!!act.refusal}
        onClick={() => {
          setError('');
          setKind('issue');
          setText('');
          setAsking(true);
        }}
      />
      {asking ? (
        <Sheet label={LABEL} kind="launch-backdrop stop-backdrop" onClose={() => !sending && setAsking(false)}>
          <p class="line">
            {kind === 'issue'
              ? `${ship.name} files its plan as an issue, then Admiral relaunches it as a Commodore ship with the same name, checkout and conversation.`
              : `${ship.name} follows your note, then Admiral relaunches it as a Commodore ship with the same name, checkout and conversation.`}
          </p>
          <fieldset class="seg" role="radiogroup" aria-label="What the ship hands over">
            {(
              [
                ['issue', 'GitHub issue (recommended)'],
                ['other', 'Other'],
              ] as const
            ).map(([k, label]) => (
              <label key={k} class={kind === k ? 'seg-opt on' : 'seg-opt'}>
                <input type="radio" name="hand-kind" checked={kind === k} disabled={sending} onChange={() => setKind(k)} />
                {label}
              </label>
            ))}
          </fieldset>
          {kind === 'other' ? (
            <label class="field">
              <span>Your note</span>
              <textarea rows={4} value={text} disabled={sending} onInput={(e) => setText(e.currentTarget.value)} />
            </label>
          ) : null}
          {error ? (
            <p class="launch-error" role="alert">
              {error}
            </p>
          ) : null}
          <Actions class="launch-foot">
            <Button label="Cancel" onClick={() => setAsking(false)} />
            <Button label={LABEL} variant="primary" pending={key} disabled={kind === 'other' && text.trim() === ''} onClick={submit} />
          </Actions>
        </Sheet>
      ) : null}
    </>
  );
}
