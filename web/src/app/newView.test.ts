import { describe, expect, it } from 'vitest';
import { forSave, type ViewEntry } from './githubViews';
import { MAX_SEARCH_BYTES, VIEW_PRESETS, addView, buildSearch, labelProblem, searchBytes, shownSearch, viewPreset, withHandEdit, withState, type BuilderState } from './newView';

const base: BuilderState = { show: 'pr', where: 'reviewer', drafts: true, approved: true, without: [] };
const search = (patch: Partial<BuilderState>) => buildSearch({ ...base, ...patch });

describe('the presets', () => {
  it('come in the order of the design', () => {
    expect(VIEW_PRESETS.map((p) => p.label)).toEqual(['Needs my review', 'My PRs awaiting review', 'Approved and ready to merge', 'Assigned issues', 'Has label…', 'Mentions me']);
  });

  it.each([
    ['needs-my-review', 'Needs my review', 'is:open is:pr review-requested:@me draft:false -review:approved'],
    ['my-prs-awaiting-review', 'My PRs awaiting review', 'is:open is:pr author:@me draft:false -review:approved'],
    ['approved-ready-to-merge', 'Approved and ready to merge', 'is:open is:pr author:@me draft:false review:approved'],
    ['assigned-issues', 'Assigned issues', 'is:open is:issue assignee:@me'],
    ['mentions-me', 'Mentions me', 'is:open mentions:@me'],
  ])('%s names the view and builds its search', (id, name, query) => {
    const p = viewPreset(id);
    expect(p.name).toBe(name);
    expect(buildSearch(p.state)).toBe(query);
  });

  it('Has label… asks for a label, and the label is the name and the search', () => {
    expect(viewPreset('has-label').needsLabel).toBe(true);
    const p = viewPreset('has-label', 'needs design');
    expect(p.name).toBe('needs design');
    expect(buildSearch(p.state)).toBe('is:open involves:@me label:"needs design"');
  });

  it('are the only ones that ask for a label', () => {
    expect(VIEW_PRESETS.filter((p) => p.needsLabel).map((p) => p.label)).toEqual(['Has label…']);
  });

  it('keep an Extra qualifier in the state while other controls change, and a new preset replaces it', () => {
    const approved = viewPreset('approved-ready-to-merge').state;
    expect(buildSearch({ ...approved, where: 'assignee' })).toBe('is:open is:pr assignee:@me draft:false review:approved');
    expect(buildSearch(viewPreset('needs-my-review').state)).not.toMatch(/(^| )review:approved/);
  });
});

