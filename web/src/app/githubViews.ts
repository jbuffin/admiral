/** A GitHub source's stored view entry: a built-in is {id, hidden, title?}; a custom view is {title, query, hidden?} and carries the id the server gives it. */
export interface ViewEntry {
  id?: string;
  title?: string;
  query?: string;
  hidden?: boolean;
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

const isBuiltin = (e: ViewEntry) => !!e.id && BUILTIN_VIEWS.some((b) => b.id === e.id) && e.query === undefined;

export const isRemovable = (e: ViewEntry): boolean => !isBuiltin(e);

const PLAIN: Record<string, string> = {
  'is:open': 'Open issues and PRs',
  'is:pr': 'PRs',
  'is:open is:pr': 'Open PRs',
};

/** A search in plain words; the search itself when it holds a qualifier these words don't cover. */
export function plainWords(query: string): string {
  const q = query.trim().split(/\s+/).filter(Boolean);
  const has = (t: string) => q.includes(t);
  const known = new Set(['is:open', 'is:pr', 'author:@me', 'assignee:@me', 'review-requested:@me', 'draft:false', '-review:approved']);
  if (q.length === 0 || q.some((t) => !known.has(t))) return query;
  const open = has('is:open');
  const pr = has('is:pr');
  let words: string;
  if (has('review-requested:@me')) {
    words = `${open ? 'Open ' : ''}PRs where you are a requested reviewer`;
    if (has('draft:false')) words += ', not drafts';
    if (has('-review:approved')) words += ', not yet approved';
  } else if (has('author:@me')) words = `${open ? 'Open ' : ''}${pr ? 'PRs' : 'issues and PRs'} you wrote`;
  else if (has('assignee:@me')) words = `${open ? 'Open ' : ''}${pr ? 'PRs' : 'issues and PRs'} assigned to you`;
  else return PLAIN[q.join(' ')] ?? query;
  return words;
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
    if (isBuiltin(e)) {
      const { query: _q, ...rest } = e;
      return rest;
    }
    const { id: _id, ...rest } = e;
    return rest;
  });
}

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
