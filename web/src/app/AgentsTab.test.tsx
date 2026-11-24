import { render, screen, within } from '@testing-library/preact';
import { expect, test } from 'vitest';
import type { Ship } from './api';
import { AgentsTab } from './AgentsTab';

const T = (hhmm: string) => `2026-10-24T${hhmm}:00Z`;
const boat = (b: Record<string, unknown> & { name: string }) => ({ conversation: false, harness: 'claude', status: 'working', exit_code: null, exit_signal: null, launched: T('09:00'), ...b });
const shipOf = (crew: unknown[] = [], agents: unknown[] = []) => ({ session: 'calm-otter', name: 'Calm Otter', crew, agents }) as unknown as Ship;

const rows = () => screen.getAllByRole('listitem');

test('an empty ship says there is nothing yet', () => {
  render(<AgentsTab ship={shipOf()} />);
  expect(screen.getByText('No crew, subagents or background tasks yet')).toBeTruthy();
  expect(screen.queryAllByRole('listitem')).toEqual([]);
});

test('a ship whose server sends no agents lists its crew and does not fail', () => {
  render(<AgentsTab ship={{ session: 's', name: 'S', crew: [boat({ name: 'mate' })] } as unknown as Ship} />);
  expect(rows()).toHaveLength(1);
  expect(within(rows()[0]).getByText('mate')).toBeTruthy();
});

test('each row names its kind, its name and whether it runs or has finished', () => {
  render(
    <AgentsTab
      ship={shipOf(
        [boat({ name: 'mate' }), boat({ name: 'gone', status: 'anchored', exit_code: 0 })],
        [
          { id: 'a1', type: 'Explore', start: T('10:00'), kind: 'subagent' },
          { id: 'a2', type: 'Plan', start: T('10:00'), kind: 'teammate', end: T('10:20') },
          { id: 'sh1', type: '', start: T('10:00'), kind: 'shell' },
          { id: 'mon1', type: '', start: T('10:00'), kind: 'monitor', end: T('10:40') },
        ],
      )}
    />,
  );
  const text = rows().map((r) => r.textContent ?? '');
  expect(text).toHaveLength(6);
  const row = (name: string) => text.find((t) => t.includes(name)) ?? '';
  expect(row('mate')).toMatch(/Crew boat/);
  expect(row('mate')).toMatch(/running/i);
  expect(row('gone')).toMatch(/finished/i);
  expect(row('a1')).toMatch(/Subagent/);
  expect(row('a1')).toMatch(/Explore/);
  expect(row('a2')).toMatch(/Agent-team teammate/);
  expect(row('a2')).toMatch(/finished/i);
  expect(row('sh1')).toMatch(/Background shell/);
  expect(row('sh1')).toMatch(/running/i);
  expect(row('mon1')).toMatch(/Background monitor/);
  expect(row('mon1')).toMatch(/finished/i);
});

test('running rows come before finished ones, finished newest first', () => {
  render(
    <AgentsTab
      ship={shipOf(
        [],
        [
          { id: 'fin-old', type: '', start: T('09:00'), kind: 'subagent', end: T('10:00') },
          { id: 'run-1', type: '', start: T('09:30'), kind: 'subagent' },
          { id: 'fin-new', type: '', start: T('09:00'), kind: 'subagent', end: T('11:00') },
        ],
      )}
    />,
  );
  const order = rows().map((r) => ['run-1', 'fin-new', 'fin-old'].find((id) => r.textContent?.includes(id)));
  expect(order).toEqual(['run-1', 'fin-new', 'fin-old']);
});

test('start and end are shown as times a reader or a screen reader can take', () => {
  const { container } = render(<AgentsTab ship={shipOf([], [{ id: 'a1', type: '', start: T('10:00'), kind: 'subagent', end: T('10:20') }, { id: 'a2', type: '', start: T('10:05'), kind: 'subagent' }])} />);
  const stamps = [...container.querySelectorAll('time')].map((t) => t.getAttribute('datetime'));
  expect(stamps).toEqual([T('10:00'), T('10:20'), T('10:05')]);
});

test('a running entry shows no end time', () => {
  const { container } = render(<AgentsTab ship={shipOf([], [{ id: 'a2', type: '', start: T('10:05'), kind: 'subagent' }])} />);
  expect(container.querySelectorAll('time')).toHaveLength(1);
});

test('text from the server is shown as text, never as markup', () => {
  const { container } = render(<AgentsTab ship={shipOf([], [{ id: 'a1', type: '<img src=x onerror=alert(1)>', start: T('10:00'), kind: 'subagent' }])} />);
  expect(container.querySelector('img')).toBeNull();
  expect(container.textContent).toContain('<img src=x onerror=alert(1)>');
});

test('a background shell shows nothing beyond its kind, id, times and state', () => {
  render(<AgentsTab ship={shipOf([], [{ id: 'bdevsrv1', type: '', start: T('10:00'), kind: 'shell', command: 'sleep 99', description: 'dev server' }])} />);
  expect(rows()[0].textContent).not.toMatch(/sleep 99|dev server/);
});
