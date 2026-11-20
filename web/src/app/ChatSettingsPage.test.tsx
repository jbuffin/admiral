import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { ChatCommitment, ChatNote, Fleet } from './api';
import { ChatSettingsPage } from './ChatSettingsPage';
import { fleets } from './fleets';

const claude = { harness: 'claude', offered: true, reason: '', capability: '', models: ['sonnet', 'opus', 'haiku'], efforts: ['', 'low', 'medium', 'high'] };
const codex = { harness: 'codex', offered: false, reason: 'Codex has not been checked', capability: 'chat.codex', models: ['gpt-5'], efforts: ['', 'low'] };
const chat = (over: Record<string, unknown> = {}) => ({ harness: 'claude', model: 'sonnet', effort: '', context_threshold: 150000, on: true, off_reason: '', harnesses: ['claude'], harness_options: [claude, codex], models: claude.models, efforts: claude.efforts, ...over });
const harbor = { key: 'harbor', name: 'Harborlight', chat: chat(), docks: [] } as unknown as Fleet;

let stored: Fleet;
let instructions: string;
let notes: ChatNote[];
let commitments: ChatCommitment[];
let puts: Record<string, unknown>[];
let calls: string[];
let refuse: string | null;
let closedAlready: boolean;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const note = (id: string, text: string): ChatNote => ({ id, kind: 'note', text, ts: '2026-11-01T09:00:00Z', via: 'admiralbot' });
const commitment = (over: Partial<ChatCommitment>): ChatCommitment => ({ id: 'c', text: 't', trigger: { kind: 'none' }, action: 'remind', ts: '2026-10-01T09:00:00Z', via: 'admiralbot', ...over }) as ChatCommitment;

beforeEach(() => {
  stored = { ...harbor, chat: chat() } as Fleet;
  instructions = '';
  notes = [note('n1', 'Deploys go out on Tuesdays.'), note('n2', 'The staging box is flaky.')];
  commitments = [
    commitment({ id: 'c1', text: 'Ask me about the dock review.', shown: true } as Partial<ChatCommitment>),
    commitment({ id: 'c2', text: 'Ring the bell at midsummer.', trigger: { kind: 'time', subject: '2099-06-15T12:00:00Z' }, ts: '2026-11-05T09:00:00Z' }),
    commitment({ id: 'w1', text: 'Launch the quartz ferry.', trigger: { kind: 'ship_sailed', subject: 'gone-session' }, action: 'launch', waiting: true, ts: '2026-11-07T09:00:00Z' } as Partial<ChatCommitment>),
  ];
  puts = [];
  calls = [];
  refuse = null;
  closedAlready = false;
  fleets.value = [stored];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      const body = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : undefined;
      if (path.endsWith('/fleets/harbor') && method === 'PUT') {
        puts.push(body!);
        if (refuse) return json({ error: refuse }, 400);
        stored = { ...stored, chat: { ...stored.chat, ...(body!.chat as object) } } as Fleet;
        return json({ fleets: [stored] });
      }
      if (path.endsWith('/instructions')) {
        if (method === 'PUT') {
          instructions = String(body!.text);
          calls.push(`PUT instructions ${JSON.stringify(body)}`);
        }
        return json({ text: instructions });
      }
      if (path.endsWith('/notes')) return json({ notes });
      const del = /\/(notes|commitments)\/([^/]+?)(\/promote)?$/.exec(path);
      if (del && method !== 'GET') {
        calls.push(`${method} ${del[1]}/${del[2]}${del[3] ?? ''}`);
        if (del[1] === 'commitments' && closedAlready) {
          commitments = commitments.filter((c) => c.id !== del[2]);
          return json({ error: 'gone' }, 404);
        }
        if (del[1] === 'notes') {
          const n = notes.find((x) => x.id === del[2]);
          notes = notes.filter((x) => x.id !== del[2]);
          if (del[3]) instructions = n!.text;
        } else commitments = commitments.filter((c) => c.id !== del[2]);
        return del[3] ? json({ text: instructions }) : new Response(null, { status: 204 });
      }
      if (path.endsWith('/commitments')) return json({ commitments });
      return json({});
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const pick = (group: string, name: string) => within(screen.getByRole('radiogroup', { name: group })).getByRole('radio', { name });
const on = (r: HTMLElement) => r.getAttribute('aria-checked') === 'true' || (r as HTMLInputElement).checked === true;
const greyed = (r: HTMLElement) => r.getAttribute('aria-disabled') === 'true' || r.hasAttribute('disabled');
const modelSelect = () => screen.getByLabelText('Model') as HTMLSelectElement;
const modelLabels = () => within(modelSelect()).getAllByRole('option').map((o) => o.textContent?.trim() ?? '');
const chosenModel = () => modelSelect().selectedOptions[0]!.textContent?.trim();
const labels = (group: string) => within(screen.getByRole('radiogroup', { name: group })).getAllByRole('radio').map((r) => r.textContent?.trim() ?? '');
const save = () => screen.getByRole('button', { name: 'Save' });
const settled = (b: HTMLElement) => waitFor(() => expect(b.getAttribute('aria-busy')).not.toBe('true'));
const row = (text: string) => screen.getAllByRole('listitem').find((li) => li.textContent?.includes(text))!;

test('shows the harness, model, effort and refresh threshold with what is chosen, and draws nothing this page does not build', async () => {
  render(<ChatSettingsPage fleet={stored} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });
  expect(document.body.textContent).toContain("Your chief of staff for the Harborlight fleet. It only sees this fleet's ships, orders, campaigns and logins.");
  expect(modelSelect().tagName).toBe('SELECT');
  expect(chosenModel()).toBe('Sonnet');
  expect(modelLabels()).toEqual(['Sonnet', 'Opus', 'Haiku']);
  expect(labels('Effort')).toEqual(['Default', 'Low', 'Medium', 'High']);
  expect(on(pick('Effort', 'Default'))).toBe(true);
  expect(labels('Refresh context at')).toEqual(['100K', '150K', '300K', 'Never']);
  expect(on(pick('Refresh context at', '150K'))).toBe(true);
  for (const absent of ['Worker model', 'Also refresh after an idle hour', 'Acting for you', 'Guardrails', 'ledger']) expect(document.body.textContent).not.toContain(absent);
});

test('a change sends only the chat object, Default effort drops the key, and a refusal shows the error and keeps the pick', async () => {
  const view = render(<ChatSettingsPage fleet={stored} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });
  fireEvent.change(modelSelect(), { target: { value: 'opus' } });
  fireEvent.click(pick('Effort', 'High'));
  fireEvent.click(pick('Refresh context at', '300K'));
  fireEvent.click(save());
  await waitFor(() => expect(puts).toEqual([{ chat: { model: 'opus', effort: 'high', context_threshold: 300000 } }]));
  view.rerender(<ChatSettingsPage fleet={stored} />);
  await settled(save());

  fireEvent.click(pick('Effort', 'Default'));
  fireEvent.click(pick('Refresh context at', 'Never'));
  fireEvent.click(save());
  await waitFor(() => expect(puts[1]).toEqual({ chat: { effort: '', context_threshold: 'never' } }));
  await settled(save());

  refuse = 'fleets.harbor.chat.model: "haiku" is refused for this test';
  fireEvent.change(modelSelect(), { target: { value: 'haiku' } });
  fireEvent.click(save());
  expect((await screen.findAllByRole('alert')).some((a) => a.textContent?.includes('"haiku" is refused for this test'))).toBe(true);
  expect(chosenModel()).toBe('Haiku');
});

