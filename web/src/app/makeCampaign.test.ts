import { describe, expect, it } from 'vitest';
import type { Order } from './api';
import { accountForHost, campaignNote, createBody, createFailure, issuesHost, moveIssue, repoChoices, safeHref, sheetIssues } from './makeCampaign';
import type { OrderRow } from './ordersView';

const row = (url: string, title: string): OrderRow => ({
  key: `gh/${url}`,
  source: 'gh',
  order: { id: url, title, url } as Order,
  reason: '',
  state: '',
  needsYou: false,
  inView: true,
  launchable: true,
});
const u = (repo: string, n: number) => `https://github.com/${repo}/issues/${n}`;

describe('the repo for the parent issue', () => {
  it('offers the fleet projects of the issues owner plus the issues own repos, once each', () => {
    const got = repoChoices([u('acme/widgets', 1), u('acme/docs', 2)], ['acme/widgets', 'acme/api', 'other/site']);
    expect(got.choices).toEqual(['acme/api', 'acme/docs', 'acme/widgets']);
  });

  it('keeps an issue repo the fleet does not list, and drops a project of another owner', () => {
    const got = repoChoices([u('acme/forked', 1)], ['someone/else']);
    expect(got.choices).toEqual(['acme/forked']);
  });

  it('offers the issues repos alone when the fleet projects are unknown', () => {
    expect(repoChoices([u('acme/widgets', 1), u('acme/api', 2)], []).choices).toEqual(['acme/api', 'acme/widgets']);
  });

  it('defaults to the repo most of the issues are in', () => {
    const got = repoChoices([u('acme/api', 1), u('acme/widgets', 2), u('acme/widgets', 3)], ['acme/api', 'acme/widgets']);
    expect(got.initial).toBe('acme/widgets');
  });

  it('breaks a tie with the repo of the first issue', () => {
    const got = repoChoices([u('acme/widgets', 1), u('acme/api', 2)], ['acme/api', 'acme/widgets']);
    expect(got.initial).toBe('acme/widgets');
  });
});

describe('the sheet issues', () => {
  it('takes a title from the loaded order with that url, in the order of the refs', () => {
    const got = sheetIssues([u('acme/widgets', 42), u('acme/widgets', 41)], [row(u('acme/widgets', 41), 'Pool the logs'), row(u('acme/widgets', 42), 'Add a budget')]);
    expect(got).toEqual([
      { url: u('acme/widgets', 42), title: 'Add a budget', ref: 'acme/widgets #42' },
      { url: u('acme/widgets', 41), title: 'Pool the logs', ref: 'acme/widgets #41' },
    ]);
  });

  it('titles a ref that is in no loaded order by its owner, repo and number', () => {
    const got = sheetIssues([u('acme/widgets', 7)], [row(u('acme/widgets', 8), 'Another')]);
    expect(got).toEqual([{ url: u('acme/widgets', 7), title: 'acme/widgets #7', ref: 'acme/widgets #7' }]);
  });

  it('reads an issue on another host the same way', () => {
    const n = 3;
    expect(sheetIssues([`https://ghe.example.com/acme/api/issues/${n}`], [])[0].title).toBe(`acme/api #${n}`);
  });
});

describe('the order sent', () => {
  const issues = [{ url: u('acme/widgets', 41) }, { url: u('acme/widgets', 42) }, { url: u('acme/widgets', 43) }];

  it('moves an issue up or down by one without touching the others', () => {
    expect(moveIssue(issues, 1, 0).map((i) => i.url)).toEqual([u('acme/widgets', 42), u('acme/widgets', 41), u('acme/widgets', 43)]);
    expect(moveIssue(issues, 0, 1).map((i) => i.url)).toEqual([u('acme/widgets', 42), u('acme/widgets', 41), u('acme/widgets', 43)]);
  });

  it('moves an issue to any place a drag drops it, and leaves the list it was given alone', () => {
    expect(moveIssue(issues, 0, 2).map((i) => i.url)).toEqual([u('acme/widgets', 42), u('acme/widgets', 43), u('acme/widgets', 41)]);
    expect(issues.map((i) => i.url)).toEqual([u('acme/widgets', 41), u('acme/widgets', 42), u('acme/widgets', 43)]);
  });

  it('leaves the order alone for a move off either end', () => {
    expect(moveIssue(issues, 0, -1)).toEqual(issues);
    expect(moveIssue(issues, 2, 3)).toEqual(issues);
  });

  it('sends the repo, the title, the description and the issue urls in the order of the list', () => {
    const moved = moveIssue(issues, 2, 0);
    expect(createBody('acme/widgets', 'A title', 'A body', moved)).toEqual({
      repo: 'acme/widgets',
      title: 'A title',
      body: 'A body',
      issues: [u('acme/widgets', 43), u('acme/widgets', 41), u('acme/widgets', 42)],
    });
  });
});

