import type { Ship } from './api';

export type ChildStatus = 'ready' | 'blocked' | 'underway' | 'claimed-no-ship' | 'done';
export type Mode = 'with-you' | 'without-you';

export interface Blocker {
  url: string;
  title?: string;
  capped?: boolean;
}

export interface CampaignNode {
  number: number;
  title: string;
  url: string;
  parent?: boolean;
  status?: ChildStatus;
  mode?: Mode;
  blockers?: Blocker[];
  ship?: string;
  children?: CampaignNode[];
}

export type GroupKey = 'ready-with-you' | 'ready-without-you' | 'claimed' | 'blocked' | 'done';

export interface Row {
  node: CampaignNode;
  /** The title of the nested parent the child sits under; empty for a child of the root. */
  prefix: string;
}

export interface Group {
  key: GroupKey;
  title: string;
  rows: Row[];
}

/** The non-empty status groups of a campaign's tree, in the page's order, each in sub-issue order. */
export function groupChildren(_root: CampaignNode): Group[] {
  return [];
}

export function statusLabel(_status: ChildStatus): string {
  return '';
}

export function modeLabel(_mode: Mode): string {
  return '';
}

export type BlockerLine = { kind: 'link'; text: string; url: string } | { kind: 'note'; text: string };

export function blockerLine(_b: Blocker): BlockerLine {
  return { kind: 'note', text: '' };
}

/** A hint for a paste the server would refuse for its shape, or null when the text needs none. */
export function pasteHint(_text: string): string | null {
  return null;
}

/** The sidebar's second line: "Map #230 · 6 of 13". */
export function campaignMeta(_c: { number: number; progress: { closed: number; total: number } }): string {
  return '';
}

/** The ship a child's campaign launch started, if one is afloat. */
export function shipOfChild(_child: { url: string }, _ships: Ship[]): Ship | undefined {
  return undefined;
}

/** Changes whenever a ship of one of these children appears, goes or changes status. */
export function childShipsKey(_children: { url: string }[], _ships: Ship[]): string {
  return '';
}

/** Milliseconds until the next get of the open page; `fastForMs` is how long the page has polled fast. */
export function nextPoll(_s: { reading: boolean; fastForMs: number }): number {
  return 0;
}
