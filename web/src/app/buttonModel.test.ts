import { describe, expect, it } from 'vitest';
import { buttonModel } from './buttonModel';

describe('button model', () => {
  it('defaults to the secondary look: just btn', () => {
    expect(buttonModel({ label: 'Cancel' }).class).toBe('btn');
  });

  it('adds a class per variant and modifier, and passes the caller class through', () => {
    const m = buttonModel({ label: 'Send', variant: 'primary', icon: true, round: true, class: 'launch-open' });
    expect(m.class.split(' ').sort()).toEqual(['btn', 'icon', 'launch-open', 'primary', 'round']);
    for (const v of ['danger', 'quiet', 'link'] as const) {
      expect(buttonModel({ label: 'x', variant: v }).class).toBe(`btn ${v}`);
    }
  });

  it('is a button element with type button unless it submits', () => {
    expect(buttonModel({ label: 'x' })).toMatchObject({ tag: 'button', type: 'button' });
    expect(buttonModel({ label: 'x', type: 'submit' })).toMatchObject({ tag: 'button', type: 'submit' });
  });

  it('marks a busy button aria-busy, still enabled so it stays focusable', () => {
    const m = buttonModel({ label: 'Merge', busy: true });
    expect(m.ariaBusy).toBe('true');
    expect(m.disabled).toBe(false);
    expect(m.ignoreTap).toBe(true);
  });

  it('never disables the busy button even when disabled is also passed', () => {
    expect(buttonModel({ label: 'Merge', busy: true, disabled: true }).disabled).toBe(false);
  });

  it('keeps disabled as its own state when not busy, and taps still reach a rest button', () => {
    const m = buttonModel({ label: 'Merge', disabled: true });
    expect(m.disabled).toBe(true);
    expect(m.ariaBusy).toBeUndefined();
    expect(buttonModel({ label: 'Merge' }).ignoreTap).toBe(false);
  });

  it('keeps the accessible name while busy', () => {
    expect(buttonModel({ label: 'Merge', busy: true }).name).toBe('Merge');
  });

  it('uses busyLabel as the accessible name only while busy', () => {
    expect(buttonModel({ label: 'Stop', busyLabel: 'Stopping', busy: true }).name).toBe('Stopping');
    expect(buttonModel({ label: 'Stop', busyLabel: 'Stopping' }).name).toBe('Stop');
  });

  it('uses label as the accessible name of an icon button', () => {
    expect(buttonModel({ label: 'Close', icon: true }).name).toBe('Close');
  });

  it('sets aria-pressed from pressed, true and false, and omits it otherwise', () => {
    expect(buttonModel({ label: 'a', pressed: true }).ariaPressed).toBe('true');
    expect(buttonModel({ label: 'a', pressed: false }).ariaPressed).toBe('false');
    expect(buttonModel({ label: 'a' }).ariaPressed).toBeUndefined();
  });

  it('renders an anchor for href, with the same class, and never goes busy', () => {
    const m = buttonModel({ label: 'Open on GitHub', href: 'https://example.test/pr/1', busy: true });
    expect(m).toMatchObject({ tag: 'a', href: 'https://example.test/pr/1', class: 'btn' });
    expect(m.ariaBusy).toBeUndefined();
    expect(m.ignoreTap).toBe(false);
  });

  it('opens an external link in a new tab without leaking the opener', () => {
    const m = buttonModel({ label: 'View files', href: 'https://example.test', external: true });
    expect(m.target).toBe('_blank');
    expect(m.rel).toBe('noopener noreferrer');
    expect(buttonModel({ label: 'x', href: '/app/' }).target).toBeUndefined();
  });
});
