import type { PRActor, PRDetail } from './api';

export type Tone = 'ok' | 'err' | 'muted';
export type Verdict = 'Approved' | 'Changes requested' | 'Commented' | 'Dismissed' | 'Waiting';
export interface ReviewerRow {
  login: string;
  bot: boolean;
  verdict: Verdict;
  tone: Tone;
}
export interface Reply {
  author: PRActor;
  body: string;
  at: string;
}
export interface TimelineItem {
  kind: 'thread' | 'comment';
  author: PRActor;
  at: string;
  /** The first comment's GitHub URL, which names the item across refreshes. */
  url: string;
  /** `path:line`, `path` when the line is 0, '' for a PR comment. */
  ref: string;
  resolved: boolean;
  body: string;
  replies: Reply[];
}

/** The description's Markdown source: the body with its HTML comments removed. */
export function descriptionSource(body: string): string {
  return body.replace(/<!--[\s\S]*?-->/g, '');
}

const loginOf = (a: PRActor) => a.login || 'ghost';
const DECIDED = new Set(['APPROVED', 'CHANGES_REQUESTED', 'DISMISSED']);
const VERDICTS: Record<string, [Verdict, Tone]> = {
  APPROVED: ['Approved', 'ok'],
  CHANGES_REQUESTED: ['Changes requested', 'err'],
  DISMISSED: ['Dismissed', 'muted'],
  COMMENTED: ['Commented', 'muted'],
};

/** One row per person: the latest verdict, in the order they first reviewed, then the requested reviewers who have not. */
export function reviewerRows(reviews: PRDetail['reviews'], requested: PRActor[]): ReviewerRow[] {
  const latest = new Map<string, { bot: boolean; state: string }>();
  for (const r of reviews) {
    if (r.state === 'PENDING') continue;
    const login = loginOf(r.author);
    const seen = latest.get(login);
    if (!seen) latest.set(login, { bot: r.author.bot, state: r.state });
    else if (DECIDED.has(r.state) || !DECIDED.has(seen.state)) seen.state = r.state;
  }
  const rows: ReviewerRow[] = [...latest].map(([login, { bot, state }]) => {
    const [verdict, tone] = VERDICTS[state] ?? VERDICTS.COMMENTED;
    return { login, bot, verdict, tone };
  });
  for (const a of requested) {
    const login = loginOf(a);
    if (!latest.has(login)) rows.push({ login, bot: a.bot, verdict: 'Waiting', tone: 'muted' });
  }
  return rows;
}

const SUMMARY_ORDER: Verdict[] = ['Approved', 'Changes requested', 'Commented', 'Dismissed', 'Waiting'];

export function reviewSummary(rows: ReviewerRow[]): string {
  return SUMMARY_ORDER.flatMap((v) => {
    const n = rows.filter((r) => r.verdict === v).length;
    return n ? [`${n} ${v.toLowerCase()}`] : [];
  }).join(' · ');
}

export function checksLine({ checks, failing_checks }: Pick<PRDetail, 'checks' | 'failing_checks'>): { tone: Tone; text: string } {
  if (failing_checks.length) return { tone: 'err', text: `${failing_checks.length} failing` };
  if (checks === 'SUCCESS') return { tone: 'ok', text: 'All checks passed' };
  if (checks === 'FAILURE' || checks === 'ERROR') return { tone: 'err', text: 'Checks failing' };
  if (checks === 'PENDING' || checks === 'EXPECTED') return { tone: 'muted', text: 'Checks running' };
  return { tone: 'muted', text: 'No checks' };
}

/** The URL a "Log" link may open, or null when GitHub sent something that is not http(s). */
export function logHref(url: string): string | null {
  return /^https?:\/\//i.test(url) ? url : null;
}

/** Threads and PR comments in time order, oldest first, the resolved threads apart. */
export function prTimeline({ threads, comments }: Pick<PRDetail, 'threads' | 'comments'>): { open: TimelineItem[]; resolved: TimelineItem[] } {
  const items: TimelineItem[] = [];
  for (const t of threads) {
    const [first, ...rest] = t.comments;
    if (!first) continue;
    items.push({
      kind: 'thread',
      author: first.author,
      at: first.created_at,
      url: first.url,
      ref: t.line ? `${t.path}:${t.line}` : t.path,
      resolved: t.resolved,
      body: first.body,
      replies: rest.map((c) => ({ author: c.author, body: c.body, at: c.created_at })),
    });
  }
  for (const c of comments) items.push({ kind: 'comment', author: c.author, at: c.created_at, url: c.url, ref: '', resolved: false, body: c.body, replies: [] });
  items.sort((a, b) => Date.parse(a.at) - Date.parse(b.at));
  return { open: items.filter((i) => !i.resolved), resolved: items.filter((i) => i.resolved) };
}

export function resolvedLabel(n: number): string {
  return `${n} resolved thread${n === 1 ? '' : 's'}`;
}

export function firstLine(text: string): string {
  return text.split(/\r?\n/).map((l) => l.trim()).find((l) => l) ?? '';
}

export function relativeTime(iso: string, nowMs: number): string {
  const s = Math.floor((nowMs - Date.parse(iso)) / 1000);
  if (!(s >= 60)) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86_400)}d ago`;
}
