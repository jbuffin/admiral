// Module under test: web/src/app/sourceSettings.ts (pure; no imports from web/src/SettingsDialog.tsx). It exports:
//   UNCHANGED                     '__admiral_unchanged__'
//   DEFAULT_VIEWS                 [{ id, title, query }] x3, the GitHub source's defaults
//   fieldKinds(schema, stored)    key -> { kind, stored }; kind is 'enum' | 'boolean' | 'number' | 'text' | 'strings' | 'objects' | 'json'
//   jsonText(value)               the text a JSON field starts with ('' when the value is undefined)
//   buildSettings(schema, stored, edits)
//                                 -> { ok: true, settings } | { ok: false, error }
//                                 edits holds only what the person touched: key -> typed value
//                                 (the text for a 'json' field, the array for 'strings'/'objects', undefined to clear)
//   parseWholeJson(text)          for a source with no schema -> { ok: true, settings } | { ok: false, error }
//   replacedDefault(title)        the default view a custom title replaces (by its slug), or null
import { describe, expect, it } from 'vitest';
import { DEFAULT_VIEWS, UNCHANGED, buildSettings, fieldKinds, jsonText, parseWholeJson, replacedDefault } from './sourceSettings';

const schema = {
  type: 'object',
  properties: {
    label: { type: 'string' },
    token: { type: 'string', format: 'password' },
    // The server masks a string whose schema holds a keyword it does not follow, whatever the format says.
    odd: { type: 'string', pattern: '^x', 'x-vendor': true },
    hosts: { type: 'array', items: { type: 'string' } },
    tokens: { type: 'array', items: { type: 'string', format: 'password' } },
    keys: { type: 'array', items: { type: 'object', properties: { name: { type: 'string' }, key: { type: 'string', format: 'password' } } } },
    peers: { type: 'array', items: { type: 'object', properties: { name: { type: 'string' }, port: { type: 'integer' } } } },
    options: { type: 'object' },
  },
};

const ok = (r: ReturnType<typeof buildSettings>) => {
  if (!r.ok) throw new Error(r.error);
  return r.settings;
};

describe('fieldKinds', () => {
  it('draws a kept secret as an empty text field that knows a value is stored', () => {
    const k = fieldKinds(schema, { token: UNCHANGED });
    expect(k.token).toEqual({ kind: 'text', stored: true });
  });

  it('never draws the marker, even when the schema does not call the field a password', () => {
    const k = fieldKinds(schema, { odd: UNCHANGED, label: 'plain' });
    expect(k.odd).toEqual({ kind: 'text', stored: true });
    expect(k.label).toEqual({ kind: 'text', stored: false });
  });

  it('draws a plain list as rows, with the key absent or present', () => {
    expect(fieldKinds(schema, {}).hosts.kind).toBe('strings');
    expect(fieldKinds(schema, { hosts: ['a.example'] }).hosts.kind).toBe('strings');
    expect(fieldKinds(schema, { peers: [{ name: 'a', port: 1 }] }).peers.kind).toBe('objects');
  });

  it('keeps a list whose stored value holds the marker as a JSON field', () => {
    const k = fieldKinds(schema, { tokens: [UNCHANGED, UNCHANGED], keys: [{ name: 'a', key: UNCHANGED }] });
    expect(k.tokens.kind).toBe('json');
    expect(k.keys.kind).toBe('json');
  });

  it('keeps a list of secrets and a stored non-array value as JSON fields', () => {
    expect(fieldKinds(schema, {}).tokens.kind).toBe('json');
    expect(fieldKinds(schema, {}).keys.kind).toBe('json');
    expect(fieldKinds(schema, { hosts: 'not-a-list' }).hosts.kind).toBe('json');
    expect(fieldKinds(schema, {}).options.kind).toBe('json');
  });
});

