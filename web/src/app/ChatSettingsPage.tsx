import type { ComponentChildren } from 'preact';
import { useEffect, useRef, useState } from 'preact/hooks';
import { cancelCommitment, deleteNote, getCommitments, getInstructions, getNotes, promoteNote, saveFleet, saveInstructions, type ChatCommitment, type ChatNote, type Fleet } from './api';
import { checkErrors, checkKey } from './capabilities';
import { MissingCapability } from './MissingCapability';
import { Flag } from './Flag';
import { fleets } from './fleets';
import { ships } from './shipsLink';
import { safeHref } from './campaign';
import { CHAT_THRESHOLDS, INSTRUCTIONS_MAX, canSaveInstructions, chatErrorMessage, actionWords, cancelErrorMessage, commitmentMarks, chatSaveBody, effortLabel, harnessLabel, instructionsCount, memoryNotRead, modelLabel, noteAge, readsMemory, switchHarness, thresholdLabel, triggerWords, type ChatSettings } from './chatSettings';

const settingsOf = (f: Fleet): ChatSettings => ({ harness: f.chat.harness, model: f.chat.model, effort: f.chat.effort, context_threshold: f.chat.context_threshold });

function Choice<T extends string | number>({ legend, help, values, value, label, disabled, unavailable, notes, onPick, flag }: {
  legend: string;
  help?: string;
  values: T[];
  value: T;
  label: (v: T) => string;
  disabled: boolean;
  /** Values shown greyed out and not selectable. */
  unavailable?: T[];
  /** Why a value is unavailable, by value; shown inside the row under the chips and read with the chip. */
  notes?: (v: T) => ComponentChildren;
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
          <button key={String(v)} type="button" role="radio" class="chip" aria-checked={v === value} aria-describedby={unavailable?.includes(v) && notes ? `${id}-note-${v}` : undefined} disabled={disabled || !!unavailable?.includes(v)} onClick={() => onPick(v)}>
            {flag ? <Flag harness={String(v)} size="row" /> : null}
            {label(v)}
          </button>
        ))}
      </div>
      {notes ? unavailable?.map((v) => notes(v)) : null}
    </div>
  );
}

