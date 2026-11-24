import { render } from '@testing-library/preact';
import { expect, test } from 'vitest';
import { diffLines, LineDiff } from './LineDiff';

test('diffLines keeps the common lines, and marks removed and added ones in order', () => {
  expect(diffLines('a\nb\nc\n', 'a\nB\nc\nd\n')).toEqual([
    { kind: 'same', text: 'a' },
    { kind: 'del', text: 'b' },
    { kind: 'add', text: 'B' },
    { kind: 'same', text: 'c' },
    { kind: 'add', text: 'd' },
  ]);
});

test('diffLines of identical text is all same, and of nothing against text is all added', () => {
  expect(diffLines('x\ny\n', 'x\ny\n').every((l) => l.kind === 'same')).toBe(true);
  expect(diffLines('', 'x\ny\n')).toEqual([
    { kind: 'add', text: 'x' },
    { kind: 'add', text: 'y' },
  ]);
});

test('LineDiff shows a - or + marker per changed line and wraps long lines', () => {
  const long = 'word '.repeat(80);
  const { container } = render(<LineDiff before={'keep\nold\n'} after={`keep\n${long}\n`} />);
  const rows = [...container.querySelectorAll('[data-kind]')];
  expect(rows.map((r) => r.getAttribute('data-kind'))).toEqual(['same', 'del', 'add']);
  expect(rows[1]!.textContent).toContain('-');
  expect(rows[1]!.textContent).toContain('old');
  expect(rows[2]!.textContent).toContain('+');
  expect(rows[0]!.textContent).not.toMatch(/^[-+]/);
  expect(container.querySelector('.line-diff')!.classList.contains('wrap')).toBe(true);
});
