import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { BUILTIN_VIEWS, HOT_LABEL_DEFAULTS, addLabel, forSave, intervalLabel, intervalOptions, isRemovable, moveView, needsSignIn, plainWords, removeLabel, removeView, renameView, setHidden, type ViewEntry } from './githubViews';

const goSettings = readFileSync(fileURLToPath(new URL('../../../internal/github/settings.go', import.meta.url)), 'utf8');

describe('the built-in views table', () => {
  it('lists the built-ins the server defaults to, with their ids, titles and searches', () => {
    const block = goSettings.slice(goSettings.indexOf('var defaultViews'));
    const rows = [...block.slice(0, block.indexOf('\n}\n')).matchAll(/ID: "([^"]+)", Title: "([^"]+)"(?:, Query: "([^"]*)")?/g)];
    expect(rows.length).toBe(4);
    expect(BUILTIN_VIEWS.map((v) => [v.id, v.title, v.query ?? ''])).toEqual(rows.map((m) => [m[1], m[2], m[3] ?? '']));
  });

  it('describes For you in the words of the design', () => {
    expect(BUILTIN_VIEWS.find((v) => v.id === 'for-you')?.plain).toBe('Everything below that needs you now: reviews, your PRs with changes requested or failing checks, high-priority issues');
  });
});

describe('a search in plain words', () => {
  it.each([
    ['is:open is:pr author:@me', 'Open PRs you wrote'],
    ['is:open is:pr review-requested:@me draft:false -review:approved', 'Open PRs where you are a requested reviewer, not drafts, not yet approved'],
    ['is:open is:pr review-requested:@me', 'Open PRs where you are a requested reviewer'],
    ['is:open assignee:@me', 'Open issues and PRs assigned to you'],
  ])('says %s as "%s"', (query, words) => {
    expect(plainWords(query)).toBe(words);
  });

  it('shows the search itself when it holds a qualifier it has no words for', () => {
    expect(plainWords('is:open is:pr reviews:approved')).toBe('is:open is:pr reviews:approved');
  });
});

describe('the views list', () => {
  const list = (): ViewEntry[] => [
    { id: 'for-you', hidden: false },
    { id: 'assigned-to-me', hidden: false, title: 'My plate' },
    { id: 'review-requested', hidden: true },
    { id: 'my-prs', hidden: false },
    { id: 'bugs', title: 'Bugs', query: 'is:open label:bug' },
    { id: 'docs', title: 'Docs', query: 'is:open label:docs', hidden: true },
  ];

  it('moves a view from one place to another and keeps the rest in order', () => {
    expect(moveView(list(), 0, 2).map((e) => e.id)).toEqual(['assigned-to-me', 'review-requested', 'for-you', 'my-prs', 'bugs', 'docs']);
    expect(moveView(list(), 5, 0).map((e) => e.id)).toEqual(['docs', 'for-you', 'assigned-to-me', 'review-requested', 'my-prs', 'bugs']);
  });

  it('leaves the list as it was when it moves a view nowhere', () => {
    expect(moveView(list(), 3, 3)).toEqual(list());
  });

  it('hides and shows a view', () => {
    expect(setHidden(list(), 0, true)[0].hidden).toBe(true);
    expect(setHidden(list(), 2, false)[2].hidden).toBe(false);
  });

  it('renames a built-in by setting its title, and clears the title when the name is emptied', () => {
    expect(renameView(list(), 3, 'Mine')[3]).toEqual({ id: 'my-prs', hidden: false, title: 'Mine' });
    const cleared = renameView(list(), 1, '  ')[1];
    expect(cleared).toEqual({ id: 'assigned-to-me', hidden: false });
    expect('title' in cleared).toBe(false);
  });

  it('renames a custom view, and keeps its title when the name is emptied', () => {
    expect(renameView(list(), 4, 'Defects')[4].title).toBe('Defects');
    expect(renameView(list(), 4, '')[4].title).toBe('Bugs');
  });

  it('offers removal for a custom view only', () => {
    expect(list().map(isRemovable)).toEqual([false, false, false, false, true, true]);
  });

  it('removes a custom view', () => {
    expect(removeView(list(), 4).map((e) => e.id)).toEqual(['for-you', 'assigned-to-me', 'review-requested', 'my-prs', 'docs']);
  });

  it('never removes a built-in', () => {
    expect(removeView(list(), 0)).toEqual(list());
  });

  it('does not change the list it was given', () => {
    const before = list();
    const frozen = structuredClone(before);
    moveView(before, 0, 3);
    setHidden(before, 1, true);
    renameView(before, 1, 'x');
    removeView(before, 4);
    expect(before).toEqual(frozen);
  });

  it('saves without the ids the server added to custom views, and without a search on a built-in', () => {
    const saved = forSave(list());
    expect(saved).toEqual([
      { id: 'for-you', hidden: false },
      { id: 'assigned-to-me', hidden: false, title: 'My plate' },
      { id: 'review-requested', hidden: true },
      { id: 'my-prs', hidden: false },
      { title: 'Bugs', query: 'is:open label:bug' },
      { title: 'Docs', query: 'is:open label:docs', hidden: true },
    ]);
    for (const e of saved.slice(0, 4)) expect('query' in e).toBe(false);
    for (const e of saved.slice(4)) expect('id' in e).toBe(false);
  });
});

describe('high-priority labels', () => {
  it('start from the defaults the server uses', () => {
    expect(HOT_LABEL_DEFAULTS).toEqual(['priority: high', 'P0', 'P1', 'urgent']);
  });

  it('adds a label to the defaults the first time anything changes', () => {
    expect(addLabel(undefined, 'sev1')).toEqual(['priority: high', 'P0', 'P1', 'urgent', 'sev1']);
  });

  it('removes a label from the defaults the first time anything changes, and writes the whole list', () => {
    expect(removeLabel(undefined, 'P0')).toEqual(['priority: high', 'P1', 'urgent']);
  });

  it('adds to a stored list and trims the label', () => {
    expect(addLabel(['bug'], '  sev1 ')).toEqual(['bug', 'sev1']);
  });

  it('does not add a label that is there already, whatever its case, or an empty one', () => {
    expect(addLabel(['P0', 'urgent'], 'p0')).toEqual(['P0', 'urgent']);
    expect(addLabel(['P0'], '   ')).toEqual(['P0']);
    expect(addLabel(undefined, 'URGENT')).toEqual(['priority: high', 'P0', 'P1', 'urgent']);
  });

  it('removes a stored label whatever its case', () => {
    expect(removeLabel(['P0', 'urgent'], 'URGENT')).toEqual(['P0']);
  });
});

describe('how often to check', () => {
  it('offers 1, 5 and 15 minutes and selects 5 when none is set', () => {
    expect(intervalOptions(undefined)).toEqual({ options: [1, 5, 15], selected: 5 });
  });

  it('selects the stored one of the three', () => {
    expect(intervalOptions(15)).toEqual({ options: [1, 5, 15], selected: 15 });
  });

  it('shows a stored value off the list as a fourth, selected option', () => {
    expect(intervalOptions(7)).toEqual({ options: [1, 5, 15, 7], selected: 7 });
  });

  it('labels an option in minutes', () => {
    expect([1, 5, 15, 7].map(intervalLabel)).toEqual(['1 min', '5 min', '15 min', '7 min']);
  });
});

describe('the dot on the GitHub settings item', () => {
  it.each([['signed_out', true], ['gh_missing', true], ['connected', false], ['checking', false], ['unreachable', false]])('is %s: %s', (state, dot) => {
    expect(needsSignIn({ connection: { state } })).toBe(dot);
  });

  it('is absent with no GitHub source or no connection', () => {
    expect(needsSignIn(undefined)).toBe(false);
    expect(needsSignIn(null)).toBe(false);
    expect(needsSignIn({})).toBe(false);
  });
});
