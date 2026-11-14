export const KEY_HINT = 'J K move · 1–3 answer · ↵ bridge · E remove';

const MERGE_KEY_HINT = 'J K move · ↵ bridge';

export type KeyAct = { kind: 'move'; by: 1 | -1 } | { kind: 'bridge' } | { kind: 'remove' } | { kind: 'pick'; n: 1 | 2 | 3 };

interface KeyEvent {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  target?: object | null;
}

const TYPING = new Set(['INPUT', 'TEXTAREA', 'SELECT']);

/** The hint line under the Inbox; a merge entry takes no answer or remove key. */
export const keyHint = (type?: 'item' | 'stop' | 'merge' | 'rereview'): string => (type === 'merge' ? MERGE_KEY_HINT : KEY_HINT);

/** What a key press means on the Inbox, or null when the browser or a field owns it. */
export function keyAction(e: KeyEvent, type?: 'item' | 'stop' | 'merge' | 'rereview'): KeyAct | null {
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  const t = e.target as { tagName?: string; isContentEditable?: boolean } | null | undefined;
  if (t && (TYPING.has((t.tagName ?? '').toUpperCase()) || t.isContentEditable)) return null;
  const k = e.key.toLowerCase();
  // Merging is irreversible, so it has no key, and a merge entry has nothing to answer or remove.
  if (type === 'merge' && k !== 'j' && k !== 'k' && e.key !== 'Enter') return null;
  if (k === 'j') return { kind: 'move', by: 1 };
  if (k === 'k') return { kind: 'move', by: -1 };
  if (e.key === 'Enter') return { kind: 'bridge' };
  if (k === 'e') return { kind: 'remove' };
  if (e.key === '1' || e.key === '2' || e.key === '3') return { kind: 'pick', n: Number(e.key) as 1 | 2 | 3 };
  return null;
}
