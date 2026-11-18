import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/preact';
import { afterEach, expect, test, vi } from 'vitest';
import type { Entry } from './inbox';
import { lockedBy } from './itemActions';
import { AllowRulesBody } from './PeekAllowRules';
import { Failure, useActing } from './PeekItem';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const entry = (id: string, cards: unknown[]) =>
  ({
    key: `item:_admiral/${id}`,
    type: 'item',
    session: '_admiral',
    id,
    folded: null,
    since: 0,
    fleet: { key: 'zulu', name: 'Zulu Fleet', color: '#123456' },
    item: { id, kind: 'allow_rules', allow_rules: { fleet: 'zulu', cards } },
  }) as unknown as Entry;

const claude = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  harness: 'claude',
  repo: 'acme/api',
  rule: 'Bash(gh pr view:*)',
  count: 1,
  prompts: [{ ship: 'Dory', id: 'p1' }],
  ...over,
});
const codex = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  harness: 'codex',
  pattern: ['git', 'status'],
  every_repo: true,
  count: 1,
  prompts: [{ ship: 'Pip', id: 'p2' }],
  ...over,
});

interface Sent {
  path: string;
  body: unknown;
}

function serve(reply: () => Response | Promise<Response>) {
  const sent: Sent[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      sent.push({ path, body: init?.body ? JSON.parse(init.body as string) : undefined });
      return reply();
    }),
  );
  return sent;
}

function Proposal({ e, fold }: { e: Entry; fold: (t: string) => void }) {
  const acting = useActing(e, fold, lockedBy);
  return (
    <>
      <AllowRulesBody entry={e} acting={acting} fleet="Zulu Fleet" />
      <Failure message={acting.error} />
    </>
  );
}

const card = (n: number) => screen.getByRole('region', { name: `Allow rule card ${n}` });
const press = (name: string, scope: HTMLElement | Document = document) => fireEvent.click(within(scope as HTMLElement).getByRole('button', { name }));
const send = () => screen.getByRole('button', { name: 'Send' }) as HTMLButtonElement;

test('a Claude card shows the exact rule, the harness flag, the repo, the ships and the reason as plain text', () => {
  const reason = 'Reads only **bold** <b>x</b>\nsecond line';
  render(<Proposal e={entry('ar-1', [claude('c1', { count: 3, prompts: [{ ship: 'Dory', id: 'p1' }, { ship: 'Pip', id: 'p3' }], reason })])} fold={vi.fn()} />);
  const c = card(1);
  expect(within(c).getByText('Bash(gh pr view:*)')).toBeTruthy();
  expect(within(c).getByRole('img', { name: 'Claude' })).toBeTruthy();
  expect(c.textContent).toContain('acme/api');
  expect(c.textContent).toContain('Dory');
  expect(c.textContent).toContain('Pip');
  expect(c.textContent).not.toContain('every repo');
  expect(c.textContent).toContain('**bold**');
  expect(c.textContent).toContain('<b>x</b>');
  expect(c.textContent).toContain('second line');
  expect(c.querySelector('b, strong')).toBeNull();
});

test('a Codex card says it applies to every repo in the fleet, has no repo, and shows its pattern as words', () => {
  render(<Proposal e={entry('ar-2', [codex('x1')])} fold={vi.fn()} />);
  const c = card(1);
  expect(within(c).getByText('git status')).toBeTruthy();
  expect(within(c).getByRole('img', { name: 'Codex' })).toBeTruthy();
  expect(c.textContent).toContain('applies to every repo in Zulu Fleet');
});

test('the count shows only when a rule came from more than one prompt', () => {
  render(<Proposal e={entry('ar-3', [claude('c1', { count: 3 }), claude('c2', { rule: 'Bash(ls:*)', count: 1 })])} fold={vi.fn()} />);
  expect(card(1).textContent).toMatch(/\b3\b/);
  expect(card(2).textContent).not.toMatch(/\d/);
});

test('Send stays off until every card is Add or Skip, then posts the decisions once and folds with the counts', async () => {
  const sent = serve(() => new Response('{"answered":"ar-4"}'));
  const fold = vi.fn();
  render(<Proposal e={entry('ar-4', [claude('c1'), codex('c2'), claude('c3', { rule: 'Bash(ls:*)' })])} fold={fold} />);
  expect(screen.getByText('0 of 3 decided')).toBeTruthy();
  expect(send().disabled).toBe(true);
  press('Add', card(1));
  press('Skip', card(2));
  expect(screen.getByText('2 of 3 decided')).toBeTruthy();
  expect(send().disabled).toBe(true);
  press('Add', card(3));
  expect(send().disabled).toBe(false);
  expect(within(card(1)).getByRole('button', { name: 'Add' }).getAttribute('aria-pressed')).toBe('true');
  expect(within(card(1)).getByRole('button', { name: 'Skip' }).getAttribute('aria-pressed')).toBe('false');

  fireEvent.click(send());
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Allow rules sent: 2 added, 1 skipped'));
  expect(sent).toEqual([
    { path: '/api/v1/inbox/_admiral/ar-4/allow-rules', body: { cards: { c1: { decision: 'add' }, c2: { decision: 'skip' }, c3: { decision: 'add' } } } },
  ]);
});

