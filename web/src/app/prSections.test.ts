import { describe, expect, it } from 'vitest';
import type { PRActor, PRDetail } from './api';
import { canReRequest, checksLine, descriptionSource, foldedLine, logHref, prTimeline, relativeTime, resolvedLabel, reviewerRows, reviewSummary } from './prSections';

const who = (login: string, bot = false): PRActor => ({ login, bot });
const review = (login: string, state: string, bot = false) => ({ author: who(login, bot), state });
const rows = (reviews: PRDetail['reviews'], requested: PRActor[] = []) => reviewerRows(reviews, requested).map((r) => `${r.login}:${r.verdict}`);

describe('reviewerRows', () => {
  it('shows a later approval after changes requested as Approved', () => {
    expect(rows([review('ann', 'CHANGES_REQUESTED'), review('ann', 'APPROVED')])).toEqual(['ann:Approved']);
  });

  it('shows changes requested after an approval as Changes requested', () => {
    expect(rows([review('ann', 'APPROVED'), review('ann', 'CHANGES_REQUESTED')])).toEqual(['ann:Changes requested']);
  });

  it('keeps Approved when the same person only commented afterwards', () => {
    expect(rows([review('ann', 'APPROVED'), review('ann', 'COMMENTED')])).toEqual(['ann:Approved']);
    expect(rows([review('ann', 'CHANGES_REQUESTED'), review('ann', 'COMMENTED')])).toEqual(['ann:Changes requested']);
  });

  it('shows Commented for a person who only commented', () => {
    expect(rows([review('ann', 'COMMENTED'), review('ann', 'COMMENTED')])).toEqual(['ann:Commented']);
  });

  it('shows Dismissed when a dismissal is the latest verdict', () => {
    expect(rows([review('ann', 'APPROVED'), review('ann', 'DISMISSED')])).toEqual(['ann:Dismissed']);
    expect(rows([review('ann', 'DISMISSED'), review('ann', 'APPROVED')])).toEqual(['ann:Approved']);
  });

  it('skips pending reviews, so a person with only a pending one has no row', () => {
    expect(rows([review('ann', 'PENDING')])).toEqual([]);
    expect(rows([review('ann', 'APPROVED'), review('ann', 'PENDING')])).toEqual(['ann:Approved']);
  });

  it('orders the rows by when each person first reviewed, not by their latest review', () => {
    expect(rows([review('bob', 'COMMENTED'), review('ann', 'APPROVED'), review('bob', 'APPROVED')])).toEqual(['bob:Approved', 'ann:Approved']);
    expect(rows([review('zed', 'PENDING'), review('ann', 'APPROVED'), review('zed', 'APPROVED')])).toEqual(['ann:Approved', 'zed:Approved']);
  });

  it('puts a requested reviewer with no review after the reviewed rows as Waiting', () => {
    expect(rows([review('ann', 'APPROVED')], [who('cy')])).toEqual(['ann:Approved', 'cy:Waiting']);
    expect(rows([], [who('cy'), who('di')])).toEqual(['cy:Waiting', 'di:Waiting']);
  });

  it('shows a reviewer who is requested again as Waiting, not with their last verdict', () => {
    expect(rows([review('ann', 'CHANGES_REQUESTED')], [who('ann'), who('cy')]).sort()).toEqual(['ann:Waiting', 'cy:Waiting']);
    expect(rows([review('ann', 'APPROVED'), review('bob', 'APPROVED')], [who('ann')]).sort()).toEqual(['ann:Waiting', 'bob:Approved']);
  });

  it('shows a requested reviewer whose only review is pending as Waiting', () => {
    expect(rows([review('cy', 'PENDING')], [who('cy')])).toEqual(['cy:Waiting']);
  });

  it('marks a bot, and tones Approved ok, Changes requested err and the rest muted', () => {
    const got = reviewerRows(
      [review('ann', 'APPROVED'), review('bob', 'CHANGES_REQUESTED'), review('github-copilot', 'COMMENTED', true), review('dee', 'DISMISSED')],
      [who('cy')],
    );
    expect(got.map((r) => [r.login, r.bot, r.tone])).toEqual([
      ['ann', false, 'ok'],
      ['bob', false, 'err'],
      ['github-copilot', true, 'muted'],
      ['dee', false, 'muted'],
      ['cy', false, 'muted'],
    ]);
  });

  it('shows a deleted user (an empty login) as ghost', () => {
    expect(rows([review('', 'APPROVED')], [])).toEqual(['ghost:Approved']);
  });

  it('has no rows with no reviews and no requests', () => {
    expect(rows([])).toEqual([]);
  });
});

