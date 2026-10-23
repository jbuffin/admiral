import { describe, expect, it } from 'vitest';
import { createDecisions, readCards, type FindingCard } from './findings';

// The decision state of a findings item (#116 u4, S4). The interface the module is built to, all pure:
//
//   readCards(raw)             the cards of an item's `findings.cards`, which a ship wrote and is untrusted:
//                              an entry with no string id, or an id already seen, is dropped; an unknown verdict
//                              reads as 'unclear'; a missing or malformed proposal reads as null; evidence is a
//                              list of strings
//   createDecisions(cards)     nothing decided yet
//   d.approve(id)              'approved', but a card already 'edited' stays edited (Approve after a Save keeps the edit)
//   d.reject(id)               'rejected', and drops a saved edit
//   d.save(id, text)           the engineer saved an edit: 'edited', with the proposal's kind kept and the new
//                              content. False, and nothing changes, when text is blank (the server refuses it).
//                              Cancel is not a call: the textarea closes and the decision is as it was.
//   d.decision(id)             'approved' | 'edited' | 'rejected' | undefined
//   d.proposal(id)             what the card shows: the saved edit if edited, else the original (null if it has none)
//   d.progress()               { decided, total } for "n of N decided"
//   d.canSend()                every card has a decision (a list with no cards can be sent)
//   d.payload()                the POST body: { cards: [{ id, decision, proposal? }] }, in card order, with
//                              `proposal` only on an edited card
//   d.summary()                the fold text: "Sent 1 approved, 1 edited, 1 rejected", with no zero buckets

const card = (id: string, extra: Partial<FindingCard> = {}): FindingCard => ({
  id,
  source: 'reviewer',
  quote: `quote ${id}`,
  verdict: 'agree',
  reason: 'because',
  proposal: { kind: 'diff', content: `- old ${id}\n+ new ${id}` },
  evidence: ['main.go:3'],
  ...extra,
});

describe('readCards', () => {
  it('keeps a well-formed card as written', () => {
    const c = card('r1');
    expect(readCards([c])).toEqual([c]);
  });

  it('survives what a ship might write: junk, a duplicate id, a bad verdict, no proposal', () => {
    const got = readCards([
      null,
      'x',
      { source: 'no id' },
      { id: 'a', verdict: 'maybe', quote: 'q', reason: 'r', source: 's', evidence: ['e', 3] },
      { id: 'a', verdict: 'agree' },
      { id: 'b', verdict: 'agree', proposal: 'not an object' },
    ]);
    expect(got.map((c) => c.id)).toEqual(['a', 'b']);
    expect(got[0].verdict).toBe('unclear');
    expect(got[0].proposal).toBeNull();
    expect(got[0].evidence).toEqual(['e']);
    expect(got[1].proposal).toBeNull();
  });

  it('reads anything that is not a list as no cards', () => {
    expect(readCards(undefined)).toEqual([]);
    expect(readCards({ id: 'a' })).toEqual([]);
  });
});

describe('finding card decisions', () => {
  it('Send is enabled only when every card is decided, and the progress counts them', () => {
    const d = createDecisions([card('r1'), card('r2'), card('r3')]);
    expect(d.canSend()).toBe(false);
    expect(d.progress()).toEqual({ decided: 0, total: 3 });
    d.approve('r1');
    d.reject('r2');
    expect(d.canSend()).toBe(false);
    expect(d.progress()).toEqual({ decided: 2, total: 3 });
    d.save('r3', 'my own words');
    expect(d.canSend()).toBe(true);
    expect(d.progress()).toEqual({ decided: 3, total: 3 });
  });

  it('a list with no cards can be sent', () => {
    const d = createDecisions([]);
    expect(d.canSend()).toBe(true);
    expect(d.payload()).toEqual({ cards: [] });
  });

  it('Save makes the card edited, keeps the proposal kind and shows the new content', () => {
    const d = createDecisions([card('r1', { proposal: { kind: 'reply', content: 'old reply' } })]);
    expect(d.proposal('r1')).toEqual({ kind: 'reply', content: 'old reply' });
    expect(d.save('r1', 'new reply')).toBe(true);
    expect(d.decision('r1')).toBe('edited');
    expect(d.proposal('r1')).toEqual({ kind: 'reply', content: 'new reply' });
  });

  it('Approve after a Save keeps the edit', () => {
    const d = createDecisions([card('r1')]);
    d.save('r1', 'edited text');
    d.approve('r1');
    expect(d.decision('r1')).toBe('edited');
    expect(d.proposal('r1')?.content).toBe('edited text');
  });

  it('Reject drops a saved edit, and a later Approve is approved with the original', () => {
    const c = card('r1');
    const d = createDecisions([c]);
    d.save('r1', 'edited text');
    d.reject('r1');
    expect(d.decision('r1')).toBe('rejected');
    expect(d.proposal('r1')).toEqual(c.proposal);
    d.approve('r1');
    expect(d.decision('r1')).toBe('approved');
    expect(d.proposal('r1')).toEqual(c.proposal);
  });

  it('a blank edit is refused: nothing changes, so the server never sees a 400', () => {
    const d = createDecisions([card('r1')]);
    d.approve('r1');
    expect(d.save('r1', '  \n\t ')).toBe(false);
    expect(d.decision('r1')).toBe('approved');
    expect(d.save('r1', '')).toBe(false);
  });

  it('the payload carries a proposal only on the edited card, with the original kind', () => {
    const d = createDecisions([card('r1'), card('r2', { verdict: 'unclear', proposal: { kind: 'reply', content: 'what do you mean?' } }), card('r3')]);
    d.approve('r1');
    d.save('r2', 'could you say more?');
    d.reject('r3');
    expect(d.payload()).toEqual({
      cards: [
        { id: 'r1', decision: 'approved' },
        { id: 'r2', decision: 'edited', proposal: { kind: 'reply', content: 'could you say more?' } },
        { id: 'r3', decision: 'rejected' },
      ],
    });
  });

  it('an unknown card id changes nothing', () => {
    const d = createDecisions([card('r1')]);
    d.approve('nope');
    expect(d.save('nope', 'x')).toBe(false);
    expect(d.progress()).toEqual({ decided: 0, total: 1 });
  });
});

describe('the fold text', () => {
  const decided = (...ds: ('approve' | 'edit' | 'reject')[]) => {
    const d = createDecisions(ds.map((_, i) => card(`c${i}`)));
    ds.forEach((x, i) => (x === 'approve' ? d.approve(`c${i}`) : x === 'reject' ? d.reject(`c${i}`) : d.save(`c${i}`, 'edit')));
    return d;
  };

  it('names every bucket that has cards, in the order approved, edited, rejected', () => {
    expect(decided('reject', 'edit', 'approve').summary()).toBe('Sent 1 approved, 1 edited, 1 rejected');
  });

  it('leaves out the buckets with none', () => {
    expect(decided('approve', 'reject', 'approve').summary()).toBe('Sent 2 approved, 1 rejected');
    expect(decided('edit', 'edit').summary()).toBe('Sent 2 edited');
    expect(decided('reject').summary()).toBe('Sent 1 rejected');
  });
});