test('a decision can be changed before Send', async () => {
  const sent = serve(() => new Response('{}'));
  render(<Proposal e={entry('ar-5', [claude('c1')])} fold={vi.fn()} />);
  press('Add', card(1));
  press('Skip', card(1));
  fireEvent.click(send());
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0].body).toEqual({ cards: { c1: { decision: 'skip' } } });
});

test.each([
  ['a 400', () => new Response(JSON.stringify({ error: 'card c1 has no decision' }), { status: 400 }), 'card c1 has no decision'],
  ['a network failure', () => Promise.reject(new TypeError('network down')), ''],
])('%s shows the error, keeps every decision and lets the person try again', async (_n, reply, text) => {
  serve(reply as () => Response);
  const fold = vi.fn();
  render(<Proposal e={entry('ar-6', [claude('c1'), codex('c2')])} fold={fold} />);
  press('Add', card(1));
  press('Skip', card(2));
  fireEvent.click(send());
  const alert = await screen.findByRole('alert');
  expect(alert.textContent).toContain(text);
  expect(fold).not.toHaveBeenCalled();
  expect(within(card(1)).getByRole('button', { name: 'Add' }).getAttribute('aria-pressed')).toBe('true');
  expect(within(card(2)).getByRole('button', { name: 'Skip' }).getAttribute('aria-pressed')).toBe('true');
  expect(screen.getByText('2 of 2 decided')).toBeTruthy();
  expect(send().disabled).toBe(false);
  expect((within(card(1)).getByRole('button', { name: 'Add' }) as HTMLButtonElement).disabled).toBe(false);
});

test('a 409 shows the error and locks Add, Skip and Send', async () => {
  serve(() => new Response(JSON.stringify({ error: 'this item was already answered' }), { status: 409 }));
  render(<Proposal e={entry('ar-7', [claude('c1')])} fold={vi.fn()} />);
  press('Add', card(1));
  fireEvent.click(send());
  expect((await screen.findByRole('alert')).textContent).toContain('already answered');
  expect(send().disabled).toBe(true);
  for (const name of ['Add', 'Skip']) expect((within(card(1)).getByRole('button', { name }) as HTMLButtonElement).disabled).toBe(true);
});

test('a bidi override in a rule and a zero-width character in a command show as visible tokens', () => {
  const rule = 'Bash(ls\u202e:*)';
  const reason = 'safe\u200b read';
  const e = entry('ar-8', [claude('c1', { rule, reason, prompts: [{ ship: 'Dory', id: 'p1', command: 'ls\u200b -la' }] })]);
  render(<Proposal e={e} fold={vi.fn()} />);
  const c = card(1);
  expect(c.textContent).toContain('Bash(ls⟨U+202E⟩:*)');
  expect(c.textContent).toContain('ls⟨U+200B⟩ -la');
  expect(c.textContent).toContain('safe⟨U+200B⟩ read');
  expect(c.textContent).not.toMatch(/[\u202e\u200b]/);
});

test('a refused card set reloads the item, keeps the decisions of cards still there and asks for the new one', async () => {
  const gets: string[] = [];
  const posts: unknown[] = [];
  let first = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      if (init?.method === 'POST') {
        posts.push(JSON.parse(init.body as string));
        if (first) {
          first = false;
          return new Response(JSON.stringify({ error: 'card c2 has no decision' }), { status: 400 });
        }
        return new Response('{"answered":"ar-9"}');
      }
      gets.push(path);
      return new Response(JSON.stringify({ items: [entry('ar-9', [claude('c1'), claude('c2', { rule: 'Bash(ls:*)' })]).item] }));
    }),
  );
  const fold = vi.fn();
  render(<Proposal e={entry('ar-9', [claude('c1')])} fold={fold} />);
  press('Add', card(1));
  fireEvent.click(send());
  await screen.findByRole('status');
  expect(gets).toEqual(['/api/v1/inbox']);
  expect(screen.getByText('1 of 2 decided')).toBeTruthy();
  expect(within(card(1)).getByRole('button', { name: 'Add' }).getAttribute('aria-pressed')).toBe('true');
  expect(send().disabled).toBe(true);
  press('Skip', card(2));
  fireEvent.click(send());
  await waitFor(() => expect(fold).toHaveBeenCalledWith('Allow rules sent: 1 added, 1 skipped'));
  expect(posts[1]).toEqual({ cards: { c1: { decision: 'add' }, c2: { decision: 'skip' } } });
});
