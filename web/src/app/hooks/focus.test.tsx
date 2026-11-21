import { render } from '@testing-library/preact';
import { useRef } from 'preact/hooks';
import { expect, it } from 'vitest';
import { useFocusOnClose, useFocusOnOpen } from './focus';

type Mode = 'ref' | 'fn' | 'null';

function Harness({ hook, open, mode = 'ref' }: { hook: typeof useFocusOnOpen; open: boolean; mode?: Mode }) {
  const ref = useRef<HTMLButtonElement>(null);
  const target = mode === 'ref' ? ref : mode === 'fn' ? () => ref.current : { current: null };
  hook(open, target);
  return <button ref={ref}>target</button>;
}

const target = () => document.querySelector('button')!;

it('focuses the target when mounted open', () => {
  render(<Harness hook={useFocusOnOpen} open />);
  expect(document.activeElement).toBe(target());
});

it('does not focus the target when mounted closed', () => {
  render(<Harness hook={useFocusOnOpen} open={false} />);
  expect(document.activeElement).not.toBe(target());
});

it('focuses the target when open turns true, and not when it turns false', () => {
  const { rerender } = render(<Harness hook={useFocusOnOpen} open={false} />);
  rerender(<Harness hook={useFocusOnOpen} open />);
  expect(document.activeElement).toBe(target());
  target().blur();
  rerender(<Harness hook={useFocusOnOpen} open={false} />);
  expect(document.activeElement).not.toBe(target());
});

it('takes a function target', () => {
  render(<Harness hook={useFocusOnOpen} open mode="fn" />);
  expect(document.activeElement).toBe(target());
});

it('does nothing and does not throw for a null target', () => {
  expect(() => render(<Harness hook={useFocusOnOpen} open mode="null" />)).not.toThrow();
  expect(document.activeElement).toBe(document.body);
});

it('useFocusOnClose never focuses at mount, open or closed', () => {
  const a = render(<Harness hook={useFocusOnClose} open />);
  expect(document.activeElement).not.toBe(target());
  a.unmount();
  render(<Harness hook={useFocusOnClose} open={false} />);
  expect(document.activeElement).not.toBe(target());
});

it('useFocusOnClose focuses the target when open turns false, not when it turns true', () => {
  const { rerender } = render(<Harness hook={useFocusOnClose} open={false} />);
  rerender(<Harness hook={useFocusOnClose} open />);
  expect(document.activeElement).not.toBe(target());
  rerender(<Harness hook={useFocusOnClose} open={false} />);
  expect(document.activeElement).toBe(target());
});

it('useFocusOnClose takes a function target and survives a null one', () => {
  const a = render(<Harness hook={useFocusOnClose} open mode="fn" />);
  a.rerender(<Harness hook={useFocusOnClose} open={false} mode="fn" />);
  expect(document.activeElement).toBe(target());
  a.unmount();
  const b = render(<Harness hook={useFocusOnClose} open mode="null" />);
  expect(() => b.rerender(<Harness hook={useFocusOnClose} open={false} mode="null" />)).not.toThrow();
});
