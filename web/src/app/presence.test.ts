import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { watchPresence } from './presence';

describe('watchPresence', () => {
  let doc: EventTarget & { visibilityState: string };
  let sent: { visible: boolean; idle_ms: number }[];
  let clock: number;
  const send = (m: object) => {
    sent.push(m as { visible: boolean; idle_ms: number });
    return true;
  };

  beforeEach(() => {
    vi.useFakeTimers();
    sent = [];
    clock = 0;
    doc = Object.assign(new EventTarget(), { visibilityState: 'visible' });
    vi.stubGlobal('document', doc);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('reports on start, every 30 seconds while visible with how long ago it was used, and on a change of visibility', async () => {
    const stop = watchPresence(send, () => clock);
    expect(sent).toEqual([{ type: 'presence', visible: true, idle_ms: 0 }]);

    clock = 30_000;
    await vi.advanceTimersByTimeAsync(30_000);
    expect(sent.at(-1)).toEqual({ type: 'presence', visible: true, idle_ms: 30_000 });

    clock = 40_000;
    doc.dispatchEvent(new Event('pointerdown'));
    clock = 60_000;
    await vi.advanceTimersByTimeAsync(30_000);
    expect(sent.at(-1)?.idle_ms).toBe(20_000);

    doc.visibilityState = 'hidden';
    doc.dispatchEvent(new Event('visibilitychange'));
    expect(sent.at(-1)?.visible).toBe(false);
    const n = sent.length;
    await vi.advanceTimersByTimeAsync(90_000);
    expect(sent.length).toBe(n);

    stop();
    doc.visibilityState = 'visible';
    doc.dispatchEvent(new Event('visibilitychange'));
    expect(sent.length).toBe(n);
  });

  it('reports on the first input after a pause, not on every input, and again when the socket reopens', () => {
    let opened = () => {};
    const stop = watchPresence(send, () => clock, (fn) => {
      opened = fn;
      return () => {};
    });
    sent.length = 0;

    clock = 5_000;
    doc.dispatchEvent(new Event('pointermove'));
    expect(sent).toEqual([]);

    clock = 60_000;
    doc.dispatchEvent(new Event('touchstart'));
    expect(sent).toEqual([{ type: 'presence', visible: true, idle_ms: 0 }]);
    clock = 60_500;
    doc.dispatchEvent(new Event('touchstart'));
    expect(sent.length).toBe(1);

    clock = 61_000;
    opened();
    expect(sent.length).toBe(2);
    stop();
  });
});
