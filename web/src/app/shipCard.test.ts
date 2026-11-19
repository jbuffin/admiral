import { describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads; the card's ship link comes from it.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/chat', search: '' });
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', { replaceState: () => {} });
});

import type { Ship } from './api';
import { shipCardView, threadOf, type ChatSnapshot, type ShipCard } from './chat';
import { shipPath } from './router';

const base: ShipCard = {
  ship: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', fleet: 'harbor', skill: 'tdd', skill_kind: 'skill', result: 'moored',
  prs: [{ url: 'https://github.com/acme/api/pull/7', number: 7 }],
};
const item = (card: Partial<ShipCard> = {}) => ({ kind: 'ship' as const, id: 'ship-calm-otter-x-moored', text: 'Calm Otter', at: '2026-01-02T03:04:05Z', card: { ...base, ...card } });
const live = (over: Partial<Ship> = {}) => ({ session: 'calm-otter', name: 'Calm Otter', first_launched: '2026-01-02T03:04:05Z', status: 'moored', ...over }) as Ship;

describe('the ship card in the thread', () => {
  it('comes from a ship_card entry as a ship item that keeps its fields', () => {
    const s: ChatSnapshot = { fleet: 'harbor', running: true, state: 'idle', questions: [], messages: [{ id: 'ship-calm-otter-x-moored', kind: 'ship_card', text: 'Calm Otter', at: '2026-01-02T03:04:05Z', card: base }] };
    const t = threadOf(s);
    expect(t).toHaveLength(1);
    expect(t[0]).toMatchObject({ kind: 'ship', id: 'ship-calm-otter-x-moored', card: { ship: 'calm-otter', result: 'moored' } });
  });
});

describe('the ship card view', () => {
  it('says each result in words, never the wire value', () => {
    const say = (result: ShipCard['result']) => shipCardView(item({ result }), []).result;
    expect(say('moored')).toBe('Moored');
    expect(say('anchored')).toBe('Anchored');
    expect(say('ready_to_merge')).toBe('Ready to merge');
    expect(say('sailed_off')).toBe('Sailed off');
    expect(say('returned')).toBe('Returned to port');
  });

  it('says Done for a local-only ship the server reports as done', () => {
    const card = JSON.parse('{"ship":"calm-otter","name":"Calm Otter","first_launched":"2026-01-02T03:04:05Z","fleet":"harbor","skill":"","skill_kind":"quick","result":"done","branch":"admiral/calm-otter","prs":[]}') as ShipCard;
    expect(shipCardView({ ...item(), card }, []).result).toBe('Done');
  });

  it('degrades an unknown result to a neutral line that is not its wire value', () => {
    const v = shipCardView(item({ result: 'teleported' as ShipCard['result'] }), []);
    expect(v.result).not.toBe('');
    expect(v.result).not.toContain('teleported');
  });

  it('names the ship and its kind in the eyebrow: the skill, or No skill', () => {
    expect(shipCardView(item(), []).eyebrow).toBe('Calm Otter · tdd');
    expect(shipCardView(item({ skill: '', skill_kind: 'none' }), []).kind).toBe('No skill');
    expect(shipCardView(item({ skill: 'jasonbuffin:commodore', skill_kind: 'commodore' }), []).eyebrow).toBe('Calm Otter · Commodore');
    expect(shipCardView(item({ skill: '', skill_kind: 'commodore' }), []).kind).toBe('Commodore');
    expect(shipCardView(item({ skill: '', skill_kind: 'none' }), []).eyebrow).not.toContain('Quick');
  });

  it('reads Scout for a scout and Quick ship for a quick ship', () => {
    const scout = shipCardView(item({ skill: '', skill_kind: 'scout' as ShipCard['skill_kind'] }), []);
    expect(scout.kind).toBe('Scout');
    expect(scout.eyebrow).toBe('Calm Otter · Scout');
    const quick = shipCardView(item({ skill: '', skill_kind: 'quick' as ShipCard['skill_kind'] }), []);
    expect(quick.kind).toBe('Quick ship');
    expect(quick.eyebrow).toBe('Calm Otter · Quick ship');
  });

  it('shows the ship’s name, and the session slug when the name is empty or missing', () => {
    expect(shipCardView(item(), []).name).toBe('Calm Otter');
    expect(shipCardView(item({ name: '' }), []).name).toBe('calm-otter');
    expect(shipCardView(item({ name: undefined }), []).name).toBe('calm-otter');
  });

  it('gives each https PR a link and every other URL none', () => {
    const urls = ['https://github.com/acme/api/pull/7', 'http://github.com/acme/api/pull/8', 'javascript:alert(1)', 'not a url', 'https://'];
    const v = shipCardView(item({ prs: urls.map((url, i) => ({ url, number: i + 7 })) }), []);
    expect(v.prs.map((p) => p.href)).toEqual(['https://github.com/acme/api/pull/7', '', '', '', '']);
    expect(v.prs.map((p) => p.url)).toEqual(urls);
    expect(v.prs[0].label).toBe('PR #7');
  });

  it('links the ship while its voyage is in the ships list, through the encoding route', () => {
    expect(shipCardView(item(), [live()]).shipHref).toBe(shipPath('calm-otter'));
    expect(shipCardView(item({ ship: 'odd name/x' }), [live({ session: 'odd name/x' })]).shipHref).toBe(shipPath('odd name/x'));
    expect(shipPath('odd name/x')).toContain(encodeURIComponent('odd name/x'));
  });

  it('gives no link for a gone ship, or for a later voyage that reused the name', () => {
    expect(shipCardView(item(), [live()]).shipHref).not.toBe('');
    expect(shipCardView(item(), []).shipHref).toBe('');
    expect(shipCardView(item(), [live({ session: 'brisk-heron' })]).shipHref).toBe('');
    expect(shipCardView(item(), [live({ first_launched: '2026-02-03T04:05:06Z' })]).shipHref).toBe('');
  });
});