describe('reviewSummary', () => {
  const summary = (reviews: PRDetail['reviews'], requested: PRActor[] = []) => reviewSummary(reviewerRows(reviews, requested));

  it('counts the verdicts in the order approved, changes requested, commented, dismissed, waiting', () => {
    expect(
      summary(
        [review('a', 'APPROVED'), review('b', 'DISMISSED'), review('c', 'COMMENTED'), review('d', 'CHANGES_REQUESTED'), review('e', 'APPROVED')],
        [who('f'), who('g')],
      ),
    ).toBe('2 approved · 1 changes requested · 1 commented · 1 dismissed · 2 waiting');
  });

  it("follows the issue's example", () => {
    expect(summary([review('a', 'APPROVED'), review('b', 'CHANGES_REQUESTED')], [who('c'), who('d')])).toBe('1 approved · 1 changes requested · 2 waiting');
  });

  it('leaves out a verdict nobody has', () => {
    expect(summary([review('a', 'COMMENTED')])).toBe('1 commented');
  });

  it('is empty with no rows', () => {
    expect(summary([])).toBe('');
  });
});

describe('checksLine', () => {
  const line = (checks: string, failing: { name: string; url: string }[] = []) => checksLine({ checks, failing_checks: failing });

  it('says All checks passed with an ok tone for SUCCESS and nothing failing', () => {
    expect(line('SUCCESS')).toEqual({ tone: 'ok', text: 'All checks passed' });
  });

  it('counts the failing checks with an err tone', () => {
    const f = { name: 'build', url: 'https://ci.example/1' };
    expect(line('FAILURE', [f])).toEqual({ tone: 'err', text: '1 failing' });
    expect(line('FAILURE', [f, { name: 'lint', url: '' }])).toEqual({ tone: 'err', text: '2 failing' });
  });

  it('counts failing checks even when the rollup is not a failure', () => {
    expect(line('PENDING', [{ name: 'build', url: '' }])).toEqual({ tone: 'err', text: '1 failing' });
  });

  it('says Checks failing for a failed rollup that names no check', () => {
    expect(line('FAILURE')).toEqual({ tone: 'err', text: 'Checks failing' });
    expect(line('ERROR')).toEqual({ tone: 'err', text: 'Checks failing' });
  });

  it('says Checks running, muted, while pending or expected', () => {
    expect(line('PENDING')).toEqual({ tone: 'muted', text: 'Checks running' });
    expect(line('EXPECTED')).toEqual({ tone: 'muted', text: 'Checks running' });
  });

  it('says No checks, muted, when the rollup is empty', () => {
    expect(line('')).toEqual({ tone: 'muted', text: 'No checks' });
  });
});

describe('logHref', () => {
  it('keeps an https or http URL as it is', () => {
    expect(logHref('https://github.com/acme/api/actions/runs/1')).toBe('https://github.com/acme/api/actions/runs/1');
    expect(logHref('http://ci.example/run/2')).toBe('http://ci.example/run/2');
  });

  it('drops anything else: javascript:, data:, relative and empty', () => {
    for (const bad of ['javascript:alert(1)', 'JavaScript:alert(1)', ' javascript:alert(1)', 'data:text/html,x', 'ftp://x/y', '//evil.example/x', '/relative', '']) expect(logHref(bad), bad).toBeNull();
  });
});

describe('descriptionSource', () => {
  it('removes HTML comments, including multi-line ones, and keeps the Markdown around them', () => {
    const got = descriptionSource('Intro\n<!-- fill in the\nsummary -->\n## Why\n- a <!-- inline --> b');
    expect(got).not.toContain('<!--');
    expect(got).not.toContain('fill in');
    expect(got).not.toContain('inline');
    expect(got).toContain('Intro');
    expect(got).toContain('## Why');
    expect(got).toContain('- a');
  });

  it('is blank for a body that is only comments, so the tab shows No description', () => {
    expect(descriptionSource('<!-- one -->\n<!-- two -->\n').trim()).toBe('');
    expect(descriptionSource('').trim()).toBe('');
  });

  it('leaves other HTML alone', () => {
    expect(descriptionSource('<b>x</b> <img src=a>')).toBe('<b>x</b> <img src=a>');
  });
});

const comment = (login: string, body: string, created_at: string, bot = false) => ({ author: who(login, bot), body, url: `https://github.com/acme/api/pull/8#c-${created_at}`, created_at });
const thread = (path: string, line: number, resolved: boolean, comments: ReturnType<typeof comment>[]) => ({ path, line, resolved, comments });

