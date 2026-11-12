import type { DirsAnswer, Dock } from './api';
import { Flag } from './Flag';
import { CLAUDE_MODELS, effortsFor, type SkillChoice, type SkillsAnswer } from './launchForm';
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

export function ModelEffortPair({ harness, model, effort, onModel, onEffort }: { harness: string; model: string; effort: string; onModel: (m: string) => void; onEffort: (e: string) => void }) {
  const efforts = effortsFor(harness);
  return (
    <div class="pair">
      <label class="field">
        <span>Model</span>
        {harness === 'claude' ? (
          <select value={model} onChange={(e) => onModel(target(e).value)}>
            <option value="">Default</option>
            {CLAUDE_MODELS.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        ) : (
          <input type="text" autocomplete="off" autocapitalize="off" spellcheck={false} placeholder="Harness default" value={model} onInput={(e) => onModel(target(e).value.trim())} />
        )}
      </label>
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
