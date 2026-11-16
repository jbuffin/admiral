import { cleanup, fireEvent, render, screen, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { DirsAnswer } from './api';
import { CheckoutSelect, DockPicker, ModelEffortPair, ResourcesPicker } from './LaunchPickers';
import { resources } from './shipsLink';

beforeEach(() => {
  resources.value = [];
});
afterEach(cleanup);

const optionsOf = (label: string) => within(screen.getByLabelText(label)).getAllByRole('option').map((o) => o.textContent);

test('Effort offers each harness its own levels, and Codex takes a typed model where Claude Code picks one', () => {
  const { rerender } = render(<ModelEffortPair harness="claude" model="" effort="" onModel={() => undefined} onEffort={() => undefined} />);
  expect(optionsOf('Effort')).toEqual(['Default', 'Low', 'Medium', 'High', 'Max']);
  expect(screen.getByLabelText('Model').tagName).toBe('SELECT');

  const onModel = vi.fn();
  rerender(<ModelEffortPair harness="codex" model="" effort="" onModel={onModel} onEffort={() => undefined} />);
  expect(optionsOf('Effort')).not.toContain('Max');
  fireEvent.input(screen.getByLabelText('Model'), { target: { value: ' gpt-x ' } });
  expect(onModel).toHaveBeenCalledWith('gpt-x');

  rerender(<ModelEffortPair harness="mystery" model="" effort="" onModel={() => undefined} onEffort={() => undefined} />);
  expect(screen.queryByLabelText('Effort')).toBeNull();
});

test('the resources list shows how much of each is in use and holds the names ticked', () => {
  resources.value = [
    { name: 'db', capacity: 1, in_use: 0 },
    { name: 'phone', capacity: 2, in_use: 3 },
  ];
  const onChange = vi.fn();
  const { rerender } = render(<ResourcesPicker held={[]} onChange={onChange} />);

  expect(screen.getByRole('checkbox', { name: /^db/ }).closest('label')!.textContent).toContain('0/1 in use');
  expect(screen.getByRole('checkbox', { name: /^phone/ }).closest('label')!.textContent).toContain('2/2 in use');
  fireEvent.click(screen.getByRole('checkbox', { name: /^db/ }));
  expect(onChange).toHaveBeenLastCalledWith(['db']);

  rerender(<ResourcesPicker held={['db', 'phone']} onChange={onChange} />);
  fireEvent.click(screen.getByRole('checkbox', { name: /^db/ }));
  expect(onChange).toHaveBeenLastCalledWith(['phone']);
});

test('the resources list is absent when the server has none', () => {
  render(<ResourcesPicker held={[]} onChange={() => undefined} />);

  expect(screen.queryByRole('group', { name: 'Resources' })).toBeNull();
});

test('the dock picker marks the chosen dock and reports a change', () => {
  const onChange = vi.fn();
  render(<DockPicker docks={[{ harness: 'claude' }, { harness: 'codex' }]} harness="claude" onChange={onChange} />);

  expect((screen.getByRole('radio', { name: 'Claude' }) as HTMLInputElement).checked).toBe(true);
  fireEvent.click(screen.getByRole('radio', { name: 'Codex' }));
  expect(onChange).toHaveBeenCalledWith('codex');
});

test('the checkout list says what each folder is, flags a ship underway, and offers the clone, saying when its folder exists', () => {
  const dirs: DirsAnswer = {
    entries: [
      { path: '/w/proj', name: 'proj', kind: 'checkout', ships_here: 1 },
      { path: '/w', name: 'w', kind: 'search_root', ships_here: 0 },
    ],
    selected: '/w/proj',
    clone: { repo: 'github.com/acme/widget', path: '/w/widget', exists: true },
  };
  const { rerender } = render(<CheckoutSelect dir="/w/proj" dirs={dirs} allowClone onChange={() => undefined} />);

  expect(optionsOf('Checkout')).toEqual(['Clone github.com/acme/widget (folder exists)', 'proj (checkout, ship underway)', 'w (search root)']);
  rerender(<CheckoutSelect dir="/w/proj" dirs={dirs} allowClone={false} onChange={() => undefined} />);
  expect(optionsOf('Checkout')).toEqual(['proj (checkout, ship underway)', 'w (search root)']);
  rerender(<CheckoutSelect dir="" dirs={null} allowClone onChange={() => undefined} />);
  expect(optionsOf('Checkout')).toEqual(['Choose a checkout']);
});
