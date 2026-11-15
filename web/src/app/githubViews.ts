import { shipAge } from './shipList';

/** A GitHub source's stored view entry: a built-in is {id, hidden, title?}; a custom view is {title, query, hidden?}. The settings GET adds each view's id and marks a built-in with `builtin`; neither is saved back for a custom view. */
export interface ViewEntry {
  id?: string;
  builtin?: boolean;
  title?: string;
  query?: string;
  hidden?: boolean;
  /** Names a row added this session, so its key holds still while the server's id and the saves queued on it catch up; never saved. */
  fresh?: string;
}

export interface BuiltinView {
  id: string;
  title: string;
  query?: string;
  plain: string;
}

// Kept in step with defaultViews in internal/github/settings.go; a test compares them.
export const BUILTIN_VIEWS: BuiltinView[] = [
  { id: 'for-you', title: 'For you', plain: 'Everything below that needs you now: reviews, your PRs with changes requested or failing checks, high-priority issues' },
  { id: 'assigned-to-me', title: 'Assigned to me', query: 'is:open assignee:@me', plain: 'Open issues and PRs assigned to you' },
  { id: 'review-requested', title: 'Review requested', query: 'is:open is:pr review-requested:@me', plain: 'Open PRs where you are a requested reviewer' },
  { id: 'my-prs', title: 'My PRs', query: 'is:open is:pr author:@me', plain: 'Open PRs you wrote' },
];

export const isBuiltin = (e: ViewEntry) => e.builtin === true;

/** A row's key: the server's id for the view, which holds still while rows move. */
export const viewKey = (e: ViewEntry, i: number): string => e.fresh ?? e.id ?? `view-${i}`;

/** Runs a list change on the entry that holds `key` now, so a change made on a row still lands on it after another change moved it. */
export const onKey = (list: ViewEntry[], key: string, change: (i: number) => ViewEntry[]): ViewEntry[] => {
  const i = list.findIndex((e, j) => viewKey(e, j) === key);
  return i < 0 ? list : change(i);
};

/** The list in the order of `keys`; an entry no key names keeps its place at the end. */
export function inOrder(list: ViewEntry[], keys: string[]): ViewEntry[] {
  const keyed = list.map((e, i) => ({ e, k: viewKey(e, i) }));
  const named = keys.flatMap((k) => keyed.filter((x) => x.k === k));
  return [...named, ...keyed.filter((x) => !keys.includes(x.k))].map((x) => x.e);
}

/** "just now", then "2m ago", "3h ago", "2d ago". */
export function checkedWords(iso: string | undefined, now: number): string {
  const age = iso ? shipAge(iso, now) : '';
  return age === 'now' ? 'just now' : age && `${age} ago`;
}

/** What GitHub shows for a view under its row: nothing for For you, and nothing for a hidden or unasked one. */
export const searchOf = (e: ViewEntry): string | undefined => e.query ?? BUILTIN_VIEWS.find((b) => b.id === e.id)?.query;
export const titleOf = (e: ViewEntry): string => e.title || BUILTIN_VIEWS.find((b) => b.id === e.id)?.title || '';

export const isRemovable = (e: ViewEntry): boolean => !isBuiltin(e);

const PLAIN: Record<string, string> = {
  'is:open': 'Open issues and PRs',
  'is:pr': 'PRs',
  'is:open is:pr': 'Open PRs',
};

export type Count = { n: number } | { error: string };

/** What a search-count answer says; a 422 reads as a failed search, any other refusal as its own text. An aborted request says nothing. */
export function countOutcome(r: { ok: true; data: { count: number } } | { ok: false; error: string; status?: number; aborted?: boolean }): Count | undefined {
  if (r.ok) return { n: r.data.count };
  if (r.aborted) return undefined;
  return { error: r.status === 422 && !r.error.startsWith('Search failed') ? `Search failed: ${r.error}` : r.error };
}

const ROLES: Record<string, string> = {
  'author:@me': 'you wrote',
  'assignee:@me': 'assigned to you',
  'mentions:@me': 'that mention you',
  'involves:@me': 'you are involved in',
};
const SUFFIX: Record<string, string> = { 'draft:false': 'not drafts', '-review:approved': 'not yet approved', 'review:approved': 'approved' };
const LABEL = /^(-?)label:"([^"]*)"$/;

