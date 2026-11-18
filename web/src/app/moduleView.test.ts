// A module page's chip tones, client filter and open-row refetch rule.
import { readFileSync } from 'node:fs';
import { expect, test } from 'vitest';
import type { PageGroup } from './api';
import { chipTone, filterGroups, shouldRefetchRow } from './moduleView';

const groups: PageGroup[] = [
  {
    id: 'review',
    title: 'Needs review',
    rows: [
      { id: 'a', title: 'Fix login', subtitle: 'Payments team', chips: [{ label: 'P1', tone: 'warn' }], reason: 'Review is open' },
      { id: 'b', title: 'Rotate keys', subtitle: '', chips: [{ label: 'security' }], reason: '' },
    ],
  },
  { id: 'done', title: 'Done', rows: [{ id: 'c', title: 'Ship docs', subtitle: 'Docs', chips: [], reason: 'Merged last week' }] },
];

const ids = (gs: PageGroup[]) => gs.map((g) => [g.id, g.rows.map((r) => r.id)]);

test.each([
  ['good', 'good'],
  ['warn', 'warn'],
  ['muted', 'muted'],
  ['neutral', 'neutral'],
  ['', 'neutral'],
  [undefined, 'neutral'],
  ['red', 'neutral'],
  ['#ff0000', 'neutral'],
  ['GOOD', 'neutral'],
])('chip tone %j draws as %s', (tone, want) => {
  expect(chipTone(tone)).toBe(want);
});

test('every tone is drawn with an existing shell token and the stylesheet defines no new one', () => {
  const css = readFileSync(new URL('./shell.css', import.meta.url), 'utf8');
  const rule = (tone: string) => css.match(new RegExp(`\\.mchip\\[data-tone="${tone}"\\]\\s*\\{([^}]*)\\}`))?.[1] ?? '';
  expect(rule('good')).toContain('var(--color-ok)');
  expect(rule('warn')).toContain('var(--color-needs)');
  expect(rule('muted')).toContain('var(--color-muted)');
  expect(rule('neutral')).toMatch(/var\(--color-(text2|line2)\)/);
  for (const tone of ['good', 'warn', 'muted', 'neutral']) expect(rule(tone)).not.toMatch(/(^|[\s;{])--[\w-]+\s*:/);
});

test('a filter matches a row by its title, its subtitle or a chip label, ignoring case and outer space', () => {
  expect(ids(filterGroups(groups, 'FIX'))).toEqual([['review', ['a']]]);
  expect(ids(filterGroups(groups, 'payments'))).toEqual([['review', ['a']]]);
  expect(ids(filterGroups(groups, ' SECURITY '))).toEqual([['review', ['b']]]);
});

test('a filter does not match a row by its reason', () => {
  expect(filterGroups(groups, 'merged')).toEqual([]);
  expect(filterGroups(groups, 'review is open')).toEqual([]);
});

test('a filter drops a group left with no rows and keeps the others in order', () => {
  expect(ids(filterGroups(groups, 'docs'))).toEqual([['done', ['c']]]);
  expect(ids(filterGroups(groups, 'c'))).toEqual([
    ['review', ['b']],
    ['done', ['c']],
  ]);
});

test('an empty or blank filter keeps every group and row', () => {
  expect(ids(filterGroups(groups, ''))).toEqual(ids(groups));
  expect(ids(filterGroups(groups, '   '))).toEqual(ids(groups));
});

test('a filter copes with a row that has no subtitle, chips or reason', () => {
  const bare: PageGroup[] = [{ id: 'g', title: 'G', rows: [{ id: 'x', title: 'Bare' }] }];
  expect(ids(filterGroups(bare, 'bare'))).toEqual([['g', ['x']]]);
  expect(filterGroups(bare, 'nothing')).toEqual([]);
});

test('the open row is refetched when the page change names it', () => {
  expect(shouldRefetchRow('r1', 1, { version: 2, rows: ['r1', 'r5'] })).toBe(true);
});

test('the open row is left alone when the page change names other rows', () => {
  expect(shouldRefetchRow('r1', 1, { version: 2, rows: ['r5'] })).toBe(false);
});

test('the open row is refetched when the page change names no rows, because absent means every row', () => {
  expect(shouldRefetchRow('r1', 1, { version: 2 })).toBe(true);
});

test('the open row is refetched when the version jumped by more than one, whatever rows are named', () => {
  expect(shouldRefetchRow('r1', 1, { version: 3, rows: ['r5'] })).toBe(true);
  expect(shouldRefetchRow('r1', 4, { version: 9, rows: [] })).toBe(true);
});

test('an empty list of named rows names none of them', () => {
  expect(shouldRefetchRow('r1', 1, { version: 2, rows: [] })).toBe(false);
});

test('nothing is refetched while the version has not moved', () => {
  expect(shouldRefetchRow('r1', 2, { version: 2 })).toBe(false);
  expect(shouldRefetchRow('r1', 2, { version: 2, rows: ['r1'] })).toBe(false);
});
