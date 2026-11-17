import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Capability, CheckAnswer, Fleet, FleetCapabilities, HarnessCapabilities as Harness } from './api';
import { capabilities, capabilitiesFailed, capabilitiesLoaded, checkErrors, checking } from './capabilities';
import { HarnessCapabilities } from './HarnessCapabilities';

const cap = (id: string, label: string, status: Capability['status'] = 'not_checked', detail = ''): Capability => ({ id, label, status, checked_at: null, detail });
const guard = (status: Capability['status'], detail = '') => cap('autopilot.guard_hook', 'Guard hook', status, detail);
const harness = (name: string, version: string, g: Capability, judge = cap('judge.json', 'Judge schema')): Harness => ({
  harness: name,
  version,
  detected_at: null,
  features: [{ id: 'f', label: 'Features', capabilities: [g, judge, cap('usage', 'Usage reports')] }] as Harness['features'],
});

const idle = (): FleetCapabilities[] => [
  { key: 'zulu', name: 'Westwind', harnesses: [harness('claude', '', guard('not_checked')), harness('codex', '', guard('not_checked'))], judge: { harness: null, reason: 'no harness proved judge calls' } },
];

const fleet = { key: 'zulu', name: 'Westwind' } as Fleet;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

// The server's answer to a check; the test sets it before the tap.
let list: FleetCapabilities[];
let answer: () => Promise<Response>;
const posts: unknown[] = [];

beforeEach(() => {
  list = idle();
  posts.length = 0;
  capabilities.value = [];
  capabilitiesLoaded.value = false;
  capabilitiesFailed.value = false;
  checking.value = {};
  checkErrors.value = {};
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    if (url.endsWith('/harness-capabilities/check')) {
      posts.push(JSON.parse(String(init?.body)));
      return answer();
    }
    if (url.endsWith('/harness-capabilities')) return json({ fleets: list });
    return json({ fleets: [] });
  });
});
afterEach(() => vi.unstubAllGlobals());

const box = (name: string) => screen.getByRole('group', { name });
const row = (g: HTMLElement, label: string) =>
  [...g.querySelectorAll('li')].find((li) => li.querySelector('strong')?.textContent === label) as HTMLElement;
const checkAgain = (g: HTMLElement) => within(g).getByRole('button', { name: /^Check again (Claude|Codex)$/ }) as HTMLButtonElement;

async function shown() {
  render(<HarnessCapabilities fleet={fleet} />);
  await screen.findByRole('group', { name: 'Claude' });
}

test('lists the fleet\'s own harnesses with their logos, every capability not checked, no notes, and the judge line off with the server\'s reason', async () => {
  await shown();
  expect(screen.getAllByRole('group').map((g) => g.querySelector('h2')?.textContent)).toEqual(['Claude', 'Codex']);
  expect(box('Claude').querySelector('img[src="/api/v1/logos/claude"]')).not.toBeNull();
  for (const g of [box('Claude'), box('Codex')]) {
    for (const li of g.querySelectorAll('li')) expect(li.textContent).toContain('not checked');
    expect(within(g).queryByRole('note')).toBeNull();
  }
  expect(screen.getByText(/Judge calls: off/).textContent).toContain('no harness proved judge calls');
});

