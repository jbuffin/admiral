import type { RefObject } from 'preact';
import { useLayoutEffect } from 'preact/hooks';

/** Calls onOutside when a press lands outside every one of refs while active. */
export function useOutsidePress(active: boolean, refs: RefObject<Node | null>[], onOutside: () => void) {
  useLayoutEffect(() => {
    if (!active) return;
    const onDown = (e: Event) => {
      const t = e.target as Node;
      if (!refs.some((r) => r.current?.contains(t))) onOutside();
    };
    document.addEventListener('pointerdown', onDown);
    return () => document.removeEventListener('pointerdown', onDown);
  }, [active]);
}
