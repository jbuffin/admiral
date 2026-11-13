import { describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads; the card's ship link comes from it.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', { replaceState: () => {} });
});

import { actionWords, commitmentMarks, triggerWords } from './chatSettings';
import { commitmentCardView, type ThreadItem } from './chat';

const cardItem = (outcome: string, extra: Record<string, string> = {}) =>
  ({ kind: 'commitment', id: `commitment-c1-${outcome}`, text: 'work on acme/widgets#20', at: '2026-01-02T03:04:06Z', card: { commitment: 'c1', outcome, ...extra } }) as unknown as Extract<ThreadItem, { kind: 'commitment' }>;

describe('a campaign commitment in words', () => {
  it('names the campaign by its title and links its issue', () => {
    const w = triggerWords({ kind: 'campaign' as never, subject: 'camp-1' }, [], { title: 'Ship the harbour', url: 'https://github.com/acme/widgets/issues/20' } as never);
    expect(w.text).toBe('Campaign Ship the harbour');
    expect(w.href).toBe('https://github.com/acme/widgets/issues/20');
  });
  it('falls back to owner/repo#n when the campaign has no title', () => {
    const w = triggerWords({ kind: 'campaign' as never, subject: 'camp-1' }, [], { url: 'https://github.com/acme/widgets/issues/20' } as never);
    expect(w.text).toBe('Campaign acme/widgets#20');
  });
  it('never reads as a PR trigger when the API gave no campaign', () => {
    const w = triggerWords({ kind: 'campaign' as never, subject: 'camp-1' }, []);
    expect(w.text).toMatch(/^Campaign/);
    expect(w.text).not.toContain('PR');
    expect(w.href).toBeUndefined();
  });
  it('says the action launches Without-you children', () => {
    expect(actionWords('work' as never)).toBe('Launch Without-you children');
  });
  it('marks the launched and waiting counts, and leaves out a zero', () => {
    expect(commitmentMarks({ launched: 2, waiting_children: 1 } as never)).toEqual(['2 launched', '1 waiting']);
    expect(commitmentMarks({ launched: 0, waiting_children: 0 } as never)).toEqual([]);
    expect(commitmentMarks({ launched: 1 } as never)).toEqual(['1 launched']);
  });
});

describe('a campaign commitment card', () => {
  it('gives an ended card an eyebrow of its own, with the reason', () => {
    const v = commitmentCardView(cardItem('ended', { reason: 'the campaign ended' }));
    expect(v.eyebrow, 'an ended card has no eyebrow').toBeTruthy();
    expect(v.eyebrow.toLowerCase()).toContain('campaign');
    expect(v.reason).toBe('the campaign ended');
    expect(v.shipHref).toBe('');
    const others = ['launched', 'reminded', 'cant_run', 'dropped', 'aged'].map((o) => commitmentCardView(cardItem(o)).eyebrow);
    expect(others).not.toContain(v.eyebrow);
  });
  it('says a too-large campaign is too large', () => {
    const v = commitmentCardView(cardItem('too_large', { reason: 'too large to work unattended' }));
    expect(v.eyebrow.toLowerCase()).toContain('too large');
  });
  it("names the child a launched card is for, and links the ship", () => {
    const v = commitmentCardView(cardItem('launched', { ship: 'saltpaw-2', child: 'https://github.com/acme/widgets/issues/21' }));
    expect(v.childLabel).toBe('acme/widgets#21');
    expect(v.shipHref).toContain('saltpaw-2');
  });
  it('names the child a can\'t-run card is for', () => {
    const v = commitmentCardView(cardItem('cant_run', { reason: 'autopilot is not proven', child: 'https://github.com/acme/widgets/issues/22' }));
    expect(v.childLabel).toBe('acme/widgets#22');
    expect(v.reason).toBe('autopilot is not proven');
  });
  it('has no child label on a card that has no child', () => {
    expect(commitmentCardView(cardItem('reminded')).childLabel ?? '').toBe('');
  });
});
