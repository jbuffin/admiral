import type { Item } from './api';

const VERBS: Record<string, [one: string, many: string]> = {
  Read: ['read 1 file', 'read {n} files'],
  Bash: ['ran a command', 'ran {n} commands'],
  Edit: ['edited 1 file', 'edited {n} files'],
  Write: ['edited 1 file', 'edited {n} files'],
};
// Edit and Write say the same thing, so they count together.
const VERB_OF: Record<string, string> = { Write: 'Edit', view: 'Read', bash: 'Bash', edit: 'Edit', create: 'Edit' };

const duration = (ms: number) => {
  const m = Math.floor(ms / 60_000);
  return m >= 1 ? `${m}m` : `${Math.max(1, Math.round(ms / 1000))}s`;
};

/** The folded row of a finished turn's tool calls. */
export function toolRowLabel(durationMs: number, toolNames: string[]): string {
  const counts = new Map<string, number>();
  let other = 0;
  for (const name of toolNames) {
    const verb = VERB_OF[name] ?? name;
    if (VERBS[verb]) counts.set(verb, (counts.get(verb) ?? 0) + 1);
    else other++;
  }
  const parts = [...counts].map(([verb, n]) => VERBS[verb][n === 1 ? 0 : 1].replace('{n}', String(n)));
  if (other > 0) parts.push(`used ${other} ${other === 1 ? 'tool' : 'tools'}`);
  return [`Worked for ${duration(durationMs)}`, ...(parts.length > 0 ? [parts.join(', ')] : [])].join(' · ');
}

/** How long the ship has been working, from the engineer's last message. */
export function workingLabel(items: { kind: string; at?: string }[], now: number): string {
  const last = [...items].reverse().find((i) => i.kind === 'user');
  const start = last?.at ? Date.parse(last.at) : NaN;
  if (Number.isNaN(start)) return 'Working';
  return `Working · ${Math.max(0, Math.floor((now - start) / 60_000))}m`;
}

const OUTPUT_LINES = 200;

/** A tool's output cut to its first lines; `total` is set only while lines are hidden. */
export function capOutput(text: string, all: boolean): { text: string; total?: number } {
  const lines = text.split('\n');
  if (all || lines.length <= OUTPUT_LINES) return { text };
  return { text: lines.slice(0, OUTPUT_LINES).join('\n'), total: lines.length };
}

export type Block =
  | { kind: 'item'; item: Item }
  | { kind: 'tools'; calls: Item[]; durationMs?: number };

// A finished turn's tool calls fold into one row where its first call was; a turn still running lists each call.
export function blocksOf(items: Item[]): Block[] {
  const blocks: Block[] = [];
  let group: Extract<Block, { kind: 'tools' }> | null = null;
  for (const item of items) {
    if (item.kind === 'tool_call') {
      if (!group) blocks.push((group = { kind: 'tools', calls: [] }));
      group.calls.push(item);
    } else if (item.kind === 'turn_end') {
      if (group) group.durationMs = item.durationMs ?? 0;
      group = null;
    } else {
      if (item.kind === 'user' || item.kind === 'compaction') group = null;
      blocks.push({ kind: 'item', item });
    }
  }
  return blocks;
}
