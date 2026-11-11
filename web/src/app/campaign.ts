import type { Ship } from './api';

export type ChildStatus = 'ready' | 'blocked' | 'underway' | 'claimed-no-ship' | 'done';
export type Mode = 'with-you' | 'without-you';

export interface Blocker {
  url: string;
  title?: string;
  capped?: boolean;
}

export interface SkillView {
  kind: 'commodore' | 'skill' | 'none' | '';
  name: string;
  args: string;
}

export interface CampaignNode {
  number: number;
  title: string;
  url: string;
  repo?: string;
  closed?: boolean;
  parent?: boolean;
  unreadable?: number;
  status?: ChildStatus;
  mode?: Mode;
  skill?: SkillView;
  labels?: string[];
  overridden?: boolean;
  default_mode?: Mode;
  default_skill?: SkillView;
  blockers?: Blocker[];
  ship?: string;
  children?: CampaignNode[];
}

export interface Campaign {
  id: string;
  url: string;
  fleet: string;
  work_repo: string;
  ended: boolean;
  last_read: string;
  read_error: string;
  progress: { closed: number; total: number };
  counts: Record<string, number>;
  title: string;
  number: number;
  repo: string;
  followed_at: string;
  ended_at?: string;
  ready_with_you: number;
  ready_without_you: number;
  reading: boolean;
  last_tried?: string;
  root?: CampaignNode;
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
  note: string;
  rows: Row[];
}

const GROUPS: { key: GroupKey; title: string; note: string }[] = [
  { key: 'ready-with-you', title: 'Ready, with you', note: 'needs your answers' },
  { key: 'ready-without-you', title: 'Ready, without you', note: 'AdmiralBot can run these later' },
  { key: 'claimed', title: 'Claimed, no ship', note: 'assigned on GitHub, nothing running here' },
  { key: 'blocked', title: 'Blocked', note: 'waits on GitHub blocks' },
  { key: 'done', title: 'Done', note: '' },
];

function groupOf(n: CampaignNode): GroupKey | null {
  switch (n.status) {
    case 'ready':
    case 'underway':
      return n.mode === 'without-you' ? 'ready-without-you' : 'ready-with-you';
    case 'claimed-no-ship':
      return 'claimed';
    case 'blocked':
      return 'blocked';
    case 'done':
      return 'done';
    default:
      return null;
  }
}

/** The non-empty status groups of a campaign's tree, in the page's order, each in sub-issue order. */
export function groupChildren(root: CampaignNode): Group[] {
  const by = new Map<GroupKey, Row[]>();
  const walk = (n: CampaignNode, prefix: string) => {
    for (const c of n.children ?? []) {
      if (c.parent) {
        walk(c, c.title);
        continue;
      }
      const key = groupOf(c);
      if (key) by.set(key, [...(by.get(key) ?? []), { node: c, prefix }]);
    }
  };
  walk(root, '');
  return GROUPS.filter((g) => by.has(g.key)).map((g) => ({ ...g, rows: by.get(g.key)! }));
}

const STATUS_LABELS: Record<ChildStatus, string> = {
  ready: 'Ready',
  blocked: 'Blocked',
  underway: 'Underway',
  'claimed-no-ship': 'Claimed, no ship',
  done: 'Done',
};

export const statusLabel = (status: ChildStatus): string => STATUS_LABELS[status];

export const modeLabel = (mode: Mode): string => (mode === 'with-you' ? 'With you' : 'Without you');

/** The skill a child's ship starts on, as the page words it. */
export function skillLabel(s: SkillView | undefined): string {
  if (!s || s.kind === 'none' || s.kind === '') return 'Just launch';
  if (s.kind === 'commodore') return s.args ? `Commodore ${s.args}` : 'Commodore';
  return `/${s.name}${s.args ? ` ${s.args}` : ''}`;
}

/** The kind label the design shows over the title: a wayfinder label makes it a Wayfinder map. */
export const kindLabel = (labels: string[] | undefined): string => ((labels ?? []).some((l) => l.startsWith('wayfinder')) ? 'Wayfinder map' : 'Campaign');

/** The type pill of a child, from its `wayfinder:<type>` label. */
export function typeLabel(labels: string[] | undefined): string {
  const t = (labels ?? []).map((l) => /^wayfinder:(.+)$/.exec(l)?.[1]).find((x) => x && x !== 'map');
  return t ? t.charAt(0).toUpperCase() + t.slice(1) : 'Task';
}

export type BlockerLine = { kind: 'link'; text: string; url: string } | { kind: 'note'; text: string };

export function blockerLine(b: Blocker): BlockerLine {
  if (b.capped) return { kind: 'note', text: b.title ?? '' };
  return { kind: 'link', text: b.title || b.url, url: b.url };
}

/** A hint for a paste the server would refuse for its shape, or null when the text needs none. */
export function pasteHint(text: string): string | null {
  const t = text.trim();
  if (/#issuecomment/i.test(t)) return "A link to a comment isn't accepted. Paste the issue's own URL.";
  if (t.endsWith('/')) return "A trailing slash isn't accepted. Remove it.";
  return null;
}

/** The sidebar's second line: "Map #230 · 6 of 13". */
export function campaignMeta(c: { number: number; progress: { closed: number; total: number } }): string {
  return `Map #${c.number} · ${c.progress.closed} of ${c.progress.total}`;
}

/** The ship a child's campaign launch started, if one is afloat. */
export function shipOfChild(child: { url: string }, ships: Ship[]): Ship | undefined {
  return ships.find((s) => s.source === 'campaign' && s.url === child.url);
}

/** Changes whenever a ship of one of these children appears, goes or changes status. */
export function childShipsKey(children: { url: string }[], ships: Ship[]): string {
  const urls = new Set(children.map((c) => c.url));
  return ships
    .filter((s) => s.source === 'campaign' && s.url && urls.has(s.url))
    .map((s) => `${s.session}:${s.status}`)
    .sort()
    .join(',');
}

export const FAST_POLL_MS = 3000;
export const SLOW_POLL_MS = 60_000;

/** Milliseconds until the next get of the open page; `fastForMs` is how long the page has polled fast. */
export function nextPoll(s: { reading: boolean; fastForMs: number }): number {
  return s.reading && s.fastForMs < SLOW_POLL_MS ? FAST_POLL_MS : SLOW_POLL_MS;
}

/** Every child below the root, nested parents' children included. */
export function leaves(root: CampaignNode | undefined): CampaignNode[] {
  return (root?.children ?? []).flatMap((c) => (c.parent ? leaves(c) : [c]));
}

/** Whether any node in the tree has sub-issues Admiral couldn't read. */
export const hasUnreadable = (n: CampaignNode | undefined): boolean => !!n && ((n.unreadable ?? 0) > 0 || (n.children ?? []).some(hasUnreadable));
