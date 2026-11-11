import { beforeEach, describe, expect, it, vi } from 'vitest';

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

import { campaignOf, campaignPath, loc, startRouter, surfaceOf } from './router';

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
    expect(surfaceOf('/app/ships/3f9c0a41b7d2e8a5')).toBeUndefined();
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
