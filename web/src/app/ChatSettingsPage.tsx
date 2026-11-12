import { useState } from 'preact/hooks';
import { saveFleet, type Fleet } from './api';
import { Flag } from './Flag';
import { fleets } from './fleets';
import { CHAT_EFFORTS, CHAT_MODELS, CHAT_THRESHOLDS, chatErrorMessage, chatSaveBody, effortLabel, harnessLabel, modelLabel, thresholdLabel, type ChatSettings } from './chatSettings';

const settingsOf = (f: Fleet): ChatSettings => ({ harness: f.chat.harness, model: f.chat.model, effort: f.chat.effort, context_threshold: f.chat.context_threshold });

function Choice<T extends string | number>({ legend, help, values, value, label, disabled, onPick, flag }: {
  legend: string;
  help?: string;
  values: T[];
  value: T;
  label: (v: T) => string;
  disabled: boolean;
  onPick: (v: T) => void;
  flag?: boolean;
}) {
  const id = `chat-${legend.toLowerCase().replace(/\W+/g, '-')}`;
  return (
    <div class="set">
      <div class="t">
        <span id={id}>{legend}</span>
        {help ? <span>{help}</span> : null}
      </div>
      <div class="control choice-group" role="radiogroup" aria-labelledby={id}>
        {values.map((v) => (
          <button key={String(v)} type="button" role="radio" class="choice" aria-checked={v === value} disabled={disabled} onClick={() => onPick(v)}>
            {flag ? <Flag harness={String(v)} size="row" /> : null}
            {label(v)}
          </button>
        ))}
      </div>
    </div>
  );
}

export function ChatSettingsPage({ fleet }: { fleet: Fleet }) {
  const [shown, setShown] = useState(() => settingsOf(fleet));
  const [edited, setEdited] = useState(shown);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const body = chatSaveBody(shown, edited);
  const set = (patch: Partial<ChatSettings>) => setEdited({ ...edited, ...patch });

  const save = async () => {
    if (!body) return;
    setBusy(true);
    setError('');
    const r = await saveFleet(fleet.key, body);
    if (r.ok) {
      fleets.value = r.data;
      const f = r.data.find((x) => x.key === fleet.key);
      if (f) {
        const next = settingsOf(f);
        setShown(next);
        setEdited(next);
      }
    } else {
      setError(chatErrorMessage(r.error, fleet.key));
    }
    setBusy(false);
  };

  const { harnesses } = fleet.chat;
  return (
    <>
      <p class="line">Your chief of staff for the {fleet.name} fleet. It only sees this fleet's ships, orders, campaigns and logins.</p>
      {!fleet.chat.on && fleet.chat.off_reason ? <p class="note warning">{fleet.chat.off_reason}</p> : null}
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      <div class="box">
        {harnesses.length > 0 ? <Choice legend="Harness" values={harnesses} value={edited.harness} label={harnessLabel} disabled={busy} onPick={(v) => set({ harness: v })} flag /> : null}
        <Choice legend="Model" values={CHAT_MODELS} value={edited.model} label={modelLabel} disabled={busy} onPick={(v) => set({ model: v })} />
        <Choice legend="Effort" values={CHAT_EFFORTS} value={edited.effort} label={effortLabel} disabled={busy} onPick={(v) => set({ effort: v })} />
        <Choice
          legend="Refresh context at"
          help="When AdmiralBot’s context reaches this size, it will start fresh. Your chat history stays."
          values={CHAT_THRESHOLDS}
          value={edited.context_threshold}
          label={thresholdLabel}
          disabled={busy}
          onPick={(v) => set({ context_threshold: v })}
        />
        <div class="set actions">
          <button type="button" class="btn primary" disabled={busy || !body} onClick={() => void save()}>
            Save
          </button>
        </div>
      </div>
    </>
  );
}
