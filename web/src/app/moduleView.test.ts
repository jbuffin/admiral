// A module page's chip tones, client filter, open-row refetch rule and how a typed duration reads.
import { expect, test } from 'vitest';
import type { PageGroup } from './api';
import { chipTone, durationLabel, filterGroups, parseDuration, shouldRefetchRow, toHHMM } from './moduleView';

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

test.each([
  ['1:30', 90],
  ['01:30', 90],
  ['0:1', 1],
  ['0:90', 90],
  ['90', 90],
  ['1', 1],
  ['1.5', 90],
  ['.5', 30],
  ['1h30', 90],
  ['1h 30m', 90],
  ['1hr30', 90],
  ['1h', 60],
  ['45m', 45],
  ['1.5h', 90],
  ['90 min', 90],
  ['2 hours', 120],
  [' 1 Hour 30 Minutes ', 90],
])('a duration typed as %j reads as %i minutes', (typed, want) => {
  expect(parseDuration(typed)).toBe(want);
});

test.each(['', '  ', 'abc', '1:', ':30', '1:2:3', 'h', '30m 1h', '-5', '1,5', '100:00'])('a duration typed as %j cannot be read', (typed) => {
  expect(parseDuration(typed)).toBeNull();
});

test('minutes read back as hours and minutes, and go to the plugin as two-digit hh:mm', () => {
  expect([1, 45, 60, 90, 125].map(durationLabel)).toEqual(['1 min', '45 min', '1 h', '1 h 30 min', '2 h 5 min']);
  expect([1, 90, 600, 5999].map(toHHMM)).toEqual(['00:01', '01:30', '10:00', '99:59']);
});
