import type { RefObject } from 'preact';
import { useLayoutEffect } from 'preact/hooks';

// Focuses the element in the commit where `want` turns true, then calls `done` so a later mount or remount never takes focus.
// A keyed reorder can drop focus from the node it moved.
export function useRefocus(want: boolean, target: RefObject<HTMLElement | null>, done: () => void): void {
  useLayoutEffect(() => {
    if (!want) return;
    target.current?.focus();
    done();
  }, [want]);
}