describe('prTimeline', () => {
  it('lists threads and PR comments in one list, oldest first, by a thread\'s first comment', () => {
    const { open } = prTimeline({
      threads: [thread('b.ts', 2, false, [comment('ann', 'late thread', '2026-01-03T00:00:00Z')]), thread('a.ts', 1, false, [comment('bob', 'early thread', '2026-01-01T00:00:00Z'), comment('ann', 'reply, newest of all', '2026-01-09T00:00:00Z')])],
      comments: [comment('cy', 'middle comment', '2026-01-02T00:00:00Z')],
    });
    expect(open.map((i) => [i.kind, i.body])).toEqual([
      ['thread', 'early thread'],
      ['comment', 'middle comment'],
      ['thread', 'late thread'],
    ]);
  });

  it("takes a thread's author and time from its first comment and makes the later comments its replies", () => {
    const { open } = prTimeline({
      threads: [thread('src/x.ts', 42, false, [comment('bob', 'first', '2026-01-01T00:00:00Z'), comment('ann', 'second', '2026-01-01T01:00:00Z', false), comment('bot', 'third', '2026-01-01T02:00:00Z', true)])],
      comments: [],
    });
    expect(open).toHaveLength(1);
    expect(open[0].author).toEqual(who('bob'));
    expect(open[0].at).toBe('2026-01-01T00:00:00Z');
    expect(open[0].ref).toBe('src/x.ts:42');
    expect(open[0].replies.map((r) => [r.author.login, r.author.bot, r.body, r.at])).toEqual([
      ['ann', false, 'second', '2026-01-01T01:00:00Z'],
      ['bot', true, 'third', '2026-01-01T02:00:00Z'],
    ]);
  });

  it('shows a deleted user (an empty login) as ghost on a thread, a reply and a comment', () => {
    const { open } = prTimeline({ threads: [thread('a.ts', 1, false, [comment('', 'first', '2026-01-01T00:00:00Z'), comment('', 'second', '2026-01-01T01:00:00Z')])], comments: [comment('', 'hi', '2026-01-02T00:00:00Z')] });
    expect(open.map((i) => i.author.login)).toEqual(['ghost', 'ghost']);
    expect(open[0].replies.map((r) => r.author.login)).toEqual(['ghost']);
  });

  it('shows just the path when the line is 0, and no ref for a PR comment', () => {
    const { open } = prTimeline({ threads: [thread('README.md', 0, false, [comment('bob', 'file-level', '2026-01-01T00:00:00Z')])], comments: [comment('ann', 'hi', '2026-01-02T00:00:00Z')] });
    expect(open.map((i) => i.ref)).toEqual(['README.md', '']);
  });

  it('splits the resolved threads out, each marked resolved, and keeps their order', () => {
    const { open, resolved } = prTimeline({
      threads: [
        thread('r2.ts', 5, true, [comment('bob', 'resolved later', '2026-01-04T00:00:00Z')]),
        thread('o.ts', 1, false, [comment('bob', 'open', '2026-01-03T00:00:00Z')]),
        thread('r1.ts', 9, true, [comment('ann', 'resolved first', '2026-01-01T00:00:00Z')]),
      ],
      comments: [comment('cy', 'a comment', '2026-01-02T00:00:00Z')],
    });
    expect(open.map((i) => i.body)).toEqual(['a comment', 'open']);
    expect(resolved.map((i) => i.body)).toEqual(['resolved first', 'resolved later']);
    expect(resolved.every((i) => i.resolved)).toBe(true);
    expect(open.some((i) => i.resolved)).toBe(false);
  });

  it('is empty with no threads and no comments', () => {
    expect(prTimeline({ threads: [], comments: [] })).toEqual({ open: [], resolved: [] });
  });

  it('carries the bot flag of the first author', () => {
    const { open } = prTimeline({ threads: [], comments: [comment('github-actions', 'Coverage', '2026-01-01T00:00:00Z', true)] });
    expect(open[0].author).toEqual(who('github-actions', true));
  });

  it('keeps a thread with no comments out of the list instead of failing', () => {
    expect(prTimeline({ threads: [thread('x.ts', 1, false, [])], comments: [] })).toEqual({ open: [], resolved: [] });
  });
});

describe('resolvedLabel', () => {
  it('says thread for one and threads for more', () => {
    expect(resolvedLabel(1)).toBe('1 resolved thread');
    expect(resolvedLabel(2)).toBe('2 resolved threads');
    expect(resolvedLabel(12)).toBe('12 resolved threads');
  });
});

