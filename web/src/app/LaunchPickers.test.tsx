import { cleanup, fireEvent, render, screen, within } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { DirsAnswer, Dock } from './api';
import { CheckoutSelect, DockPicker, ModelEffortPair, ModelSelect, ResourcesPicker } from './LaunchPickers';
import { resources } from './shipsLink';

beforeEach(() => {
  resources.value = [];
});
afterEach(cleanup);

const optionsOf = (label: string) => within(screen.getByLabelText(label)).getAllByRole('option').map((o) => o.textContent);

const dockOf = (over: Partial<Dock> = {}): Dock => ({ harness: 'claude', executable: 'claude', executable_found: true, warnings: [], models: ['opus', 'sonnet', 'fable', 'haiku', 'claude-opus-4-8'], models_source: 'listed', ...over });
const modelSelect = () => screen.getByLabelText('Model') as HTMLSelectElement;
const chosenOption = () => modelSelect().selectedOptions[0]!.textContent;
const other = () => screen.getByLabelText('Other model') as HTMLInputElement;

test('Effort offers each harness its own levels', () => {
  const { rerender } = render(<ModelEffortPair harness="claude" dock={dockOf()} model="" effort="" onModel={() => undefined} onEffort={() => undefined} />);
  expect(optionsOf('Effort')).toEqual(['Default', 'Low', 'Medium', 'High', 'Max']);

  rerender(<ModelEffortPair harness="codex" dock={dockOf({ harness: 'codex' })} model="" effort="" onModel={() => undefined} onEffort={() => undefined} />);
  expect(optionsOf('Effort')).not.toContain('Max');

  rerender(<ModelEffortPair harness="mystery" dock={undefined} model="" effort="" onModel={() => undefined} onEffort={() => undefined} />);
  expect(screen.queryByLabelText('Effort')).toBeNull();
});

test('the model select offers Default, the dock list in the server order, then Other…', () => {
  render(<ModelSelect dock={dockOf()} model="" onModel={() => undefined} />);

  expect(modelSelect().tagName).toBe('SELECT');
  expect(optionsOf('Model')).toEqual(['Default', 'opus', 'sonnet', 'fable', 'haiku', 'claude-opus-4-8', 'Other…']);
  expect(chosenOption()).toBe('Default');
  expect(screen.queryByLabelText('Other model')).toBeNull();
});

test('choosing a listed model reports it and Default reports the empty model', () => {
  const onModel = vi.fn();
  const { rerender } = render(<ModelSelect dock={dockOf()} model="" onModel={onModel} />);

  fireEvent.change(modelSelect(), { target: { value: 'fable' } });
  expect(onModel).toHaveBeenLastCalledWith('fable');
  rerender(<ModelSelect dock={dockOf()} model="fable" onModel={onModel} />);
  expect(chosenOption()).toBe('fable');
  fireEvent.change(modelSelect(), { target: { value: '' } });
  expect(onModel).toHaveBeenLastCalledWith('');
});

test('Other… opens a text field that sends what is typed, trimmed, and the field is not named Model', () => {
  const onModel = vi.fn();
  render(<ModelSelect dock={dockOf({ harness: 'copilot', models: ['gpt-5.4'] })} model="" onModel={onModel} />);

  fireEvent.change(modelSelect(), { target: { value: screen.getByRole('option', { name: 'Other…' }).getAttribute('value')! } });
  expect(chosenOption()).toBe('Other…');
  expect(screen.getAllByLabelText('Model')).toHaveLength(1);
  expect(other().getAttribute('autocapitalize')).toBe('off');
  expect(other().getAttribute('autocorrect')).toBe('off');
  expect(other().getAttribute('spellcheck')).toBe('false');
  fireEvent.input(other(), { target: { value: ' my-model ' } });
  expect(onModel).toHaveBeenLastCalledWith('my-model');
});

test('a model outside the list shows as Other… with its text, never dropped or changed', () => {
  const onModel = vi.fn();
  render(<ModelSelect dock={dockOf()} model="old-model" onModel={onModel} />);

  expect(chosenOption()).toBe('Other…');
  expect(other().value).toBe('old-model');
  expect(onModel).not.toHaveBeenCalled();
});

test('editing a stored model outside the list keeps Other… showing, even when cleared or typed to a listed name', () => {
  const Held = () => {
    const [model, setModel] = useState('old-model');
    return <ModelSelect dock={dockOf()} model={model} onModel={setModel} />;
  };
  render(<Held />);

  fireEvent.input(other(), { target: { value: '' } });
  expect(chosenOption()).toBe('Other…');
  expect(other().value).toBe('');
  fireEvent.input(other(), { target: { value: 'opus' } });
  expect(chosenOption()).toBe('Other…');
  expect(other().value).toBe('opus');
  fireEvent.change(modelSelect(), { target: { value: 'haiku' } });
  expect(modelSelect().value).toBe('haiku');
  expect(screen.queryByLabelText('Other model')).toBeNull();
});

test('a stored model that the dock list gains shows as that entry, and the Other field goes', () => {
  const { rerender } = render(<ModelSelect dock={dockOf({ models: [] })} model="opus" onModel={() => undefined} />);
  expect(chosenOption()).toBe('Other…');
  expect(other().value).toBe('opus');

  rerender(<ModelSelect dock={dockOf({ models: ['opus', 'sonnet'] })} model="opus" onModel={() => undefined} />);
  expect(chosenOption()).toBe('opus');
  expect(screen.queryByLabelText('Other model')).toBeNull();
});

test('a list that gains the model while its Other field is being edited leaves Other… open with the typed text', () => {
  const Held = ({ models }: { models: string[] }) => {
    const [model, setModel] = useState('old-model');
    return <ModelSelect dock={dockOf({ models })} model={model} onModel={setModel} />;
  };
  const { rerender } = render(<Held models={['sonnet']} />);

  other().focus();
  fireEvent.input(other(), { target: { value: 'opus' } });
  rerender(<Held models={['sonnet', 'opus']} />);

  expect(chosenOption()).toBe('Other…');
  expect(other().value).toBe('opus');
  expect(document.activeElement).toBe(other());
});

test('a dock with no list offers Default and Other… only, and no dock the same', () => {
  const { rerender } = render(<ModelSelect dock={dockOf({ models: [] })} model="" onModel={() => undefined} />);
  expect(optionsOf('Model')).toEqual(['Default', 'Other…']);
  rerender(<ModelSelect dock={undefined} model="" onModel={() => undefined} />);
  expect(optionsOf('Model')).toEqual(['Default', 'Other…']);
});

test("the built-in note shows when the harness hasn't listed its models, and not when it has", () => {
  const { rerender } = render(<ModelSelect dock={dockOf({ models_source: 'built_in' })} model="" onModel={() => undefined} />);
  expect(document.body.textContent).toMatch(/built-in/i);
  rerender(<ModelSelect dock={dockOf({ models_source: 'listed' })} model="" onModel={() => undefined} />);
  expect(document.body.textContent).not.toMatch(/built-in/i);
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
      { path: '/w/proj', name: 'proj', kind: 'checkout', ships_here: 1, remotes: [] },
      { path: '/w', name: 'w', kind: 'search_root', ships_here: 0, remotes: [] },
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
