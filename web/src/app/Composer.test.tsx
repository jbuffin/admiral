import { fireEvent, render, screen } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, expect, test, vi } from 'vitest';
import { MessageField } from './Composer';

afterEach(() => vi.unstubAllGlobals());

const skills = {
  mark: '/',
  skills: [
    { name: 'tdd', description: 'Test-driven development.' },
    { name: 'triage', description: 'Sort the new issues.' },
    { name: 'review', description: 'Review the diff.' },
  ],
};

function Box() {
  const [text, setText] = useState('');
  return <MessageField label="Message" placeholder="Message the ship" text={text} setText={setText} send={() => undefined} disabled={false} canSend skills="/api/v1/ships/s1/skills" />;
}

test('typing / offers the ship\'s skills, and arrows with Tab or a tap complete one', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(skills))));
  render(<Box />);
  const box = screen.getByRole('textbox', { name: 'Message' }) as HTMLTextAreaElement;

  fireEvent.input(box, { target: { value: '/' } });
  const menu = await screen.findByRole('listbox', { name: 'Skills' });
  const names = () => screen.getAllByRole('option').map((o) => o.textContent);
  expect(names()).toEqual(['/reviewReview the diff.', '/tddTest-driven development.', '/triageSort the new issues.']);

  fireEvent.input(box, { target: { value: '/t' } });
  expect(names()).toEqual(['/tddTest-driven development.', '/triageSort the new issues.']);
  fireEvent.keyDown(box, { key: 'ArrowDown' });
  expect(screen.getByRole('option', { selected: true }).textContent).toContain('/triage');
  fireEvent.keyDown(box, { key: 'Tab' });
  expect(box.value).toBe('/triage ');
  expect(screen.queryByRole('listbox', { name: 'Skills' })).toBeNull();
  expect(menu.isConnected).toBe(false);

  fireEvent.input(box, { target: { value: '/rev' } });
  fireEvent.click(await screen.findByRole('option', { name: /review/ }));
  expect(box.value).toBe('/review ');

  fireEvent.input(box, { target: { value: '/x' } });
  expect(screen.queryByRole('listbox', { name: 'Skills' })).toBeNull();
});
