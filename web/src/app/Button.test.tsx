import { cleanup, fireEvent, render, screen } from '@testing-library/preact';
import { expect, test, vi } from 'vitest';
import { Button } from './Button';

// A new render root numbers its ids from the start, so its first button gets the id the old root's first button had.
test('a button mounted where a busy one just unmounted is not busy and takes a tap', () => {
  render(<Button label="Go" onClick={() => new Promise<void>(() => {})} />);
  fireEvent.click(screen.getByRole('button', { name: 'Go' }));
  expect(screen.getByRole('button').getAttribute('aria-busy')).toBe('true');
  cleanup();

  const next = vi.fn();
  render(<Button label="Go" onClick={next} />);
  const fresh = screen.getByRole('button', { name: 'Go' });
  expect(fresh.getAttribute('aria-busy')).toBeNull();
  fireEvent.click(fresh);
  expect(next).toHaveBeenCalledTimes(1);
});
