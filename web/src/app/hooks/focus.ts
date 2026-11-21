import type { RefObject } from 'preact';
import { useEffect, useRef } from 'preact/hooks';

export type FocusTarget = RefObject<HTMLElement | null> | (() => HTMLElement | null | undefined);

const resolve = (target: FocusTarget) => (typeof target === 'function' ? target() : target.current);

// Focus runs in an effect, after the render that made the target visible.
export function useFocusOnOpen(open: boolean, target: FocusTarget): void {
  const latest = useRef(target);
  latest.current = target;
  useEffect(() => {
    if (open) resolve(latest.current)?.focus();
  }, [open]);
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
