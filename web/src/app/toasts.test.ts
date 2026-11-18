import { describe, expect, it } from 'vitest';
import { createToasts, TOAST_MS } from './toasts';

const alert = (tag: string, extra: Record<string, string> = {}) => ({ name: `Ship ${tag}`, line: `line ${tag}`, tag, ...extra });

function queue() {
  let t = 1_000_000;
  const toasts = createToasts({ now: () => t });
  return { toasts, advance: (ms: number) => (t += ms) };
}

describe('toast queue', () => {
  it('shows an alert as a toast with its name and line', () => {
    const { toasts } = queue();
    toasts.push(alert('a'));
    expect(toasts.list().map((x) => [x.name, x.line])).toEqual([['Ship a', 'line a']]);
  });

  it('keeps at most two, and a third replaces the oldest', () => {
    const { toasts } = queue();
    toasts.push(alert('a'));
    toasts.push(alert('b'));
    toasts.push(alert('c'));
    expect(toasts.list().map((x) => x.tag)).toEqual(['b', 'c']);
  });

  it('replaces a toast that has the same tag instead of stacking', () => {
    const { toasts } = queue();
    toasts.push(alert('a'));
    toasts.push(alert('b'));
    toasts.push({ ...alert('a'), line: 'newer' });
    expect(toasts.list().map((x) => [x.tag, x.line])).toEqual([
      ['b', 'line b'],
      ['a', 'newer'],
    ]);
  });

  it('lets a toast go after a few seconds, each by its own age', () => {
    expect(TOAST_MS).toBeGreaterThanOrEqual(3000);
    expect(TOAST_MS).toBeLessThanOrEqual(10_000);
    const { toasts, advance } = queue();
    toasts.push(alert('a'));
    advance(TOAST_MS - 1000);
    toasts.push(alert('b'));
    advance(999);
    toasts.expire();
    expect(toasts.list().map((x) => x.tag)).toEqual(['a', 'b']);
    advance(1);
    toasts.expire();
    expect(toasts.list().map((x) => x.tag)).toEqual(['b']);
    advance(TOAST_MS - 1000);
    toasts.expire();
    expect(toasts.list()).toEqual([]);
  });

  it('starts the clock again when a same-tag alert replaces a toast', () => {
    const { toasts, advance } = queue();
    toasts.push(alert('a'));
    advance(TOAST_MS - 1);
    toasts.push(alert('a'));
    advance(2);
    toasts.expire();
    expect(toasts.list().map((x) => x.tag)).toEqual(['a']);
  });

  it('removes a dismissed toast and leaves the others', () => {
    const { toasts } = queue();
    const a = toasts.push(alert('a'));
    toasts.push(alert('b'));
    toasts.dismiss(a.id);
    expect(toasts.list().map((x) => x.tag)).toEqual(['b']);
    toasts.dismiss(a.id);
    expect(toasts.list()).toHaveLength(1);
  });

  it('keeps markup as plain text', () => {
    const { toasts } = queue();
    toasts.push({ name: '<b>Dory</b>', line: '<img src=x onerror=alert(1)>', tag: 'x' });
    expect(toasts.list()[0]).toMatchObject({ name: '<b>Dory</b>', line: '<img src=x onerror=alert(1)>' });
  });
});

describe('a module action result toast', () => {
  it('keeps its link and shows it with its name and line', () => {
    const { toasts } = queue();
    toasts.pushResult(alert('r', { url: 'https://example.com/t/1' }));
    expect(toasts.list().map((x) => [x.name, x.line, x.url])).toEqual([['Ship r', 'line r', 'https://example.com/t/1']]);
  });

  it('does not expire, while an alert beside it still does', () => {
    const { toasts, advance } = queue();
    toasts.pushResult(alert('r'));
    toasts.push(alert('a'));
    advance(TOAST_MS * 10);
    toasts.expire();
    expect(toasts.list().map((x) => x.tag)).toEqual(['r']);
  });

  it('goes when dismissed', () => {
    const { toasts } = queue();
    const r = toasts.pushResult(alert('r'));
    toasts.dismiss(r.id);
    expect(toasts.list()).toEqual([]);
  });

  it('counts toward the cap of two, and a same-tag result replaces the one before it', () => {
    const { toasts } = queue();
    toasts.pushResult(alert('r1'));
    toasts.pushResult(alert('r2', { url: 'https://example.com/2' }));
    toasts.push(alert('a'));
    expect(toasts.list().map((x) => [x.tag, x.url])).toEqual([
      ['r2', 'https://example.com/2'],
      ['a', undefined],
    ]);
    toasts.pushResult({ ...alert('r2'), line: 'newer' });
    expect(toasts.list().map((x) => [x.tag, x.line])).toEqual([
      ['a', 'line a'],
      ['r2', 'newer'],
    ]);
  });

  it('leaves an alert without a link or a sticky mark', () => {
    const { toasts } = queue();
    toasts.push(alert('a'));
    expect(toasts.list()[0].url).toBeUndefined();
  });
});

