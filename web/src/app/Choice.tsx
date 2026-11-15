import type { ComponentChildren } from 'preact';
import { Flag } from './Flag';

export function Choice<T extends string | number>({ legend, help, values, value, label, disabled, unavailable, notes, onPick, flag }: {
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
