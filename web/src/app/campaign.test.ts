import { describe, expect, it } from 'vitest';
import type { Ship } from './api';
import { blockerLine, campaignMeta, childShipsKey, groupChildren, kindLabel, modeLabel, nextPoll, pasteHint, overrideBody, safeHref, shipOfChild, skillLabel, statusLabel, typeLabel, type CampaignNode } from './campaign';

const url = (n: number) => `https://github.com/acme/widgets/issues/${n}`;
const child = (n: number, status: CampaignNode['status'], mode: CampaignNode['mode'] = 'with-you', extra: Partial<CampaignNode> = {}): CampaignNode => ({
  number: n,
  title: `Issue ${n}`,
  url: url(n),
  status,
  mode,
  ...extra,
});

describe('groupChildren', () => {
  // Written by hand from the design's groups: 23 is a nested parent holding 24 and 25.
  const root: CampaignNode = {
    number: 20,
    title: 'The map',
    url: url(20),
    parent: true,
    children: [
      child(21, 'ready'),
      child(22, 'ready', 'without-you'),
      {
        number: 23,
        title: 'Sub map',
        url: url(23),
        parent: true,
        children: [child(24, 'ready', 'without-you'), child(25, 'blocked', 'with-you', { blockers: [{ url: url(28), title: 'Issue 28' }] })],
      },
      child(26, 'underway', 'with-you', { ship: 'Quiet Heron' }),
      child(27, 'claimed-no-ship'),
      child(28, 'blocked', 'with-you', { blockers: [{ url: url(29), title: 'Issue 29' }] }),
      child(29, 'done'),
      child(30, 'ready'),
    ],
  };
  const numbers = (g: { rows: { node: CampaignNode }[] }) => g.rows.map((r) => r.node.number);

  it('lists the groups in the design\'s order, each child in sub-issue order, an underway child in its Ready group', () => {
    const groups = groupChildren(root);
    expect(groups.map((g) => g.title)).toEqual(['Ready, with you', 'Ready, without you', 'Claimed, no ship', 'Blocked', 'Done']);
    expect(groups.map(numbers)).toEqual([[21, 26, 30], [22, 24], [27], [25, 28], [29]]);
  });
  it('shows a nested parent\'s title as the prefix of the children under it, and never as a row', () => {
    const rows = groupChildren(root).flatMap((g) => g.rows);
    expect(rows.find((r) => r.node.number === 24)?.prefix).toBe('Sub map');
    expect(rows.find((r) => r.node.number === 25)?.prefix).toBe('Sub map');
    expect(rows.find((r) => r.node.number === 21)?.prefix).toBe('');
    expect(rows.some((r) => r.node.number === 23 || r.node.number === 20)).toBe(false);
  });
  it('leaves out a group with no child', () => {
    const groups = groupChildren({ ...root, children: [child(21, 'ready'), child(29, 'done')] });
    expect(groups.map((g) => g.title)).toEqual(['Ready, with you', 'Done']);
  });
});

describe('labels', () => {
  it('names each status as the page shows it', () => {
    expect(statusLabel('ready')).toBe('Ready');
    expect(statusLabel('blocked')).toBe('Blocked');
    expect(statusLabel('underway')).toBe('Underway');
    expect(statusLabel('claimed-no-ship')).toBe('Claimed, no ship');
    expect(statusLabel('done')).toBe('Done');
  });
  it('names each mode', () => {
    expect(modeLabel('with-you')).toBe('With you');
    expect(modeLabel('without-you')).toBe('Without you');
  });
  it('gives the sidebar row its map number and progress', () => {
    expect(campaignMeta({ number: 230, progress: { closed: 6, total: 13 } })).toBe('Map #230 · 6 of 13');
  });
});

describe('blockerLine', () => {
  it('links a blocker to its issue under its title', () => {
    expect(blockerLine({ url: url(28), title: 'Drive figures' })).toEqual({ kind: 'link', text: 'Drive figures', url: url(28) });
  });
  it('falls back to the URL for a blocker with no title', () => {
    expect(blockerLine({ url: url(28) })).toEqual({ kind: 'link', text: url(28), url: url(28) });
  });
  it('reads the stand-in past the cap as a note, never a link', () => {
    const line = blockerLine({ url: url(28), title: 'More Blocked by lines than Admiral reads; check the issue', capped: true });
    expect(line).toEqual({ kind: 'note', text: 'More Blocked by lines than Admiral reads; check the issue' });
  });
});

