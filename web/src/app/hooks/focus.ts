import type { RefObject } from 'preact';
import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';

export type FocusTarget = RefObject<HTMLElement | null> | (() => HTMLElement | null | undefined);

const resolve = (target: FocusTarget) => (typeof target === 'function' ? target() : target.current);

// Focus runs in a passive effect, after the render that made the target visible.
// `layout` focuses before paint instead, for a target the person could reach
// and start typing into before a passive effect lands.
export function useFocusOnOpen(open: boolean, target: FocusTarget, opts?: { layout?: boolean }): void {
  const latest = useRef(target);
  latest.current = target;
  const layout = opts?.layout === true;
  useEffect(() => {
    if (open && !layout) resolve(latest.current)?.focus();
  }, [open, layout]);
  useLayoutEffect(() => {
    if (open && layout) resolve(latest.current)?.focus();
  }, [open, layout]);
}

// Only the open-to-closed edge focuses: a closed mount does nothing.
export function useFocusOnClose(open: boolean, target: FocusTarget): void {
  const latest = useRef(target);
  latest.current = target;
  const wasOpen = useRef(open);
  useEffect(() => {
    if (wasOpen.current && !open) resolve(latest.current)?.focus();
    wasOpen.current = open;
  }, [open]);
}
