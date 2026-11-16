import { describe, expect, it } from 'vitest';
import type { Ship } from './api';
import { handAction, handBody, handFailure } from './hand';

const ship = (over: Partial<Ship> = {}): Ship => ({
  session: 's-h', name: 'h', fleet: 'zulu', harness: 'claude', status: 'underway', order_id: '', title: '', tail: '',
  fleet_color: '', launched: '', crew_live: 0, dir: '', repo: '', exit_code: null, exit_signal: null, crew: [], ...over,
});

describe('the Hand to Commodore action', () => {
  it('is offered on a live Claude ship', () => {
    expect(handAction(ship())).toEqual({ show: true, busy: false, refusal: '' });
    expect(handAction(ship({ status: 'needs_you' }))).toEqual({ show: true, busy: false, refusal: '' });
  });
  it('is hidden on a moored, anchored or sailing-off ship, while returning, and on a Commodore ship', () => {
    for (const over of [{ status: 'moored' }, { status: 'anchored' }, { status: 'sailing_off' }, { returning: true }, { skill_kind: 'commodore' }] as Partial<Ship>[]) {
      expect(handAction(ship(over))).toEqual({ show: false });
    }
  });
  it('is busy while handing', () => {
    expect(handAction(ship({ handing: true }))).toEqual({ show: true, busy: true, refusal: '' });
  });
  it('carries the reason a ship cannot be handed', () => {
    expect(handAction(ship({ harness: 'codex', hand_refusal: 'ship h runs on codex' }))).toEqual({ show: true, busy: false, refusal: 'ship h runs on codex' });
  });
});

describe('a failed hand', () => {
  it('shows its reason on a live ship', () => {
    expect(handFailure(ship({ hand_failed: 'the ship wrote no ref file' }))).toBe('the ship wrote no ref file');
  });
  it('is gone while the next hand is under way and once the ship has ended', () => {
    expect(handFailure(ship({ hand_failed: 'x', handing: true }))).toBe('');
    expect(handFailure(ship({ hand_failed: 'x', status: 'anchored' }))).toBe('');
  });
});

describe('the hand request', () => {
  it('sends only the kind for a GitHub issue', () => {
    expect(handBody('issue', 'ignored')).toEqual({ kind: 'issue' });
  });
  it('sends the trimmed text for Other', () => {
    expect(handBody('other', '  use the plan in notes.md \n')).toEqual({ kind: 'other', text: 'use the plan in notes.md' });
  });
});
