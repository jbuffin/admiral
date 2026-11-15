import { describe, expect, it } from 'vitest';
import { createActionGroup, createPending, SPINNER_MIN_MS } from './busy';

// A manual clock plus a scheduler the test advances, standing in for setTimeout.
function store() {
  let t = 1_000_000;
  const timers: { at: number; fn: () => void }[] = [];
  let changes = 0;
  const pending = createPending({
    now: () => t,
    after: (fn, ms) => void timers.push({ at: t + ms, fn }),
    onChange: () => void changes++,
  });
  const advance = (ms: number) => {
    t += ms;
    for (const timer of timers.filter((x) => x.at <= t)) {
      timers.splice(timers.indexOf(timer), 1);
      timer.fn();
    }
  };
  return { pending, advance, changes: () => changes };
}

function deferred<T = void>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => ((resolve = res), (reject = rej)));
  return { promise, resolve, reject };
}

const tick = () => new Promise<void>((r) => setTimeout(r, 0));
const merge = ['ship', 'alpha', 'merge'] as const;

describe('pending requests', () => {
  it('is busy at once when onClick returns a promise, and calls onClick once', () => {
    const { pending } = store();
    let calls = 0;
    pending.run(merge, () => (calls++, deferred().promise));
    expect(calls).toBe(1);
    expect(pending.isBusy(merge)).toBe(true);
  });

  it('ignores a second tap while busy', () => {
    const { pending } = store();
    let calls = 0;
    pending.run(merge, () => (calls++, deferred().promise));
    pending.run(merge, () => (calls++, deferred().promise));
    expect(calls).toBe(1);
  });

  it('never shows busy when onClick returns nothing', () => {
    const { pending } = store();
    pending.run(merge, () => undefined);
    expect(pending.isBusy(merge)).toBe(false);
  });

  it('returns to rest when the promise resolves after the minimum spinner time', async () => {
    const { pending, advance } = store();
    const d = deferred();
    const done = pending.run(merge, () => d.promise);
    advance(SPINNER_MIN_MS + 1);
    d.resolve();
    await done;
    expect(pending.isBusy(merge)).toBe(false);
  });

  it('returns to rest on failure, resolves the run, and leaves no rejection', async () => {
    const { pending, advance } = store();
    const d = deferred();
    const done = pending.run(merge, () => d.promise);
    advance(SPINNER_MIN_MS);
    d.reject(new Error('boom'));
    await expect(done).resolves.toBeUndefined();
    expect(pending.isBusy(merge)).toBe(false);
  });

  it('keeps the spinner at least 300 ms once shown, even when the answer is fast', async () => {
    expect(SPINNER_MIN_MS).toBe(300);
    const { pending, advance } = store();
    const d = deferred();
    pending.run(merge, () => d.promise);
    advance(50);
    d.resolve();
    await tick();
    expect(pending.isBusy(merge)).toBe(true);
    advance(SPINNER_MIN_MS - 51);
    expect(pending.isBusy(merge)).toBe(true);
    advance(1);
    expect(pending.isBusy(merge)).toBe(false);
  });

  it('keeps ignoring taps while the minimum hold runs after a fast answer', async () => {
    const { pending } = store();
    const d = deferred();
    pending.run(merge, () => d.promise);
    d.resolve();
    await tick();
    let calls = 0;
    pending.run(merge, () => (calls++, deferred().promise));
    expect(calls).toBe(0);
  });

  it('keys by what the request acts on and the action', () => {
    const { pending } = store();
    pending.run(['ship', 'alpha', 'stop'], () => deferred().promise);
    expect(pending.isBusy(['ship', 'alpha', 'stop'])).toBe(true);
    expect(pending.isBusy(['ship', 'beta', 'stop'])).toBe(false);
    expect(pending.isBusy(['ship', 'alpha', 'return-to-port'])).toBe(false);
    expect(pending.isBusy(['item', 'alpha', 'stop'])).toBe(false);
  });

  it('keeps a request busy across a remount, because the store outlives the button', () => {
    const { pending } = store();
    pending.run(merge, () => deferred().promise);
    // The button unmounts and a new one mounts for the same key: it reads the store.
    expect(pending.isBusy(merge)).toBe(true);
  });

  it('does not replay a spinner on remount for a request that settled while away', async () => {
    const { pending, advance } = store();
    const d = deferred();
    pending.run(merge, () => d.promise);
    advance(SPINNER_MIN_MS + 100);
    d.resolve();
    await tick();
    expect(pending.isBusy(merge)).toBe(false);
  });

  it('counts the minimum from when the spinner first showed, not from a remount', async () => {
    const { pending, advance } = store();
    const d = deferred();
    pending.run(merge, () => d.promise);
    advance(200);
    expect(pending.shownAt(merge)).toBe(1_000_000);
    d.resolve();
    await tick();
    advance(99);
    expect(pending.isBusy(merge)).toBe(true);
    advance(1);
    expect(pending.isBusy(merge)).toBe(false);
  });

  it('starts empty: a new store (a reload) shows everything at rest', () => {
    const first = store();
    first.pending.run(merge, () => deferred().promise);
    expect(store().pending.isBusy(merge)).toBe(false);
  });

  it('tells subscribers when a request starts and when it ends', async () => {
    const { pending, advance, changes } = store();
    const d = deferred();
    pending.run(merge, () => d.promise);
    expect(changes()).toBe(1);
    advance(SPINNER_MIN_MS);
    d.resolve();
    await tick();
    expect(changes()).toBe(2);
  });
});

describe('action group', () => {
  it('disables the other buttons while one is busy, never the busy one', () => {
    const group = createActionGroup();
    group.setBusy('merge', true);
    expect(group.disabledFor('merge')).toBe(false);
    expect(group.disabledFor('cancel')).toBe(true);
    expect(group.disabledFor('close')).toBe(true);
  });

  it('re-enables the others when the busy one settles', () => {
    const group = createActionGroup();
    group.setBusy('merge', true);
    group.setBusy('merge', false);
    expect(group.disabledFor('cancel')).toBe(false);
  });

  it('leaves everything enabled when nothing is busy', () => {
    expect(createActionGroup().disabledFor('cancel')).toBe(false);
  });

  it('keeps the others disabled until every busy button has settled', () => {
    const group = createActionGroup();
    group.setBusy('a', true);
    group.setBusy('b', true);
    group.setBusy('a', false);
    expect(group.disabledFor('cancel')).toBe(true);
    expect(group.disabledFor('b')).toBe(false);
    expect(group.disabledFor('a')).toBe(true);
  });
});