test('Check again posts the fleet and harness, shows Checking while the probe runs, then shows its answer: the failed row with its detail, one note, the version, the judge line', async () => {
  let release!: () => void;
  answer = () =>
    new Promise((resolve) => {
      release = () =>
        resolve(
          json({
            ...harness('claude', '2.1.300', guard('failed', 'hook did not run'), cap('judge.json', 'Judge schema', 'passed')),
            judge: { harness: 'claude', lookout_model: 'haiku', strong_model: 'sonnet' },
          } satisfies CheckAnswer),
        );
    });
  await shown();
  fireEvent.click(checkAgain(box('Claude')));
  await waitFor(() => expect(checkAgain(box('Claude')).getAttribute('aria-busy')).toBe('true'));
  expect(box('Claude').textContent).toContain('Checking…');
  expect(posts).toEqual([{ fleet: 'zulu', harness: 'claude' }]);
  expect(box('Codex').textContent).not.toContain('Checking…');

  release();
  await waitFor(() => expect(row(box('Claude'), 'Guard hook').textContent).toContain('failed'));
  expect(row(box('Claude'), 'Guard hook').textContent).toContain('hook did not run');
  expect(box('Claude').textContent).toContain('version 2.1.300');
  await waitFor(() => expect(box('Claude').textContent).not.toContain('Checking…'));
  expect(row(box('Claude'), 'Judge schema').textContent).toContain('passed');
  expect(row(box('Claude'), 'Usage reports').textContent).toContain('not checked');
  const notes = within(box('Claude')).getAllByRole('note');
  expect(notes).toHaveLength(1);
  expect(row(box('Claude'), 'Guard hook').contains(notes[0])).toBe(true);
  expect(notes[0].textContent).toContain('Guard hook failed on Claude.');
  expect(row(box('Codex'), 'Guard hook').textContent).toContain('not checked');
  expect(screen.getByText(/Judge calls:/).textContent).toBe('Judge calls: Claude (haiku / sonnet)');
});

test('the missing-capability line re-runs the same probe, and its note goes away once the capability passes', async () => {
  list[0].harnesses[0] = harness('claude', '2.1.300', guard('failed', 'hook did not run'));
  answer = async () => json(harness('claude', '2.1.300', guard('passed')));
  await shown();
  const note = within(box('Claude')).getByRole('note');
  fireEvent.click(within(note).getByRole('button', { name: 'Check again Guard hook on Claude' }));
  await waitFor(() => expect(row(box('Claude'), 'Guard hook').textContent).toContain('passed'));
  expect(posts).toEqual([{ fleet: 'zulu', harness: 'claude' }]);
  expect(within(box('Claude')).queryByRole('note')).toBeNull();
});

test('a judge that fails its probes reads off with the reason, and each failed row gets a note', async () => {
  answer = async () =>
    json({
      ...harness('claude', '2.1.300', guard('passed'), cap('judge.json', 'Judge schema', 'failed', 'reply leaked')),
      judge: { harness: null, reason: 'Claude judge probes failed' },
    });
  await shown();
  fireEvent.click(checkAgain(box('Claude')));
  await waitFor(() => expect(row(box('Claude'), 'Judge schema').textContent).toContain('failed'));
  expect(screen.getByText(/Judge calls: off/).textContent).toContain('Claude judge probes failed');
  expect(within(box('Claude')).getAllByRole('note')).toHaveLength(1);
});

test('a check that cannot run shows its error, re-enables the button, leaves the guard not checked, and a later success clears the error', async () => {
  // The server clears a pass when the version stops answering, so the page must read the list again after the failure.
  list[0].harnesses[0] = harness('claude', '2.1.300', guard('passed'));
  answer = async () => {
    list = idle();
    return json({ error: 'claude --version failed' }, 502);
  };
  await shown();
  expect(row(box('Claude'), 'Guard hook').textContent).toContain('passed');
  fireEvent.click(checkAgain(box('Claude')));
  expect((await screen.findByRole('alert')).textContent).toBe('claude --version failed');
  await waitFor(() => expect(checkAgain(box('Claude')).getAttribute('aria-busy')).toBeNull());
  expect(checkAgain(box('Claude')).disabled).toBe(false);
  expect(box('Claude').textContent).not.toContain('Checking…');
  expect(row(box('Claude'), 'Guard hook').textContent).toContain('not checked');
  expect(row(box('Claude'), 'Guard hook').textContent).not.toContain('passed');

  answer = async () => json(harness('claude', '2.1.300', guard('passed')));
  fireEvent.click(checkAgain(box('Claude')));
  await waitFor(() => expect(row(box('Claude'), 'Guard hook').textContent).toContain('passed'));
  expect(screen.queryByRole('alert')).toBeNull();
});