const withModels = (models: string[], over: Record<string, unknown> = {}) => {
  const listed = { ...claude, models };
  return { ...harbor, chat: chat({ models, harness_options: [listed, codex], ...over }) } as unknown as Fleet;
};

test('the Model select offers the dock list with Fable, no Default and no Other…, and a saved Fable is still selected after a reload', async () => {
  stored = withModels(['sonnet', 'opus', 'haiku', 'fable']);
  fleets.value = [stored];
  const view = render(<ChatSettingsPage fleet={stored} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });
  expect(modelLabels()).toEqual(['Sonnet', 'Opus', 'Haiku', 'Fable']);

  fireEvent.change(modelSelect(), { target: { value: 'fable' } });
  fireEvent.click(save());
  await waitFor(() => expect(puts).toEqual([{ chat: { model: 'fable' } }]));
  view.unmount();

  render(<ChatSettingsPage fleet={stored} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });
  expect(chosenModel()).toBe('Fable');
});

test('a saved model the list no longer offers stays selected, marked as no longer offered', async () => {
  const gone = withModels(['sonnet', 'opus'], { model: 'retired-model' });
  render(<ChatSettingsPage fleet={gone} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });

  expect(modelSelect().value).toBe('retired-model');
  expect(chosenModel()).toMatch(/retired-model/);
  expect(chosenModel()).toMatch(/no longer offered/i);
  expect(modelLabels().filter((l) => /no longer offered/i.test(l))).toHaveLength(1);
  expect(modelLabels()).toHaveLength(3);
});

test('a new list for the open page updates the options and keeps the model chosen', async () => {
  stored = withModels(['sonnet', 'opus']);
  const view = render(<ChatSettingsPage fleet={stored} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });
  fireEvent.change(modelSelect(), { target: { value: 'opus' } });

  view.rerender(<ChatSettingsPage fleet={withModels(['sonnet', 'opus', 'fable'])} />);

  expect(modelLabels()).toEqual(['Sonnet', 'Opus', 'Fable']);
  expect(chosenModel()).toBe('Opus');
});

test('a harness not yet proven is listed greyed out with why and a way to check it again', async () => {
  render(<ChatSettingsPage fleet={stored} />);
  await screen.findByRole('heading', { name: 'What AdmiralBot knows' });
  expect(greyed(pick('Harness', 'Codex'))).toBe(true);
  expect(document.body.textContent).toMatch(/Codex.*has not been checked/i);
  expect(screen.getByRole('button', { name: /Check again.*Codex/ })).toBeTruthy();
});

