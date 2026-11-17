import { cleanup, fireEvent, render, screen } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, expect, test, vi } from 'vitest';
import type { SkillChoice, SkillsAnswer } from './launchForm';
import { LaunchSkillPicker } from './LaunchSkillPicker';

afterEach(cleanup);

const answer: SkillsAnswer = {
  mark: '/',
  commodore: { found: false, name: 'commodore', state: 'missing' },
  skills: [
    { name: 'grill', description: 'Grill the plan', favorite: true },
    { name: 'tdd', description: 'Test first', favorite: false },
  ],
};

// The form owns the pick and the typed text; this holds them the same way.
function Picker({ onPick = () => undefined }: { onPick?: (c: SkillChoice) => void }) {
  const [typed, setTyped] = useState<string | null>(null);
  const [value, setValue] = useState<SkillChoice>({ kind: 'none' });
  return (
    <LaunchSkillPicker
      answer={answer}
      value={value}
      typed={typed}
      warning={null}
      onType={setTyped}
      onPick={(c) => {
        setTyped(null);
        setValue(c);
        onPick(c);
      }}
    />
  );
}

const box = () => screen.getByRole('combobox', { name: 'Skill' }) as HTMLInputElement;

test('a tap opens the list with the current pick selected, and the combobox names the open row', () => {
  render(<Picker />);
  expect(box().getAttribute('aria-expanded')).toBe('false');

  fireEvent.click(box());

  expect(box().getAttribute('aria-expanded')).toBe('true');
  const rows = screen.getAllByRole('option');
  expect(rows.map((r) => r.textContent)).toEqual(['Commodorenot found', '/grillGrill the plan', 'Just launch']);
  expect(rows.map((r) => r.getAttribute('aria-selected'))).toEqual(['false', 'false', 'true']);
  expect(box().getAttribute('aria-activedescendant')).toBe(rows[2]!.id);
});

test('Arrow keys move the active row and stop at the ends, and Enter picks it', () => {
  const onPick = vi.fn();
  render(<Picker onPick={onPick} />);
  fireEvent.keyDown(box(), { key: 'ArrowDown' });
  const rows = screen.getAllByRole('option');

  fireEvent.keyDown(box(), { key: 'ArrowDown' });
  expect(box().getAttribute('aria-activedescendant')).toBe(rows[2]!.id);
  fireEvent.keyDown(box(), { key: 'ArrowUp' });
  fireEvent.keyDown(box(), { key: 'ArrowUp' });
  fireEvent.keyDown(box(), { key: 'ArrowUp' });
  expect(box().getAttribute('aria-activedescendant')).toBe(rows[0]!.id);
  fireEvent.keyDown(box(), { key: 'ArrowDown' });
  fireEvent.keyDown(box(), { key: 'Enter' });

  expect(onPick).toHaveBeenCalledWith({ kind: 'skill', name: 'grill' });
  expect(screen.queryByRole('listbox')).toBeNull();
});

test('a row the dock cannot offer is not picked by Enter or a tap', () => {
  const onPick = vi.fn();
  render(<Picker onPick={onPick} />);
  fireEvent.keyDown(box(), { key: 'ArrowDown' });
  fireEvent.keyDown(box(), { key: 'ArrowUp' });
  fireEvent.keyDown(box(), { key: 'ArrowUp' });

  fireEvent.keyDown(box(), { key: 'Enter' });
  fireEvent.click(screen.getByRole('option', { name: /^Commodore/ }));

  expect(onPick).not.toHaveBeenCalled();
  expect(screen.getByRole('listbox')).toBeTruthy();
});

test('Escape on a lone slash clears it, so the box shows the pick again', () => {
  render(<Picker />);

  fireEvent.input(box(), { target: { value: '/' } });
  expect(box().value).toBe('/');
  fireEvent.keyDown(box(), { key: 'Escape' });

  expect(box().value).toBe('Just launch');
  expect(screen.queryByRole('listbox')).toBeNull();
});

test('a skill load that failed shows its line beside the box', () => {
  render(<LaunchSkillPicker answer={null} value={{ kind: 'none' }} typed={null} warning={null} failure="Couldn't load skills: down" onType={() => undefined} onPick={() => undefined} />);

  expect(screen.getByText("Couldn't load skills: down")).toBeTruthy();
});
