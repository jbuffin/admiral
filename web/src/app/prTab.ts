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

/** The stack to order by: the first one a carried PR's detail returns, in the ship's order. */
export function chooseStack(carried: MooredPR[], details: Record<string, { stack: PRStack | null } | undefined>): PRStack | null {
  for (const p of carried) {
    const stack = details[p.url]?.stack;
    if (stack) return stack;
  }
  return null;
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

export const stackBarText = (stacked: number, hasStack: boolean): string => (hasStack && stacked >= 2 ? `Stacked on GitHub · ${stacked} PRs, bottom to top.` : '');
