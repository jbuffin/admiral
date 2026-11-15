import { useEffect, useId, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { bareSkillName, sameChoice, skillOptions, type SkillChoice, type SkillsAnswer } from './launchForm';

const LIST_MAX = 264;
const GAP = 4;

interface Placement {
  left: number;
  width: number;
  maxHeight: number;
  top?: number;
  bottom?: number;
}

// The software keyboard shrinks the visual viewport, not the layout one, so the list sits in the larger gap beside the input inside it.
function place(input: HTMLElement, rows: number): Placement {
  const r = input.getBoundingClientRect();
  const vv = window.visualViewport;
  const top = vv?.offsetTop ?? 0;
  const left = vv?.offsetLeft ?? 0;
  const height = vv?.height ?? window.innerHeight;
  const below = Math.max(0, top + height - r.bottom - GAP * 2);
  const above = Math.max(0, r.top - top - GAP * 2);
  const want = Math.min(LIST_MAX, rows * 44 + 10);
  const down = below >= want || below >= above;
  const x = Math.max(left, r.left);
  const base = { left: x, width: Math.min(r.width, left + (vv?.width ?? window.innerWidth) - x) };
  return down
    ? { ...base, top: r.bottom + GAP, maxHeight: Math.min(LIST_MAX, below) }
    : { ...base, bottom: document.documentElement.clientHeight - r.top + GAP, maxHeight: Math.min(LIST_MAX, above) };
}

const same = (a: Placement | null, b: Placement) => (a && a.left === b.left && a.width === b.width && a.maxHeight === b.maxHeight && a.top === b.top && a.bottom === b.bottom ? a : b);

export const choiceLabel = (c: SkillChoice, mark: string) => (c.kind === 'skill' ? `${mark}${c.name}` : c.kind === 'commodore' ? 'Commodore' : 'Just launch');

interface Props {
  answer: SkillsAnswer | null;
  /** The box has no pick to show yet. */
  blank?: boolean;
  value: SkillChoice;
  /** What the engineer typed, or null while the box shows the pick. */
  typed: string | null;
  warning: string | null;
  failure?: string | null;
  onType: (text: string | null) => void;
  onPick: (choice: SkillChoice) => void;
}

export function LaunchSkillPicker({ answer, value, blank, typed, warning, failure, onType, onPick }: Props) {
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [at, setAt] = useState<Placement | null>(null);
  const rows = answer ? skillOptions(answer, typed ?? '') : [];
  const shown = open && rows.length > 0;
  const listId = `${id}-list`;

  const show = () => {
    setActive(Math.max(0, typed === null ? rows.findIndex((r) => sameChoice(r.choice, value)) : 0));
    setOpen(true);
  };

  useLayoutEffect(() => {
    if (!shown || !input.current) return;
    const el = input.current;
    const measure = () => setAt((cur) => same(cur, place(el, rows.length)));
    measure();
    const vv = window.visualViewport;
    vv?.addEventListener('resize', measure);
    vv?.addEventListener('scroll', measure);
    window.addEventListener('resize', measure);
    document.addEventListener('scroll', measure, true);
    return () => {
      vv?.removeEventListener('resize', measure);
      vv?.removeEventListener('scroll', measure);
      window.removeEventListener('resize', measure);
      document.removeEventListener('scroll', measure, true);
    };
  }, [shown, rows.length]);

  // The sheet is bottom-anchored, so a line mounting or unmounting below the box moves it with no resize event: measure after every render while open.
  useLayoutEffect(() => {
    if (shown && input.current) setAt((cur) => same(cur, place(input.current!, rows.length)));
  });

  useEffect(() => {
    if (!shown) return;
    document.getElementById(`${id}-${active}`)?.scrollIntoView?.({ block: 'nearest' });
  }, [shown, active]);

  const take = (i: number) => {
    const row = rows[i];
    if (!row || row.disabled) return;
    onPick(row.choice);
    setOpen(false);
  };

  const keys = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      if (!open) return;
      e.stopPropagation();
      if (typed !== null && !bareSkillName(typed)) onType(null);
      return setOpen(false);
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!shown) return show();
      return setActive(Math.min(rows.length - 1, Math.max(0, active + (e.key === 'ArrowDown' ? 1 : -1))));
    }
    // An open list takes Enter to pick; the form is sent from a closed one.
    if (e.key === 'Enter' && open) {
      e.preventDefault();
      take(active);
    }
  };

  return (
    <div class="field skill-field">
      <label class="skill-label" for={`${id}-box`}>
        <span>Skill</span>
      </label>
      <input
        ref={input}
        id={`${id}-box`}
        type="text"
        role="combobox"
        aria-expanded={shown}
        aria-controls={shown ? listId : undefined}
        aria-activedescendant={shown ? `${id}-${active}` : undefined}
        aria-autocomplete="list"
        aria-describedby={warning ? `${id}-warn` : undefined}
        aria-invalid={warning ? true : undefined}
        autocomplete="off"
        autocapitalize="off"
        spellcheck={false}
        value={typed ?? (blank ? '' : choiceLabel(value, answer?.mark ?? ''))}
        placeholder={blank ? 'Loading skills' : undefined}
        onFocus={(e) => {
          (e.currentTarget as HTMLInputElement).select();
          (e.currentTarget as HTMLInputElement).scrollIntoView?.({ block: 'nearest' });
        }}
        onClick={() => !open && show()}
        onBlur={() => {
          setOpen(false);
          if (typed !== null && !bareSkillName(typed)) onType(null);
        }}
        onInput={(e) => {
          onType((e.currentTarget as HTMLInputElement).value);
          setActive(0);
          setOpen(true);
        }}
        onKeyDown={keys}
      />
      {warning ? (
        <p id={`${id}-warn`} class="note warn">
          {warning}
        </p>
      ) : null}
      {failure ? <p class="note warn">{failure}</p> : null}
      {shown ? (
        <ul
          class="skill-options skill-drop"
          role="listbox"
          id={listId}
          aria-label="Skills"
          style={at ? { left: `${at.left}px`, width: `${at.width}px`, maxHeight: `${at.maxHeight}px`, ...(at.top !== undefined ? { top: `${at.top}px` } : { bottom: `${at.bottom}px` }) } : { visibility: 'hidden' }}
          // Keeps focus in the box, so a tap on a row doesn't blur it and close the list first.
          onMouseDown={(e) => e.preventDefault()}
        >
          {rows.map((r, i) => (
            <li
              key={`${r.choice.kind}:${r.label}`}
              id={`${id}-${i}`}
              class={i === active ? 'skill-choice active' : 'skill-choice'}
              role="option"
              aria-selected={typed === null && sameChoice(r.choice, value)}
              aria-disabled={r.disabled ? true : undefined}
              onClick={() => take(i)}
            >
              <span class="skill-name">{`${r.mark}${r.label}`}</span>
              {r.description ? <span class="skill-desc">{r.description}</span> : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
