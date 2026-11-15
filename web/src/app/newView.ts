import type { ViewEntry } from './githubViews';

export const MAX_SEARCH_BYTES = 1024;

export type Show = 'pr' | 'issue' | 'both';
export type Where = 'reviewer' | 'author' | 'assignee' | 'mentioned';

/** What the builder's controls hold; `without` is the labels left out. */
export interface BuilderState {
  show: Show;
  where?: Where;
  drafts: boolean;
  approved: boolean;
  without: string[];
  extra?: string;
}

const SHOW: Record<Show, string> = { pr: 'is:pr', issue: 'is:issue', both: '' };
const WHERE: Record<Where, string> = { reviewer: 'review-requested:@me', author: 'author:@me', assignee: 'assignee:@me', mentioned: 'mentions:@me' };

export const labelQualifier = (label: string): string => `label:"${label}"`;

/** The search the controls make. Reviewer, Drafts, Already approved and a review: Extra apply to pull requests only. */
export function buildSearch(s: BuilderState): string {
  const pr = s.show === 'pr';
  const where = s.where && (pr || s.where !== 'reviewer') ? WHERE[s.where] : 'involves:@me';
  const extra = s.extra && (pr || !s.extra.startsWith('review:')) ? s.extra : undefined;
  return [
    'is:open',
    SHOW[s.show],
    where,
    pr && s.drafts ? 'draft:false' : '',
    pr && s.approved && extra !== 'review:approved' ? '-review:approved' : '',
    ...s.without.map((l) => `-${labelQualifier(l)}`),
    extra ?? '',
  ]
    .filter(Boolean)
    .join(' ');
}

export interface ViewPreset {
  id: string;
  label: string;
  name: string;
  state: BuilderState;
  needsLabel?: boolean;
}

const PRESETS: { id: string; label: string; name?: string; state: (label?: string) => BuilderState; needsLabel?: boolean }[] = [
  { id: 'needs-my-review', label: 'Needs my review', state: () => ({ show: 'pr', where: 'reviewer', drafts: true, approved: true, without: [] }) },
  { id: 'my-prs-awaiting-review', label: 'My PRs awaiting review', state: () => ({ show: 'pr', where: 'author', drafts: true, approved: true, without: [] }) },
  { id: 'approved-ready-to-merge', label: 'Approved and ready to merge', state: () => ({ show: 'pr', where: 'author', drafts: true, approved: false, without: [], extra: 'review:approved' }) },
  { id: 'assigned-issues', label: 'Assigned issues', state: () => ({ show: 'issue', where: 'assignee', drafts: false, approved: false, without: [] }) },
  { id: 'has-label', label: 'Has label…', needsLabel: true, state: (label) => ({ show: 'both', drafts: false, approved: false, without: [], extra: label ? labelQualifier(label) : undefined }) },
  { id: 'mentions-me', label: 'Mentions me', state: () => ({ show: 'both', where: 'mentioned', drafts: false, approved: false, without: [] }) },
];

/** A preset's name and builder state; Has label… takes the label it was given. */
export function viewPreset(id: string, label?: string): ViewPreset {
  const p = PRESETS.find((x) => x.id === id) ?? PRESETS[0];
  const given = label?.trim();
  return { id: p.id, label: p.label, name: p.needsLabel ? (given ?? '') : p.label, state: p.state(given), ...(p.needsLabel ? { needsLabel: true } : {}) };
}

export const VIEW_PRESETS: ViewPreset[] = PRESETS.map((p) => viewPreset(p.id));

/** What is wrong with a label, if anything; a blank one is not a problem here. */
export const labelProblem = (label: string): string | undefined => (label.includes('"') ? `Labels with " can't be searched` : undefined);

/** The length GitHub limits, in UTF-8 bytes of the trimmed search. */
export const searchBytes = (search: string): number => new TextEncoder().encode(search.trim()).length;

/** The builder's state, and a search the person typed over it, which stands until a control changes. */
export interface Draft {
  state: BuilderState;
  hand?: string;
}

export const shownSearch = (d: Draft): string => d.hand ?? buildSearch(d.state);
export const withHandEdit = (d: Draft, text: string): Draft => ({ ...d, hand: text });
export const withState = (_d: Draft, state: BuilderState): Draft => ({ state });

export const addView = (list: ViewEntry[], title: string, query: string): ViewEntry[] => [...list, { title: title.trim(), query: query.trim() }];
