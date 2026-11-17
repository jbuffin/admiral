import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { useState } from 'preact/hooks';
import { afterEach, expect, test, vi } from 'vitest';
import type { CrewMember } from './crew';
import { SkillPicker } from './SkillPicker';

const skill = (name: string, description: string, harnesses: string[]) => ({ name, description, harnesses });

const INSTALLED = [skill('tdd', 'Write the test first.', ['claude', 'codex']), skill('zeta', 'Last by name.', ['claude']), skill('alpha', 'First by name.', ['claude'])];

// The fetched lists, by the harness asked for; a harness with no entry answers 400, as a fleet with no such dock does.
function listings(byHarness: Record<string, typeof INSTALLED>) {
  const asked: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string) => {
      const harness = new URL(path, 'http://x').searchParams.get('harness') ?? '';
      asked.push(harness);
      const list = byHarness[harness];
      return list ? new Response(JSON.stringify({ skills: list })) : new Response(JSON.stringify({ error: 'no dock' }), { status: 400 });
    }),
  );
  return asked;
}

// The form around the picker: its skills are the form's own, and Save hands over what the form holds.
function Form({ start, harness, saved }: { start: string[]; harness: string; saved: (skills: string[]) => void }) {
  const [skills, setSkills] = useState(start);
  return (
    <>
      <SkillPicker fleetKey="zulu" harness={harness} skills={skills} onChange={setSkills} role="scribe" pickMeWhen="Pick for notes." members={[] as CrewMember[]} />
      <button type="button" onClick={() => saved(skills)}>
        Save
      </button>
    </>
  );
}

const names = () => within(screen.getByRole('list', { name: 'Skills' })).queryAllByRole('listitem').map((li) => li.querySelector('.skill-pill-name')!.textContent);
const open = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Add skill' }));
  return screen.findByRole('combobox', { name: 'Search skills' });
};

afterEach(() => vi.unstubAllGlobals());

test('the pills show the saved skills in their saved order', async () => {
  listings({ '': INSTALLED });
  render(<Form start={['zeta', 'tdd', 'alpha']} harness="" saved={() => undefined} />);
  await waitFor(() => expect(names()).toEqual(['zeta', 'tdd', 'alpha']));
});

test("a harness-locked form lists only that harness's skills, asked for by that harness", async () => {
  const asked = listings({ codex: [skill('tdd', 'Write the test first.', ['codex'])] });
  render(<Form start={[]} harness="codex" saved={() => undefined} />);
  await open();
  const list = await screen.findByRole('listbox', { name: 'Installed skills' });
  expect(within(list).getAllByRole('option').map((o) => o.querySelector('.skill-name')!.textContent)).toEqual(['tdd']);
  expect(asked).toEqual(['codex']);
});

test('a pill for a skill the locked harness lacks says so, and with no harness locked it says it was not found', async () => {
  listings({ codex: [skill('tdd', 'Write the test first.', ['codex'])], '': INSTALLED });
  const locked = render(<Form start={['tdd', 'gone']} harness="codex" saved={() => undefined} />);
  await waitFor(() => expect(locked.container.querySelector('.skill-pill-warn')).not.toBeNull());
  const pills = within(screen.getByRole('list', { name: 'Skills' })).getAllByRole('listitem');
  expect(pills.map((p) => p.querySelector('.skill-pill-warn')?.textContent ?? null)).toEqual([null, 'not installed on codex']);
  locked.unmount();

  render(<Form start={['tdd', 'gone']} harness="" saved={() => undefined} />);
  await waitFor(() => expect(document.querySelector('.skill-pill-warn')).not.toBeNull());
  const open2 = within(screen.getByRole('list', { name: 'Skills' })).getAllByRole('listitem');
  expect(open2.map((p) => p.querySelector('.skill-pill-warn')?.textContent ?? null)).toEqual([null, 'not found']);
});

test('× removes a pill, typing filters the list, a tap on a skill adds it last, and Save sends the list in the order shown', async () => {
  listings({ '': INSTALLED });
  const saved = vi.fn();
  render(<Form start={['tdd', 'zeta']} harness="" saved={saved} />);
  await waitFor(() => expect(names()).toEqual(['tdd', 'zeta']));

  fireEvent.click(screen.getByRole('button', { name: 'Remove tdd' }));
  expect(names()).toEqual(['zeta']);

  const search = await open();
  fireEvent.input(search, { target: { value: 'alp' } });
  const list = await screen.findByRole('listbox', { name: 'Installed skills' });
  expect(within(list).getAllByRole('option').map((o) => o.querySelector('.skill-name')!.textContent)).toEqual(['alpha']);

  fireEvent.click(within(list).getByRole('option', { name: /alpha/ }));
  expect(names()).toEqual(['zeta', 'alpha']);
  expect((search as HTMLInputElement).value).toBe('');

  fireEvent.input(search, { target: { value: 'tdd' } });
  fireEvent.keyDown(search, { key: 'Enter' });
  expect(names()).toEqual(['zeta', 'alpha', 'tdd']);

  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(saved).toHaveBeenCalledExactlyOnceWith(['zeta', 'alpha', 'tdd']);
});

test('a skill that is already added is refused with a message and not added twice', async () => {
  listings({ '': INSTALLED });
  render(<Form start={['tdd']} harness="" saved={() => undefined} />);
  await waitFor(() => expect(names()).toEqual(['tdd']));
  const search = await open();
  fireEvent.input(search, { target: { value: 'tdd' } });
  fireEvent.keyDown(search, { key: 'Enter' });
  expect(screen.getByText('tdd is already added.')).toBeTruthy();
  expect(names()).toEqual(['tdd']);
});
