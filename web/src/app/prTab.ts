import type { MooredPR, PRStack } from './api';

export type DetailTone = 'ok' | 'err' | 'muted' | 'rev';
export interface DetailChip {
  text: string;
  tone: DetailTone;
}
export type DotKind = 'ok' | 'err' | 'wait' | 'rev' | 'closed';

const todo = (): never => {
  throw new Error('not implemented');
};

export const openCount = (_prs: MooredPR[] | undefined): number => todo();
export const prSummary = (_prs: MooredPR[] | undefined): string => todo();
export const prDot = (_pr: MooredPR): DotKind => todo();
export const headerChips = (_pr: MooredPR): DetailChip[] => todo();
export const orderPRs = (_prs: MooredPR[], _stack: Pick<PRStack, 'entries'> | null | undefined): MooredPR[] => todo();
export const stackBarText = (_shown: number, _hasStack: boolean): string => todo();
