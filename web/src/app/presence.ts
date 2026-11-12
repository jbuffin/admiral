import { onShipsOpen, sendShips } from './shipsLink';

const HEARTBEAT_MS = 30_000;
const IDLE_MS = 10_000;
const USE_EVENTS = ['pointerdown', 'pointermove', 'keydown', 'touchstart', 'wheel'];

/** Tells the server whether this window is being watched: on a visibility change, on connect, and every 30 s while visible. Returns a stop function. */
export function watchPresence(send: (msg: object) => boolean = sendShips, now: () => number = Date.now, onOpen: (fn: () => void) => () => void = onShipsOpen): () => void {
  let lastUsed = now();
  const report = () => send({ type: 'presence', visible: document.visibilityState === 'visible', idle_ms: Math.max(0, now() - lastUsed) });
  const used = () => {
    const wasIdle = now() - lastUsed >= IDLE_MS;
    lastUsed = now();
    // A tap after a pause counts at once; it need not wait for the heartbeat.
    if (wasIdle && document.visibilityState === 'visible') report();
  };
  const onVisibility = () => {
    if (document.visibilityState === 'visible') lastUsed = now();
    report();
  };
  document.addEventListener('visibilitychange', onVisibility);
  for (const e of USE_EVENTS) document.addEventListener(e, used, { passive: true });
  const beat = setInterval(() => {
    if (document.visibilityState === 'visible') report();
  }, HEARTBEAT_MS);
  const offOpen = onOpen(report);
  report();
  return () => {
    clearInterval(beat);
    offOpen();
    document.removeEventListener('visibilitychange', onVisibility);
    for (const e of USE_EVENTS) document.removeEventListener(e, used);
  };
}
