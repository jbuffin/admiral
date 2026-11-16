import { describe, expect, it } from 'vitest';
import type { Source } from './api';
import { buttonModel } from './buttonModel';
import { refreshBusy } from './ordersView';

const source = (key: string, over: Partial<Source & { refreshing: boolean }> = {}) =>
  ({ key, name: key, plugin: 'github', fleet: 'zulu', icon_url: '', has_schema: false, state: 'running', error: null, warnings: [], views: [], capabilities: ['refresh'], ...over }) as Source;

// The Refresh control as OrdersPanel builds it.
const control = (busy: boolean) => buttonModel({ label: 'Refresh now', icon: true, busy, busyLabel: 'Refreshing' });

describe('orders Refresh control', () => {
  it('is the Refresh now button when nothing is refreshing', () => {
    const busy = refreshBusy([source('gh', { refreshing: false })], 'for-you', false);
    expect(busy).toBe(false);
    expect(control(busy)).toMatchObject({ name: 'Refresh now', ariaBusy: undefined, ignoreTap: false });
  });

  it('is a busy spinner named Refreshing while the source refreshes, whoever asked', () => {
    const busy = refreshBusy([source('gh', { refreshing: true })], 'for-you', false);
    expect(busy).toBe(true);
    expect(control(busy)).toMatchObject({ name: 'Refreshing', ariaBusy: 'true', ignoreTap: true });
  });

  it('is busy while its own refresh request is in flight, before the source says so', () => {
    expect(refreshBusy([source('gh', { refreshing: false })], 'for-you', true)).toBe(true);
    expect(refreshBusy([source('gh')], 'for-you', true)).toBe(true);
  });

  it('follows the source of the open tab, not another one', () => {
    const list = [source('gh', { refreshing: false }), source('lin', { plugin: 'test', refreshing: true, views: [{ id: 'triage', title: 'Triage', orders: [] }] })];
    expect(refreshBusy(list, 'for-you', false)).toBe(false);
    expect(refreshBusy(list, 'lin/triage', false)).toBe(true);
  });

  it('treats a source that sends no refreshing field as idle', () => {
    expect(refreshBusy([source('gh')], 'for-you', false)).toBe(false);
  });
});
