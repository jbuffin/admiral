import type { RefObject } from 'preact';
import { useLayoutEffect, useRef } from 'preact/hooks';

// Focuses the element in the commit where `want` turns true, then calls `done` so a later mount or remount never takes focus.
// A keyed reorder can drop focus from the node it moved.
export function useRefocus(want: boolean, target: RefObject<HTMLElement | null>, done: () => void): void {
  const latest = useRef(done);
  latest.current = done;
  useLayoutEffect(() => {
    if (!want) return;
    target.current?.focus();
    latest.current();
  }, [want, target]);
}
