import { render, screen, within } from '@testing-library/preact';
import { expect, test } from 'vitest';
import type { MooredPR, Ship } from './api';
import { ShipStatusGroup } from './ShipStatusGroup';
import type { ShipGroup } from './shipList';

const ship = (name: string, extra: Partial<Ship> = {}): Ship => ({
  session: `s-${name}`,
  name,
  fleet: 'zulu',
  harness: 'claude',
  status: 'underway',
  order_id: '',
  title: '',
  tail: '',
  fleet_color: '',
  launched: '',
  crew_live: 0,
  dir: `/src/${name}`,
  repo: name,
  origin_repo: 'acme/widgets',
  exit_code: null,
  exit_signal: null,
  crew: [],
  ...extra,
});

const show = (ships: Ship[], byRepo: boolean) => {
  const group: ShipGroup = { status: 'underway', ships };
  return render(<ShipStatusGroup group={group} bar="#369" now={Date.now()} byRepo={byRepo} />);
};

const dotfilesPR = { url: 'https://github.com/jbuffin/dotfiles/pull/3', state: 'OPEN' } as MooredPR;

test('keeps the status heading and count, with a sub-heading per repo and its count, most ships first', () => {
  show([ship('a', { origin_repo: 'b/small' }), ship('b'), ship('c'), ship('d', { origin_repo: 'b/small' }), ship('e')], true);
  const head = screen.getByRole('heading', { level: 2 });
  expect(head.textContent).toContain('Underway');
  expect(head.textContent).toContain('5');
  const sections = screen.getAllByRole('list');
  expect(sections).toHaveLength(2);
  expect(screen.getByText('acme/widgets').textContent).toBe('acme/widgets');
  const text = document.body.textContent ?? '';
  expect(text.indexOf('acme/widgets')).toBeLessThan(text.indexOf('b/small'));
  expect(within(sections[0]).getAllByRole('listitem')).toHaveLength(3);
  expect(within(sections[1]).getAllByRole('listitem')).toHaveLength(2);
  expect(screen.getByText('acme/widgets').closest('.repo-head')?.textContent).toContain('3');
  expect(screen.getByText('b/small').closest('.repo-head')?.textContent).toContain('2');
});

test('rows under a sub-heading show no repo label', () => {
  show([ship('a'), ship('b', { origin_repo: 'b/small' })], true);
  expect(document.querySelectorAll('.repo-head')).toHaveLength(2);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
  expect(document.querySelector('.ship-repo')).toBeNull();
});

test('a re-homed row says "from <folder>" in place of the repo label', () => {
  show([ship('zephyr', { origin_repo: undefined, prs: [dotfilesPR], repo: 'personal', dir: '/src/personal' }), ship('b')], true);
  const label = document.querySelector('.ship-repo') as HTMLElement;
  expect(label.textContent).toContain('from personal');
  expect(label.getAttribute('title')).toBe('/src/personal');
  expect(screen.getByText('jbuffin/dotfiles').closest('.repo-head')).not.toBeNull();
  expect(document.querySelectorAll('.ship-repo')).toHaveLength(1);
});

test('a re-homed row with no folder name shows nothing in the label place', () => {
  show([ship('zephyr', { origin_repo: undefined, prs: [dotfilesPR], repo: '', dir: '' }), ship('b')], true);
  expect(screen.getByText('jbuffin/dotfiles').closest('.repo-head')).not.toBeNull();
  expect(document.querySelector('.ship-repo')).toBeNull();
});

test('without sub-headings the rows keep their repo label and sit in one list', () => {
  show([ship('a'), ship('b')], false);
  expect(document.querySelector('.repo-head')).toBeNull();
  expect(screen.getAllByRole('list')).toHaveLength(1);
  expect(document.querySelectorAll('.ship-repo')).toHaveLength(2);
  expect(screen.getAllByRole('listitem')).toHaveLength(2);
});
