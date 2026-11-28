import { describe, expect, it } from 'vitest';
import type { MooredPR, Ship, ShipStatus } from './api';
import { byWorkRepo, groupShips, oneWorkRepo, runningFor, shipAge, shipRowAge, workRepo } from './shipList';

const ship = (name: string, status: ShipStatus, fleet = 'zulu'): Ship => ({
  session: `s-${name}`,
  name,
  fleet,
  harness: 'claude',
  status,
  order_id: '',
  title: '',
  tail: '',
  fleet_color: '',
  launched: '',
  crew_live: 0,
  dir: '',
  repo: '',
  exit_code: null,
  exit_signal: null,
  crew: [],
});

const names = (groups: { ships: Ship[] }[]) => groups.map((g) => g.ships.map((s) => s.name));

describe('groupShips', () => {
  it('groups the fleet by status: Needs you, Underway, Anchored', () => {
    const groups = groupShips(
      [ship('a', 'anchored'), ship('b', 'underway'), ship('c', 'needs_you'), ship('d', 'underway')],
      'zulu',
    );
    expect(groups.map((g) => g.status)).toEqual(['needs_you', 'underway', 'anchored']);
    expect(names(groups).map((n) => n.sort())).toEqual([['c'], ['b', 'd'], ['a']]);
  });

  it('leaves out an empty group', () => {
    const groups = groupShips([ship('a', 'anchored'), ship('b', 'needs_you')], 'zulu');
    expect(groups.map((g) => g.status)).toEqual(['needs_you', 'anchored']);
  });

  it('lists only the active fleet', () => {
    const list = [ship('mine', 'underway'), ship('theirs', 'underway', 'alpha')];
    expect(names(groupShips(list, 'zulu'))).toEqual([['mine']]);
    expect(names(groupShips(list, 'alpha'))).toEqual([['theirs']]);
  });

  it('hides a ship sailing off', () => {
    expect(groupShips([ship('gone', 'sailing_off')], 'zulu')).toEqual([]);
    expect(names(groupShips([ship('gone', 'sailing_off'), ship('here', 'underway')], 'zulu'))).toEqual([['here']]);
  });
});

describe('shipAge', () => {
  const now = Date.parse('2026-11-11T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it('counts minutes, then hours, then days', () => {
    expect(shipAge(ago(10_000), now)).toBe('now');
    expect(shipAge(ago(24 * 60_000), now)).toBe('24m');
    expect(shipAge(ago(59 * 60_000 + 59_000), now)).toBe('59m');
    expect(shipAge(ago(2 * 3600_000 + 5 * 60_000), now)).toBe('2h');
    expect(shipAge(ago(3 * 86400_000), now)).toBe('3d');
  });

  it('is blank for a missing or unreadable launch time, and never negative', () => {
    expect(shipAge('', now)).toBe('');
    expect(shipAge('soon', now)).toBe('');
    expect(shipAge(ago(-5 * 60_000), now)).toBe('now');
  });
});

describe('how long a running ship has been going', () => {
  const now = Date.parse('2026-11-11T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it('counts minutes until the hour, then hours and minutes', () => {
    expect(runningFor(ago(20_000), now)).toBe('<1m');
    expect(runningFor(ago(59 * 60_000 + 59_000), now)).toBe('59m');
    expect(runningFor(ago(3600_000), now)).toBe('1h 0m');
    expect(runningFor(ago(2 * 3600_000 + 5 * 60_000), now)).toBe('2h 5m');
    expect(runningFor(ago(26 * 3600_000 + 7 * 60_000), now)).toBe('26h 7m');
  });

  it('is blank for a missing or unreadable launch time, and never negative', () => {
    expect(runningFor('', now)).toBe('');
    expect(runningFor('soon', now)).toBe('');
    expect(runningFor(ago(-5 * 60_000), now)).toBe('<1m');
  });

  it('shows on the row of an underway or needs-you ship, not an anchored one', () => {
    const launched = ago(90 * 60_000);
    expect(shipRowAge({ ...ship('a', 'underway'), launched }, now)).toBe('1h 30m');
    expect(shipRowAge({ ...ship('b', 'needs_you'), launched }, now)).toBe('1h 30m');
    expect(shipRowAge({ ...ship('c', 'anchored'), launched }, now)).toBe('1h');
  });
});