describe('pasteHint', () => {
  it('stays quiet for an issue URL and for owner/repo#n, whitespace around them aside', () => {
    expect(pasteHint(url(20))).toBeNull();
    expect(pasteHint('acme/widgets#20')).toBeNull();
    expect(pasteHint(`  ${url(20)}\n`)).toBeNull();
    expect(pasteHint('')).toBeNull();
  });
  it('says a trailing slash is not accepted', () => {
    expect(pasteHint(`${url(20)}/`)).toMatch(/trailing slash/i);
  });
  it('says a comment link is not accepted', () => {
    expect(pasteHint(`${url(20)}#issuecomment-123456`)).toMatch(/comment/i);
  });
});

describe('matching ships to children', () => {
  const ship = (name: string, status: Ship['status'], extra: Partial<Ship> = {}): Ship => ({
    session: `s-${name}`,
    name,
    fleet: 'zulu',
    harness: 'claude',
    status,
    order_id: '',
    title: '',
    tail: '',
    fleet_color: '',
    launched: '',
    crew_live: 0,
    source: 'campaign',
    url: url(21),
    ...extra,
  });

  it('finds the campaign ship that holds the child\'s URL', () => {
    const mine = ship('Quiet Heron', 'underway');
    const ships = [ship('Other', 'underway', { url: url(99) }), ship('Not campaign', 'underway', { source: 'github' }), mine];
    expect(shipOfChild({ url: url(21) }, ships)).toBe(mine);
    expect(shipOfChild({ url: url(22) }, ships)).toBeUndefined();
  });
  it('keys the ships of the children, so a change in one is a change and an unrelated ship is not', () => {
    const children = [{ url: url(21) }, { url: url(22) }];
    const before = childShipsKey(children, [ship('Quiet Heron', 'underway')]);
    expect(childShipsKey(children, [ship('Quiet Heron', 'needs_you')])).not.toBe(before);
    expect(childShipsKey(children, [])).not.toBe(before);
    expect(childShipsKey(children, [ship('Quiet Heron', 'underway'), ship('Stranger', 'underway', { url: url(99) })])).toBe(before);
  });
});

describe('nextPoll', () => {
  it('polls every 3 seconds while a read is in progress', () => {
    expect(nextPoll({ reading: true, fastForMs: 0 })).toBe(3000);
    expect(nextPoll({ reading: true, fastForMs: 57_000 })).toBe(3000);
  });
  it('drops to every 60 seconds once a read has run for 60 seconds', () => {
    expect(nextPoll({ reading: true, fastForMs: 60_000 })).toBe(60_000);
  });
  it('polls every 60 seconds when no read is in progress', () => {
    expect(nextPoll({ reading: false, fastForMs: 0 })).toBe(60_000);
  });
});

describe('overrideBody', () => {
  const node = child(21, 'ready', 'with-you', { default_mode: 'without-you', default_skill: { kind: 'commodore', name: '', args: '' } });
  const same = { kind: 'commodore' as const, name: '', args: '' };
  it('sends only the mode when the skill is the default', () => {
    expect(overrideBody(node, 'with-you', same)).toEqual({ child_url: url(21), mode: 'with-you' });
  });
  it('sends only the skill when the mode is the default', () => {
    const skill = { kind: 'skill' as const, name: 'wayfinder', args: '' };
    expect(overrideBody(node, 'without-you', skill)).toEqual({ child_url: url(21), mode: '', skill });
  });
  it('clears the override when nothing differs from the defaults', () => {
    expect(overrideBody(node, 'without-you', same)).toEqual({ child_url: url(21), mode: '' });
  });
});

describe('safeHref', () => {
  it('passes http and https and nothing else', () => {
    expect(safeHref('https://github.com/a/b/issues/1')).toBe('https://github.com/a/b/issues/1');
    expect(safeHref('javascript:alert(1)')).toBeNull();
    expect(safeHref('not a url')).toBeNull();
  });
});

describe('type and kind', () => {
  it('reads a child\'s type from its wayfinder label, Task when it has none', () => {
    expect(typeLabel(['wayfinder:grilling'])).toBe('Grilling');
    expect(typeLabel(['ready-for-agent'])).toBe('Task');
    expect(typeLabel(undefined)).toBe('Task');
  });
  it('calls a campaign with a wayfinder label a Wayfinder map', () => {
    expect(kindLabel(['wayfinder:map'])).toBe('Wayfinder map');
    expect(kindLabel([])).toBe('Campaign');
  });
  it('words a skill', () => {
    expect(skillLabel({ kind: 'commodore', name: '', args: '' })).toBe('Commodore');
    expect(skillLabel({ kind: 'skill', name: 'wayfinder', args: 'a b' })).toBe('/wayfinder a b');
    expect(skillLabel({ kind: 'none', name: '', args: '' })).toBe('Just launch');
  });
});
