import { signal } from '@preact/signals';

export interface Reordering {
  group: string;
  key: string;
  keys: string[];
}
export const reordering = signal<Reordering | null>(null);

/** The items in the order a drag in progress shows, or as given when this group isn't being dragged. */
export function arranged<T>(group: string, items: T[], keyOf: (t: T) => string): T[] {
  const r = reordering.value;
  if (r?.group !== group) return items;
  return r.keys.map((k) => items.find((t) => keyOf(t) === k)).filter((t): t is T => t !== undefined);
}

const moved = (keys: string[], key: string, to: number) => {
  const rest = keys.filter((k) => k !== key);
  rest.splice(Math.max(0, Math.min(to, rest.length)), 0, key);
  return rest;
};

// Pointer Events from a grip with touch-action: none, so a touch moves the row in any direction at once.
export function pressGrip(e: PointerEvent, group: string, key: string, keys: string[], commit: (keys: string[]) => void): void {
  if (e.button !== 0 || !e.isPrimary || reordering.value) return;
  e.preventDefault();
  const id = e.pointerId;
  const mids = [...document.querySelectorAll<HTMLElement>(`[data-reorder="${CSS.escape(group)}"]`)]
    .filter((el) => el.dataset.key !== key)
    .map((el) => {
      const r = el.getBoundingClientRect();
      return r.top + r.height / 2;
    });
  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== id) return;
    if (!reordering.value) {
      document.body.classList.add('dragging');
      getSelection()?.removeAllRanges();
    }
    reordering.value = { group, key, keys: moved(keys, key, mids.filter((m) => m < ev.clientY).length) };
  };
  const end = (ev: PointerEvent, cancelled: boolean) => {
    if (ev.pointerId !== id) return;
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', up);
    window.removeEventListener('pointercancel', cancel);
    const r = reordering.value;
    reordering.value = null;
    document.body.classList.remove('dragging');
    if (!cancelled && r && r.keys.some((k, i) => k !== keys[i])) commit(r.keys);
  };
  const up = (ev: PointerEvent) => end(ev, false);
  const cancel = (ev: PointerEvent) => end(ev, true);
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', up);
  window.addEventListener('pointercancel', cancel);
}

export function gripKey(e: KeyboardEvent, key: string, keys: string[], commit: (keys: string[]) => void): void {
  const step = e.key === 'ArrowUp' ? -1 : e.key === 'ArrowDown' ? 1 : 0;
  const at = keys.indexOf(key);
  if (!step || at + step < 0 || at + step >= keys.length) return;
  e.preventDefault();
  commit(moved(keys, key, at + step));
}
