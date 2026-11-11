import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// router.ts reads the page's location when it loads.
const page = vi.hoisted(() => {
  const l = { pathname: '/app/inbox', search: '' };
  vi.stubGlobal('location', l);
  vi.stubGlobal('addEventListener', () => {});
  vi.stubGlobal('history', {
    replaceState: (_s: unknown, _t: string, url: string) => {
      const [pathname, search = ''] = url.split('?');
      l.pathname = pathname;
      l.search = search ? `?${search}` : '';
    },
  });
  return l;
});

import { campaignOf, campaignPath, loc, preferencesFleet, preferencesPage, startRouter, surfaceOf } from './router';

describe('campaign paths', () => {
  it('round-trips an id through its page path', () => {
    expect(campaignPath('3f9c0a41b7d2e8a5')).toBe('/app/campaigns/3f9c0a41b7d2e8a5');
    expect(campaignOf('/app/campaigns/3f9c0a41b7d2e8a5')).toBe('3f9c0a41b7d2e8a5');
    expect(campaignOf('/app/campaigns/3f9c0a41b7d2e8a5/')).toBe('3f9c0a41b7d2e8a5');
  });
  it('names no campaign on the list page or another surface', () => {
    expect(campaignOf('/app/campaigns')).toBeNull();
    expect(campaignOf('/app/ships')).toBeNull();
  });
  it('is the Campaigns surface, and no surface below it deeper than the id', () => {
    expect(surfaceOf('/app/campaigns/3f9c0a41b7d2e8a5')?.id).toBe('campaigns');
    expect(surfaceOf('/app/campaigns/3f9c0a41b7d2e8a5/extra')).toBeUndefined();
    expect(surfaceOf('/app/inbox/3f9c0a41b7d2e8a5')).toBeUndefined();
  });
});

describe('the router', () => {
  beforeEach(() => {
    page.search = '?fleet=zulu';
  });
  it('keeps a Campaign page path on load', () => {
    page.pathname = '/app/campaigns/3f9c0a41b7d2e8a5';
    startRouter();
    expect(page.pathname).toBe('/app/campaigns/3f9c0a41b7d2e8a5');
    expect(loc.value.path).toBe('/app/campaigns/3f9c0a41b7d2e8a5');
  });
  it('still sends an unknown path to the inbox', () => {
    page.pathname = '/app/nowhere';
    startRouter();
    expect(page.pathname).toBe('/app/inbox');
  });
});

describe('the ship route', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.stubGlobal('location', { pathname: '/app/ships', search: '' });
  });
  afterEach(() => {
    vi.stubGlobal('location', page);
  });

  it('is a ship under Ships, and a path with more below it is still unknown', async () => {
    const { surfaceOf, shipSession } = await import('./router');
    expect(surfaceOf('/app/ships/s-abc')?.id).toBe('ships');
    expect(shipSession('/app/ships/s-abc')).toBe('s-abc');
    expect(shipSession('/app/ships')).toBeNull();
    expect(shipSession('/app/inbox')).toBeNull();
    expect(surfaceOf('/app/ships/s-abc/more')).toBeUndefined();
  });

  it('carries a session whose name needs escaping', async () => {
    const { shipPath, shipSession } = await import('./router');
    const path = shipPath('a b/c');
    expect(path).toBe('/app/ships/a%20b%2Fc');
    expect(shipSession(path)).toBe('a b/c');
  });

  it('survives a reload and keeps ?fleet=', async () => {
    vi.stubGlobal('location', { pathname: '/app/ships/s-abc', search: '?fleet=alpha' });
    const replaceState = vi.fn();
    vi.stubGlobal('history', { replaceState, pushState: vi.fn() });
    vi.stubGlobal('addEventListener', vi.fn());
    const { startRouter, loc } = await import('./router');
    startRouter();
    expect(replaceState).not.toHaveBeenCalled();
    expect(loc.value).toEqual({ path: '/app/ships/s-abc', search: '?fleet=alpha' });
  });
});

describe('Remote access route', () => {
  it('is its own Preferences surface under the reserved admiral segment, and a fleet named remote or admiral keeps its pages', () => {
    expect(surfaceOf('/app/preferences/admiral/remote')?.id).toBe('preferences');
    expect(preferencesFleet('/app/preferences/admiral/remote')).toBeNull();
    expect(preferencesPage('/app/preferences/admiral/remote')).toBe('remote');

    expect(preferencesFleet('/app/preferences/remote/fleet')).toBe('remote');
    expect(preferencesPage('/app/preferences/remote/fleet')).toBe('fleet');
    expect(preferencesFleet('/app/preferences/admiral/sources')).toBe('admiral');
    expect(preferencesPage('/app/preferences/admiral/sources')).toBe('sources');
    expect(surfaceOf('/app/preferences/admiral/bogus')).toBeUndefined();
    expect(surfaceOf('/app/preferences/zulu/remote')).toBeUndefined();
  });
});
