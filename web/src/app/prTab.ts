import type { MooredPR, PRStack, Ship } from './api';
import { isStackUrl, prChips, type ChipTone } from './moored';

export type DetailTone = ChipTone | 'rev';
export interface DetailChip {
  text: string;
  tone: DetailTone;
}
export type DotKind = 'ok' | 'err' | 'wait' | 'rev' | 'closed';

const open = (prs: MooredPR[] | undefined) => (prs ?? []).filter((p) => p.state === 'OPEN');

export const openCount = (prs: MooredPR[] | undefined): number => open(prs).length;

const by = (author: string | undefined) => (author ? ` by @${author}` : '');
const plural = (n: number) => (n === 1 ? '1 PR' : `${n} PRs`);

/** The ship view's one line about its open PRs, or '' when none is open. The server's `ready` decides; a reviewed PR never reads as ready to merge, since Admiral doesn't merge it. */
export function prSummary(prs: MooredPR[] | undefined): string {
  const live = open(prs);
  if (live.length === 0) return '';
  const reviewed = live.filter((p) => p.reviewing);
  if (reviewed.length > 0) return `Reviewing ${plural(reviewed.length)}${by(reviewed[0].author)} · ${reviewed.some((p) => p.responded) ? 'author responded' : 'waiting on the author'}`;
  const n = plural(live.length);
  const blocked = live.find((p) => !p.ready);
  if (!blocked) return `${n} · Ready to merge`;
  const why = blocked.review === 'CHANGES_REQUESTED' ? 'changes requested' : blocked.checks === 'FAILURE' || blocked.checks === 'ERROR' ? 'checks failing' : 'waiting on review';
  return `${n} · #${prNumber(blocked.url)} ${why}`;
}

/** The feedback bar's text for a PR, or '' when there is no bar. */
export function feedbackBarText(pr: MooredPR): string {
  if (!pr.answer || !pr.feedback || pr.state !== 'OPEN') return '';
  const items = pr.feedback === 1 ? '1 new item' : `${pr.feedback} new items`;
  return `New feedback since the ship's last round: ${pr.review === 'CHANGES_REQUESTED' ? 'changes requested, ' : ''}${items}.`;
}

export const prNumber = (url: string): string => /\/pull\/(\d+)/.exec(url)?.[1] ?? '';

export function prDot(pr: MooredPR): DotKind {
  if (pr.state === 'MERGED') return 'rev';
  if (pr.state === 'CLOSED') return 'closed';
  if (pr.review === 'CHANGES_REQUESTED' || pr.checks === 'FAILURE' || pr.checks === 'ERROR') return 'err';
  if (pr.review === 'APPROVED' && pr.checks === 'SUCCESS') return 'ok';
  return 'wait';
}

export function headerChips(pr: MooredPR): DetailChip[] {
  return prChips(pr).map((c): DetailChip => (pr.state === 'MERGED' ? { ...c, tone: 'rev' } : c));
}

/** The carried PRs that are in the stack, bottom layer first by its positions; an entry matches a PR by URL, so another repo's PR with the same number is not in it. */
export function stackedPRs(prs: MooredPR[], stack: Pick<PRStack, 'entries'> | null | undefined): MooredPR[] {
  if (!stack) return [];
  const position = new Map(stack.entries.map((e) => [e.url, e.position]));
  return prs.filter((p) => position.has(p.url)).sort((a, b) => position.get(a.url)! - position.get(b.url)!);
}

/** The stack first, then the carried PRs outside it in the ship's order; stack entries the ship doesn't carry are left out. */
export function orderPRs(prs: MooredPR[], stack: Pick<PRStack, 'entries'> | null | undefined): MooredPR[] {
  if (!stack) return [...prs];
  const inStack = stackedPRs(prs, stack);
  return [...inStack, ...prs.filter((p) => !inStack.includes(p))];
}

/** The PR the tab opens on: the first open one in stack order, or the bottom one when none is open. */
export const defaultPR = (ordered: MooredPR[]): MooredPR | undefined => ordered.find((p) => p.state === 'OPEN') ?? ordered[0];

/** The role mark in a PR's header: `Reviewing · by @author`, `Reviewing` before a poll has the author, '' for an own PR. */
export const reviewMark = (pr: Pick<MooredPR, 'reviewing' | 'author'>): string => (pr.reviewing ? `Reviewing${pr.author ? ` · by @${pr.author}` : ''}` : '');

/** The stack bar of a ship reviewing a GitHub stack, or '' for any other ship. */
export function reviewStackBar(ship: Pick<Ship, 'url' | 'prs'>): string {
  // A PR-URL ship that also carries its own PRs is no stack, so the order's URL decides.
  if (!isStackUrl(ship.url)) return '';
  const layers = (ship.prs ?? []).filter((p) => p.reviewing);
  return layers.length >= 2 ? `Reviewing a GitHub stack${by(layers[0].author)} · ${layers.length} PRs, bottom to top.` : '';
}

export const stackBarText = (stacked: number, hasStack: boolean): string => (hasStack && stacked >= 2 ? `Stacked on GitHub · ${stacked} PRs, bottom to top.` : '');

/** The detail that holds the stack: the first carried PR's, with its stack and answer_all. */
export function chooseStackDetail(
  carried: MooredPR[],
  details: Record<string, { stack: PRStack | null; answer_all?: string } | undefined>,
): { url: string; stack: PRStack; answerAll?: string } | null {
  for (const p of carried) {
    const d = details[p.url];
    if (d?.stack) return { url: p.url, stack: d.stack, answerAll: d.answer_all };
  }
  return null;
}

/** Whether the stack bar offers Answer all reviews. The pushed list decides when feedback clears, ahead of the detail's refresh. */
export function showAnswerAll(prs: MooredPR[], stack: PRStack | null | undefined, answerAll: string | undefined): boolean {
  const live = open(prs);
  return !!stack && !!answerAll && live.length > 1 && live.every((p) => (p.feedback ?? 0) > 0);
}

/** Whether Merge and Don't follow reviews stay locked: a confirm locks them, and a ship that left its mooring is running its wake, so they are free when it moors again. */
export const actingWhile = (acting: boolean, moored: boolean): boolean => moored && acting;
