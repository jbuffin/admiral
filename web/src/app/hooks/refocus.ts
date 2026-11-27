import type { RefObject } from 'preact';
import { useLayoutEffect } from 'preact/hooks';

// Re-focuses the element each time `token` changes to a non-zero value: a keyed reorder can drop focus from the node it moved.
export function useRefocus(token: number, target: RefObject<HTMLElement | null>): void {
  useLayoutEffect(() => {
    if (token) target.current?.focus();
  }, [token, target]);
}
