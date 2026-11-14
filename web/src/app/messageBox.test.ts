import { describe, expect, it } from 'vitest';
import type { Item } from './api';
import { boxState, bubblesShowing, snapshotOf, wakeRefused } from './messageBox';

const exit = (status: 'needs_you' | 'underway' | 'anchored' | 'sailing_off', exit_code: number | null = null, exit_signal: string | null = null) => ({ status, exit_code, exit_signal });
const user = (id: string, text: string): Item => ({ id, kind: 'user', text });
const assistant = (id: string): Item => ({ id, kind: 'assistant', text: 'ok' });

describe('boxState', () => {
  it('accepts a message while the ship works and while it waits for you', () => {
    expect(boxState(exit('underway')).enabled).toBe(true);
    expect(boxState(exit('needs_you')).enabled).toBe(true);
  });

  it('is read only on a ship that exited with an error', () => {
    expect(boxState(exit('anchored', 1))).toEqual({ enabled: false, note: 'Exited with error, read only' });
  });

  it('names the signal when the harness was killed', () => {
    const killed = boxState(exit('anchored', null, 'KILL'));
    expect(killed.enabled).toBe(false);
    expect(killed.note).toBe('Killed (KILL), read only');
  });

  it('says the wall clock stopped the ship, with its minutes, instead of naming the signal', () => {
    const stopped = boxState({ ...exit('anchored', null, 'TERM'), stopped_by: 'wall_clock:60' });
    expect(stopped).toEqual({ enabled: false, note: 'Stopped at the 60-minute wall clock, read only' });
  });

  it('keeps naming the signal when stopped_by is a reason it does not know', () => {
    expect(boxState({ ...exit('anchored', null, 'TERM'), stopped_by: 'something_else' }).note).toBe('Killed (TERM), read only');
  });

  it('is disabled while the ship sails off', () => {
    expect(boxState(exit('sailing_off', 0)).enabled).toBe(false);
  });
});

describe('boxState on a moored ship', () => {
  const moored = (extra: { no_resume?: boolean } = {}) => ({ status: 'moored' as const, exit_code: null, exit_signal: null, ...extra });

  it('takes a message, and says that sending it wakes the ship', () => {
    expect(boxState(moored())).toEqual({ enabled: true, placeholder: 'Wake the ship with this message' });
  });

  it('is off with the reason when the ship has no valid resume text', () => {
    const state = boxState(moored({ no_resume: true }));
    expect(state.enabled).toBe(false);
    expect(state.note).toMatch(/resume/i);
  });

  it('still takes a message from a ship whose poll failed, since waking needs no GitHub', () => {
    expect(boxState({ ...moored(), stale: true } as Parameters<typeof boxState>[0]).enabled).toBe(true);
  });
});

describe('bubblesShowing', () => {
  const send = (id: number, items: Item[] | null, answered = false) => ({ id, text: `m${id}`, snapshot: items && snapshotOf(items), answered });
  const ids = (sends: ReturnType<typeof send>[]) => sends.map((s) => s.id);
  const base = [user('u1', 'hi'), assistant('a1')];

  it('shows while the request is in flight', () => {
    expect(ids(bubblesShowing([send(1, base)], base, true))).toEqual([1]);
  });

  it('stays until a user item arrives, whatever its text', () => {
    expect(ids(bubblesShowing([send(1, base, true)], base, true))).toEqual([1]);
    expect(bubblesShowing([send(1, base, true)], [...base, user('u2', 'recorded differently')], true)).toEqual([]);
  });

  it('resolves two sends in a row one user item at a time, in send order', () => {
    const sends = [send(1, base), send(2, base)];
    expect(ids(bubblesShowing(sends, [...base, user('u2', 'a')], true))).toEqual([2]);
    expect(bubblesShowing(sends, [...base, user('u2', 'a'), user('u3', 'b')], true)).toEqual([]);
  });

  it('is not resolved by older items loaded after the send', () => {
    const older = [user('u0', 'earlier'), assistant('a0')];
    expect(ids(bubblesShowing([send(1, base)], [...older, ...base], true))).toEqual([1]);
    expect(bubblesShowing([send(1, base)], [...older, ...base, user('u2', 'a')], true)).toEqual([]);
  });

  it('waits for the first page when the send came before it', () => {
    expect(ids(bubblesShowing([send(1, null)], base, true))).toEqual([1]);
  });

  it('is resolved by the first user item of an empty conversation', () => {
    expect(bubblesShowing([send(1, [])], [user('u1', 'a')], true)).toEqual([]);
  });

  it('goes on the answer when the ship has no conversation', () => {
    expect(ids(bubblesShowing([send(1, null), send(2, null, true)], [], false))).toEqual([1]);
  });
});

describe('wakeRefused', () => {
  it('brings the typed text back to an empty box with the route message and turns the box on', () => {
    expect(wakeRefused({ text: '' }, 'check the docs', 'ship is not moored')).toEqual({ waking: false, error: 'ship is not moored', text: 'check the docs' });
  });
  it('keeps whatever was typed meanwhile', () => {
    expect(wakeRefused({ text: 'newer' }, 'older', 'no')).toEqual({ waking: false, error: 'no' });
  });
  it('has no text to restore for a Wake with none', () => {
    expect(wakeRefused({ text: '' }, undefined, 'no')).toEqual({ waking: false, error: 'no' });
  });
});
