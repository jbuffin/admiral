import { act, render } from '@testing-library/preact';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { usePollUntil } from './poll';

function Harness({ tick, active, dep = 0, every = 1000 }: { tick: () => unknown; active?: boolean; dep?: number; every?: number }) {
  usePollUntil(tick, every, { active, deps: [dep] });
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
