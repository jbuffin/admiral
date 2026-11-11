const VERBS: Record<string, [one: string, many: string]> = {
  Read: ['read 1 file', 'read {n} files'],
  Bash: ['ran a command', 'ran {n} commands'],
  Edit: ['edited 1 file', 'edited {n} files'],
  Write: ['edited 1 file', 'edited {n} files'],
};
// Edit and Write say the same thing, so they count together.
const VERB_OF: Record<string, string> = { Write: 'Edit' };

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