describe('the message for a create that failed', () => {
  it('shows the server message as it is when the parent was not made', () => {
    expect(createFailure({ error: 'acme/widgets#4 already has a parent, so nothing was created' })).toBe('acme/widgets#4 already has a parent, so nothing was created');
  });

  it('says the parent was made and some links failed, with the parent and the issues linked so far in order', () => {
    const msg = createFailure({ error: 'could not link the second issue', parent: u('acme/widgets', 50), linked: [u('acme/widgets', 42), u('acme/widgets', 41)] });
    expect(msg).toMatch(/parent issue was made/i);
    expect(msg).toMatch(/links? failed/i);
    expect(msg).toContain('could not link the second issue');
    expect(msg).toContain(u('acme/widgets', 50));
    expect(msg.indexOf(u('acme/widgets', 42))).toBeGreaterThan(-1);
    expect(msg.indexOf(u('acme/widgets', 41))).toBeGreaterThan(msg.indexOf(u('acme/widgets', 42)));
  });

  it('names the parent when no issue was linked', () => {
    const msg = createFailure({ error: 'boom', parent: u('acme/widgets', 50), linked: [] });
    expect(msg).toMatch(/parent issue was made/i);
    expect(msg).toContain(u('acme/widgets', 50));
  });
});

describe('the sheet text', () => {
  it('names the source account', () => {
    expect(campaignNote('work-bot')).toBe('Admiral creates a parent issue on GitHub as @work-bot and links these issues under it. Then it follows the campaign. Nothing launches.');
  });

  it("falls back to the fleet's gh account when there is none", () => {
    const want = "Admiral creates a parent issue on GitHub as the fleet's gh account and links these issues under it. Then it follows the campaign. Nothing launches.";
    expect(campaignNote('')).toBe(want);
    expect(campaignNote(undefined)).toBe(want);
  });
});

describe('a link in the sheet', () => {
  it('is a link only for an https url', () => {
    expect(safeHref('https://github.com/acme/widgets/issues/50')).toBe('https://github.com/acme/widgets/issues/50');
    for (const bad of ['javascript:alert(1)', 'http://github.com/a/b/issues/1', 'data:text/html,x', '//evil.example/x', 'acme/widgets#1']) expect(safeHref(bad)).toBeUndefined();
  });
});

describe('the source that writes the parent', () => {
  const src = (key: string, settings: Record<string, unknown>) => ({ key, settings });

  it('takes the issues host from the first issue', () => {
    expect(issuesHost([u('acme/widgets', 1)])).toBe('github.com');
    expect(issuesHost(['https://GHE.example.com/acme/api/issues/3'])).toBe('ghe.example.com');
  });

  it('picks the source whose host matches, github.com when a source sets none', () => {
    const list = [src('ghe', { host: 'ghe.example.com', account: 'ghe-bot' }), src('dotcom', { account: 'work-bot' })];
    expect(accountForHost(list, 'github.com')).toEqual({ key: 'dotcom', account: 'work-bot' });
    expect(accountForHost(list, 'ghe.example.com')).toEqual({ key: 'ghe', account: 'ghe-bot' });
  });

  it('answers none when no source matches, and an empty account for a source with none', () => {
    expect(accountForHost([src('ghe', { host: 'ghe.example.com' })], 'github.com')).toBeUndefined();
    expect(accountForHost([src('a', { account: 3 })], 'github.com')).toEqual({ key: 'a', account: '' });
  });
});
