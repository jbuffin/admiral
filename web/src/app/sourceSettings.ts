// What the server sends in place of a stored secret, and takes back to keep it.
export const UNCHANGED = '__admiral_unchanged__';

export interface Prop {
  type?: string;
  title?: string;
  description?: string;
  format?: string;
  writeOnly?: boolean;
  enum?: unknown[];
  items?: Prop;
  properties?: Record<string, Prop>;
}
export interface Schema {
  properties?: Record<string, Prop>;
  required?: string[];
}

export type Kind = 'enum' | 'boolean' | 'number' | 'text' | 'strings' | 'objects' | 'json';
export interface FieldKind {
  kind: Kind;
  /** The server holds a value it did not send. */
  stored: boolean;
}
export type Settings = Record<string, unknown>;
export type Built = { ok: true; settings: Settings } | { ok: false; error: string };

export interface DefaultView {
  id: string;
  title: string;
  query: string;
}
// The GitHub source's built-in views (internal/github/settings.go).
export const DEFAULT_VIEWS: DefaultView[] = [
  { id: 'assigned-to-me', title: 'Assigned to me', query: 'is:open assignee:@me' },
  { id: 'review-requested', title: 'Review requested', query: 'is:open is:pr review-requested:@me' },
  { id: 'my-prs', title: 'My PRs', query: 'is:open is:pr author:@me' },
];

const scalar = (p: Prop) => ['string', 'number', 'integer', 'boolean'].includes(p.type ?? '');
const flaggedSecret = (p: Prop) => p.format?.toLowerCase() === 'password' || p.writeOnly === true;
// A row field cannot tell a kept secret from a cleared one, so lists of secrets stay JSON.
const isStrings = (p: Prop) => p.type === 'array' && p.items?.type === 'string' && !p.items.enum && !flaggedSecret(p) && !flaggedSecret(p.items);
const isObjects = (p: Prop) =>
  p.type === 'array' &&
  p.items?.type === 'object' &&
  !!p.items.properties &&
  !flaggedSecret(p) &&
  !flaggedSecret(p.items) &&
  Object.values(p.items.properties).every((f) => scalar(f) && !f.enum && !flaggedSecret(f));

/** Decided once at load, from the schema and what the server stored and masked: a list holding the marker stays a JSON field so it round-trips untouched. */
export function fieldKinds(schema: Schema | undefined, stored: Settings): Record<string, FieldKind> {
  const out: Record<string, FieldKind> = {};
  for (const [k, p] of Object.entries(schema?.properties ?? {})) {
    const v = stored[k];
    if (v === UNCHANGED) out[k] = { kind: 'text', stored: true };
    else if (p.enum) out[k] = { kind: 'enum', stored: false };
    else if (p.type === 'boolean') out[k] = { kind: 'boolean', stored: false };
    else if (p.type === 'number' || p.type === 'integer') out[k] = { kind: 'number', stored: false };
    else if (p.type === 'string') out[k] = { kind: 'text', stored: false };
    else if ((isStrings(p) || isObjects(p)) && (v === undefined || (Array.isArray(v) && !JSON.stringify(v).includes(UNCHANGED)))) {
      out[k] = { kind: isStrings(p) ? 'strings' : 'objects', stored: false };
    } else out[k] = { kind: 'json', stored: false };
  }
  return out;
}

export const jsonText = (v: unknown) => (v === undefined ? '' : JSON.stringify(v, null, 2));

const nonEmptyRow = (r: unknown) => (typeof r === 'string' ? r !== '' : typeof r === 'object' && r !== null && Object.keys(r).length > 0);

/** The settings a save sends: the stored ones, with only the touched keys (`edits`) replaced. */
export function buildSettings(schema: Schema | undefined, stored: Settings, edits: Settings): Built {
  const kinds = fieldKinds(schema, stored);
  const out: Settings = { ...stored };
  for (const [k, v] of Object.entries(edits)) {
    const kind = kinds[k] ?? { kind: 'json', stored: false };
    if (v === undefined) delete out[k];
    else if (kind.kind === 'text') {
      if (v === '') {
        if (kind.stored) out[k] = UNCHANGED;
        else delete out[k];
      } else out[k] = v;
    } else if (kind.kind === 'strings' || kind.kind === 'objects') {
      const rows = (Array.isArray(v) ? v : []).filter(nonEmptyRow);
      if (rows.length === 0) delete out[k];
      else out[k] = rows;
    } else if (kind.kind === 'json') {
      const text = String(v);
      if (text.trim() === '') delete out[k];
      else {
        try {
          out[k] = JSON.parse(text);
        } catch {
          return { ok: false, error: 'The settings must be valid JSON.' };
        }
      }
    } else out[k] = v;
  }
  return { ok: true, settings: out };
}

/** A source without a schema has one JSON field for all its settings. */
export function parseWholeJson(text: string): Built {
  try {
    const v: unknown = JSON.parse(text);
    if (v !== null && typeof v === 'object' && !Array.isArray(v)) return { ok: true, settings: v as Settings };
  } catch {
    /* falls through to the error */
  }
  return { ok: false, error: 'The settings must be valid JSON and an object.' };
}

const slug = (title: string) => title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'panel';

/** The default view a custom view with this title takes the place of. */
export const replacedDefault = (title: string): DefaultView | null => DEFAULT_VIEWS.find((d) => d.id === slug(title)) ?? null;