describe('the builder', () => {
  it('starts every search with is:open, then Show, Where I am, Leave out and Extra', () => {
    expect(search({ without: ['wip'], extra: 'label:"x"' })).toBe('is:open is:pr review-requested:@me draft:false -review:approved -label:"wip" label:"x"');
  });

  it.each([
    ['pr', 'is:pr'],
    ['issue', 'is:issue'],
    ['both', ''],
  ] as const)('Show %s says %j', (show, part) => {
    const q = buildSearch({ show, where: 'author', drafts: false, approved: false, without: [] });
    expect(q).toBe(['is:open', part, 'author:@me'].filter(Boolean).join(' '));
  });

  it.each([
    ['author', 'author:@me'],
    ['assignee', 'assignee:@me'],
    ['mentioned', 'mentions:@me'],
    ['reviewer', 'review-requested:@me'],
  ] as const)('Where I am %s says %s', (where, part) => {
    expect(buildSearch({ show: 'pr', where, drafts: false, approved: false, without: [] })).toBe(`is:open is:pr ${part}`);
  });

  it('adds involves:@me in the place of Where I am when none is chosen', () => {
    expect(buildSearch({ show: 'both', drafts: false, approved: false, without: [] })).toBe('is:open involves:@me');
    expect(buildSearch({ show: 'pr', drafts: true, approved: false, without: [] })).toBe('is:open is:pr involves:@me draft:false');
  });

  it('quotes a label, leaving it out with its spaces', () => {
    expect(search({ show: 'both', where: 'author', drafts: false, approved: false, without: ['needs design', 'wip'] })).toBe('is:open author:@me -label:"needs design" -label:"wip"');
  });

  it.each(['issue', 'both'] as const)('leaves the pull-request-only parts out with Show %s, and Reviewer gives way to involves:@me', (show) => {
    expect(search({ show, extra: 'review:approved' })).toBe(`is:open${show === 'issue' ? ' is:issue' : ''} involves:@me`);
    expect(search({ show, where: 'author', extra: 'label:"x"' })).toBe(`is:open${show === 'issue' ? ' is:issue' : ''} author:@me label:"x"`);
  });

  it('restores what was chosen when Show goes back to Pull requests', () => {
    const chosen: BuilderState = { ...base, extra: 'review:approved', approved: false };
    const issues = { ...chosen, show: 'issue' as const };
    const again = { ...issues, show: 'pr' as const };
    expect(buildSearch(again)).toBe(buildSearch(chosen));
    expect(buildSearch(again)).toBe('is:open is:pr review-requested:@me draft:false review:approved');
  });

  it('leaves Already approved out while the Extra is review:approved', () => {
    expect(search({ extra: 'review:approved' })).toBe('is:open is:pr review-requested:@me draft:false review:approved');
  });
});

describe('labels', () => {
  it('refuses a label with a double quote', () => {
    expect(labelProblem('say "hi"')).toBe(`Labels with " can't be searched`);
  });

  it('accepts a label with spaces and says nothing for a blank one', () => {
    expect(labelProblem('needs design')).toBeUndefined();
    expect(labelProblem('  ')).toBeUndefined();
  });
});

describe('the length GitHub accepts', () => {
  it('counts bytes of UTF-8, not characters', () => {
    expect(searchBytes('is:open')).toBe(7);
    expect(searchBytes(' é ')).toBe(2);
    expect(searchBytes('日本')).toBe(6);
  });

  it('is 1024', () => {
    expect(MAX_SEARCH_BYTES).toBe(1024);
  });
});

describe('a hand edit', () => {
  const draft = { state: viewPreset('needs-my-review').state };

  it('is the search until a control changes', () => {
    const edited = withHandEdit(draft, 'is:open is:pr label:"hand"');
    expect(shownSearch(edited)).toBe('is:open is:pr label:"hand"');
    expect(shownSearch(draft)).toBe('is:open is:pr review-requested:@me draft:false -review:approved');
  });

  it('is dropped when a control changes, and the search is rebuilt', () => {
    const edited = withHandEdit(draft, 'is:open is:pr label:"hand"');
    const changed = withState(edited, { ...draft.state, where: 'author' });
    expect(shownSearch(changed)).toBe('is:open is:pr author:@me draft:false -review:approved');
  });
});

describe('Add view', () => {
  const list = (): ViewEntry[] => [{ id: 'for-you', builtin: true, hidden: false }, { id: 'my-prs', builtin: true, hidden: false }];

  it('appends the trimmed name and search at the end of the list', () => {
    const next = addView(list(), '  Mine  ', ' is:open is:pr author:@me ');
    expect(next.slice(0, 2)).toEqual(list());
    expect(next[2]).toEqual({ title: 'Mine', query: 'is:open is:pr author:@me' });
    expect(next).toHaveLength(3);
  });

  it('is kept by forSave as a custom entry, with no id', () => {
    expect(forSave(addView(list(), 'Mine', 'is:open')).at(-1)).toEqual({ title: 'Mine', query: 'is:open' });
  });

  it('allows a name another view already has', () => {
    const twice = addView(addView(list(), 'Mine', 'is:open'), 'Mine', 'is:open is:pr');
    expect(twice.filter((e) => e.title === 'Mine')).toHaveLength(2);
  });
});