// The engineer's instructions and the notes AdmiralBot saved. Each acts at once and never rides on the settings Save.
function Knows({ fleet, harness }: { fleet: string; harness: string }) {
  const [saved, setSaved] = useState('');
  const [text, setText] = useState('');
  const [notes, setNotes] = useState<ChatNote[]>([]);
  const [commitments, setCommitments] = useState<ChatCommitment[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  // Shown beside the list: the page's error line is out of view when the list is scrolled.
  const [cancelError, setCancelError] = useState('');

  // What the textarea and the saved text were at the last render, so a refresh can tell a clean textarea from a draft.
  const shown = useRef({ saved, text });
  shown.current = { saved, text };

  const load = async () => {
    const [i, n, c] = await Promise.all([getInstructions(fleet), getNotes(fleet), getCommitments(fleet)]);
    if (i.ok) {
      if (shown.current.text.trim() === shown.current.saved.trim()) setText(i.data.text);
      setSaved(i.data.text);
    }
    if (n.ok) setNotes(n.data);
    if (c.ok) setCommitments(c.data);
    setError(!i.ok ? i.error : !n.ok ? n.error : !c.ok ? c.error : '');
  };
  useEffect(() => {
    void load();
  }, [fleet]);

  const act = async (run: () => Promise<{ ok: true } | { ok: false; error: string }>) => {
    setBusy(true);
    setError('');
    setCancelError('');
    const r = await run();
    if (r.ok) await load();
    else {
      // A commitment can close before Cancel lands, so reload to drop it, then keep the failure on the line.
      await load();
      setError(r.error);
    }
    setBusy(false);
  };
  const cancel = async (id: string) => {
    setBusy(true);
    setError('');
    setCancelError('');
    const r = await cancelCommitment(fleet, id);
    // A commitment can close before Cancel lands, so reload either way to drop it.
    await load();
    if (!r.ok) setCancelError(cancelErrorMessage(r.status, r.error));
    setBusy(false);
  };
  const count = instructionsCount(text);
  const dirty = text.trim() !== saved.trim();
  const now = Date.now();
  return (
    <>
      <h2 class="section-title">What AdmiralBot knows</h2>
      {error ? (
        <p class="note error" role="alert">
          {error}
        </p>
      ) : null}
      <div class="box">
        <div class="set">
          <div class="t">
            <label for="chat-instructions">Instructions</label>
            <span>Plain words AdmiralBot follows every time. Only you can change them.</span>
            <textarea id="chat-instructions" rows={6} value={text} disabled={busy} onInput={(e) => setText(e.currentTarget.value)} />
            <span class={count > INSTRUCTIONS_MAX ? 'note error' : undefined}>
              {count} / {INSTRUCTIONS_MAX}
            </span>
          </div>
        </div>
        <div class="set actions">
          <button type="button" class="btn primary" disabled={busy || !canSaveInstructions(saved, text)} onClick={() => void act(() => saveInstructions(fleet, text))}>
            Save instructions
          </button>
        </div>
        <div class="set">
          <div class="t">
            <span class="label">Notes</span>
            <span>{dirty ? 'Save your instructions first; a note moves into the saved text.' : 'What AdmiralBot saved when you asked it to remember something.'}</span>
          </div>
        </div>
        {notes.length === 0 ? (
          <div class="set">
            <div class="t">
              <span>Nothing yet. When you ask AdmiralBot to remember something, it shows here.</span>
            </div>
          </div>
        ) : (
          <ul class="set-list" aria-label="Notes">
            {notes.map((n) => (
              <li key={n.id} class="set note-row">
                <div class="t">
                  <span class="label set-text">{n.text}</span>
                  <span>{noteAge(n.ts, now)}</span>
                </div>
                <div class="note-actions">
                  <button type="button" class="btn" disabled={busy || dirty} onClick={() => void act(() => promoteNote(fleet, n.id))}>
                    Move to instructions
                  </button>
                  <button type="button" class="btn" disabled={busy} onClick={() => void act(() => deleteNote(fleet, n.id))}>
                    Delete
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
        <div class="set">
          <div class="t">
            <span class="label">Commitments</span>
            <span>What you asked AdmiralBot to do later. Cancel one and it never runs.</span>
          </div>
        </div>
        {cancelError ? (
          <p class="note error" role="alert">
            {cancelError}
          </p>
        ) : null}
        {commitments.length === 0 ? (
          <div class="set">
            <div class="t">
              <span>AdmiralBot has no open commitments.</span>
            </div>
          </div>
        ) : (
          <ul class="set-list" aria-label="Commitments">
            {commitments.map((c) => {
              const t = triggerWords(c.trigger, ships.value);
              const href = t.href ? safeHref(t.href) : null;
              const marks = commitmentMarks(c);
              return (
                <li key={c.id} class="set note-row">
                  <div class="t">
                    <span class="label set-text">{c.text}</span>
                    <span>
                      {href ? (
                        <a href={href} target="_blank" rel="noopener noreferrer">
                          {t.text}
                        </a>
                      ) : (
                        t.text
                      )}
                      {' · '}
                      {actionWords(c.action)}
                      {' · '}
                      {noteAge(c.ts, now)}
                      {marks.map((m) => ` · ${m}`).join('')}
                    </span>
                  </div>
                  <div class="note-actions">
                    <button type="button" class="btn" disabled={busy} onClick={() => void cancel(c.id)}>
                      Cancel
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>
      {readsMemory(harness) ? null : <p class="note">{memoryNotRead(harness)}</p>}
    </>
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
              notes={(v) => {
                const o = closed.find((x) => x.harness === v);
                if (!o) return null;
                const error = checkErrors.value[checkKey(fleet.key, o.harness)];
                return (
                  <div key={v}>
                    <MissingCapability id={`chat-harness-note-${v}`} fleet={fleet.key} harness={o.harness} reason={o.reason} />
                    {error ? (
                      <p class="note error" role="alert">
                        {error}
                      </p>
                    ) : null}
                  </div>
                );
              }}
              onPick={(v) => {
                const o = options.find((x) => x.harness === v);
                if (o) setEdited(switchHarness(edited, o));
              }}
              flag
            />
          </>
        ) : null}
        <Choice legend="Model" values={models} value={edited.model} label={modelLabel} disabled={busy} onPick={(v) => set({ model: v })} />
        <Choice legend="Effort" values={efforts} value={edited.effort} label={effortLabel} disabled={busy} onPick={(v) => set({ effort: v })} />
        {readsMemory(edited.harness) ? (
          <Choice
            legend="Refresh context at"
            help="How large AdmiralBot's context can grow before a refresh. Your chat history stays."
            values={CHAT_THRESHOLDS}
            value={edited.context_threshold}
            label={thresholdLabel}
            disabled={busy}
            onPick={(v) => set({ context_threshold: v })}
          />
        ) : null}
        <div class="set actions">
          <button type="button" class="btn primary" disabled={busy || !body} onClick={() => void save()}>
            Save
          </button>
        </div>
      </div>
      <Knows fleet={fleet.key} harness={edited.harness} />
    </>
  );
}
