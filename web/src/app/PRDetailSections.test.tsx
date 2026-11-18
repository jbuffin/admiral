import { fireEvent, render, screen } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { PRActor, PRDetail } from './api';
import { PRSections } from './PRDetailSections';

beforeEach(() => vi.stubGlobal('fetch', vi.fn(async () => new Response('{}'))));
afterEach(() => vi.unstubAllGlobals());

const who = (login: string, bot = false): PRActor => ({ login, bot });
const MD = 'Some **bold** and `inline` text\n\n```ts\nconst a = 1;\n```\n\n- one\n- two';
const at = '2026-11-01T10:00:00Z';
const comment = (author: PRActor, body: string, n = 1) => ({ author, body, url: `https://github.com/acme/api/pull/7#c${n}`, created_at: at });
const detail = (over: Partial<PRDetail> = {}) =>
  ({ number: 7, url: 'https://github.com/acme/api/pull/7', body: '', author: who('octocat'), reviews: [], requested: [], failing_checks: [], checks: 'SUCCESS', threads: [], comments: [], ...over }) as unknown as PRDetail;
const timeline = (c: Element) => c.querySelector('.timeline') as HTMLElement;
const showsFormatted = (el: HTMLElement) => {
  expect(el.querySelector('strong')?.textContent).toBe('bold');
  expect(el.querySelector('code')?.textContent).toBe('inline');
  expect(el.querySelector('pre code')?.textContent).toContain('const a = 1;');
  expect([...el.querySelectorAll('ul li')].map((li) => li.textContent)).toEqual(['one', 'two']);
  expect(el.textContent).not.toContain('**');
  expect(el.textContent).not.toContain('```');
};

test('a PR comment renders its markdown as formatted elements', () => {
  const { container } = render(<PRSections detail={detail({ comments: [comment(who('hubot'), MD)] })} />);
  showsFormatted(timeline(container));
});

test("a thread's own body and its reply render their markdown", () => {
  const d = detail({ threads: [{ path: 'a.go', line: 3, resolved: false, comments: [comment(who('hubot'), MD, 1), comment(who('monalisa'), MD, 2)] }] });
  const { container } = render(<PRSections detail={d} />);
  const [body, reply] = [...timeline(container).querySelectorAll('.md')] as HTMLElement[];
  showsFormatted(body);
  showsFormatted(reply);
});

test('the description still renders its markdown, a fenced code block included', () => {
  const { container } = render(<PRSections detail={detail({ body: MD })} />);
  showsFormatted(container.querySelector('.desc') as HTMLElement);
});

test('raw HTML in a comment stays visible text and HTML comments are dropped', () => {
  const body = '<!-- hidden note -->Look <details><summary>more</summary></details> at <b>this</b>';
  const { container } = render(<PRSections detail={detail({ comments: [comment(who('hubot'), body)] })} />);
  const t = timeline(container);
  expect(t.querySelector('details')).toBeNull();
  expect(t.querySelector('b')).toBeNull();
  expect(t.textContent).toContain('<details>');
  expect(t.textContent).toContain('<b>this</b>');
  expect(t.textContent).not.toContain('hidden note');
});

test('a comment that is only an HTML comment shows no body, and a bot one gets no Show all', () => {
  const d = detail({
    comments: [comment(who('hubot'), '<!-- marker -->', 1), comment(who('ci-bot', true), '<!-- marker -->', 2)],
    threads: [{ path: 'a.go', line: 1, resolved: false, comments: [comment(who('hubot'), 'real', 3), comment(who('monalisa'), '<!-- r -->', 4)] }],
  });
  const { container } = render(<PRSections detail={d} />);
  const t = timeline(container);
  expect(t.textContent).toContain('real');
  expect(t.textContent).not.toContain('marker');
  expect(t.textContent).not.toContain('<!--');
  expect(screen.queryByRole('button', { name: /show all/i })).toBeNull();
});

test('a folded bot comment shows only its first line, formatted, and Show all reveals the rest', () => {
  const body = '<!-- bot marker -->\n## **Heading** with `code`\n\nsecond paragraph\n\n```ts\nconst hidden = 1;\n```';
  const { container } = render(<PRSections detail={detail({ comments: [comment(who('ci-bot', true), body)] })} />);
  const t = timeline(container);
  expect(t.textContent).toContain('Heading with code');
  expect(t.querySelector('strong')?.textContent).toBe('Heading');
  expect(t.querySelector('code')?.textContent).toBe('code');
  expect(t.querySelector('h2')).toBeNull();
  expect(t.textContent).not.toContain('second paragraph');
  expect(t.textContent).not.toContain('##');
  expect(t.textContent).not.toContain('bot marker');

  fireEvent.click(screen.getByRole('button', { name: 'Show all' }));
  expect(t.textContent).toContain('second paragraph');
  expect(t.querySelector('pre code')?.textContent).toContain('const hidden = 1;');
  expect(t.querySelector('h2')?.textContent).toBe('Heading with code');

  fireEvent.click(screen.getByRole('button', { name: 'Show less' }));
  expect(t.textContent).not.toContain('second paragraph');
  expect(t.querySelector('pre')).toBeNull();
});

test('a bot body of only an empty fence shows no body and no Show all', () => {
  const { container } = render(<PRSections detail={detail({ comments: [comment(who('ci-bot', true), '```\n```')] })} />);
  expect(timeline(container).querySelector('.item-body')).toBeNull();
  expect(screen.queryByRole('button', { name: /show all/i })).toBeNull();
});
