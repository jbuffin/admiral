import { computed, effect, signal } from '@preact/signals';
import type { HarnessName } from './config';
import { type Order, panelViews } from './sources';

// An order as launch needs it: where it came from and what it says.
export interface OrderRef {
  source: string;
  order: Order;
}

export interface Pointer {
  x: number;
  y: number;
}

// The order under the pointer while it is dragged, and the dock below it.
export const dragging = signal<(OrderRef & Pointer & { dock: string | null }) | null>(null);
// The order picked by a tap.
export const selected = signal<OrderRef | null>(null);
// The open launch orders: this order for this dock.
export const launching = signal<(OrderRef & { fleet: string; harness: HarnessName }) | null>(null);

// An order that leaves the sidebar (launched elsewhere, removed, filtered out)
// is no longer selected, and Escape deselects.
effect(() => {
  const s = selected.peek();
  const shown = panelViews.value.some((v) => v.source.key === s?.source && v.orders.some((o) => o.id === s.order.id));
  if (s && !shown) selected.value = null;
});
window.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') selected.value = null;
});

export const armed = computed(() => dragging.value !== null || selected.value !== null);

export const dockKey = (fleet: string, harness: string) => `${fleet}/${harness}`;

const sameOrder = (a: OrderRef, b: OrderRef) => a.source === b.source && a.order.id === b.order.id;
export const isSelected = (r: OrderRef) => !!selected.value && sameOrder(selected.value, r);

export function openLaunch(ref: OrderRef, fleet: string, harness: HarnessName): void {
  selected.value = null;
  launching.value = { ...ref, fleet, harness };
}

const DRAG_THRESHOLD = 8;

// Drag is Pointer Events, not HTML5 drag (ADR 0004). A press that stays under
// the threshold is a tap. A touch that moves mainly sideways picks the order up;
// one that moves mainly down or up is a scroll and is left alone. A mouse
// picks it up in any direction.
export function pressOrder(e: PointerEvent, ref: OrderRef): void {
  if (e.button !== 0 || !e.isPrimary || dragging.value) return;
  if ((e.target as Element).closest('button, a, input, textarea')) return;
  const id = e.pointerId;
  const touch = e.pointerType !== 'mouse';
  const from = { x: e.clientX, y: e.clientY };
  let phase: 'down' | 'drag' | 'scroll' = 'down';

  const dockAt = (x: number, y: number) => {
    const el = document.elementFromPoint(x, y)?.closest<HTMLElement>('[data-dock]');
    return el?.dataset.dock ?? null;
  };
  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== id) return;
    const dx = ev.clientX - from.x;
    const dy = ev.clientY - from.y;
    if (phase === 'down' && Math.hypot(dx, dy) >= DRAG_THRESHOLD) {
      phase = touch && Math.abs(dy) > Math.abs(dx) ? 'scroll' : 'drag';
    }
    if (phase === 'drag') {
      if (!dragging.value) {
        // A drag never selects text.
        document.body.classList.add('dragging');
        getSelection()?.removeAllRanges();
      }
      selected.value = null;
      dragging.value = { ...ref, x: ev.clientX, y: ev.clientY, dock: dockAt(ev.clientX, ev.clientY) };
    }
  };
  const end = (ev: PointerEvent, cancelled: boolean) => {
    if (ev.pointerId !== id) return;
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', up);
    window.removeEventListener('pointercancel', cancel);
    const over = dragging.value?.dock;
    dragging.value = null;
    document.body.classList.remove('dragging');
    if (cancelled) return;
    if (phase === 'down') selected.value = isSelected(ref) ? null : ref;
    else if (phase === 'drag' && over) {
      const at = over.lastIndexOf('/');
      openLaunch(ref, over.slice(0, at), over.slice(at + 1) as HarnessName);
    }
  };
  const up = (ev: PointerEvent) => end(ev, false);
  const cancel = (ev: PointerEvent) => end(ev, true);
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', up);
  window.addEventListener('pointercancel', cancel);
}

// The dock whose ship just sailed, so its mooring can play the departure.
export const departed = signal<{ dock: string; n: number } | null>(null);

const reducedMotion = () => typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;
let departures = 0;

// Told to the ships layer (u10) after the 201 from POST /api/ships: the dock, the
// order's source and ID, and the new ship's session. The `ships` push can arrive
// before or after this event, so the ships layer must handle either order.
export interface Launched {
  fleet: string;
  harness: HarnessName;
  source: string;
  order_id: string;
  session: string;
}

declare global {
  interface WindowEventMap {
    'admiral:launched': CustomEvent<Launched>;
  }
}

export function announceLaunch(d: Launched): void {
  if (!reducedMotion()) {
    const n = ++departures;
    departed.value = { dock: dockKey(d.fleet, d.harness), n };
    setTimeout(() => {
      if (departed.value?.n === n) departed.value = null;
    }, 2000);
  }
  window.dispatchEvent(new CustomEvent('admiral:launched', { detail: d }));
}
