import type { DirsAnswer, Dock } from './api';
import { useEffect, useId, useRef, useState } from 'preact/hooks';
import { Flag } from './Flag';
import { effortsFor, type SkillChoice, type SkillsAnswer } from './launchForm';
import { resources } from './shipsLink';

export const CLONE = 'clone';
export const NO_DIR = '';
export const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

// Skill select values: 'commodore', 'none', 'custom' or `skill:<favorite>`.
export const skillValue = (c: SkillChoice) => (c.kind === 'skill' ? `skill:${c.name}` : c.kind);
export const skillOfValue = (v: string): SkillChoice | null => (v === 'commodore' || v === 'none' ? { kind: v } : v.startsWith('skill:') ? { kind: 'skill', name: v.slice(6) } : null);

const target = (e: Event) => e.target as HTMLInputElement & HTMLSelectElement;

export function DockPicker({ docks, harness, onChange }: { docks: { harness: string }[] | Dock[]; harness: string; onChange: (h: string) => void }) {
  return (
    <fieldset class="docks">
      <legend>Dock</legend>
      {docks.map((d) => (
        <label key={d.harness} class={harness === d.harness ? 'dock on' : 'dock'}>
          <input type="radio" name="dock" checked={harness === d.harness} onChange={() => onChange(d.harness)} />
          <Flag harness={d.harness} size="row" />
          {cap(d.harness)}
        </label>
      ))}
    </fieldset>
  );
}

export function CheckoutSelect({ dir, dirs, allowClone, onChange }: { dir: string; dirs: DirsAnswer | null; allowClone: boolean; onChange: (dir: string) => void }) {
  const clone = allowClone ? (dirs?.clone ?? null) : null;
  return (
    <label class="field">
      <span>Checkout</span>
      <select value={dir} onChange={(e) => onChange(target(e).value)}>
        {dir === NO_DIR ? <option value={NO_DIR}>Choose a checkout</option> : null}
        {clone ? <option value={CLONE}>{`Clone ${clone.repo}${clone.exists ? ' (folder exists)' : ''}`}</option> : null}
        {(dirs?.entries ?? []).map((d) => (
          <option key={d.path} value={d.path}>
            {d.name} ({d.kind === 'search_root' ? 'search root' : 'checkout'}
            {d.ships_here > 0 ? ', ship underway' : ''})
          </option>
        ))}
      </select>
    </label>
  );
}

export function SkillSelect({ value, skills, onChange }: { value: string; skills: SkillsAnswer | null; onChange: (v: string) => void }) {
  const favorites = (skills?.skills ?? []).filter((s) => s.favorite);
  return (
    <label class="field">
      <span>Skill</span>
      <select value={value} onChange={(e) => onChange(target(e).value)}>
        <option value="commodore" disabled={!skills?.commodore.found}>
          {skills && !skills.commodore.found ? 'Commodore (not found)' : 'Commodore'}
        </option>
        {favorites.map((s) => (
          <option key={s.name} value={`skill:${s.name}`}>
            {s.name}
          </option>
        ))}
        <option value="custom">Custom</option>
        <option value="none">Just launch</option>
      </select>
    </label>
  );
}

const OTHER = '__other__';

// Preact drops a false spellcheck prop where the DOM has no such property, leaving the attribute off.
const noSpellcheck = (el: HTMLInputElement | null) => el?.setAttribute('spellcheck', 'false');

/** Default, the dock's model list, then Other… for a name the list lacks; a stored name outside the list shows under Other… as it is. */
export function ModelSelect({ dock, model, onModel, describedBy }: { dock: Dock | undefined; model: string; onModel: (m: string) => void; describedBy?: string }) {
  const list = dock?.models ?? [];
  const [pickedOther, setPickedOther] = useState(() => model !== '' && !list.includes(model));
  const selectId = useId();
  const editing = useRef(false);
  // A list that gains the stored model shows it as the entry, unless its Other… field is being edited.
  useEffect(() => {
    if (!editing.current && list.includes(model)) setPickedOther(false);
  }, [dock?.models]);
  const other = pickedOther || (model !== '' && !list.includes(model));
  return (
    <div class="model-pick">
      <div class="field">
        <label for={selectId}>Model</label>
        <select
          id={selectId}
          aria-describedby={describedBy}
          aria-invalid={describedBy ? true : undefined}
          value={other ? OTHER : model}
          onChange={(e) => {
            const v = target(e).value;
            setPickedOther(v === OTHER);
            onModel(v === OTHER ? (list.includes(model) ? '' : model) : v);
          }}
        >
          <option value="">Default</option>
          {list.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
          <option value={OTHER}>Other…</option>
        </select>
      </div>
      {other ? (
        <label class="field">
          <span>Other model</span>
          <input type="text" autocomplete="off" autocapitalize="off" autocorrect="off" ref={noSpellcheck} placeholder="Model name" value={model} onInput={(e) => {
            editing.current = true;
            onModel(target(e).value.trim());
          }} onBlur={() => (editing.current = false)} />
        </label>
      ) : null}
      {dock?.models_source === 'built_in' ? <p class="meta">{"The harness hasn't listed its models, so this is Admiral's built-in list."}</p> : null}
    </div>
  );
}

export function ModelEffortPair({ harness, dock, model, effort, onModel, onEffort }: { harness: string; dock?: Dock; model: string; effort: string; onModel: (m: string) => void; onEffort: (e: string) => void }) {
  const efforts = effortsFor(harness);
  return (
    <div class="pair">
      <ModelSelect key={harness} dock={dock} model={model} onModel={onModel} />
      {efforts.length > 0 ? (
        <label class="field">
          <span>Effort</span>
          <select value={efforts.includes(effort) ? effort : ''} onChange={(e) => onEffort(target(e).value)}>
            <option value="">Default</option>
            {efforts.map((x) => (
              <option key={x} value={x}>
                {cap(x)}
              </option>
            ))}
          </select>
        </label>
      ) : null}
    </div>
  );
}

export function ResourcesPicker({ held, onChange }: { held: string[]; onChange: (names: string[]) => void }) {
  if (resources.value.length === 0) return null;
  return (
    <fieldset class="checks">
      <legend>Resources</legend>
      {resources.value.map((r) => (
        <label key={r.name} class="check">
          <input type="checkbox" checked={held.includes(r.name)} onChange={(e) => onChange(target(e).checked ? [...held, r.name] : held.filter((n) => n !== r.name))} />
          {r.name}
          <span class="meta">{`${Math.min(r.in_use, r.capacity)}/${r.capacity} in use`}</span>
        </label>
      ))}
    </fieldset>
  );
}
