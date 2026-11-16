import { fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import { arranged, pressGrip, reordering } from './gripReorder';

const KEYS = ['a', 'b', 'c'];

function List({ commit }: { commit: (keys: string[]) => void }) {
  return (
    <ul>
      {arranged('rows', KEYS, (k) => k).map((k) => (
        <li key={k} data-reorder="rows" data-key={k}>
          <button type="button" aria-label={`Move ${k}`} onPointerDown={(e) => pressGrip(e, 'rows', k, KEYS, commit)} />
        </li>
      ))}
    </ul>
  );
}

// Rows 40px tall from y=0, so the midpoints are 20, 60 and 100.
const layOut = () =>
  document.querySelectorAll<HTMLElement>('li').forEach((li, i) => {
    li.getBoundingClientRect = () => ({ top: i * 40, height: 40, bottom: i * 40 + 40, left: 0, right: 100, width: 100, x: 0, y: i * 40, toJSON: () => ({}) });
  });

const touch = (type: string, init: PointerEventInit & { clientY?: number } = {}) => new PointerEvent(type, { pointerType: 'touch', pointerId: 7, isPrimary: true, button: 0, bubbles: true, cancelable: true, ...init });

afterEach(() => {
  reordering.value = null;
  document.body.classList.remove('dragging');
});

test('a touch drag of a grip past a row saves the order it shows, and the row follows the finger meanwhile', () => {
  const commit = vi.fn();
  render(<List commit={commit} />);
  layOut();
  fireEvent(screen.getByRole('button', { name: 'Move a' }), touch('pointerdown', { clientY: 20 }));
  fireEvent(window, touch('pointermove', { clientY: 70 }));
  expect([...document.querySelectorAll('li')].map((li) => (li as HTMLElement).dataset.key)).toEqual(['b', 'a', 'c']);
  expect(document.body.classList.contains('dragging')).toBe(true);
  expect(commit).not.toHaveBeenCalled();
  fireEvent(window, touch('pointerup', { clientY: 70 }));
  expect(commit).toHaveBeenCalledExactlyOnceWith(['b', 'a', 'c']);
  expect(document.body.classList.contains('dragging')).toBe(false);
  expect(reordering.value).toBeNull();
});

test('a drag that is cancelled, or ends where it began, saves nothing', () => {
  const commit = vi.fn();
  render(<List commit={commit} />);
  layOut();
  const grip = screen.getByRole('button', { name: 'Move b' });
  fireEvent(grip, touch('pointerdown', { clientY: 60 }));
  fireEvent(window, touch('pointermove', { clientY: 110 }));
  fireEvent(window, touch('pointercancel'));
  fireEvent(grip, touch('pointerdown', { clientY: 60 }));
  fireEvent(window, touch('pointermove', { clientY: 65 }));
  fireEvent(window, touch('pointerup'));
  expect(commit).not.toHaveBeenCalled();
});

test("another finger's pointer does not move the row", () => {
  const commit = vi.fn();
  render(<List commit={commit} />);
  layOut();
  fireEvent(screen.getByRole('button', { name: 'Move a' }), touch('pointerdown', { clientY: 20 }));
  fireEvent(window, touch('pointermove', { clientY: 110, pointerId: 8 }));
  fireEvent(window, touch('pointerup', { pointerId: 8 }));
  expect(commit).not.toHaveBeenCalled();
  fireEvent(window, touch('pointerup'));
});
