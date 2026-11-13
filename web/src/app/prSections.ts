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
  /** `path:line`, `path` when the line is 0, '' for a PR comment. */
  ref: string;
  resolved: boolean;
  body: string;
  replies: Reply[];
}

/** The description's Markdown source: the body with its HTML comments removed. */
export function descriptionSource(_body: string): string {
  throw new Error('not built');
}

/** One row per person: the latest verdict, in the order they first reviewed, then the requested reviewers who have not. */
export function reviewerRows(_reviews: PRDetail['reviews'], _requested: PRActor[]): ReviewerRow[] {
  throw new Error('not built');
}

export function reviewSummary(_rows: ReviewerRow[]): string {
  throw new Error('not built');
}

export function checksLine(_d: Pick<PRDetail, 'checks' | 'failing_checks'>): { tone: Tone; text: string } {
  throw new Error('not built');
}

/** The URL a "Log" link may open, or null when GitHub sent something that is not http(s). */
export function logHref(_url: string): string | null {
  throw new Error('not built');
}

/** Threads and PR comments in time order, oldest first, the resolved threads apart. */
export function prTimeline(_d: Pick<PRDetail, 'threads' | 'comments'>): { open: TimelineItem[]; resolved: TimelineItem[] } {
  throw new Error('not built');
}

export function resolvedLabel(_n: number): string {
  throw new Error('not built');
}

export function firstLine(_text: string): string {
  throw new Error('not built');
}

export function relativeTime(_iso: string, _nowMs: number): string {
  throw new Error('not built');
}
