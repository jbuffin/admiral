import { describe, expect, it, vi } from 'vitest';

// Words and card views for Admiral's With-you proposals.
vi.hoisted(() => {
  vi.stubGlobal('location', { pathname: '/app/inbox', search: '' });
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', { replaceState: () => {} });
});

import { actionWords, commitmentMarks } from './chatSettings';
import { commitmentCardView, questionEyebrow, type ThreadItem } from './chat';

const launched = (extra: Record<string, string>) =>
  ({ kind: 'commitment', id: 'commitment-c1-launched-ab12cd34', text: 'work on acme/widgets#20', at: '2026-01-02T03:04:06Z', card: { commitment: 'c1', outcome: 'launched', ...extra } }) as unknown as Extract<ThreadItem, { kind: 'commitment' }>;

describe('a campaign commitment proposes With-you children', () => {
  it('says in Preferences that it proposes With-you children one at a time and still launches Without-you ones', () => {
    const w = actionWords('work' as never);
    expect(w).toContain('Propose With-you children one at a time');
    expect(w).toContain('Without-you');
  });
  it('marks a commitment that waits for your answer, after its counts', () => {
    const open = { launched: 1, waiting_children: 0, proposal: { url: 'https://github.com/acme/widgets/issues/21', question: 'campaign-w1-ab12-1' } };
    expect(commitmentMarks(open as never)).toEqual(['1 launched', 'Waiting for your answer']);
    expect(commitmentMarks({ launched: 1 } as never)).toEqual(['1 launched']);
  });
});

describe("Admiral's question card", () => {
  it("opens with Admiral asks on a proposal and keeps AdmiralBot asks on AdmiralBot's own question", () => {
    expect(questionEyebrow('campaign-w1-ab12-1')).toBe('Admiral asks');
    expect(questionEyebrow('q-20260102-0001')).toBe('AdmiralBot asks');
  });
});

describe('the card of a launched With-you child', () => {
  it('links the child and opens its ship', () => {
    const v = commitmentCardView(launched({ ship: 'saltpaw-2', child: 'https://github.com/acme/widgets/issues/21', mode: 'with_you' }));
    expect(v.childLabel).toBe('acme/widgets#21');
    expect(v.shipHref).toBe('/app/ships/saltpaw-2');
  });
});
