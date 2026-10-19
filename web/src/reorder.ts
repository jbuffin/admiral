import { signal } from '@preact/signals';
import { type Source, api, sourcePath, sources } from './sources';

// A list being rearranged by its grip: which list, the item that moves and the
// order the list shows until the drop.
export interface Reordering {
  group: string;
  key: string;
  keys: string[];
}
export const reordering = signal<Reordering | null>(null);

// The keys in the order to show: the order being dragged, else as given.
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

// Pointer Events like the order drag (ADR 0004), but from a grip with
// touch-action: none, so a touch moves the item in any direction at once.
// The items are the elements marked data-reorder={group}; each sits where the
// pointer passes the middle of the items laid out when the press began.
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

// Arrow keys on a focused grip move its item one place.
export function gripKey(e: KeyboardEvent, key: string, keys: string[], commit: (keys: string[]) => void): void {
  const step = e.key === 'ArrowUp' ? -1 : e.key === 'ArrowDown' ? 1 : 0;
  const at = keys.indexOf(key);
  if (!step || at + step < 0 || at + step >= keys.length) return;
  e.preventDefault();
  commit(moved(keys, key, at + step));
}

// The new order shows at once; the server's next push confirms it. A refusal
// puts back what the server has.
async function save(next: Source[], method: string, path: string, body: unknown, fail: (m: string) => void) {
  sources.value = next;
  const r = await api(method, path, body);
  if (r.ok) return;
  fail(r.error);
  const now = await api<Source[]>('GET', '/api/sources');
  if (now.ok) sources.value = now.data;
}

export function saveSourceOrder(keys: string[], fail: (m: string) => void): Promise<void> {
  const list = sources.value ?? [];
  const next = keys.map((k) => list.find((s) => s.key === k)).filter((s): s is Source => s !== undefined);
  return save(next, 'PUT', '/api/source-order', { sources: keys }, fail);
}

export function savePanelOrder(source: string, ids: string[], fail: (m: string) => void): Promise<void> {
  const next = (sources.value ?? []).map((s) =>
    s.key === source ? { ...s, panels: ids.map((id) => s.panels.find((p) => p.id === id)!).filter(Boolean) } : s,
  );
  return save(next, 'PUT', sourcePath(source, '/panel-order'), { panels: ids }, fail);
}
