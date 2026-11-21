import { render } from '@testing-library/preact';
import { expect, it, vi } from 'vitest';
import { useSubscribe } from './subscribe';

function Harness({ target, type = 'ping', handler }: { target: EventTarget | null | undefined; type?: string; handler: (e: Event) => void }) {
  useSubscribe(target, type, handler);
  return null;
}

it('calls the handler when the event fires on the target', () => {
  const target = new EventTarget();
  const handler = vi.fn();
  render(<Harness target={target} handler={handler} />);
  target.dispatchEvent(new Event('ping'));
  expect(handler).toHaveBeenCalledTimes(1);
});

it('calls the latest handler without moving the listener on each render', () => {
  const target = new EventTarget();
  const add = vi.spyOn(target, 'addEventListener');
  const remove = vi.spyOn(target, 'removeEventListener');
  const first = vi.fn();
  const second = vi.fn();
  const { rerender } = render(<Harness target={target} handler={first} />);
  rerender(<Harness target={target} handler={second} />);
  target.dispatchEvent(new Event('ping'));
  expect(first).not.toHaveBeenCalled();
  expect(second).toHaveBeenCalledTimes(1);
  expect(add).toHaveBeenCalledTimes(1);
  expect(remove).not.toHaveBeenCalled();
});

it('does not call the handler after unmount', () => {
  const target = new EventTarget();
  const handler = vi.fn();
  const { unmount } = render(<Harness target={target} handler={handler} />);
  unmount();
  target.dispatchEvent(new Event('ping'));
  expect(handler).not.toHaveBeenCalled();
});

it('moves to a new target', () => {
  const a = new EventTarget();
  const b = new EventTarget();
  const handler = vi.fn();
  const { rerender } = render(<Harness target={a} handler={handler} />);
  rerender(<Harness target={b} handler={handler} />);
  a.dispatchEvent(new Event('ping'));
  expect(handler).not.toHaveBeenCalled();
  b.dispatchEvent(new Event('ping'));
  expect(handler).toHaveBeenCalledTimes(1);
});

it('moves to a new event type', () => {
  const target = new EventTarget();
  const handler = vi.fn();
  const { rerender } = render(<Harness target={target} type="a" handler={handler} />);
  rerender(<Harness target={target} type="b" handler={handler} />);
  target.dispatchEvent(new Event('a'));
  expect(handler).not.toHaveBeenCalled();
  target.dispatchEvent(new Event('b'));
  expect(handler).toHaveBeenCalledTimes(1);
});

it('subscribes to nothing for a null target', () => {
  expect(() => render(<Harness target={null} handler={vi.fn()} />)).not.toThrow();
});
