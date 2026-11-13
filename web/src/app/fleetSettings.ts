import { LIMIT_FIELDS } from './limitsForm';

export interface FleetFields {
  search_roots: string[];
  owners: string[];
  wake_reviewers: string[];
  clone_root: string;
  color: string;
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i]);

/** What a save sends: only the fields that changed, so one field's stored form is never rewritten by another's save. */
export function saveBody(shown: FleetFields, edited: FleetFields): Partial<FleetFields> {
  const body: Partial<FleetFields> = {};
  if (!sameList(shown.search_roots, edited.search_roots)) body.search_roots = edited.search_roots;
  if (!sameList(shown.owners, edited.owners)) body.owners = edited.owners;
  if (!sameList(shown.wake_reviewers, edited.wake_reviewers)) body.wake_reviewers = edited.wake_reviewers;
  if (shown.clone_root !== edited.clone_root) body.clone_root = edited.clone_root;
  if (shown.color !== edited.color) body.color = edited.color;
  return body;
}

export interface ErrorTarget {
  field: keyof FleetFields | 'limits';
  index?: number;
  /** The sentence field a Limits error sits under, such as `ceilings.t4`. */
  limit?: string;
}

/** The field a 400's `fleets.<key>.<field>[i]: …` message names; null sends it to the page top. */
export function errorTarget(message: string, fleetKey: string): ErrorTarget | null {
  const prefix = `fleets.${fleetKey}.`;
  if (!message.startsWith(prefix)) return null;
  const rest = message.slice(prefix.length);
  if (rest.startsWith('limits.')) {
    const path = rest.slice('limits.'.length).split(':')[0];
    const limit = LIMIT_FIELDS.filter((f) => path === f || path.startsWith(`${f}.`) || path.startsWith(`${f}[`)).sort((a, b) => b.length - a.length)[0];
    return limit ? { field: 'limits', limit } : null;
  }
  const m = /^(search_roots|owners|wake_reviewers)\[(\d+)\]|^(clone_root|color)\b/.exec(message.slice(prefix.length));
  if (!m) return null;
  return m[1] ? { field: m[1] as 'search_roots' | 'owners' | 'wake_reviewers', index: Number(m[2]) } : { field: m[3] as 'clone_root' | 'color' };
}

/** The server's message without its `fleets.<key>.<field>[i]: ` path, for showing beside the field it names. */
export function fieldMessage(message: string, fleetKey: string): string {
  const prefix = `fleets.${fleetKey}.`;
  return message.startsWith(prefix) ? message.replace(/^[^:]*:\s*/, '') : message;
}