describe('buildSettings', () => {
  it('sends the marker for a kept secret nobody typed into', () => {
    const s = ok(buildSettings(schema, { label: 'a', token: UNCHANGED }, { label: 'b' }));
    expect(s).toEqual({ label: 'b', token: UNCHANGED });
  });

  it('sends the marker when a stored secret is emptied again', () => {
    const s = ok(buildSettings(schema, { token: UNCHANGED }, { token: '' }));
    expect(s.token).toBe(UNCHANGED);
  });

  it('sends what was typed over a stored secret', () => {
    const s = ok(buildSettings(schema, { token: UNCHANGED }, { token: 'decoy-secret-ab12' }));
    expect(s.token).toBe('decoy-secret-ab12');
  });

  it('leaves a cleared secret out of the save', () => {
    const s = ok(buildSettings(schema, { token: UNCHANGED, label: 'a' }, { token: undefined }));
    expect(s).toEqual({ label: 'a' });
  });

  it('sends the marker for a stored field the schema does not call a password', () => {
    const s = ok(buildSettings(schema, { odd: UNCHANGED }, {}));
    expect(s.odd).toBe(UNCHANGED);
  });

  it('round-trips markers inside a list untouched', () => {
    const stored = { tokens: [UNCHANGED, UNCHANGED], keys: [{ name: 'a', key: UNCHANGED }] };
    const s = ok(buildSettings(schema, stored, {}));
    expect(s).toEqual(stored);
    const again = ok(buildSettings(schema, stored, { tokens: jsonText(stored.tokens), keys: jsonText(stored.keys) }));
    expect(again).toEqual(stored);
  });

  it('takes an edited JSON field and keeps a marker left in its text', () => {
    const stored = { keys: [{ name: 'a', key: UNCHANGED }] };
    const text = jsonText(stored.keys).replace('"a"', '"renamed"');
    expect(ok(buildSettings(schema, stored, { keys: text })).keys).toEqual([{ name: 'renamed', key: UNCHANGED }]);
  });

  it('refuses a JSON field that is not valid JSON', () => {
    const r = buildSettings(schema, { tokens: [UNCHANGED] }, { tokens: '[oops' });
    expect(r.ok).toBe(false);
  });

  it('drops rows nothing was typed into, and the key when none are left', () => {
    expect(ok(buildSettings(schema, { hosts: ['a'] }, { hosts: ['a', '', 'b'] })).hosts).toEqual(['a', 'b']);
    expect('hosts' in ok(buildSettings(schema, { hosts: ['a'] }, { hosts: [''] }))).toBe(false);
    expect(ok(buildSettings(schema, {}, { peers: [{ name: 'a', port: 2 }, {}] })).peers).toEqual([{ name: 'a', port: 2 }]);
  });

  it('deletes a JSON field emptied of text', () => {
    expect('options' in ok(buildSettings(schema, { options: { a: 1 } }, { options: '  ' }))).toBe(false);
  });
});

describe('parseWholeJson', () => {
  it('takes an object and keeps its markers', () => {
    const r = parseWholeJson(`{"a": "${UNCHANGED}", "list": ["${UNCHANGED}"]}`);
    expect(r).toEqual({ ok: true, settings: { a: UNCHANGED, list: [UNCHANGED] } });
  });

  it('refuses text that is not an object', () => {
    expect(parseWholeJson('[1]').ok).toBe(false);
    expect(parseWholeJson('null').ok).toBe(false);
    expect(parseWholeJson('{nope').ok).toBe(false);
  });
});

describe('default views', () => {
  it('lists the three the GitHub source always has', () => {
    expect(DEFAULT_VIEWS.map((v) => v.title)).toEqual(['Assigned to me', 'Review requested', 'My PRs']);
    expect(DEFAULT_VIEWS.map((v) => v.query)).toEqual(['is:open assignee:@me', 'is:open is:pr review-requested:@me', 'is:open is:pr author:@me']);
  });

  it('names the default a custom title replaces, by its slug', () => {
    expect(replacedDefault('my prs')?.title).toBe('My PRs');
    expect(replacedDefault(' Review  Requested! ')?.title).toBe('Review requested');
    expect(replacedDefault('Bugs')).toBeNull();
    expect(replacedDefault('')).toBeNull();
  });
});