describe('foldedLine', () => {
  it('is the first non-blank line of the body, HTML comments dropped', () => {
    expect(foldedLine('<!-- bot marker -->\n\nCoverage **91%**\nlines: 120')).toBe('Coverage **91%**');
  });

  it('drops a leading heading marker', () => {
    expect(foldedLine('## Heading\nbody')).toBe('Heading');
    expect(foldedLine('# One')).toBe('One');
  });

  it('drops a bullet, number, quote or task marker', () => {
    expect(foldedLine('- first item\n- second')).toBe('first item');
    expect(foldedLine('* star item')).toBe('star item');
    expect(foldedLine('+ plus item')).toBe('plus item');
    expect(foldedLine('1. numbered')).toBe('numbered');
    expect(foldedLine('> quoted text')).toBe('quoted text');
    expect(foldedLine('[ ] todo')).toBe('todo');
    expect(foldedLine('- [x] done')).toBe('done');
  });

  it('drops markers repeatedly', () => {
    expect(foldedLine('> - item')).toBe('item');
    expect(foldedLine('## - [ ] mixed')).toBe('mixed');
  });

  it('drops a GitHub alert tag, and moves on when nothing is left', () => {
    for (const tag of ['NOTE', 'TIP', 'IMPORTANT', 'WARNING', 'CAUTION']) expect(foldedLine(`> [!${tag}]\n> Real text`)).toBe('Real text');
    expect(foldedLine('> [!NOTE] inline text')).toBe('inline text');
  });

  it('skips a code-fence line and shows the first line after it', () => {
    expect(foldedLine('```ts\nconst a = 1;\n```')).toBe('const a = 1;');
    expect(foldedLine('~~~\nplain\n~~~')).toBe('plain');
  });

  it('keeps inline markdown for the caller to format and leaves a plain line whole', () => {
    expect(foldedLine('Use `go test` and **bold**')).toBe('Use `go test` and **bold**');
    expect(foldedLine('3 files changed')).toBe('3 files changed');
  });

  it('is empty when nothing is left to show', () => {
    expect(foldedLine('')).toBe('');
    expect(foldedLine('<!-- only a marker -->')).toBe('');
    expect(foldedLine('```\n```')).toBe('');
  });
});

describe('relativeTime', () => {
  const now = Date.parse('2026-03-10T12:00:00Z');
  const ago = (seconds: number) => relativeTime(new Date(now - seconds * 1000).toISOString(), now);

  it('says just now under a minute, and for a time in the future', () => {
    expect(ago(0)).toBe('just now');
    expect(ago(59)).toBe('just now');
    expect(ago(-30)).toBe('just now');
    expect(ago(-86_400)).toBe('just now');
  });

  it('says minutes under an hour', () => {
    expect(ago(60)).toBe('1m ago');
    expect(ago(59 * 60 + 59)).toBe('59m ago');
  });

  it('says hours under a day', () => {
    expect(ago(3600)).toBe('1h ago');
    expect(ago(23 * 3600 + 3599)).toBe('23h ago');
  });

  it('says days from a day on', () => {
    expect(ago(86_400)).toBe('1d ago');
    expect(ago(10 * 86_400 + 5)).toBe('10d ago');
    expect(ago(400 * 86_400)).toBe('400d ago');
  });
});

describe('canReRequest', () => {
  it('offers nothing beside a deleted user, who has no one to request', () => {
    expect(canReRequest(pr([review('ghost', 'APPROVED')]), 'ghost')).toBe(false);
    expect(canReRequest(pr([review('', 'APPROVED')]), 'ghost')).toBe(false);
  });
  const pr = (reviews: PRDetail['reviews'], requested: PRActor[] = [], author = 'octocat') => ({ author: who(author), reviews, requested });

  it('offers it to a person whose submitted review stands, whatever its verdict', () => {
    for (const state of ['APPROVED', 'CHANGES_REQUESTED', 'COMMENTED', 'DISMISSED']) {
      expect(canReRequest(pr([review('hubot', state)]), 'hubot'), state).toBe(true);
    }
  });

  it('offers it to a bot that reviewed', () => {
    expect(canReRequest(pr([review('copilot-pull-request-reviewer', 'COMMENTED', true)]), 'copilot-pull-request-reviewer')).toBe(true);
  });

  it('offers it for a person who has any submitted review among a pending one', () => {
    expect(canReRequest(pr([review('hubot', 'APPROVED'), review('hubot', 'PENDING')]), 'hubot')).toBe(true);
  });

  it('does not offer it to someone with no submitted review', () => {
    expect(canReRequest(pr([review('hubot', 'PENDING')]), 'hubot')).toBe(false);
    expect(canReRequest(pr([], [who('hubot')]), 'hubot')).toBe(false);
    expect(canReRequest(pr([review('hubot', 'APPROVED')]), 'monalisa')).toBe(false);
  });

  it('does not offer it while the person is in the current review requests', () => {
    expect(canReRequest(pr([review('hubot', 'APPROVED')], [who('hubot')]), 'hubot')).toBe(false);
  });

  it('does not offer it to the PR author', () => {
    expect(canReRequest(pr([review('octocat', 'COMMENTED')]), 'octocat')).toBe(false);
  });

  it('never offers it for a team, which only ever appears as a pending request', () => {
    expect(canReRequest(pr([review('hubot', 'APPROVED')], [who('acme/core')]), 'acme/core')).toBe(false);
  });
});