test('a fleet with no Claude Code dock lists the harnesses it has, greys the unproven one and says why its chat is off', () => {
  const off = { ...harbor, chat: chat({ on: false, off_reason: 'No Claude Code dock in this fleet.', harness: 'codex', harness_options: [{ ...codex, offered: true, reason: '' }, { ...claude, harness: 'copilot', offered: false, reason: 'Copilot is not proven', models: ['m'], efforts: [''] }] }) } as unknown as Fleet;
  render(<ChatSettingsPage fleet={off} />);
  expect(screen.getAllByText(/No Claude Code dock/i).length).toBeGreaterThan(0);
  expect(labels('Harness')).toEqual(['Codex', 'Copilot']);
  expect(greyed(pick('Harness', 'Copilot'))).toBe(true);
});

test('instructions count live, are capped, save by themselves, and an unsaved draft survives Delete on a note and holds Move to instructions', async () => {
  render(<ChatSettingsPage fleet={stored} />);
  const box = (await screen.findByLabelText('Instructions')) as HTMLTextAreaElement;
  await screen.findByText('Deploys go out on Tuesdays.');
  const saveIns = () => screen.getByRole('button', { name: 'Save instructions' });
  expect(box.value).toBe('');
  expect(document.body.textContent).toContain('0 / 2000');
  expect(saveIns().hasAttribute('disabled')).toBe(true);

  fireEvent.click(within(row('Deploys go out')).getByRole('button', { name: 'Move to instructions' }));
  await waitFor(() => expect(box.value).toBe('Deploys go out on Tuesdays.'));
  expect(screen.queryByText('Deploys go out on Tuesdays.', { selector: '.set-text' })).toBeNull();

  fireEvent.input(box, { target: { value: '😀'.repeat(2001) } });
  expect(document.body.textContent).toContain('2001 / 2000');
  expect(saveIns().hasAttribute('disabled')).toBe(true);

  fireEvent.input(box, { target: { value: 'Deploys go out on Tuesdays.\n\nBe terse.' } });
  fireEvent.click(saveIns());
  await waitFor(() => expect(calls).toContain('PUT instructions {"text":"Deploys go out on Tuesdays.\\n\\nBe terse."}'));
  await waitFor(() => expect(saveIns().hasAttribute('disabled')).toBe(true));

  fireEvent.input(box, { target: { value: 'Deploys go out on Tuesdays.\n\nBe terse.\n\nA draft.' } });
  expect(within(row('staging box')).getByRole('button', { name: 'Move to instructions' }).hasAttribute('disabled')).toBe(true);
  fireEvent.click(within(row('staging box')).getByRole('button', { name: 'Delete' }));
  await waitFor(() => expect(notes).toEqual([]));
  await waitFor(() => expect(screen.queryByText('The staging box is flaky.')).toBeNull());
  expect(box.value).toBe('Deploys go out on Tuesdays.\n\nBe terse.\n\nA draft.');
});

test('commitments list the open ones with their marks and never show a launch prompt; Cancel removes one, and one that closed already says so and leaves', async () => {
  commitments[2] = { ...commitments[2]!, launch: { prompt: 'SECRET LAUNCH PROMPT' } } as ChatCommitment;
  render(<ChatSettingsPage fleet={stored} />);
  const list = await screen.findByRole('list', { name: 'Commitments' });
  const rows = within(list).getAllByRole('listitem');
  expect(rows.map((r) => r.querySelector('.set-text')?.textContent)).toEqual(['Ask me about the dock review.', 'Ring the bell at midsummer.', 'Launch the quartz ferry.']);
  expect(rows[0]!.textContent).toMatch(/no trigger/i);
  expect(rows[0]!.textContent).toContain('Remind you');
  expect(rows[0]!.textContent).toContain('Still want this?');
  expect(rows[1]!.textContent).toContain('2099');
  expect(rows[1]!.textContent).not.toContain('Still want this?');
  expect(rows[2]!.textContent).toContain('gone-session');
  expect(rows[2]!.textContent).toMatch(/waiting/i);
  expect(rows[2]!.textContent).toContain('Launch a ship');
  expect(document.body.textContent).not.toContain('SECRET LAUNCH PROMPT');
  expect(document.body.textContent).not.toMatch(/ledger|promise/i);

  fireEvent.click(within(row('midsummer')).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByText('Ring the bell at midsummer.')).toBeNull());
  expect(calls).toContain('DELETE commitments/c2');

  closedAlready = true;
  await waitFor(() => expect(within(row('dock review')).getByRole('button', { name: 'Cancel' }).getAttribute('aria-busy')).not.toBe('true'));
  fireEvent.click(within(row('dock review')).getByRole('button', { name: 'Cancel' }));
  expect((await screen.findAllByRole('alert')).map((a) => a.textContent)).toContain('That commitment already closed.');
  await waitFor(() => expect(screen.queryByText('Ask me about the dock review.')).toBeNull());
});
