import type { MooredPR, PRStack } from './api';
import { prChips, type ChipTone } from './moored';

export type DetailTone = ChipTone | 'rev';
export interface DetailChip {
  text: string;
  tone: DetailTone;
}
export type DotKind = 'ok' | 'err' | 'wait' | 'rev' | 'closed';

const open = (prs: MooredPR[] | undefined) => (prs ?? []).filter((p) => p.state === 'OPEN');

export const openCount = (prs: MooredPR[] | undefined): number => open(prs).length;

/** The ship view's one line about its open PRs, or '' when none is open. The server's `ready` decides, and there is no per-PR feedback flag to name. */
export function prSummary(prs: MooredPR[] | undefined): string {
  const live = open(prs);
  if (live.length === 0) return '';
  const n = live.length === 1 ? '1 PR' : `${live.length} PRs`;
  const blocked = live.find((p) => !p.ready);
  if (!blocked) return `${n} · Ready to merge`;
  const why = blocked.review === 'CHANGES_REQUESTED' ? 'changes requested' : blocked.checks === 'FAILURE' || blocked.checks === 'ERROR' ? 'checks failing' : 'waiting on review';
  return `${n} · #${prNumber(blocked.url)} ${why}`;
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

/** The carried PRs bottom layer first by the stack's positions, then the carried ones outside the stack in the ship's order; stack entries the ship doesn't carry are left out. */
export function orderPRs(prs: MooredPR[], stack: Pick<PRStack, 'entries'> | null | undefined): MooredPR[] {
  if (!stack) return [...prs];
  const position = new Map(stack.entries.map((e) => [String(e.number), e.position]));
  const inStack = prs.filter((p) => position.has(prNumber(p.url))).sort((a, b) => position.get(prNumber(a.url))! - position.get(prNumber(b.url))!);
  return [...inStack, ...prs.filter((p) => !position.has(prNumber(p.url)))];
}

export const stackBarText = (shown: number, hasStack: boolean): string => (hasStack && shown >= 2 ? `Stacked on GitHub · ${shown} PRs, bottom to top.` : '');
