import type { FixOffer, MooredPR, PRStack, Ship, ShipStatus, StackShape } from './api';
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

/** The ship view's one line about its open PRs, or '' when none is open. The server's `ready` and `stack_ready` decide; a reviewed PR never reads as ready to merge, since Admiral doesn't merge it. */
export function prSummary(prs: MooredPR[] | undefined, stack?: { stack_ready?: boolean; fix?: FixOffer }): string {
  const live = open(prs);
  if (live.length === 0) return '';
  const reviewed = live.filter((p) => p.reviewing);
  if (reviewed.length > 0) return `Reviewing ${plural(reviewed.length)}${by(reviewed[0].author)} · ${reviewed.some((p) => p.responded) ? 'author responded' : 'waiting on the author'}`;
  const n = plural(live.length);
  const blocked = live.find((p) => !p.ready);
  if (!blocked) {
    const why = stack?.stack_ready === false ? fixItems(stack.fix).join(' \u00b7 ') : '';
    return why ? `${n} · ${why}` : stack?.stack_ready === false ? `${n} · Not ready to merge` : `${n} · Ready to merge`;
  }
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

/** The stack bar of a ship reviewing a GitHub stack: its bold lead and the rest, or null for any other ship. */
export function reviewStackBar(ship: Pick<Ship, 'url' | 'prs'>): { lead: string; rest: string } | null {
  // A PR-URL ship that also carries its own PRs is no stack, so the order's URL decides.
  const prs = ship.prs ?? [];
  const layers = prs.filter((p) => p.reviewing);
  if (!isStackUrl(ship.url) || prs.length < 2 || layers.length === 0) return null;
  return { lead: 'Reviewing a GitHub stack', rest: `${by(layers[0].author)} · ${layers.length} ${layers.length === 1 ? 'PR' : 'PRs'}, bottom to top.` };
}

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
export function showAnswerAll(prs: MooredPR[], stack: PRStack | null | undefined, answerAll: string | undefined, fix?: FixOffer): boolean {
  const live = open(prs);
  return !fix && !!stack && !!answerAll && live.length > 1 && live.every((p) => (p.feedback ?? 0) > 0);
}

/** Whether Merge and Don't follow reviews stay locked: a confirm locks them, and a ship that left its mooring is running its wake, so they are free when it moors again. */
export const actingWhile = (acting: boolean, moored: boolean): boolean => moored && acting;

/** The stack bar's bold lead and the rest of its line for the shape the server read, or null when no bar shows. */
export function stackBarLine(shape: StackShape | null): { lead: string; rest: string } | null {
  if (!shape || shape.prs.length < 2) return null;
  const n = shape.prs.length;
  if (shape.shape === 'stacked') return { lead: 'Stacked on GitHub', rest: ` · stack #${shape.stack_number} · ${n} PRs, bottom to top. Merge takes the whole stack.` };
  if (shape.shape === 'chained') return { lead: 'Not a GitHub stack.', rest: ` These ${n} PRs build on each other branch by branch. Stacking them lets GitHub merge them together.` };
  if (shape.shape === 'mixed') return { lead: 'Not one chain or stack.', rest: ' Merge these PRs on GitHub.' };
  return null;
}

/** What the bar offers beside its line: Stack on GitHub on a moored ship, the words for a ship still out, or nothing. */
export function stackBarAction(shape: StackShape | null, status: ShipStatus): 'stack' | 'after-moor' | 'none' {
  if (shape?.shape !== 'chained' || shape.prs.length < 2) return 'none';
  return status === 'moored' ? 'stack' : status === 'underway' || status === 'needs_you' ? 'after-moor' : 'none';
}

export interface MergeChoice {
  kind: 'none' | 'single' | 'stack' | 'pr';
  /** The line shown where Merge would be when there is none. */
  note: string;
}

/** Which Merge the selected sub-tab of a moored ship shows, from the shape the server read; the client never classifies PRs itself. */
export function mergeFor(shape: StackShape | null, prs: MooredPR[], current: MooredPR, stackReady: boolean): MergeChoice {
  const none: MergeChoice = { kind: 'none', note: '' };
  const live = current.state === 'OPEN';
  if (prs.length < 2) return stackReady ? { kind: 'single', note: '' } : none;
  switch (shape?.shape) {
    case 'stacked':
      return live && stackReady ? { kind: 'stack', note: '' } : none;
    case 'chained':
      return live && stackReady ? { kind: 'none', note: 'Stack them on GitHub to merge together' } : none;
    case 'independent':
      return live && !!current.ready ? { kind: 'pr', note: '' } : none;
    default:
      return none;
  }
}

/** The sub-tabs' order: the shape's `prs` for a chained or stacked shape, else the details' stack through orderPRs. */
export function tabOrder(carried: MooredPR[], shape: StackShape | null, stack: Pick<PRStack, 'entries'> | null | undefined): MooredPR[] {
  if (shape && (shape.shape === 'chained' || shape.shape === 'stacked')) {
    const named = shape.prs.flatMap((u) => carried.filter((p) => p.url === u));
    return [...named, ...carried.filter((p) => !named.includes(p))];
  }
  return orderPRs(carried, stack);
}

/** The Fix button's label: Fix stack when the server's round covers more than one PR, else Fix PR. */
export const fixLabel = (ship: Pick<Ship, 'fix'>): 'Fix PR' | 'Fix stack' => (ship.fix?.stack ? 'Fix stack' : 'Fix PR');

/** Whether Fix stack sits in the stack bar, where Answer all reviews would, or in a bar of its own: the bar's own Re-review stack takes its place, so the Fix bar stands alone. */
export const fixStackInBar = (hasBar: boolean, reReviewsStack: boolean): boolean => hasBar && !reReviewsStack;

const joinAnd = (parts: string[]): string => (parts.length < 2 ? parts.join('') : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`);

function problemWords(p: FixOffer['problems'][number]): string {
  const parts: string[] = [];
  if (p.conflicts) parts.push('has merge conflicts');
  if (p.behind) parts.push('is behind its base');
  if (p.failing_checks?.length) parts.push(`has failing checks: ${p.failing_checks.join(', ')}`);
  if (p.conflicts_with_base) parts.push(`conflicts with ${p.conflicts_with_base}`);
  return joinAnd(parts);
}

/** One entry per PR with problems, `#N` and its problems in words, for the Fix sheet's list and the stack's line. */
export const fixItems = (fix: FixOffer | undefined): string[] => (fix?.problems ?? []).map((p) => `#${prNumber(p.pr)} ${problemWords(p)}`);

/** The fix bar's line naming what keeps the PRs from merging, from the served offer; '' when there is none. A stack round names each PR even when one has problems. */
export function fixLine(fix: FixOffer | undefined): string {
  const problems = fix?.problems ?? [];
  if (problems.length === 0) return '';
  if (problems.length === 1 && !fix?.stack) return `This PR ${problemWords(problems[0])}.`;
  return fixItems(fix).join(' \u00b7 ');
}

/** Whether a PR's feedback bar offers Answer review: the ship's Fix round takes the feedback with it, so Fix replaces it. */
export const showAnswerReview = (pr: MooredPR, fix: FixOffer | undefined): boolean => !fix && feedbackBarText(pr) !== '';
