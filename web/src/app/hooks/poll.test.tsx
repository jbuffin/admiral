import { act, render } from '@testing-library/preact';
import { useRef } from 'preact/hooks';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { usePollUntil } from './poll';

function Harness({ tick, active, dep = 0, every = 1000 }: { tick: (isLive: () => boolean, dep: number) => unknown; active?: boolean; dep?: number; every?: number }) {
  const latest = useRef(tick);
  latest.current = tick;
  usePollUntil((isLive) => latest.current(isLive, dep), [dep], { everyMs: every, active });
  return null;
}

beforeEach(() => void vi.useFakeTimers());
afterEach(() => {
  vi.useRealTimers();
});

const advance = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });

it('calls tick at once, then every interval', async () => {
  const tick = vi.fn();
  render(<Harness tick={tick} />);
  await advance(0);
  expect(tick).toHaveBeenCalledTimes(1);
  await advance(3000);
  expect(tick).toHaveBeenCalledTimes(4);
});

it('stops when tick returns true', async () => {
  const tick = vi.fn().mockReturnValueOnce(false).mockReturnValueOnce(true);
  render(<Harness tick={tick} />);
  await advance(5000);
  expect(tick).toHaveBeenCalledTimes(2);
});

it('stops when tick resolves to true, and keeps going on a resolved false', async () => {
  const tick = vi.fn().mockResolvedValueOnce(false).mockResolvedValueOnce(true);
  render(<Harness tick={tick} />);
  await advance(5000);
  expect(tick).toHaveBeenCalledTimes(2);
});

it('does not stop on a truthy value that is not exactly true', async () => {
  const tick = vi.fn(() => 1);
  render(<Harness tick={tick} />);
  await advance(2000);
  expect(tick).toHaveBeenCalledTimes(3);
});

it('does not call tick while inactive, and starts when active turns true', async () => {
  const tick = vi.fn();
  const { rerender } = render(<Harness tick={tick} active={false} />);
  await advance(3000);
  expect(tick).not.toHaveBeenCalled();
  rerender(<Harness tick={tick} active />);
  await advance(0);
  expect(tick).toHaveBeenCalledTimes(1);
});

it('stops when active turns false', async () => {
  const tick = vi.fn();
  const { rerender } = render(<Harness tick={tick} active />);
  await advance(1000);
  const before = tick.mock.calls.length;
  expect(before).toBeGreaterThan(1);
  rerender(<Harness tick={tick} active={false} />);
  await advance(5000);
  expect(tick).toHaveBeenCalledTimes(before);
});

it('stops on unmount', async () => {
  const tick = vi.fn();
  const { unmount } = render(<Harness tick={tick} />);
  await advance(1000);
  const before = tick.mock.calls.length;
  expect(before).toBeGreaterThan(1);
  unmount();
  await advance(5000);
  expect(tick).toHaveBeenCalledTimes(before);
});

it('restarts with an immediate call when deps change', async () => {
  const tick = vi.fn();
  const { rerender } = render(<Harness tick={tick} dep={1} />);
  await advance(0);
  expect(tick).toHaveBeenCalledTimes(1);
  rerender(<Harness tick={tick} dep={2} />);
  await advance(0);
  expect(tick).toHaveBeenCalledTimes(2);
});

it('calls the latest tick without restarting when only the tick changes', async () => {
  const first = vi.fn();
  const second = vi.fn();
  const { rerender } = render(<Harness tick={first} />);
  await advance(0);
  rerender(<Harness tick={second} />);
  await advance(0);
  expect(first).toHaveBeenCalledTimes(1);
  expect(second).not.toHaveBeenCalled();
  await advance(1000);
  expect(second).toHaveBeenCalledTimes(1);
});

it('does not call tick again when a promise resolves after unmount', async () => {
  let resolve: (v: boolean) => void = () => {};
  const tick = vi.fn(() => new Promise<boolean>((r) => (resolve = r)));
  const { unmount } = render(<Harness tick={tick} />);
  await advance(0);
  unmount();
  resolve(false);
  await advance(5000);
  expect(tick).toHaveBeenCalledTimes(1);
});

it('tells a late tick from the old deps that it is stale, and the new run that it is live', async () => {
  const resolvers: ((v: boolean) => void)[] = [];
  const writes: string[] = [];
  const tick = (dep: number) => async (isLive: () => boolean) => {
    const done = await new Promise<boolean>((r) => resolvers.push(r));
    if (isLive()) writes.push(`dep ${dep}`);
    return done;
  };
  const { rerender } = render(<Harness tick={tick(1)} dep={1} />);
  await advance(0);
  rerender(<Harness tick={tick(2)} dep={2} />);
  await advance(0);
  expect(resolvers).toHaveLength(2);
  resolvers[0](false);
  resolvers[1](true);
  await advance(0);
  expect(writes).toEqual(['dep 2']);
});
