import { cleanup, render } from '@testing-library/preact';
import { afterEach, expect, test } from 'vitest';
import type { Ship } from './api';
import { DraftingLine } from './DraftingLine';

afterEach(cleanup);

const ship = (over: Record<string, unknown> = {}) => ({ session: 'saltpaw', name: 'Saltpaw', status: 'underway', drafting: true, ...over }) as unknown as Ship;
const REFUSAL = "No fleet owns acme/widgets, so Admiral can't read it";

test("the ship's drafting_notice shows as one line, styled like the bridge's other note lines", () => {
  const { container } = render(<DraftingLine ship={ship({ drafting_notice: REFUSAL })} />);
  const lines = container.querySelectorAll('p');
  expect(lines).toHaveLength(1);
  expect(lines[0].textContent).toBe(REFUSAL);
  expect(lines[0].classList.contains('note')).toBe(true);
});

test('a moored ship shows it too', () => {
  const { container } = render(<DraftingLine ship={ship({ status: 'moored', drafting_notice: REFUSAL })} />);
  expect(container.textContent).toBe(REFUSAL);
});

test('markup and markdown in it stay literal text', () => {
  const value = 'Ignored <b>acme/widgets#21</b>: *already* followed <img src=x onerror="window.__xss=1">';
  const { container } = render(<DraftingLine ship={ship({ drafting_notice: value })} />);
  expect(container.textContent).toBe(value);
  expect(container.querySelector('b, em, img')).toBeNull();
});

test('nothing shows while the ship has none', () => {
  const absent = render(<DraftingLine ship={ship()} />);
  expect(absent.container.textContent).toBe('');
  cleanup();
  const empty = render(<DraftingLine ship={ship({ drafting_notice: '' })} />);
  expect(empty.container.textContent).toBe('');
});
