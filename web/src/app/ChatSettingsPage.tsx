import { useState } from 'preact/hooks';
import { fetchFleets, saveFleet, type Fleet } from './api';
import { MissingCapability } from './MissingCapability';
import { Flag } from './Flag';
import { fleets } from './fleets';
import { CHAT_THRESHOLDS, chatErrorMessage, chatSaveBody, effortLabel, harnessLabel, modelLabel, switchHarness, thresholdLabel, type ChatSettings } from './chatSettings';

const settingsOf = (f: Fleet): ChatSettings => ({ harness: f.chat.harness, model: f.chat.model, effort: f.chat.effort, context_threshold: f.chat.context_threshold });

function Choice<T extends string | number>({ legend, help, values, value, label, disabled, unavailable, onPick, flag }: {
  legend: string;
  help?: string;
  values: T[];
  value: T;
  label: (v: T) => string;
  disabled: boolean;
  /** Values shown greyed out and not selectable. */
  unavailable?: T[];
  onPick: (v: T) => void;
  flag?: boolean;
}) {
  const id = `chat-${legend.toLowerCase().replace(/\W+/g, '-')}`;
  return (
    <div class="set choice-set">
      <div class="t">
        <span id={id}>{legend}</span>
        {help ? <span>{help}</span> : null}
      </div>
      <div class="choice-group" role="radiogroup" aria-labelledby={id}>
        {values.map((v) => (
          <button key={String(v)} type="button" role="radio" class="chip" aria-checked={v === value} disabled={disabled || !!unavailable?.includes(v)} onClick={() => onPick(v)}>
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

  const options = fleet.chat.harness_options;
  const chosen = options.find((o) => o.harness === edited.harness);
  const models = chosen?.models ?? fleet.chat.models;
  const efforts = chosen?.efforts ?? fleet.chat.efforts;
  const closed = options.filter((o) => !o.offered);
  // A check can turn a harness on, so the picker reads the fleets again.
  const refresh = () => void fetchFleets().then((list) => (fleets.value = list), () => undefined);
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
        {options.length > 0 ? (
          <>
            <Choice
              legend="Harness"
              values={options.map((o) => o.harness)}
              unavailable={closed.map((o) => o.harness)}
              value={edited.harness}
              label={harnessLabel}
              disabled={busy}
              onPick={(v) => {
                const o = options.find((x) => x.harness === v);
                if (o) setEdited(switchHarness(edited, o));
              }}
              flag
            />
            {closed.map((o) => (
              <MissingCapability key={o.harness} fleet={fleet.key} harness={o.harness} reason={o.reason} onChecked={refresh} />
            ))}
          </>
        ) : null}
        <Choice legend="Model" values={models} value={edited.model} label={modelLabel} disabled={busy} onPick={(v) => set({ model: v })} />
        <Choice legend="Effort" values={efforts} value={edited.effort} label={effortLabel} disabled={busy} onPick={(v) => set({ effort: v })} />
        <Choice
          legend="Refresh context at"
          help="How large AdmiralBot's context can grow before a refresh. Your chat history stays."
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
