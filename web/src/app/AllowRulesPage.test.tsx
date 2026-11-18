import { fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { AllowRule, AllowRules, Fleet } from './api';
import { AllowRulesPage } from './AllowRulesPage';

const fleet = { key: 'work', name: 'Work' } as Fleet;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

const claude: AllowRule = { harness: 'claude', repo: 'acme/widgets', rule: 'Bash(gh pr view:*)', added: '2026-11-02T15:00:00Z' };
const codex: AllowRule = { harness: 'codex', pattern: ['make', 'check'], added: '2026-11-03T15:00:00Z' };
const skipped: AllowRule = { harness: 'copilot', repo: 'acme/widgets', rule: 'shell(npm test)', added: '2026-11-04T15:00:00Z' };

let stored: AllowRules;
let refuse: string | null;
const posts: { path: string; body: unknown }[] = [];
const same = (a: AllowRule, b: AllowRule) => JSON.stringify([a.harness, a.repo, a.rule, a.pattern]) === JSON.stringify([b.harness, b.repo, b.rule, b.pattern]);

beforeEach(() => {
  stored = { rules: [], skipped: [] };
  refuse = null;
  posts.length = 0;
  vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
    if (!url.includes('/fleets/work/allow-rules')) return json({ error: 'not found' }, 404);
    if (init?.method === 'POST') {
      const body = JSON.parse(String(init.body)) as { rule: AllowRule };
      posts.push({ path: url.slice(url.indexOf('/fleets/')), body });
      if (refuse) return json({ error: refuse }, 500);
      if (url.endsWith('/skipped/remove')) stored = { ...stored, skipped: stored.skipped.filter((r) => !same(r, body.rule)) };
      else stored = { ...stored, rules: stored.rules.filter((r) => !same(r, body.rule)) };
    }
    return json(stored);
  });
});
afterEach(() => vi.unstubAllGlobals());

const list = (name: string) => screen.getByRole('list', { name });
const rows = (name: string) => within(list(name)).getAllByRole('listitem');

test('a fleet with no rules and no skips says so in both sections and offers nothing to remove', async () => {
  render(<AllowRulesPage fleet={fleet} />);
  await screen.findByText('No allow rules yet.');
  expect(screen.getByText('No skipped proposals.')).toBeTruthy();
  expect(screen.queryByRole('button', { name: /^Remove/ })).toBeNull();
});

test('each row shows its harness, its repo or every repo for Codex, the rule, and when it was added', async () => {
  stored = { rules: [claude, codex], skipped: [skipped] };
  render(<AllowRulesPage fleet={fleet} />);
  await screen.findByRole('list', { name: 'Allow rules' });

  const [c, x] = rows('Allow rules');
  expect(within(c).getByRole('img', { name: 'Claude' })).toBeTruthy();
  expect(c.querySelector('code')?.textContent).toBe('Bash(gh pr view:*)');
  expect(c.textContent).toContain('acme/widgets');
  expect(c.textContent).toMatch(/Added .*2026/);
  expect(x.querySelector('code')?.textContent).toBe('make check');
  expect(x.textContent).toContain('every repo');

  const [s] = rows('Skipped proposals');
  expect(within(s).getByRole('img', { name: 'Copilot' })).toBeTruthy();
  expect(s.querySelector('code')?.textContent).toBe('shell(npm test)');
  expect(s.textContent).toContain('acme/widgets');
});

test('Remove on a rule sends that rule and drops only its row; Remove on a skip sends the skip to its own route', async () => {
  stored = { rules: [claude, codex], skipped: [skipped] };
  render(<AllowRulesPage fleet={fleet} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Remove rule make check' }));
  await waitFor(() => expect(rows('Allow rules')).toHaveLength(1));
  expect(posts).toEqual([{ path: '/fleets/work/allow-rules/remove', body: { rule: codex } }]);
  expect(rows('Allow rules')[0].querySelector('code')?.textContent).toBe('Bash(gh pr view:*)');
  expect(rows('Skipped proposals')).toHaveLength(1);

  fireEvent.click(screen.getByRole('button', { name: 'Remove skip shell(npm test)' }));
  await screen.findByText('No skipped proposals.');
  expect(posts[1]).toEqual({ path: '/fleets/work/allow-rules/skipped/remove', body: { rule: skipped } });
  expect(rows('Allow rules')).toHaveLength(1);
});

test('a Remove the server refuses shows its error and keeps the row', async () => {
  stored = { rules: [claude], skipped: [] };
  refuse = 'allow-rules.json is corrupt';
  render(<AllowRulesPage fleet={fleet} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Remove rule Bash(gh pr view:*)' }));
  expect((await screen.findByRole('alert')).textContent).toBe('allow-rules.json is corrupt');
  expect(rows('Allow rules')).toHaveLength(1);
  await waitFor(() => expect((screen.getByRole('button', { name: 'Remove rule Bash(gh pr view:*)' }) as HTMLButtonElement).disabled).toBe(false));
});
