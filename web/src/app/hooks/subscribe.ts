import { useLayoutEffect, useRef } from 'preact/hooks';

// The listener stays put across renders and calls the latest handler. A layout
// effect, because Preact runs a passive cleanup after unmount returns.
export function useSubscribe(target: EventTarget | null | undefined, type: string, handler: (e: Event) => void): void {
  const latest = useRef(handler);
  latest.current = handler;
  useLayoutEffect(() => {
    if (!target) return;
    const listener = (e: Event) => latest.current(e);
    target.addEventListener(type, listener);
    return () => target.removeEventListener(type, listener);
  }, [target, type]);
}