const pr = (repo: string, n = 1) => ({ url: `https://github.com/${repo}/pull/${n}`, state: 'OPEN' }) as MooredPR;
const placed = (name: string, extra: Partial<Ship>): Ship => ({ ...ship(name, 'underway'), ...extra });

describe('workRepo', () => {
  it('is the checkout\'s origin even when the ship\'s PRs are in that same repo', () => {
    const s = placed('a', { prs: [pr('acme/widgets')], origin_repo: 'acme/widgets', repo: 'widgets' });
    expect(workRepo(s)).toEqual({ repo: 'acme/widgets', rehomed: false });
  });

  it('keeps a ship under the repo it was launched in when its PR is in another repo', () => {
    const s = placed('a', { prs: [pr('jbuffin/dotfiles')], origin_repo: 'acme/widgets', repo: 'widgets' });
    expect(workRepo(s)).toEqual({ repo: 'acme/widgets', rehomed: false });
  });

  it('keeps Pilot Pup under the admiral repo although it read a PR in admiral-app', () => {
    const s = placed('pilot-pup', { prs: [pr('jbuffin/admiral-app', 65)], origin_repo: 'jbuffin/admiral', repo: 'admiral' });
    expect(workRepo(s)).toEqual({ repo: 'jbuffin/admiral', rehomed: false });
  });

  it('re-homes a ship launched in a search root with no origin when its PR is elsewhere', () => {
    const s = placed('zephyr', { prs: [pr('jbuffin/dotfiles')], repo: 'personal' });
    expect(workRepo(s)).toEqual({ repo: 'jbuffin/dotfiles', rehomed: true });
  });

  it('takes the repo most of its PRs are in', () => {
    const s = placed('a', { prs: [pr('a/one'), pr('b/two', 2), pr('b/two', 3)] });
    expect(workRepo(s).repo).toBe('b/two');
  });

  it('breaks a PR tie with the first PR listed', () => {
    const s = placed('a', { prs: [pr('b/two'), pr('a/one', 2)] });
    expect(workRepo(s).repo).toBe('b/two');
  });

  it('breaks a PR tie with the repo of the first PR even when another repo reaches the top count first', () => {
    const s = placed('a', { prs: [pr('a/one', 1), pr('b/two', 2), pr('b/two', 3), pr('a/one', 4)] });
    expect(workRepo(s).repo).toBe('a/one');
  });

  it('reads PRs and ignores open_prs', () => {
    const s = placed('a', { prs: [pr('acme/widgets')], open_prs: ['https://github.com/x/other/pull/9', 'https://github.com/x/other/pull/10'], origin_repo: undefined });
    expect(workRepo(s)).toEqual({ repo: 'acme/widgets', rehomed: true });
  });

  it('is the checkout\'s origin when the ship has no PR', () => {
    const s = placed('a', { origin_repo: 'jbuffin/admiral', repo: 'admiral-2' });
    expect(workRepo(s)).toEqual({ repo: 'jbuffin/admiral', rehomed: false });
  });

  it('is the repo of the order URL when there is no PR and no origin, and then it is re-homed', () => {
    const s = placed('a', { url: 'https://github.com/acme/widgets/issues/12', repo: 'personal' });
    expect(workRepo(s)).toEqual({ repo: 'acme/widgets', rehomed: true });
  });

  it('reads a PR order URL too', () => {
    const s = placed('a', { url: 'https://github.com/acme/widgets/pull/12' });
    expect(workRepo(s).repo).toBe('acme/widgets');
  });

  it('prefers the origin to the order URL', () => {
    const s = placed('a', { origin_repo: 'acme/widgets', url: 'https://github.com/other/thing/issues/1' });
    expect(workRepo(s)).toEqual({ repo: 'acme/widgets', rehomed: false });
  });

  it('prefers the origin to the PRs and the order URL', () => {
    const s = placed('a', { prs: [pr('p/q')], origin_repo: 'acme/widgets', url: 'https://github.com/other/thing/issues/1' });
    expect(workRepo(s)).toEqual({ repo: 'acme/widgets', rehomed: false });
  });

  it('is the folder name as is when nothing else says', () => {
    expect(workRepo(placed('a', { repo: 'scratch', url: 'https://example.com/ticket/4' }))).toEqual({ repo: 'scratch', rehomed: false });
    expect(workRepo(placed('b', {}))).toEqual({ repo: '', rehomed: false });
  });

  it('counts PRs without regard to case and keeps the first casing, for a ship with no origin', () => {
    const s = placed('a', { prs: [pr('other/thing', 1), pr('Acme/Widgets', 2), pr('acme/widgets', 3)] });
    expect(workRepo(s)).toEqual({ repo: 'Acme/Widgets', rehomed: true });
  });
});