/** A search in plain words; the search itself when it holds a qualifier these words don't cover. */
export function plainWords(query: string): string {
  const q: string[] = query.trim().match(/(?:[^\s"]|"[^"]*")+/g) ?? [];
  const roles = q.filter((t) => t === 'review-requested:@me' || t in ROLES);
  const rest = q.filter((t) => !roles.includes(t) && !t.startsWith('is:'));
  if (q.length === 0 || roles.length > 1 || q.some((t) => t.startsWith('is:') && !['is:open', 'is:pr', 'is:issue'].includes(t))) return query;
  const open = q.includes('is:open');
  const pr = q.includes('is:pr');
  if (q.includes('is:issue') && q.some((t) => t === 'review-requested:@me' || t === 'draft:false' || t.endsWith('review:approved'))) return query;
  if (pr && q.includes('is:issue')) return query;
  const subject = pr ? 'PRs' : q.includes('is:issue') ? 'issues' : 'issues and PRs';
  if (roles.length === 0) return rest.length === 0 ? (PLAIN[q.join(' ')] ?? query) : query;
  const suffixes: string[] = [];
  for (const t of rest) {
    const label = LABEL.exec(t);
    if (SUFFIX[t]) suffixes.push(SUFFIX[t]);
    else if (label) suffixes.push(`${label[1] ? 'not ' : ''}labelled "${label[2]}"`);
    else return query;
  }
  const lead = roles[0] === 'review-requested:@me' ? 'PRs where you are a requested reviewer' : `${subject} ${ROLES[roles[0]]}`;
  return [`${open ? 'Open ' : ''}${lead}`, ...suffixes].join(', ');
}

export function moveView(list: ViewEntry[], from: number, to: number): ViewEntry[] {
  const next = [...list];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

export const setHidden = (list: ViewEntry[], i: number, hidden: boolean): ViewEntry[] => list.map((e, j) => (j === i ? { ...e, hidden } : e));

export function renameView(list: ViewEntry[], i: number, name: string): ViewEntry[] {
  const title = name.trim();
  return list.map((e, j) => {
    if (j !== i) return e;
    if (title) return { ...e, title };
    if (!isBuiltin(e)) return e;
    const { title: _, ...rest } = e;
    return rest;
  });
}

export const removeView = (list: ViewEntry[], i: number): ViewEntry[] => (isRemovable(list[i]) ? list.filter((_, j) => j !== i) : list);

export function forSave(list: ViewEntry[]): ViewEntry[] {
  return list.map((e) => {
    const { builtin: _b, ...rest } = e;
    if (isBuiltin(e)) {
      const { query: _q, ...entry } = rest;
      return entry;
    }
    const { id: _id, fresh: _f, ...entry } = rest;
    return entry;
  });
}

/** A view's query and title changed together, as Edit search saves them; an empty field keeps what the view had. */
export const editSearch = (list: ViewEntry[], i: number, title: string, query: string): ViewEntry[] =>
  list.map((e, j) => (j === i ? { ...e, title: title.trim() || e.title, query: query.trim() || e.query } : e));

export const HOT_LABEL_DEFAULTS = ['priority: high', 'P0', 'P1', 'urgent'];

export function addLabel(labels: string[] | undefined, label: string): string[] {
  const list = labels ?? HOT_LABEL_DEFAULTS;
  const l = label.trim();
  if (!l || list.some((x) => x.toLowerCase() === l.toLowerCase())) return [...list];
  return [...list, l];
}

export const removeLabel = (labels: string[] | undefined, label: string): string[] => (labels ?? HOT_LABEL_DEFAULTS).filter((x) => x.toLowerCase() !== label.toLowerCase());

const INTERVALS = [1, 5, 15];
export function intervalOptions(minutes: number | undefined): { options: number[]; selected: number } {
  const selected = minutes ?? 5;
  return { options: INTERVALS.includes(selected) ? INTERVALS : [...INTERVALS, selected], selected };
}
export const intervalLabel = (n: number): string => `${n} min`;

/** True when the GitHub source can't search until someone runs gh auth login or installs gh. */
export const needsSignIn = (src: { connection?: { state: string } | null } | null | undefined): boolean => src?.connection?.state === 'signed_out' || src?.connection?.state === 'gh_missing';