describe('byWorkRepo', () => {
  const inRepo = (name: string, repo: string) => placed(name, { origin_repo: repo });

  it('orders repos by ship count, most first, then by name, and keeps ship order inside', () => {
    const groups = byWorkRepo([
      inRepo('a', 'z/last'),
      inRepo('b', 'm/solo'),
      inRepo('c', 'b/many'),
      inRepo('d', 'a/pair'),
      inRepo('e', 'b/many'),
      inRepo('f', 'a/pair'),
      inRepo('g', 'b/many'),
    ]);
    expect(groups.map((g) => [g.repo, g.ships.map((s) => s.name)])).toEqual([
      ['b/many', ['c', 'e', 'g']],
      ['a/pair', ['d', 'f']],
      ['m/solo', ['b']],
      ['z/last', ['a']],
    ]);
  });

  it('breaks a name tie without regard to case', () => {
    const groups = byWorkRepo([inRepo('a', 'Zed/x'), inRepo('b', 'alpha/x')]);
    expect(groups.map((g) => g.repo)).toEqual(['alpha/x', 'Zed/x']);
  });

  it('puts a re-homed ship under the repo it works in', () => {
    const z = placed('zephyr', { prs: [pr('jbuffin/dotfiles')], repo: 'personal' });
    const groups = byWorkRepo([z, inRepo('b', 'jbuffin/admiral')]);
    expect(groups.map((g) => [g.repo, g.ships.map((s) => s.name)])).toEqual([
      ['jbuffin/admiral', ['b']],
      ['jbuffin/dotfiles', ['zephyr']],
    ]);
  });

  it('groups a ship with a PR in another repo with the other ships launched in its origin', () => {
    const pup = placed('pilot-pup', { prs: [pr('jbuffin/admiral-app', 65)], origin_repo: 'jbuffin/admiral', repo: 'admiral' });
    const groups = byWorkRepo([pup, inRepo('b', 'jbuffin/admiral')]);
    expect(groups.map((g) => [g.repo, g.ships.map((s) => s.name)])).toEqual([['jbuffin/admiral', ['pilot-pup', 'b']]]);
  });
});

describe('oneWorkRepo', () => {
  it('is true for no ships and for ships all in one repo, false when two repos are in play', () => {
    expect(oneWorkRepo([])).toBe(true);
    expect(oneWorkRepo([placed('a', { origin_repo: 'a/b' }), placed('b', { prs: [pr('A/B')] })])).toBe(true);
    expect(oneWorkRepo([placed('a', { origin_repo: 'a/b' }), placed('b', { origin_repo: 'a/c' })])).toBe(false);
  });

  it('is true when every ship was launched in one repo, whatever repo a PR is in', () => {
    const ships = [
      placed('a', { origin_repo: 'jbuffin/admiral' }),
      placed('b', { origin_repo: 'jbuffin/admiral', prs: [pr('jbuffin/admiral-app', 65)] }),
    ];
    expect(oneWorkRepo(ships)).toBe(true);
  });
});
